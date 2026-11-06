package panel

import (
	"context"
	"errors"
	"strings"
	"time"

	"anypanel2/internal/update"
)

// UpdateInfo is what the settings page shows about updates.
type UpdateInfo struct {
	Current   string       `json:"current"`
	Latest    string       `json:"latest"`
	Newer     bool         `json:"newer"` // the latest release is newer than this one
	Published *time.Time   `json:"published,omitempty"`
	Notes     string       `json:"notes,omitempty"`
	URL       string       `json:"url,omitempty"`
	CanApply  bool         `json:"canApply"`
	Reason    string       `json:"reason,omitempty"` // why the panel cannot install it by itself
	Status    update.State `json:"status"`
}

// CheckUpdate looks for a release newer than the running version.
func (s *Service) CheckUpdate(ctx context.Context, refresh bool) (*UpdateInfo, error) {
	if s.Update == nil {
		return nil, invalid("обновление из панели здесь недоступно")
	}
	rel, err := s.Update.Latest(ctx, refresh)
	if err != nil {
		return nil, err
	}
	info := &UpdateInfo{Current: s.Version, Latest: rel.Version, Newer: update.Newer(s.Version, rel.Version), URL: rel.URL, Notes: rel.Notes, Status: s.Update.Status()}
	if !rel.Published.IsZero() {
		p := rel.Published
		info.Published = &p
	}
	if _, ok := update.ParseVersion(s.Version); !ok {
		info.Reason = "это сборка для разработки: версию не с чем сравнивать"
	} else {
		info.CanApply = info.Newer
	}
	return info, nil
}

// ApplyUpdate installs the latest release, which must be the version the caller saw.
func (s *Service) ApplyUpdate(ctx context.Context, version string) error {
	info, err := s.CheckUpdate(ctx, false)
	if err != nil {
		return err
	}
	if !info.CanApply {
		if info.Reason != "" {
			return invalid("%s", info.Reason)
		}
		return invalid("новых версий нет")
	}
	if strings.TrimPrefix(version, "v") != info.Latest {
		return invalid("вышла другая версия (%s): проверьте обновления ещё раз", info.Latest)
	}
	rel, err := s.Update.Latest(ctx, false)
	if err != nil {
		return err
	}
	if err := s.Update.Apply(rel); err != nil {
		return invalid("%s", err.Error())
	}
	return nil
}

// UpdateStatus is the progress of a running update, with the end of the installer's log.
func (s *Service) UpdateStatus() (update.State, string, error) {
	if s.Update == nil {
		return update.State{}, "", errors.New("обновление из панели здесь недоступно")
	}
	return s.Update.Status(), s.Update.LogTail(15), nil
}
