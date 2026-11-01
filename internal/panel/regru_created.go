package panel

import (
	"context"
	"strings"
	"time"

	"anypanel2/internal/model"
	"anypanel2/internal/regru"
)

// The names the panel creates in the zone are remembered, so that their A records go away again with the node or the
// cascade they were made for, and nothing piles up in the zone. A name the user picked from the existing ones is never
// remembered, so it is never removed.

// abandonedAfter is how long a created name may stay unused before it counts as abandoned: the installer wizard needs
// a while between making the name and saving the node.
const abandonedAfter = 30 * time.Minute

// regruTrack remembers a name the panel has just created in the zone.
func (s *Service) regruTrack(ctx context.Context, domain, ip string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	set, err := s.settings(ctx)
	if err != nil {
		return err
	}
	set.RegRu.Created = append(set.RegRu.Created, model.RegRuCreated{Domain: strings.ToLower(domain), IP: ip, At: s.Now().UTC()})
	return s.Store.PutSettings(ctx, *set)
}

// regruAssign records that a created name now belongs to a node or a cascade, so it goes away with it. A name that the
// panel did not create is not in the list and nothing happens.
func (s *Service) regruAssign(ctx context.Context, domain, serverID, cascadeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	set, err := s.settings(ctx)
	if err != nil {
		return
	}
	for i := range set.RegRu.Created {
		if e := &set.RegRu.Created[i]; strings.EqualFold(e.Domain, domain) {
			e.ServerID, e.CascadeID = serverID, cascadeID
			if err := s.Store.PutSettings(ctx, *set); err != nil && s.Log != nil {
				s.Log.Warn("reg.ru: could not record the owner of a domain", "domain", domain, "err", err)
			}
			return
		}
	}
}

