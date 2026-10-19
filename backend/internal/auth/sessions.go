package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/store"
)

// ErrNoSession means the cookie is missing, unknown, expired, idle too long, or the user is inactive.
var ErrNoSession = errors.New("no valid session")

// Principal is the authenticated staff member for one request.
type Principal struct {
	UserID      int64
	Username    string
	DisplayName string
	Role        Role
	CSRFToken   string
	tokenHash   []byte
}

// TokenHash identifies the current session (used to keep it when revoking others).
func (p *Principal) TokenHash() []byte { return p.tokenHash }

// SessionConfig holds the lifetimes from docs/WORKFLOWS.md §5.
type SessionConfig struct {
	IdleTimeout time.Duration // default 12h
	MaxAge      time.Duration // default 7 days
}

// touchInterval limits last_seen_at writes to one per 5 minutes per session.
const touchInterval = 5 * time.Minute

// Sessions creates and resolves server-side sessions.
type Sessions struct {
	cfg SessionConfig
	now func() time.Time
}

// NewSessions returns a session manager.
func NewSessions(cfg SessionConfig) *Sessions {
	return &Sessions{cfg: cfg, now: time.Now}
}

// Create inserts a session and returns the cookie token and CSRF token.
func (s *Sessions) Create(ctx context.Context, q store.Querier, userID int64, ip *netip.Addr, userAgent string) (token, csrf string, err error) {
	token, err = randomToken()
	if err != nil {
		return "", "", err
	}
	csrf, err = randomToken()
	if err != nil {
		return "", "", err
	}
	ua := userAgent
	if len(ua) > 300 {
		ua = ua[:300]
	}
	err = q.CreateSession(ctx, store.CreateSessionParams{
		TokenHash: HashToken(token),
		UserID:    userID,
		CsrfToken: csrf,
		ExpiresAt: pgtype.Timestamptz{Time: s.now().Add(s.cfg.MaxAge), Valid: true},
		Ip:        ip,
		UserAgent: pgtype.Text{String: ua, Valid: ua != ""},
	})
	return token, csrf, err
}

// Resolve loads the principal for a cookie token, enforcing absolute and idle expiry.
func (s *Sessions) Resolve(ctx context.Context, q store.Querier, token string) (*Principal, error) {
	if token == "" || len(token) > 128 {
		return nil, ErrNoSession
	}
	hash := HashToken(token)
	row, err := q.GetSessionWithUser(ctx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoSession
	}
	if err != nil {
		return nil, err
	}
	now := s.now()
	if !row.IsActive || now.After(row.ExpiresAt.Time) || now.Sub(row.LastSeenAt.Time) > s.cfg.IdleTimeout {
		_ = q.DeleteSession(ctx, hash)
		return nil, ErrNoSession
	}
	if now.Sub(row.LastSeenAt.Time) > touchInterval {
		if err := q.TouchSession(ctx, hash); err != nil {
			return nil, err
		}
	}
	return &Principal{
		UserID: row.UserID, Username: row.Username, DisplayName: row.DisplayName,
		Role: Role(row.Role), CSRFToken: row.CsrfToken, tokenHash: hash,
	}, nil
}

// HashToken is the storage form of a session token (SHA-256).
func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
