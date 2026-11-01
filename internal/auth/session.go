package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"time"

	"anypanel2/internal/store"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrNoSession          = errors.New("no valid session")
)

// Manager issues and validates administrator sessions.
type Manager struct {
	Store store.Store
	TTL   time.Duration
	Now   func() time.Time

	dummyHash string // verified against when the user does not exist, to keep timing uniform
}

func NewManager(s store.Store, ttl time.Duration) (*Manager, error) {
	dummy, err := HashPassword("timing-equalizer-not-a-real-password")
	if err != nil {
		return nil, err
	}
	return &Manager{Store: s, TTL: ttl, Now: time.Now, dummyHash: dummy}, nil
}

// Login checks the credentials and, on success, creates a session and returns the raw cookie token.
func (m *Manager) Login(ctx context.Context, username, password, ip string) (token string, expires time.Time, err error) {
	admin, err := m.Store.GetAdmin(ctx, username)
	if errors.Is(err, store.ErrNotFound) {
		_, _ = VerifyPassword(password, m.dummyHash)
		return "", time.Time{}, ErrInvalidCredentials
	}
	if err != nil {
		return "", time.Time{}, err
	}
	ok, verr := VerifyPassword(password, admin.PasswordHash)
	if verr != nil || !ok {
		return "", time.Time{}, ErrInvalidCredentials
	}

	token, err = newToken()
	if err != nil {
		return "", time.Time{}, err
	}
	now := m.Now()
	expires = now.Add(m.TTL)
	err = m.Store.CreateSession(ctx, store.Session{
		TokenHash: HashToken(token), Username: admin.Username, IP: ip, CreatedAt: now, ExpiresAt: expires,
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return token, expires, nil
}

// Authenticate returns the username owning a valid, unexpired session token.
func (m *Manager) Authenticate(ctx context.Context, token string) (string, error) {
	if token == "" {
		return "", ErrNoSession
	}
	sess, err := m.Store.GetSession(ctx, HashToken(token))
	if errors.Is(err, store.ErrNotFound) {
		return "", ErrNoSession
	}
	if err != nil {
		return "", err
	}
	if !m.Now().Before(sess.ExpiresAt) {
		_ = m.Store.DeleteSession(ctx, sess.TokenHash)
		return "", ErrNoSession
	}
	return sess.Username, nil
}

func (m *Manager) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return m.Store.DeleteSession(ctx, HashToken(token))
}

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)

// ValidUsername reports whether name may be used as the administrator's login.
func ValidUsername(name string) bool { return usernameRe.MatchString(name) }

// Errors of UpdateCredentials; their texts are shown to the administrator.
var (
	ErrInvalidUsername = errors.New("логин: только латинские буквы, цифры и _ (до 64 символов)")
	ErrPasswordLength  = fmt.Errorf("пароль: от %d до %d символов", MinPasswordLength, MaxPasswordLength)
	ErrUsernameTaken   = errors.New("такой логин уже занят")
)

// MaxPasswordLength bounds what is hashed; argon2 itself has no limit, but a megabyte password is an attack.
const MaxPasswordLength = 128

// UpdateCredentials changes the login and, when password is not empty, the password of the administrator current.
// Every session is closed afterwards, so the caller has to sign in again.
func (m *Manager) UpdateCredentials(ctx context.Context, current, username, password string) error {
	if !ValidUsername(username) {
		return ErrInvalidUsername
	}
	if password != "" && (len([]rune(password)) < MinPasswordLength || len(password) > MaxPasswordLength) {
		return ErrPasswordLength
	}
	admin, err := m.Store.GetAdmin(ctx, current)
	if err != nil {
		return err
	}
	if username != admin.Username {
		if _, err := m.Store.GetAdmin(ctx, username); err == nil {
			return ErrUsernameTaken
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}
	updated := *admin
	updated.Username = username
	if password != "" {
		if updated.PasswordHash, err = HashPassword(password); err != nil {
			return err
		}
	}
	if err := m.Store.PutAdmin(ctx, updated); err != nil {
		return err
	}
	if username != admin.Username {
		if err := m.Store.DeleteAdmin(ctx, admin.Username); err != nil {
			return err
		}
	}
	return m.Store.DeleteAllSessions(ctx)
}

// HashToken is the stored form of a session token.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
