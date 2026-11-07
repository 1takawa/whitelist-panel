package panel

import (
	"context"
	"sort"
	"time"

	"anypanel2/internal/model"
)

// onlineOwners says, for every client that is online, the one server it counts for: the server it moved traffic through
// last. A client that is on two servers at the same moment (or was moved from one to the other by the automatic node a
// minute ago) is online once, on the newest of them, so the servers' online counts add up to the overall one. LocalServerID
// stands for the machine the panel runs on. Servers that cannot be read are left out.
func (s *Service) onlineOwners(ctx context.Context, now time.Time) map[string]string {
	ids := []string{LocalServerID}
	if srvs, err := s.Store.ListServers(ctx); err == nil {
		for _, sv := range srvs {
			ids = append(ids, sv.ID)
		}
	}
	owners := map[string]string{}
	seen := map[string]time.Time{}
	for _, id := range ids {
		rows, err := s.Store.ListServerTraffic(ctx, id)
		if err != nil {
			continue
		}
		for _, r := range rows {
			if r.LastActiveAt == nil || now.Sub(*r.LastActiveAt) > OnlineWindow {
				continue
			}
			// The newest wins; on the same moment the server that comes first in the list (the main one) keeps it.
			if at, ok := seen[r.Email]; !ok || r.LastActiveAt.After(at) {
				owners[r.Email], seen[r.Email] = id, *r.LastActiveAt
			}
		}
	}
	return owners
}

// MainServerName is how the machine the panel runs on is called in lists.
const MainServerName = "Главный сервер"

// lastNodes says, for every client that ever moved traffic, the server it did so through last (whenever that was).
func (s *Service) lastNodes(ctx context.Context) map[string]model.ClientNode {
	names := map[string]model.ClientNode{LocalServerID: {ID: LocalServerID, Name: MainServerName}}
	ids := []string{LocalServerID}
	if srvs, err := s.Store.ListServers(ctx); err == nil {
		for _, sv := range srvs {
			ids = append(ids, sv.ID)
			names[sv.ID] = model.ClientNode{ID: sv.ID, Name: sv.Name, Flag: sv.Flag()}
		}
	}
	out := map[string]model.ClientNode{}
	for _, id := range ids {
		rows, err := s.Store.ListServerTraffic(ctx, id)
		if err != nil {
			continue
		}
		for _, r := range rows {
			if r.LastActiveAt == nil {
				continue
			}
			if cur, ok := out[r.Email]; !ok || r.LastActiveAt.After(cur.At) {
				n := names[id]
				n.At = *r.LastActiveAt
				out[r.Email] = n
			}
		}
	}
	return out
}

// numberClients lists the clients after giving a number to each one that has none: in the order the clients were made, after
// the highest number in use. Numbers are never handed out twice, so a deleted client's number stays unused.
func (s *Service) numberClients(ctx context.Context) ([]model.Client, error) {
	list, err := s.Store.ListClients(ctx)
	if err != nil {
		return nil, err
	}
	missing := false
	for _, c := range list {
		if c.Num == 0 {
			missing = true
			break
		}
	}
	if !missing {
		return list, nil
	}
	s.numMu.Lock()
	defer s.numMu.Unlock()
	if list, err = s.Store.ListClients(ctx); err != nil { // read again: another request may have numbered them
		return nil, err
	}
	next := 0
	var todo []int
	for i, c := range list {
		if c.Num > next {
			next = c.Num
		}
		if c.Num == 0 {
			todo = append(todo, i)
		}
	}
	sort.SliceStable(todo, func(a, b int) bool {
		x, y := list[todo[a]], list[todo[b]]
		if !x.CreatedAt.Equal(y.CreatedAt) {
			return x.CreatedAt.Before(y.CreatedAt)
		}
		return x.Email < y.Email
	})
	for _, i := range todo {
		next++
		list[i].Num = next
		if err := s.Store.PutClient(ctx, list[i]); err != nil {
			return nil, err
		}
	}
	return list, nil
}
