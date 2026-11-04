// Package customer implements café-customer identity independently from staff authentication.
//
// Customers have their own table, cookie, server-side sessions and CSRF token. Nothing here reads
// the staff session, and a staff session never grants customer access (or the reverse). The only
// shared piece is the staff middleware's Origin check, which compares against the app origin.
package customer

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/text/unicode/norm"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/auth"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/httpx"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/ratelimit"
)

const (
	cookieProd    = "__Host-rokh_customer"
	cookieDev     = "rokh_customer"
	idleLimit     = 12 * time.Hour
	absoluteLimit = 7 * 24 * time.Hour
)

var phonePattern = regexp.MustCompile(`^09[0-9]{9}$`)

var (
	errNoCookie      = errors.New("no customer cookie")
	errSessionDenied = errors.New("customer session invalid or expired")
)

// Service owns customer-only cookie and database session handling.
type Service struct {
	db            *pgxpool.Pool
	staff         *auth.Middleware
	secure        bool
	ip            func(*http.Request) netip.Addr
	loginIP       *ratelimit.Limiter
	loginPhone    *ratelimit.Limiter
	registerIP    *ratelimit.Limiter
	registerPhone *ratelimit.Limiter
}

// New returns the customer identity service. staff is used only for its Origin check and cookie
// security setting; customer sessions never touch the staff sessions.
func New(db *pgxpool.Pool, staff *auth.Middleware, ip func(*http.Request) netip.Addr) *Service {
	return &Service{db: db, staff: staff, secure: staff.Cookie.Secure, ip: ip,
		loginIP: ratelimit.New(20, 15*time.Minute), loginPhone: ratelimit.New(5, 15*time.Minute),
		registerIP: ratelimit.New(10, time.Hour), registerPhone: ratelimit.New(3, time.Hour)}
}

// CookieName is the customer session cookie (__Host- prefixed when cookies are Secure).
func (s *Service) CookieName() string {
	if s.secure {
		return cookieProd
	}
	return cookieDev
}
func (s *Service) setCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{Name: s.CookieName(), Value: token, Path: "/", MaxAge: int(absoluteLimit.Seconds()), HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode})
}
func (s *Service) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: s.CookieName(), Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode})
}

// Principal is the authenticated customer of a request.
type Principal struct {
	ID        int64
	FirstName string
	LastName  string
	Phone     string
	csrf      string
	tokenHash []byte
}
type principalKey struct{}

// FromContext returns the customer set by RequireCustomer, or nil.
func FromContext(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey{}).(*Principal)
	return p
}

// lookup resolves the request's customer cookie. errNoCookie: the request carries none.
// errSessionDenied: a cookie that is not a live session of an active account (the cookie is
// cleared and an expired or deactivated session is deleted).
func (s *Service) lookup(w http.ResponseWriter, r *http.Request) (*Principal, error) {
	c, e := r.Cookie(s.CookieName())
	if e != nil {
		return nil, errNoCookie
	}
	if len(c.Value) < 32 || len(c.Value) > 128 {
		s.clearCookie(w)
		return nil, errSessionDenied
	}
	hash := sha256.Sum256([]byte(c.Value))
	var p Principal
	var expires, lastSeen time.Time
	var active bool
	err := s.db.QueryRow(r.Context(), `
 SELECT c.id,c.first_name,c.last_name,c.phone,cs.csrf_token,cs.expires_at,cs.last_seen_at,c.is_active
 FROM customer_sessions cs JOIN customers c ON c.id=cs.customer_id
 WHERE cs.token_hash=$1`, hash[:]).Scan(&p.ID, &p.FirstName, &p.LastName, &p.Phone, &p.csrf, &expires, &lastSeen, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		s.clearCookie(w)
		return nil, errSessionDenied
	}
	if err != nil {
		return nil, err
	}
	if !active || !time.Now().Before(expires) || time.Since(lastSeen) > idleLimit {
		_, _ = s.db.Exec(r.Context(), "DELETE FROM customer_sessions WHERE token_hash=$1", hash[:])
		s.clearCookie(w)
		return nil, errSessionDenied
	}
	if time.Since(lastSeen) > 5*time.Minute {
		if _, err = s.db.Exec(r.Context(), "UPDATE customer_sessions SET last_seen_at=now() WHERE token_hash=$1", hash[:]); err != nil {
			return nil, err
		}
	}
	p.tokenHash = hash[:]
	return &p, nil
}

