package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/httpx"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/store"
)

// CSRFHeader carries the per-session synchronizer token on unsafe requests.
const CSRFHeader = "X-CSRF-Token"

type principalKey struct{}

// FromContext returns the authenticated principal, or nil.
func FromContext(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey{}).(*Principal)
	return p
}

// WithPrincipal stores p in ctx (used by tests and the authenticate middleware).
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// CookieConfig controls the session cookie (docs/SECURITY.md §2).
type CookieConfig struct {
	Secure bool
	MaxAge time.Duration
}

// Name is __Host-rokh_session over HTTPS; browsers reject __Host- cookies without Secure,
// so plain-HTTP local development uses rokh_session.
func (c CookieConfig) Name() string {
	if c.Secure {
		return "__Host-rokh_session"
	}
	return "rokh_session"
}

// Middleware wires sessions into HTTP handling.
type Middleware struct {
	Sessions      *Sessions
	Queries       store.Querier
	Cookie        CookieConfig
	AllowedOrigin string // scheme://host[:port] from APP_BASE_URL
	Logger        *slog.Logger
}

// SetCookie writes the session cookie.
func (m *Middleware) SetCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: m.Cookie.Name(), Value: token, Path: "/",
		MaxAge: int(m.Cookie.MaxAge.Seconds()), HttpOnly: true, Secure: m.Cookie.Secure, SameSite: http.SameSiteLaxMode,
	})
}

// ClearCookie removes the session cookie.
func (m *Middleware) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: m.Cookie.Name(), Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: m.Cookie.Secure, SameSite: http.SameSiteLaxMode,
	})
}

// Authenticate attaches the principal when a valid session cookie is present. It never rejects.
func (m *Middleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(m.Cookie.Name())
		if err != nil || c.Value == "" {
			next.ServeHTTP(w, r)
			return
		}
		p, err := m.Sessions.Resolve(r.Context(), m.Queries, c.Value)
		switch {
		case errors.Is(err, ErrNoSession):
			m.ClearCookie(w)
		case err != nil:
			m.Logger.Error("session lookup failed", "request_id", httpx.RequestIDFrom(r.Context()), "err", err)
			httpx.Error(w, http.StatusInternalServerError, "INTERNAL", "internal error")
			return
		default:
			r = r.WithContext(WithPrincipal(r.Context(), p))
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAuth rejects requests without a principal.
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if FromContext(r.Context()) == nil {
			httpx.Error(w, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Require enforces one action from the central policy.
func Require(a Action) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := FromContext(r.Context())
			if p == nil {
				httpx.Error(w, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
				return
			}
			if !Can(p.Role, a) {
				httpx.Error(w, http.StatusForbidden, "FORBIDDEN", "not allowed")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isSafeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// SameOrigin checks Origin (or Referer as a fallback) against the configured app origin.
// A request carrying neither is rejected for unsafe methods.
func (m *Middleware) SameOrigin(r *http.Request) bool {
	src := r.Header.Get("Origin")
	if src == "" || src == "null" {
		src = r.Header.Get("Referer")
	}
	if src == "" {
		return false
	}
	u, err := url.Parse(src)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}
	return u.Scheme+"://"+u.Host == m.AllowedOrigin
}

// RequireOrigin rejects unsafe requests from foreign origins (used on login, which has no session yet).
func (m *Middleware) RequireOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isSafeMethod(r.Method) && !m.SameOrigin(r) {
			httpx.Error(w, http.StatusForbidden, "CSRF_FAILED", "cross-site request rejected")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireCSRF checks Origin and the synchronizer token on unsafe methods. Must run after RequireAuth.
func (m *Middleware) RequireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isSafeMethod(r.Method) {
			p := FromContext(r.Context())
			got := r.Header.Get(CSRFHeader)
			if p == nil || !m.SameOrigin(r) || got == "" ||
				subtle.ConstantTimeCompare([]byte(got), []byte(p.CSRFToken)) != 1 {
				httpx.Error(w, http.StatusForbidden, "CSRF_FAILED", "missing or invalid CSRF token")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
