package panel

import (
	"context"
	"time"
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