// namesInUse is every domain name that something still uses (the panel's own domains, servers, connections and
// cascades), with the ids of the servers and cascades that exist.
func (s *Service) namesInUse(ctx context.Context, set *model.Settings) (used, servers, cascades map[string]bool, err error) {
	svs, err := s.Store.ListServers(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	ins, err := s.Store.ListInbounds(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	cas, err := s.Store.ListCascades(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	used, servers, cascades = map[string]bool{}, map[string]bool{}, map[string]bool{}
	add := func(names ...string) {
		for _, n := range names {
			if n != "" {
				used[strings.ToLower(n)] = true
			}
		}
	}
	add(set.PanelDomain, set.RealityDomain, set.Subscription.Domain)
	for _, sv := range svs {
		add(sv.Address)
		servers[sv.ID] = true
	}
	for _, in := range ins {
		add(in.Reality.ServerNames...)
		add(in.Public.Host, in.Public.SNI)
	}
	for _, c := range cas {
		add(c.Domain)
		cascades[c.ID] = true
	}
	return used, servers, cascades, nil
}

// regruOrphans are the created names that nothing uses and whose node or cascade is gone (or that never got one and
// are old enough).
func (s *Service) regruOrphans(ctx context.Context) ([]model.RegRuCreated, error) {
	set, err := s.settings(ctx)
	if err != nil {
		return nil, err
	}
	if len(set.RegRu.Created) == 0 {
		return nil, nil
	}
	used, servers, cascades, err := s.namesInUse(ctx, set)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	var out []model.RegRuCreated
	for _, e := range set.RegRu.Created {
		var gone bool
		switch {
		case e.ServerID != "":
			gone = !servers[e.ServerID]
		case e.CascadeID != "":
			gone = !cascades[e.CascadeID]
		default:
			gone = now.Sub(e.At) > abandonedAfter
		}
		if gone && !used[strings.ToLower(e.Domain)] {
			out = append(out, e)
		}
	}
	return out, nil
}

// RegRuCleanup is the result of removing created names.
type RegRuCleanup struct {
	Removed  []string `json:"removed"`
	Problems []string `json:"problems"`
}

// CleanupRegRu removes the A records of the created names that nothing uses any more.
func (s *Service) CleanupRegRu(ctx context.Context) (*RegRuCleanup, error) {
	if _, err := s.regruClient(ctx); err != nil {
		return nil, err
	}
	orphans, err := s.regruOrphans(ctx)
	if err != nil {
		return nil, err
	}
	return s.regruRelease(ctx, orphans), nil
}

// regruReleaseOwned removes the records of the created names that were made for a node or a cascade that is gone now.
// It never fails the caller: a record that cannot be removed stays in the list and the cleanup button offers it again.
// The returned text, empty when all went well, says what was left behind, for the user to see.
func (s *Service) regruReleaseOwned(ctx context.Context, serverID, cascadeID string) string {
	orphans, err := s.regruOrphans(ctx)
	if err != nil || len(orphans) == 0 {
		return ""
	}
	var mine []model.RegRuCreated
	for _, e := range orphans {
		if (serverID != "" && e.ServerID == serverID) || (cascadeID != "" && e.CascadeID == cascadeID) {
			mine = append(mine, e)
		}
	}
	if len(mine) == 0 {
		return ""
	}
	res := s.regruRelease(ctx, mine)
	if s.Log != nil {
		for _, p := range res.Problems {
			s.Log.Warn("reg.ru: a domain of a deleted node was not removed", "problem", p)
		}
	}
	if len(res.Problems) == 0 {
		return ""
	}
	return "DNS-запись в reg.ru не удалена (её можно убрать позже кнопкой в Настройках): " + strings.Join(res.Problems, "; ")
}

// regruRelease deletes the A record of each name (only if it still points at the address the panel gave it, so a record
// that someone repointed is left alone) and forgets the name. Names of another zone and names whose record is already
// gone are simply forgotten. A name whose record could not be removed stays remembered.
func (s *Service) regruRelease(ctx context.Context, entries []model.RegRuCreated) *RegRuCleanup {
	res := &RegRuCleanup{Removed: []string{}, Problems: []string{}}
	if len(entries) == 0 {
		return res
	}
	s.regruMu.Lock()
	defer s.regruMu.Unlock()
	cs, err := s.regruClients(ctx)
	if err != nil {
		res.Problems = append(res.Problems, err.Error())
		return res
	}
	cctx, cancel := context.WithTimeout(ctx, regruTimeout)
	defer cancel()
	records := map[string][]regru.Record{} // the zones read so far
	unreadable := map[string]bool{}
	forget := map[string]bool{}
	for _, e := range entries {
		// The name belongs to the longest managed zone it ends with.
		var c *regru.Client
		var sub string
		for _, cand := range cs {
			if sb, ok := cand.Sub(e.Domain); ok && (c == nil || len(cand.Zone()) > len(c.Zone())) {
				c, sub = cand, sb
			}
		}
		if c == nil { // a zone that is not connected any more
			forget[e.Domain] = true
			continue
		}
		rs, read := records[c.Zone()]
		if !read && !unreadable[c.Zone()] {
			if rs, err = c.Records(cctx); err != nil {
				unreadable[c.Zone()] = true
				res.Problems = append(res.Problems, c.Zone()+": "+regru.Describe(err))
			} else {
				records[c.Zone()] = rs
			}
		}
		if unreadable[c.Zone()] {
			continue
		}
		typ := regru.Kind(e.IP)
		present, same := false, false
		for _, r := range rs {
			if strings.EqualFold(r.Sub, sub) {
				present = true
				if r.Type == typ && r.Content == e.IP {
					same = true
				}
			}
		}
		switch {
		case same:
			if err := c.Remove(cctx, typ, sub, e.IP); err != nil {
				res.Problems = append(res.Problems, e.Domain+": "+regru.Describe(err))
				continue
			}
			res.Removed = append(res.Removed, e.Domain)
			forget[e.Domain] = true
		case present:
			res.Problems = append(res.Problems, e.Domain+": запись теперь указывает на другой адрес, её не тронули")
			forget[e.Domain] = true
		default:
			forget[e.Domain] = true // the record is already gone
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	set, err := s.settings(ctx)
	if err != nil {
		res.Problems = append(res.Problems, err.Error())
		return res
	}
	kept := make([]model.RegRuCreated, 0, len(set.RegRu.Created))
	for _, e := range set.RegRu.Created {
		if !forget[e.Domain] {
			kept = append(kept, e)
		}
	}
	set.RegRu.Created = kept
	if err := s.Store.PutSettings(ctx, *set); err != nil {
		res.Problems = append(res.Problems, err.Error())
	}
	return res
}
