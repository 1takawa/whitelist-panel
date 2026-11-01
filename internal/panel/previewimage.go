package panel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"

	"anypanel2/internal/model"
)

// MaxPreviewImage is the largest picture accepted for the link card: messengers refuse big pictures anyway.
const MaxPreviewImage = 2 << 20

var previewImageTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

// SetPreviewImage stores the picture shown in the card of the subscription link.
func (s *Service) SetPreviewImage(ctx context.Context, data []byte) (*model.Settings, error) {
	if len(data) == 0 {
		return nil, invalid("the picture is empty")
	}
	if len(data) > MaxPreviewImage {
		return nil, invalid("the picture is too big (max 2 MB)")
	}
	ct := http.DetectContentType(data)
	if !previewImageTypes[ct] {
		return nil, invalid("the picture must be PNG, JPEG, GIF or WebP")
	}
	img := model.PreviewImage{Type: ct, Data: append([]byte(nil), data...)}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil { // WebP has no decoder here: the size stays unknown
		img.Width, img.Height = cfg.Width, cfg.Height
	}
	sum := sha256.Sum256(data)
	img.Sum = hex.EncodeToString(sum[:4])
	return s.putPreviewImage(ctx, img)
}

// ClearPreviewImage removes the uploaded picture.
func (s *Service) ClearPreviewImage(ctx context.Context) (*model.Settings, error) {
	return s.putPreviewImage(ctx, model.PreviewImage{})
}

func (s *Service) putPreviewImage(ctx context.Context, img model.PreviewImage) (*model.Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, err := s.settings(ctx)
	if err != nil {
		return nil, err
	}
	v := *cur
	v.Subscription.Preview.Image = img
	if err := s.Store.PutSettings(ctx, v); err != nil {
		return nil, err
	}
	return &v, nil
}

// PreviewImage returns the uploaded picture, or nil when there is none.
func (s *Service) PreviewImage(ctx context.Context) (*model.PreviewImage, error) {
	set, err := s.settings(ctx)
	if err != nil {
		return nil, err
	}
	if img := set.Subscription.Preview.Image; img.Type != "" && len(img.Data) > 0 {
		return &img, nil
	}
	return nil, nil
}