// csrfOK requires the app origin and the session's CSRF token on unsafe methods.
func (s *Service) csrfOK(r *http.Request, p *Principal) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return true
	}
	token := r.Header.Get(auth.CSRFHeader)
	return p != nil && s.staff.SameOrigin(r) && token != "" &&
		subtle.ConstantTimeCompare([]byte(token), []byte(p.csrf)) == 1
}

func unauthorized(w http.ResponseWriter) {
	httpx.Error(w, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
}
func serverError(w http.ResponseWriter) {
	httpx.Error(w, http.StatusInternalServerError, "INTERNAL", "internal error")
}
func csrfFailed(w http.ResponseWriter) {
	httpx.Error(w, http.StatusForbidden, "CSRF_FAILED", "cross-site request rejected")
}

// RequireCustomer authenticates the customer cookie (401 otherwise), enforces CSRF on unsafe
// methods and marks the response no-store. The principal is available through FromContext.
func (s *Service) RequireCustomer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		p, err := s.lookup(w, r)
		switch {
		case errors.Is(err, errNoCookie), errors.Is(err, errSessionDenied):
			unauthorized(w)
			return
		case err != nil:
			serverError(w)
			return
		}
		if !s.csrfOK(r, p) {
			csrfFailed(w)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
	})
}

// OrderCustomer decides who places a public order. It returns 0 for a guest (no customer
// cookie). A request that carries a customer cookie is authenticated or refused: a stale or
// invalid session answers 401 CUSTOMER_SESSION_EXPIRED (never a silent guest order), and a live
// session must come from the app origin with its CSRF token. ok=false means a response was sent.
func (s *Service) OrderCustomer(w http.ResponseWriter, r *http.Request) (id int64, ok bool) {
	p, err := s.lookup(w, r)
	switch {
	case errors.Is(err, errNoCookie):
		return 0, true
	case errors.Is(err, errSessionDenied):
		httpx.Error(w, http.StatusUnauthorized, "CUSTOMER_SESSION_EXPIRED",
			"customer session expired; log in again or order as a guest")
		return 0, false
	case err != nil:
		serverError(w)
		return 0, false
	}
	if !s.csrfOK(r, p) {
		csrfFailed(w)
		return 0, false
	}
	return p.ID, true
}

func (s *Service) origin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.staff.SameOrigin(r) {
			csrfFailed(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// AuthHandler serves /customer/auth/*.
func (s *Service) AuthHandler() http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			next.ServeHTTP(w, r)
		})
	})
	r.With(s.origin).Post("/register", s.register)
	r.With(s.origin).Post("/login", s.login)
	r.Group(func(r chi.Router) {
		r.Use(s.RequireCustomer)
		r.Get("/me", s.me)
		r.Post("/logout", s.logout)
		r.Post("/password", s.changePassword)
	})
	return r
}

