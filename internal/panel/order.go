package panel

import (
	"context"
	"slices"

	"anypanel2/internal/model"
)

// AutoNodeSlot stands for the automatic node in the order the Connections page sends.
const AutoNodeSlot = "__auto__"

// ReorderInbounds sets the order of the connections in the subscriptions to the given list of ids, which must name every
// connection once. AutoNodeSlot may stand among them: it moves the automatic node to that place. Every entry takes the
// number of its place as its SortOrder. The order does not change what Xray serves, so nothing is applied.
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
	seen := map[string]bool{}
	for _, id := range order {
		if id != AutoNodeSlot {
			if _, ok := byID[id]; !ok {
				return invalid("unknown connection %q", id)
			}
		}
		if seen[id] {
			return invalid("connection %q is listed twice", id)
		}
		seen[id] = true
	}
	if n := len(seen) - btoi(seen[AutoNodeSlot]); n != len(byID) {
		return invalid("the order must name every connection")
	}
	auto := slices.Index(order, AutoNodeSlot)
	for slot, id := range order {
		if id == AutoNodeSlot {
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
	if auto < 0 {
		return nil
	}
	set, err := s.Store.GetSettings(ctx)
	if err != nil {
		return err
	}
	last := auto == len(order)-1
	if a := &set.Subscription.AutoNode; a.Position != auto || a.Last != last {
		a.Position, a.Last = auto, last
		return s.Store.PutSettings(ctx, *set)
	}
	return nil
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
