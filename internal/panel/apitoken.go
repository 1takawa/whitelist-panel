package panel

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"
	"time"
)

const tokenPrefix = "anyp_"

// APITokenInfo describes the current token without revealing it.
type APITokenInfo struct {
	Exists    bool       `json:"exists"`
	Prefix    string     `json:"prefix,omitempty"`
	CreatedAt *time.Time `json:"createdAt,omitempty"`
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

func (s *Service) APITokenInfo(ctx context.Context) (APITokenInfo, error) {
	sec, err := s.Store.GetSecrets(ctx)
	if err != nil {
		return APITokenInfo{}, err
	}
	return APITokenInfo{Exists: sec.APITokenHash != "", Prefix: sec.APITokenPrefix, CreatedAt: sec.APITokenCreatedAt}, nil
}

// GenerateAPIToken replaces the token with a new one. The plain value is returned once; only its hash is stored.
func (s *Service) GenerateAPIToken(ctx context.Context) (string, APITokenInfo, error) {
	s.secretsMu.Lock()
	defer s.secretsMu.Unlock()
	sec, err := s.Store.GetSecrets(ctx)
	if err != nil {
		return "", APITokenInfo{}, err
	}
	token := tokenPrefix + randString(alnum, 40)
	now := s.Now().UTC()
	sec.APITokenHash, sec.APITokenPrefix, sec.APITokenCreatedAt = hashToken(token), token[:len(tokenPrefix)+4], &now
	if err := s.Store.PutSecrets(ctx, *sec); err != nil {
		return "", APITokenInfo{}, err
	}
	return token, APITokenInfo{Exists: true, Prefix: sec.APITokenPrefix, CreatedAt: &now}, nil
}

func (s *Service) RevokeAPIToken(ctx context.Context) error {
	s.secretsMu.Lock()
	defer s.secretsMu.Unlock()
	sec, err := s.Store.GetSecrets(ctx)
	if err != nil {
		return err
	}
	sec.APITokenHash, sec.APITokenPrefix, sec.APITokenCreatedAt = "", "", nil
	return s.Store.PutSecrets(ctx, *sec)
}

// CheckAPIToken reports whether the presented token is the current one.
func (s *Service) CheckAPIToken(ctx context.Context, presented string) bool {
	if !strings.HasPrefix(presented, tokenPrefix) || len(presented) > 128 {
		return false
	}
	sec, err := s.Store.GetSecrets(ctx)
	if err != nil || sec.APITokenHash == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(hashToken(presented)), []byte(sec.APITokenHash)) == 1
}