// NormalizePhone returns the canonical 09XXXXXXXXX form of an Iranian mobile number, or "".
// Persian and Arabic-Indic digits, a +98 / 98 / 0098 prefix, spaces and hyphens are accepted.
func NormalizePhone(input string) string {
	input = strings.TrimSpace(input)
	var b strings.Builder
	for _, r := range input {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= '۰' && r <= '۹':
			b.WriteRune(r - '۰' + '0')
		case r >= '٠' && r <= '٩':
			b.WriteRune(r - '٠' + '0')
		case r == '+' && b.Len() == 0:
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == ' ':
			// visual separators only
		default:
			return ""
		}
	}
	v := b.String()
	switch {
	case strings.HasPrefix(v, "+98"):
		v = "0" + v[3:]
	case strings.HasPrefix(v, "0098"):
		v = "0" + v[4:]
	case strings.HasPrefix(v, "98"):
		v = "0" + v[2:]
	}
	if !phonePattern.MatchString(v) {
		return ""
	}
	return v
}
func cleanName(v string) string {
	v = norm.NFC.String(strings.TrimSpace(v))
	if utf8.RuneCountInString(v) < 1 || utf8.RuneCountInString(v) > 60 {
		return ""
	}
	for _, r := range v {
		if r < 32 || r == 127 {
			return ""
		}
	}
	return v
}
func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// throttle applies the per-IP and per-phone limits. An invalid phone is never a shared bucket:
// it is charged to the IP only (and rejected afterwards).
func (s *Service) throttle(w http.ResponseWriter, ip netip.Addr, phone string, registration bool) bool {
	byIP, byPhone := s.loginIP, s.loginPhone
	if registration {
		byIP, byPhone = s.registerIP, s.registerPhone
	}
	ok, wait := byIP.Allow(ip.String())
	if ok && phone != "" {
		var w2 time.Duration
		ok, w2 = byPhone.Allow(phone)
		wait = max(wait, w2)
	}
	if !ok {
		secs := int(wait.Seconds()) + 1
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		httpx.Error(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many attempts, try again later")
		return false
	}
	return true
}

type accountView struct {
	ID            int64  `json:"id"`
	FirstName     string `json:"first_name"`
	LastName      string `json:"last_name"`
	Phone         string `json:"phone"`
	PhoneVerified bool   `json:"phone_verified"` // always false until an OTP flow exists
}
type response struct {
	Customer  accountView `json:"customer"`
	CSRFToken string      `json:"csrf_token"`
}

func account(id int64, first, last, phone string) accountView {
	return accountView{ID: id, FirstName: first, LastName: last, Phone: phone}
}

// issue creates a session in tx. Any session the browser presented is revoked first, so a login
// or registration never leaves an older session of the same browser alive (no fixation).
func (s *Service) issue(r *http.Request, tx pgx.Tx, id int64) (string, string, error) {
	ctx := r.Context()
	if c, err := r.Cookie(s.CookieName()); err == nil && c.Value != "" {
		old := sha256.Sum256([]byte(c.Value))
		if _, err := tx.Exec(ctx, "DELETE FROM customer_sessions WHERE token_hash=$1", old[:]); err != nil {
			return "", "", err
		}
	}
	token, err := newToken()
	if err != nil {
		return "", "", err
	}
	csrf, err := newToken()
	if err != nil {
		return "", "", err
	}
	hash := sha256.Sum256([]byte(token))
	ua := r.UserAgent()
	if len(ua) > 512 {
		ua = strings.ToValidUTF8(ua[:512], "")
	}
	_, err = tx.Exec(ctx, `INSERT INTO customer_sessions(token_hash,customer_id,csrf_token,expires_at,ip,user_agent)
 VALUES ($1,$2,$3,now()+interval '7 days',$4::inet,$5)`, hash[:], id, csrf, s.ip(r).String(), ua)
	return token, csrf, err
}
func (s *Service) event(r *http.Request, tx pgx.Tx, id int64, kind string) error {
	_, err := tx.Exec(r.Context(), `INSERT INTO customer_events(customer_id,event_type,ip) VALUES($1,$2,$3::inet)`, id, kind, s.ip(r).String())
	return err
}

func (s *Service) register(w http.ResponseWriter, r *http.Request) {
	var in struct {
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		Phone     string `json:"phone"`
		Password  string `json:"password"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	first, last, phone := cleanName(in.FirstName), cleanName(in.LastName), NormalizePhone(in.Phone)
	if !s.throttle(w, s.ip(r), phone, true) {
		return
	}
	if first == "" || last == "" || phone == "" || auth.ValidatePassword(in.Password) != nil {
		httpx.ValidationError(w, map[string]string{"account": "invalid registration information"})
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		serverError(w)
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		serverError(w)
		return
	}
	defer tx.Rollback(r.Context())
	var id int64
	err = tx.QueryRow(r.Context(), `INSERT INTO customers(first_name,last_name,phone,password_hash)
 VALUES($1,$2,$3,$4) RETURNING id`, first, last, phone, hash).Scan(&id)
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && pgerr.Code == "23505" {
		httpx.Error(w, http.StatusConflict, "CONFLICT", "registration could not be completed")
		return
	}
	if err != nil {
		serverError(w)
		return
	}
	token, csrf, err := s.issue(r, tx, id)
	if err != nil {
		serverError(w)
		return
	}
	if err = s.event(r, tx, id, "REGISTERED"); err != nil {
		serverError(w)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w)
		return
	}
	s.setCookie(w, token)
	httpx.JSON(w, http.StatusCreated, response{Customer: account(id, first, last, phone), CSRFToken: csrf})
}

func (s *Service) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Phone    string `json:"phone"`
		Password string `json:"password"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	phone := NormalizePhone(in.Phone)
	if !s.throttle(w, s.ip(r), phone, false) {
		return
	}
	if phone == "" || in.Password == "" || len(in.Password) > 4*auth.MaxPasswordLen {
		auth.VerifyDummy(in.Password)
		invalidLogin(w)
		return
	}
	var id int64
	var first, last, hash string
	var active bool
	err := s.db.QueryRow(r.Context(), `SELECT id,first_name,last_name,password_hash,is_active FROM customers WHERE phone=$1`, phone).Scan(&id, &first, &last, &hash, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		auth.VerifyDummy(in.Password)
		invalidLogin(w)
		return
	}
	if err != nil {
		serverError(w)
		return
	}
	ok, e := auth.VerifyPassword(in.Password, hash)
	if e != nil {
		serverError(w)
		return
	}
	if !ok || !active {
		invalidLogin(w)
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		serverError(w)
		return
	}
	defer tx.Rollback(r.Context())
	var currentHash string
	var currentActive bool
	err = tx.QueryRow(r.Context(), "SELECT password_hash,is_active FROM customers WHERE id=$1 FOR UPDATE", id).Scan(&currentHash, &currentActive)
	if err != nil {
		serverError(w)
		return
	}
	if !currentActive || hash != currentHash {
		invalidLogin(w)
		return
	}
	token, csrf, err := s.issue(r, tx, id)
	if err != nil {
		serverError(w)
		return
	}
	if _, err = tx.Exec(r.Context(), "UPDATE customers SET last_login_at=now() WHERE id=$1", id); err != nil {
		serverError(w)
		return
	}
	if err = s.event(r, tx, id, "LOGIN_SUCCEEDED"); err != nil {
		serverError(w)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w)
		return
	}
	s.setCookie(w, token)
	httpx.JSON(w, http.StatusOK, response{Customer: account(id, first, last, phone), CSRFToken: csrf})
}
func invalidLogin(w http.ResponseWriter) {
	httpx.Error(w, http.StatusUnauthorized, "UNAUTHENTICATED", "invalid phone or password")
}
func (s *Service) me(w http.ResponseWriter, r *http.Request) {
	p := FromContext(r.Context())
	httpx.JSON(w, http.StatusOK, response{Customer: account(p.ID, p.FirstName, p.LastName, p.Phone), CSRFToken: p.csrf})
}
func (s *Service) logout(w http.ResponseWriter, r *http.Request) {
	p := FromContext(r.Context())
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		serverError(w)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), "DELETE FROM customer_sessions WHERE token_hash=$1", p.tokenHash); err != nil {
		serverError(w)
		return
	}
	if err = s.event(r, tx, p.ID, "LOGOUT"); err != nil {
		serverError(w)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w)
		return
	}
	s.clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// changePassword verifies the current password, stores the new hash, revokes EVERY session of
