// Package customer implements café-customer identity independently from staff authentication.
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
var errInvalid = errors.New("invalid credentials")

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

func New(db *pgxpool.Pool, staff *auth.Middleware, ip func(*http.Request) netip.Addr) *Service {
	return &Service{db: db, staff: staff, secure: staff.Cookie.Secure, ip: ip,
		loginIP: ratelimit.New(20, 15*time.Minute), loginPhone: ratelimit.New(5, 15*time.Minute),
		registerIP: ratelimit.New(10, time.Hour), registerPhone: ratelimit.New(3, time.Hour)}
}
func (s *Service) cookieName() string {
	if s.secure {
		return cookieProd
	}
	return cookieDev
}
func (s *Service) setCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{Name: s.cookieName(), Value: token, Path: "/", MaxAge: int(absoluteLimit.Seconds()), HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode})
}
func (s *Service) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: s.cookieName(), Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode})
}

type principal struct {
	ID    int64
	First string
	Last  string
	Phone string
	CSRF  string
	Hash  []byte
}
type principalKey struct{}

func fromContext(ctx context.Context) *principal {
	p, _ := ctx.Value(principalKey{}).(*principal)
	return p
}
func (s *Service) resolve(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	c, e := r.Cookie(s.cookieName())
	if e != nil || len(c.Value) < 32 || len(c.Value) > 128 {
		unauthorized(w)
		return r, false
	}
	hash := sha256.Sum256([]byte(c.Value))
	var p principal
	var expires, lastSeen time.Time
	var active bool
	err := s.db.QueryRow(r.Context(), `
 SELECT c.id,c.first_name,c.last_name,c.phone,cs.csrf_token,cs.expires_at,cs.last_seen_at,c.is_active
 FROM customer_sessions cs JOIN customers c ON c.id=cs.customer_id
 WHERE cs.token_hash=$1`, hash[:]).Scan(&p.ID, &p.First, &p.Last, &p.Phone, &p.CSRF, &expires, &lastSeen, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		s.clearCookie(w)
		unauthorized(w)
		return r, false
	}
	if err != nil {
		serverError(w)
		return r, false
	}
	if !active || !time.Now().Before(expires) || time.Since(lastSeen) > idleLimit {
		_, _ = s.db.Exec(r.Context(), "DELETE FROM customer_sessions WHERE token_hash=$1", hash[:])
		s.clearCookie(w)
		unauthorized(w)
		return r, false
	}
	if time.Since(lastSeen) > 5*time.Minute {
		if _, err = s.db.Exec(r.Context(), "UPDATE customer_sessions SET last_seen_at=now() WHERE token_hash=$1", hash[:]); err != nil {
			serverError(w)
			return r, false
		}
	}
	p.Hash = hash[:]
	return r.WithContext(context.WithValue(r.Context(), principalKey{}, &p)), true
}
func unauthorized(w http.ResponseWriter) {
	httpx.Error(w, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
}
func serverError(w http.ResponseWriter) {
	httpx.Error(w, http.StatusInternalServerError, "INTERNAL", "internal error")
}
func validOrigin(s *Service, r *http.Request) bool { return s.staff.SameOrigin(r) }
func (s *Service) origin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !validOrigin(s, r) {
			httpx.Error(w, 403, "CSRF_FAILED", "cross-site request rejected")
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Service) authenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, ok := s.resolve(w, r)
		if !ok {
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Service) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			p := fromContext(r.Context())
			token := r.Header.Get(auth.CSRFHeader)
			if p == nil || !validOrigin(s, r) || token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(p.CSRF)) != 1 {
				httpx.Error(w, 403, "CSRF_FAILED", "cross-site request rejected")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
func (s *Service) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(noStore)
	r.With(s.origin).Post("/register", s.register)
	r.With(s.origin).Post("/login", s.login)
	r.Group(func(r chi.Router) {
		r.Use(s.authenticated, s.csrf)
		r.Get("/me", s.me)
		r.Post("/logout", s.logout)
		r.Post("/password", s.changePassword)
	})
	return r
}
func normalizePhone(input string) string {
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
		default:
			return ""
		}
	}
	v := b.String()
	if strings.HasPrefix(v, "+98") {
		v = "0" + v[3:]
	} else if strings.HasPrefix(v, "98") {
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
func (s *Service) throttle(w http.ResponseWriter, ip netip.Addr, phone string, registration bool) bool {
	var a, b *ratelimit.Limiter
	if registration {
		a, b = s.registerIP, s.registerPhone
	} else {
		a, b = s.loginIP, s.loginPhone
	}
	ok1, wait1 := a.Allow(ip.String())
	ok2, wait2 := b.Allow(phone)
	if !ok1 || !ok2 {
		wait := wait1
		if wait2 > wait {
			wait = wait2
		}
		w.Header().Set("Retry-After", "60")
		_ = wait
		httpx.Error(w, 429, "RATE_LIMITED", "too many attempts, try again later")
		return false
	}
	return true
}

type accountView struct {
	ID            int64  `json:"id"`
	FirstName     string `json:"first_name"`
	LastName      string `json:"last_name"`
	Phone         string `json:"phone"`
	PhoneVerified bool   `json:"phone_verified"`
}
type response struct {
	Customer  accountView `json:"customer"`
	CSRFToken string      `json:"csrf_token"`
}

func view(p *principal) response {
	return response{Customer: accountView{ID: p.ID, FirstName: p.First, LastName: p.Last, Phone: p.Phone, PhoneVerified: false}, CSRFToken: p.CSRF}
}
func (s *Service) issue(ctx context.Context, tx pgx.Tx, id int64, ip netip.Addr, ua string) (string, string, error) {
	token, err := newToken()
	if err != nil {
		return "", "", err
	}
	csrf, err := newToken()
	if err != nil {
		return "", "", err
	}
	hash := sha256.Sum256([]byte(token))
	if len(ua) > 512 {
		ua = ua[:512]
	}
	_, err = tx.Exec(ctx, `INSERT INTO customer_sessions(token_hash,customer_id,csrf_token,expires_at,ip,user_agent)
 VALUES ($1,$2,$3,now()+interval '7 days',$4::inet,$5)`, hash[:], id, csrf, ip.String(), ua)
	return token, csrf, err
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
	first, last, phone := cleanName(in.FirstName), cleanName(in.LastName), normalizePhone(in.Phone)
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
		httpx.Error(w, 409, "CONFLICT", "registration could not be completed")
		return
	}
	if err != nil {
		serverError(w)
		return
	}
	token, csrf, err := s.issue(r.Context(), tx, id, s.ip(r), r.UserAgent())
	if err != nil {
		serverError(w)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO customer_events(customer_id,event_type,ip) VALUES($1,'REGISTERED',$2::inet)`, id, s.ip(r).String()); err != nil {
		serverError(w)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w)
		return
	}
	s.setCookie(w, token)
	httpx.JSON(w, 201, response{Customer: accountView{ID: id, FirstName: first, LastName: last, Phone: phone}, CSRFToken: csrf})
}
func (s *Service) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Phone    string `json:"phone"`
		Password string `json:"password"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	phone := normalizePhone(in.Phone)
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
	token, csrf, err := s.issue(r.Context(), tx, id, s.ip(r), r.UserAgent())
	if err != nil {
		serverError(w)
		return
	}
	if _, err = tx.Exec(r.Context(), "UPDATE customers SET last_login_at=now() WHERE id=$1", id); err != nil {
		serverError(w)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO customer_events(customer_id,event_type,ip) VALUES($1,'LOGIN_SUCCEEDED',$2::inet)`, id, s.ip(r).String()); err != nil {
		serverError(w)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w)
		return
	}
	s.setCookie(w, token)
	httpx.JSON(w, 200, response{Customer: accountView{ID: id, FirstName: first, LastName: last, Phone: phone}, CSRFToken: csrf})
}
func invalidLogin(w http.ResponseWriter) {
	httpx.Error(w, 401, "UNAUTHENTICATED", "invalid phone or password")
}
func (s *Service) me(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, 200, view(fromContext(r.Context())))
}
func (s *Service) logout(w http.ResponseWriter, r *http.Request) {
	p := fromContext(r.Context())
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		serverError(w)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), "DELETE FROM customer_sessions WHERE token_hash=$1", p.Hash); err != nil {
		serverError(w)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO customer_events(customer_id,event_type,ip) VALUES($1,'LOGOUT',$2::inet)`, p.ID, s.ip(r).String()); err != nil {
		serverError(w)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w)
		return
	}
	s.clearCookie(w)
	w.WriteHeader(204)
}
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
	p := fromContext(r.Context())
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
	if _, err = tx.Exec(r.Context(), "DELETE FROM customer_sessions WHERE customer_id=$1 AND token_hash<>$2", p.ID, p.Hash); err != nil {
		serverError(w)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO customer_events(customer_id,event_type,ip) VALUES($1,'PASSWORD_CHANGED',$2::inet)`, p.ID, s.ip(r).String()); err != nil {
		serverError(w)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w)
		return
	}
	w.WriteHeader(204)
}
