package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/api"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/audit"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/auth"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/catalog"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/config"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/database"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/httpx"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/migrate"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/ratelimit"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/store"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/testdb"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/users"
)

const (
	origin        = "http://app.test"
	adminPassword = "admin-pass-2026"
)

type env struct {
	t      *testing.T
	ctx    context.Context
	srv    *httptest.Server
	pool   *pgxpool.Pool
	api    *api.API
	users  *users.Service
	images *catalog.ImageStore
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db := testdb.New(t)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	m, err := migrate.New(db.OwnerURL, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.Close()

	ctx := context.Background()
	pool, err := database.Open(ctx, db.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	images, err := catalog.NewImageStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	hours, _ := config.ParseOrderingHours("00:00-24:00")
	tehran, _ := time.LoadLocation("Asia/Tehran")

	q := store.New(pool)
	a := api.New(api.Deps{
		Pool:   pool,
		Logger: quiet,
		Auth: &auth.Middleware{
			Sessions: auth.NewSessions(auth.SessionConfig{IdleTimeout: 12 * time.Hour, MaxAge: 7 * 24 * time.Hour}),
			Queries:  q, AllowedOrigin: origin, Logger: quiet,
			Cookie: auth.CookieConfig{Secure: false, MaxAge: 7 * 24 * time.Hour},
		},
		LoginPerIP:   ratelimit.New(1000, time.Minute),
		LoginPerUser: ratelimit.New(5, 15*time.Minute),
		Images:       images,
		Ordering:     hours,
		Location:     tehran,
	})
	srv := httptest.NewServer(httpx.NewRouter(httpx.Deps{Logger: quiet, DB: pool, API: a.Handler(), Media: images.MediaHandler()}))
	t.Cleanup(srv.Close)

	e := &env{t: t, ctx: ctx, srv: srv, pool: pool, api: a, users: &users.Service{Pool: pool}, images: images}
	e.createUser("admin", "ADMIN", adminPassword)
	return e
}

func (e *env) createUser(username, role, password string) int64 {
	e.t.Helper()
	u, err := e.users.Create(e.ctx, audit.System, users.CreateInput{
		Username: username, DisplayName: strings.ToUpper(username), Role: role, Password: password,
	})
	if err != nil {
		e.t.Fatalf("create %s: %v", username, err)
	}
	return u.ID
}

// client is one browser: its own cookie jar and CSRF token.
type client struct {
	e    *env
	http *http.Client
	csrf string
}

func (e *env) newClient() *client {
	jar, _ := cookiejar.New(nil)
	return &client{e: e, http: &http.Client{Jar: jar}}
}

type reqOpt func(*http.Request)

func noCSRF(r *http.Request)     { r.Header.Del(auth.CSRFHeader) }
func noOrigin(r *http.Request)   { r.Header.Del("Origin") }
func withOrigin(o string) reqOpt { return func(r *http.Request) { r.Header.Set("Origin", o) } }
func withCSRF(token string) reqOpt {
	return func(r *http.Request) { r.Header.Set(auth.CSRFHeader, token) }
}

func (c *client) do(method, path string, body any, opts ...reqOpt) (*http.Response, map[string]any) {
	c.e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.e.srv.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	if c.csrf != "" {
		req.Header.Set(auth.CSRFHeader, c.csrf)
	}
	for _, o := range opts {
		o(req)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.e.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return resp, m
}

func (c *client) login(username, password string) *client {
	c.e.t.Helper()
	resp, body := c.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": username, "password": password})
	if resp.StatusCode != http.StatusOK {
		c.e.t.Fatalf("login %s: %d %v", username, resp.StatusCode, body)
	}
	c.csrf, _ = body["csrf_token"].(string)
	return c
}

func errCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	s, _ := e["code"].(string)
	return s
}

func expect(t *testing.T, resp *http.Response, body map[string]any, status int, code string) {
	t.Helper()
	if resp.StatusCode != status || (code != "" && errCode(body) != code) {
		t.Errorf("%s %s = %d %v, want %d %s", resp.Request.Method, resp.Request.URL.Path, resp.StatusCode, body, status, code)
	}
}

func TestLoginMeLogout(t *testing.T) {
	e := newEnv(t)
	c := e.newClient()
	resp, body := c.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": " Admin ", "password": adminPassword})
	expect(t, resp, body, http.StatusOK, "")

	var cookie *http.Cookie
	for _, ck := range resp.Cookies() {
		if ck.Name == "rokh_session" {
			cookie = ck
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" || cookie.MaxAge != 7*24*3600 {
		t.Fatalf("session cookie = %+v", cookie)
	}
	c.csrf = body["csrf_token"].(string)
	if user := body["user"].(map[string]any); user["role"] != "ADMIN" || user["username"] != "admin" {
		t.Errorf("user = %v", user)
	}

	resp, body = c.do(http.MethodGet, "/api/v1/auth/me", nil)
	expect(t, resp, body, http.StatusOK, "")
	if body["csrf_token"] != c.csrf {
		t.Error("/me must return the session's CSRF token")
	}

	resp, body = c.do(http.MethodPost, "/api/v1/auth/logout", nil, noCSRF)
	expect(t, resp, body, http.StatusForbidden, "CSRF_FAILED")
	resp, body = c.do(http.MethodPost, "/api/v1/auth/logout", nil)
	expect(t, resp, body, http.StatusNoContent, "")
	resp, body = c.do(http.MethodGet, "/api/v1/auth/me", nil)
	expect(t, resp, body, http.StatusUnauthorized, "UNAUTHENTICATED")

	var n int
	_ = e.pool.QueryRow(e.ctx, "SELECT count(*) FROM sessions").Scan(&n)
	if n != 0 {
		t.Errorf("sessions left after logout: %d", n)
	}
}

func TestLoginFailuresAreUniform(t *testing.T) {
	e := newEnv(t)
	op := e.createUser("barista", "OPERATOR", "barista-pass-1")
	if _, err := e.users.Update(e.ctx, audit.System, op, users.UpdateInput{IsActive: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	var messages []string
	for _, creds := range [][2]string{{"admin", "wrong-password"}, {"nobody", "whatever-123"}, {"barista", "barista-pass-1"}, {"", ""}} {
		resp, body := e.newClient().do(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": creds[0], "password": creds[1]})
		expect(t, resp, body, http.StatusUnauthorized, "UNAUTHENTICATED")
		messages = append(messages, body["error"].(map[string]any)["message"].(string))
	}
	for _, m := range messages[1:] {
		if m != messages[0] {
			t.Errorf("login error messages differ: %q", messages)
		}
	}
	resp, body := e.newClient().do(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "admin", "password": adminPassword, "role": "ADMIN"})
	expect(t, resp, body, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
}

func TestLoginRejectsForeignOrigin(t *testing.T) {
	e := newEnv(t)
	resp, body := e.newClient().do(http.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "admin", "password": adminPassword}, withOrigin("https://evil.example"))
	expect(t, resp, body, http.StatusForbidden, "CSRF_FAILED")
}

func TestLoginRateLimitPerUsername(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 5; i++ {
		resp, body := e.newClient().do(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "admin", "password": "wrong-password"})
		expect(t, resp, body, http.StatusUnauthorized, "")
	}
	resp, body := e.newClient().do(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "ADMIN", "password": adminPassword})
	expect(t, resp, body, http.StatusTooManyRequests, "RATE_LIMITED")
	if resp.Header.Get("Retry-After") == "" {
		t.Error("missing Retry-After")
	}
}

// AC-34
func TestCSRFAndOriginRequiredForAdminWrites(t *testing.T) {
	e := newEnv(t)
	c := e.newClient().login("admin", adminPassword)
	newUser := map[string]string{"username": "cashier", "display_name": "Cashier", "role": "OPERATOR", "password": "cashier-pass-1"}

	resp, body := c.do(http.MethodPost, "/api/v1/admin/users", newUser, noCSRF)
	expect(t, resp, body, http.StatusForbidden, "CSRF_FAILED")
	resp, body = c.do(http.MethodPost, "/api/v1/admin/users", newUser, withCSRF("forged"))
	expect(t, resp, body, http.StatusForbidden, "CSRF_FAILED")
	resp, body = c.do(http.MethodPost, "/api/v1/admin/users", newUser, withOrigin("https://evil.example"))
	expect(t, resp, body, http.StatusForbidden, "CSRF_FAILED")
	resp, body = c.do(http.MethodPost, "/api/v1/admin/users", newUser, noOrigin)
	expect(t, resp, body, http.StatusForbidden, "CSRF_FAILED")
	resp, body = c.do(http.MethodPost, "/api/v1/admin/users", newUser, noOrigin, func(r *http.Request) { r.Header.Set("Referer", origin+"/admin/users") })
	expect(t, resp, body, http.StatusCreated, "")

	other := e.newClient().login("admin", adminPassword)
	resp, body = c.do(http.MethodPatch, "/api/v1/admin/users/2", map[string]any{"display_name": "x"}, withCSRF(other.csrf))
	expect(t, resp, body, http.StatusForbidden, "CSRF_FAILED") // another session's token does not work

	resp, body = e.newClient().do(http.MethodGet, "/api/v1/admin/users", nil)
	expect(t, resp, body, http.StatusUnauthorized, "UNAUTHENTICATED")
}

// AC-33 (for the routes that exist so far)
func TestOperatorIsForbiddenFromAdminRoutes(t *testing.T) {
	e := newEnv(t)
	e.createUser("barista", "OPERATOR", "barista-pass-1")
	op := e.newClient().login("barista", "barista-pass-1")
	for _, r := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/admin/users"}, {http.MethodPost, "/api/v1/admin/users"},
		{http.MethodPatch, "/api/v1/admin/users/1"}, {http.MethodPost, "/api/v1/admin/users/1/password"},
		{http.MethodGet, "/api/v1/admin/audit-logs"},
	} {
		resp, body := op.do(r.method, r.path, map[string]any{})
		expect(t, resp, body, http.StatusForbidden, "FORBIDDEN")
	}
	resp, body := op.do(http.MethodPost, "/api/v1/auth/password", map[string]string{"current_password": "barista-pass-1", "new_password": "barista-pass-2"})
	expect(t, resp, body, http.StatusNoContent, "")
}

func TestUsersAPIAndAudit(t *testing.T) {
	e := newEnv(t)
	c := e.newClient().login("admin", adminPassword)

	resp, body := c.do(http.MethodPost, "/api/v1/admin/users", map[string]string{"username": "Cashier", "display_name": " Sara ", "role": "OPERATOR", "password": "cashier-pass-1"})
	expect(t, resp, body, http.StatusCreated, "")
	if body["username"] != "cashier" || body["display_name"] != "Sara" || body["is_active"] != true {
		t.Errorf("created = %v", body)
	}
	resp, body = c.do(http.MethodPost, "/api/v1/admin/users", map[string]string{"username": "cashier", "display_name": "Dup", "role": "OPERATOR", "password": "cashier-pass-1"})
	expect(t, resp, body, http.StatusConflict, "CONFLICT")
	resp, body = c.do(http.MethodPost, "/api/v1/admin/users", map[string]string{"username": "x", "display_name": "", "role": "OWNER", "password": "1234567890"})
	expect(t, resp, body, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
	fields := body["error"].(map[string]any)["details"].(map[string]any)["fields"].(map[string]any)
	for _, f := range []string{"username", "display_name", "role", "password"} {
		if fields[f] == nil {
			t.Errorf("missing validation error for %s: %v", f, fields)
		}
	}

	resp, body = c.do(http.MethodPatch, "/api/v1/admin/users/2", map[string]any{"display_name": "Sara K", "role": "ADMIN"})
	expect(t, resp, body, http.StatusOK, "")
	if body["role"] != "ADMIN" || body["display_name"] != "Sara K" {
		t.Errorf("updated = %v", body)
	}
	resp, body = c.do(http.MethodPatch, "/api/v1/admin/users/999", map[string]any{"display_name": "x"})
	expect(t, resp, body, http.StatusNotFound, "NOT_FOUND")

	resp, body = c.do(http.MethodGet, "/api/v1/admin/users", nil)
	expect(t, resp, body, http.StatusOK, "")
	if items := body["items"].([]any); len(items) != 2 {
		t.Errorf("users = %v", items)
	}

	resp, body = c.do(http.MethodGet, "/api/v1/admin/audit-logs?entity_type=user&limit=10", nil)
	expect(t, resp, body, http.StatusOK, "")
	var actions []string
	raw, _ := json.Marshal(body)
	for _, it := range body["items"].([]any) {
		actions = append(actions, it.(map[string]any)["action"].(string))
	}
	joined := strings.Join(actions, ",")
	for _, want := range []string{"user.update", "user.create", "auth.login"} {
		if !strings.Contains(joined, want) {
			t.Errorf("audit actions %s missing %s", joined, want)
		}
	}
	if strings.Contains(strings.ToLower(string(raw)), "pass") {
		t.Errorf("audit output mentions a password: %s", raw)
	}
}

// AC-35
func TestDeactivationAndRoleChangeRevokeSessions(t *testing.T) {
	e := newEnv(t)
	e.createUser("barista", "OPERATOR", "barista-pass-1")
	admin := e.newClient().login("admin", adminPassword)
	op := e.newClient().login("barista", "barista-pass-1")

	resp, body := admin.do(http.MethodPatch, "/api/v1/admin/users/2", map[string]any{"is_active": false})
	expect(t, resp, body, http.StatusOK, "")
	resp, body = op.do(http.MethodGet, "/api/v1/auth/me", nil)
	expect(t, resp, body, http.StatusUnauthorized, "UNAUTHENTICATED")
	resp, body = e.newClient().do(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "barista", "password": "barista-pass-1"})
	expect(t, resp, body, http.StatusUnauthorized, "UNAUTHENTICATED")

	admin.do(http.MethodPatch, "/api/v1/admin/users/2", map[string]any{"is_active": true})
	op = e.newClient().login("barista", "barista-pass-1")
	resp, body = admin.do(http.MethodPatch, "/api/v1/admin/users/2", map[string]any{"role": "ADMIN"})
	expect(t, resp, body, http.StatusOK, "")
	resp, body = op.do(http.MethodGet, "/api/v1/auth/me", nil)
	expect(t, resp, body, http.StatusUnauthorized, "UNAUTHENTICATED")
}

func TestPasswordChangeKeepsOnlyCurrentSession(t *testing.T) {
	e := newEnv(t)
	first := e.newClient().login("admin", adminPassword)
	second := e.newClient().login("admin", adminPassword)

	resp, body := first.do(http.MethodPost, "/api/v1/auth/password", map[string]string{"current_password": "nope-nope-nope", "new_password": "admin-pass-2027"})
	expect(t, resp, body, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
	resp, body = first.do(http.MethodPost, "/api/v1/auth/password", map[string]string{"current_password": adminPassword, "new_password": "short"})
	expect(t, resp, body, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
	resp, body = first.do(http.MethodPost, "/api/v1/auth/password", map[string]string{"current_password": adminPassword, "new_password": "admin-pass-2027"})
	expect(t, resp, body, http.StatusNoContent, "")

	resp, body = first.do(http.MethodGet, "/api/v1/auth/me", nil)
	expect(t, resp, body, http.StatusOK, "")
	resp, body = second.do(http.MethodGet, "/api/v1/auth/me", nil)
	expect(t, resp, body, http.StatusUnauthorized, "UNAUTHENTICATED")
	resp, body = e.newClient().do(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "admin", "password": adminPassword})
	expect(t, resp, body, http.StatusUnauthorized, "")
	e.newClient().login("admin", "admin-pass-2027")
}

func TestAdminPasswordResetRevokesTargetSessions(t *testing.T) {
	e := newEnv(t)
	e.createUser("barista", "OPERATOR", "barista-pass-1")
	admin := e.newClient().login("admin", adminPassword)
	op := e.newClient().login("barista", "barista-pass-1")

	resp, body := admin.do(http.MethodPost, "/api/v1/admin/users/2/password", map[string]string{"new_password": "barista-pass-9"})
	expect(t, resp, body, http.StatusNoContent, "")
	resp, body = op.do(http.MethodGet, "/api/v1/auth/me", nil)
	expect(t, resp, body, http.StatusUnauthorized, "")
	e.newClient().login("barista", "barista-pass-9")
}

// AC-36
func TestLastActiveAdminIsProtected(t *testing.T) {
	e := newEnv(t)
	c := e.newClient().login("admin", adminPassword)
	for _, change := range []map[string]any{{"is_active": false}, {"role": "OPERATOR"}} {
		resp, body := c.do(http.MethodPatch, "/api/v1/admin/users/1", change)
		expect(t, resp, body, http.StatusConflict, "CONFLICT")
	}
	e.createUser("second", "ADMIN", "second-pass-1")
	resp, body := c.do(http.MethodPatch, "/api/v1/admin/users/2", map[string]any{"role": "OPERATOR"})
	expect(t, resp, body, http.StatusOK, "")
}

// R-F11-3: two remaining admins deactivating each other at the same time.
func TestConcurrentMutualDeactivationKeepsOneAdmin(t *testing.T) {
	for round := 0; round < 5; round++ {
		e := newEnv(t)
		e.createUser("second", "ADMIN", "second-pass-1")
		a := e.newClient().login("admin", adminPassword)
		b := e.newClient().login("second", "second-pass-1")

		var wg sync.WaitGroup
		codes := make([]int, 2)
		start := make(chan struct{})
		for i, pair := range []struct {
			c      *client
			target string
		}{{a, "2"}, {b, "1"}} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				resp, _ := pair.c.do(http.MethodPatch, "/api/v1/admin/users/"+pair.target, map[string]any{"is_active": false})
				codes[i] = resp.StatusCode
			}()
		}
		close(start)
		wg.Wait()

		ok := 0
		for _, c := range codes {
			if c == http.StatusOK {
				ok++
			} else if c != http.StatusConflict && c != http.StatusUnauthorized {
				t.Errorf("round %d: unexpected status %d", round, c)
			}
		}
		var admins int
		_ = e.pool.QueryRow(e.ctx, "SELECT count(*) FROM users WHERE role = 'ADMIN' AND is_active").Scan(&admins)
		if ok != 1 || admins != 1 {
			t.Fatalf("round %d: statuses %v, active admins %d; want exactly one success and one admin left", round, codes, admins)
		}
	}
}

func TestIdleSessionExpires(t *testing.T) {
	e := newEnv(t)
	c := e.newClient().login("admin", adminPassword)
	if _, err := e.pool.Exec(e.ctx, "UPDATE sessions SET last_seen_at = now() - interval '13 hours'"); err != nil {
		t.Fatal(err)
	}
	resp, body := c.do(http.MethodGet, "/api/v1/auth/me", nil)
	expect(t, resp, body, http.StatusUnauthorized, "UNAUTHENTICATED")
	var n int
	_ = e.pool.QueryRow(e.ctx, "SELECT count(*) FROM sessions").Scan(&n)
	if n != 0 {
		t.Errorf("idle session not removed: %d", n)
	}
}

// AC-32: every admin route is bound to a known policy action.
func TestEveryAdminRouteHasAPolicyAction(t *testing.T) {
	e := newEnv(t)
	perms := e.api.Permissions()
	seen := 0
	err := chi.Walk(e.api.Handler().(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		route = strings.TrimSuffix(route, "/")
		if !strings.HasPrefix(route, "/admin/") {
			return nil
		}
		seen++
		act, ok := perms[method+" /api/v1"+route]
		if !ok || !auth.Known(act) {
			t.Errorf("admin route %s %s has no policy action", method, route)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen == 0 || seen != len(perms) {
		t.Errorf("walked %d admin routes, %d registered with a policy", seen, len(perms))
	}
}

func ptr[T any](v T) *T { return &v }

// Review finding: a login that verified the old password must not create a session after a
// concurrent password reset has revoked everything.
func TestLoginRacingPasswordResetLeavesNoStaleSession(t *testing.T) {
	e := newEnv(t)
	op := e.createUser("barista", "OPERATOR", "barista-pass-1")
	for round := 0; round < 20; round++ {
		newPassword := fmt.Sprintf("barista-pass-new-%02d", round)
		oldPassword := "barista-pass-1"
		if round > 0 {
			oldPassword = fmt.Sprintf("barista-pass-new-%02d", round-1)
		}
		var wg sync.WaitGroup
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			e.newClient().do(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "barista", "password": oldPassword})
		}()
		go func() {
			defer wg.Done()
			<-start
			if err := e.users.ResetPassword(e.ctx, audit.System, op, newPassword); err != nil {
				t.Errorf("reset: %v", err)
			}
		}()
		close(start)
		wg.Wait()

		var n int
		if err := e.pool.QueryRow(e.ctx, "SELECT count(*) FROM sessions WHERE user_id = $1", op).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("round %d: %d session(s) survived a password reset", round, n)
		}
	}
}
