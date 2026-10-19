package api

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/audit"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/auth"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/httpx"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/store"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/users"
)

type userView struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

type sessionView struct {
	User      userView `json:"user"`
	CSRFToken string   `json:"csrf_token"`
}

func principalView(p *auth.Principal) userView {
	return userView{ID: p.UserID, Username: p.Username, DisplayName: p.DisplayName, Role: string(p.Role)}
}

func rateLimited(w http.ResponseWriter, wait time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
	httpx.Error(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many attempts, try again later")
}

func unauthenticated(w http.ResponseWriter) {
	httpx.Error(w, http.StatusUnauthorized, "UNAUTHENTICATED", "invalid username or password")
}

// POST /auth/login
func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	username := users.NormalizeUsername(in.Username)
	ip := a.clientIP(r)

	if ok, wait := a.d.LoginPerIP.Allow(ip.String()); !ok {
		rateLimited(w, wait)
		return
	}
	if ok, wait := a.d.LoginPerUser.Allow(username); !ok {
		rateLimited(w, wait)
		return
	}
	if username == "" || in.Password == "" || len(in.Password) > 4*auth.MaxPasswordLen {
		auth.VerifyDummy(in.Password)
		unauthenticated(w)
		return
	}

	u, err := a.q.GetUserByUsername(r.Context(), username)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !u.IsActive) {
		auth.VerifyDummy(in.Password)
		a.d.Logger.Warn("login failed", "request_id", httpx.RequestIDFrom(r.Context()), "username", username, "reason", "unknown or inactive")
		unauthenticated(w)
		return
	}
	if err != nil {
		a.internal(w, r, err)
		return
	}
	ok, err := auth.VerifyPassword(in.Password, u.PasswordHash)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	if !ok {
		a.d.Logger.Warn("login failed", "request_id", httpx.RequestIDFrom(r.Context()), "username", username, "reason", "bad password")
		unauthenticated(w)
		return
	}

	var token, csrf string
	err = store.InTx(r.Context(), a.d.Pool, func(q *store.Queries) error {
		var err error
		token, csrf, err = a.d.Auth.Sessions.Create(r.Context(), q, u.ID, addrPtr(ip), r.UserAgent())
		if err != nil {
			return err
		}
		if err := q.TouchUserLogin(r.Context(), u.ID); err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.UserActor(u.ID, ip), audit.Entry{
			Action: "auth.login", EntityType: "user", EntityID: strconv.FormatInt(u.ID, 10),
		})
	})
	if err != nil {
		a.internal(w, r, err)
		return
	}
	a.d.Auth.SetCookie(w, token)
	httpx.JSON(w, http.StatusOK, sessionView{
		User:      userView{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Role: u.Role},
		CSRFToken: csrf,
	})
}

// GET /auth/me
func (a *API) me(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	httpx.JSON(w, http.StatusOK, sessionView{User: principalView(p), CSRFToken: p.CSRFToken})
}

// POST /auth/logout
func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	if err := a.q.DeleteSession(r.Context(), p.TokenHash()); err != nil {
		a.internal(w, r, err)
		return
	}
	a.d.Auth.ClearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// POST /auth/password — change own password; other sessions are revoked, this one stays.
func (a *API) changePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	p := auth.FromContext(r.Context())
	u, err := a.q.GetUserByID(r.Context(), p.UserID)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	if ok, err := auth.VerifyPassword(in.CurrentPassword, u.PasswordHash); err != nil {
		a.internal(w, r, err)
		return
	} else if !ok {
		httpx.ValidationError(w, map[string]string{"current_password": "incorrect"})
		return
	}
	if err := auth.ValidatePassword(in.NewPassword); err != nil {
		httpx.ValidationError(w, map[string]string{"new_password": "policy"})
		return
	}
	hash, err := auth.HashPassword(in.NewPassword)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	err = store.InTx(r.Context(), a.d.Pool, func(q *store.Queries) error {
		if err := q.SetUserPassword(r.Context(), store.SetUserPasswordParams{ID: u.ID, PasswordHash: hash}); err != nil {
			return err
		}
		if err := q.DeleteUserSessionsExcept(r.Context(), store.DeleteUserSessionsExceptParams{
			UserID: u.ID, KeepTokenHash: p.TokenHash(),
		}); err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.UserActor(u.ID, a.clientIP(r)), audit.Entry{
			Action: "user.password_change", EntityType: "user", EntityID: strconv.FormatInt(u.ID, 10),
		})
	})
	if err != nil {
		a.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) internal(w http.ResponseWriter, r *http.Request, err error) {
	a.d.Logger.Error("request failed", "request_id", httpx.RequestIDFrom(r.Context()),
		"route", strings.TrimSpace(r.Method+" "+r.URL.Path), "err", err)
	httpx.Error(w, http.StatusInternalServerError, "INTERNAL", "internal error")
}
