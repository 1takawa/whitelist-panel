package panel

import (
	"context"
	"strings"

	"anypanel2/internal/model"
)

// AutoNodeSlot stands for the automatic node in the order the Connections page sends. The extra automatic nodes stand as
// AutoNodeSlot + ":" + their id.
const AutoNodeSlot = "__auto__"

func isAutoSlot(id string) bool { return id == AutoNodeSlot || strings.HasPrefix(id, AutoNodeSlot+":") }

// ReorderInbounds sets the order of the connections in the subscriptions to the given list of ids, which must name every
// connection once. The slots of the automatic nodes may stand among them: each moves its node to that place. Every entry
// takes the number of its place as its SortOrder. The order does not change what Xray serves, so nothing is applied.
func (s *Service) ReorderInbounds(ctx context.Context, order []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.Store.ListInbounds(ctx)
	if err != nil {
		return err
	}
	byID := make(map[string]model.Inbound, len(all))
	for _, in := range all {
		byID[in.ID] = in
	}
	set, err := s.Store.GetSettings(ctx)
	if err != nil {
		return err
	}
	groups := map[string]int{} // extra automatic node id -> index in the settings
	for i, g := range set.Subscription.AutoNode.Groups {
		groups[g.ID] = i
	}
	seen := map[string]bool{}
	autos, lastReal := 0, -1
	for slot, id := range order {
		if isAutoSlot(id) {
			autos++
			if gid, ok := strings.CutPrefix(id, AutoNodeSlot+":"); ok {
				if _, known := groups[gid]; !known {
					return invalid("unknown automatic node %q", gid)
				}
			}
		} else {
			if _, ok := byID[id]; !ok {
				return invalid("unknown connection %q", id)
			}
			lastReal = slot
		}
		if seen[id] {
			return invalid("connection %q is listed twice", id)
		}
		seen[id] = true
	}
	if len(seen)-autos != len(byID) {
		return invalid("the order must name every connection")
	}
	for slot, id := range order {
		if isAutoSlot(id) {
			continue
		}
		in := byID[id]
		if in.SortOrder == slot {
			continue
		}
		in.SortOrder = slot
		if err := s.Store.PutInbound(ctx, in); err != nil {
			return err
		}
	}
	if autos == 0 {
		return nil
	}
	changed := false
	for slot, id := range order {
		if !isAutoSlot(id) {
			continue
		}
		last := slot > lastReal
		if id == AutoNodeSlot {
			if a := &set.Subscription.AutoNode; a.Position != slot || a.Last != last {
				a.Position, a.Last, changed = slot, last, true
			}
			continue
		}
		g := &set.Subscription.AutoNode.Groups[groups[strings.TrimPrefix(id, AutoNodeSlot+":")]]
		if g.Position != slot || g.Last != last {
			g.Position, g.Last, changed = slot, last, true
		}
	}
	if !changed {
		return nil
	}
	return s.Store.PutSettings(ctx, *set)
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