// the account (including the current one) and issues a fresh session for this browser, so a
// stolen session token or CSRF token is useless after the change.
func (s *Service) changePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	if err := auth.ValidatePassword(in.NewPassword); err != nil {
		httpx.ValidationError(w, map[string]string{"new_password": "policy"})
		return
	}
	p := FromContext(r.Context())
	if !s.throttle(w, s.ip(r), p.Phone, false) {
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		serverError(w)
		return
	}
	defer tx.Rollback(r.Context())
	var oldHash string
	if err = tx.QueryRow(r.Context(), "SELECT password_hash FROM customers WHERE id=$1 FOR UPDATE", p.ID).Scan(&oldHash); err != nil {
		serverError(w)
		return
	}
	ok, e := auth.VerifyPassword(in.CurrentPassword, oldHash)
	if e != nil {
		serverError(w)
		return
	}
	if !ok {
		httpx.ValidationError(w, map[string]string{"current_password": "incorrect"})
		return
	}
	newHash, err := auth.HashPassword(in.NewPassword)
	if err != nil {
		serverError(w)
		return
	}
	if _, err = tx.Exec(r.Context(), "UPDATE customers SET password_hash=$2 WHERE id=$1", p.ID, newHash); err != nil {
		serverError(w)
		return
	}
	if _, err = tx.Exec(r.Context(), "DELETE FROM customer_sessions WHERE customer_id=$1", p.ID); err != nil {
		serverError(w)
		return
	}
	token, csrf, err := s.issue(r, tx, p.ID)
	if err != nil {
		serverError(w)
		return
	}
	if err = s.event(r, tx, p.ID, "PASSWORD_CHANGED"); err != nil {
		serverError(w)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w)
		return
	}
	s.setCookie(w, token)
	httpx.JSON(w, http.StatusOK, response{Customer: account(p.ID, p.FirstName, p.LastName, p.Phone), CSRFToken: csrf})
}
