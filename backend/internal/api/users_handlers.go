package api

import (
	"errors"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/audit"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/auth"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/httpx"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/store"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/users"
)

type adminUserView struct {
	ID          int64      `json:"id"`
	Username    string     `json:"username"`
	DisplayName string     `json:"display_name"`
	Role        string     `json:"role"`
	IsActive    bool       `json:"is_active"`
	LastLoginAt *time.Time `json:"last_login_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

func toAdminUser(u store.User) adminUserView {
	v := adminUserView{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Role: u.Role,
		IsActive: u.IsActive, CreatedAt: u.CreatedAt.Time}
	if u.LastLoginAt.Valid {
		t := u.LastLoginAt.Time
		v.LastLoginAt = &t
	}
	return v
}

func (a *API) actor(r *http.Request) audit.Actor {
	return audit.UserActor(auth.FromContext(r.Context()).UserID, a.clientIP(r))
}

// writeUserError maps users service errors to the API contract.
func (a *API) writeUserError(w http.ResponseWriter, r *http.Request, err error) {
	var bad users.ValidationError
	switch {
	case errors.As(err, &bad):
		httpx.ValidationError(w, bad)
	case errors.Is(err, users.ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "user not found")
	case errors.Is(err, users.ErrUsernameTaken):
		httpx.Error(w, http.StatusConflict, "CONFLICT", "username already exists")
	case errors.Is(err, users.ErrLastAdmin):
		httpx.Error(w, http.StatusConflict, "CONFLICT", "at least one active ADMIN must remain")
	default:
		a.internal(w, r, err)
	}
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "not found")
		return 0, false
	}
	return id, true
}

// GET /admin/users
func (a *API) listUsers(w http.ResponseWriter, r *http.Request) {
	list, err := a.users.List(r.Context())
	if err != nil {
		a.internal(w, r, err)
		return
	}
	out := make([]adminUserView, len(list))
	for i, u := range list {
		out[i] = toAdminUser(u)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": out})
}

// POST /admin/users
func (a *API) createUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Role        string `json:"role"`
		Password    string `json:"password"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	u, err := a.users.Create(r.Context(), a.actor(r), users.CreateInput{
		Username: in.Username, DisplayName: in.DisplayName, Role: in.Role, Password: in.Password,
	})
	if err != nil {
		a.writeUserError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, toAdminUser(u))
}

// PATCH /admin/users/{id}
func (a *API) updateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		DisplayName *string `json:"display_name"`
		Role        *string `json:"role"`
		IsActive    *bool   `json:"is_active"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	u, err := a.users.Update(r.Context(), a.actor(r), id, users.UpdateInput{
		DisplayName: in.DisplayName, Role: in.Role, IsActive: in.IsActive,
	})
	if err != nil {
		a.writeUserError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, toAdminUser(u))
}

// POST /admin/users/{id}/password
func (a *API) resetPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		NewPassword string `json:"new_password"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	if err := a.users.ResetPassword(r.Context(), a.actor(r), id, in.NewPassword); err != nil {
		a.writeUserError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type auditView struct {
	ID          int64     `json:"id"`
	ActorUserID *int64    `json:"actor_user_id"`
	ActorKind   string    `json:"actor_kind"`
	Action      string    `json:"action"`
	EntityType  string    `json:"entity_type"`
	EntityID    string    `json:"entity_id"`
	Details     rawJSON   `json:"details"`
	IP          *string   `json:"ip"`
	CreatedAt   time.Time `json:"created_at"`
}

type rawJSON []byte

func (j rawJSON) MarshalJSON() ([]byte, error) {
	if len(j) == 0 {
		return []byte("{}"), nil
	}
	return j, nil
}

// GET /admin/audit-logs?entity_type&entity_id&before_id&limit
func (a *API) listAuditLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	params := store.ListAuditLogsParams{RowLimit: 50}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			httpx.ValidationError(w, map[string]string{"limit": "1-100"})
			return
		}
		params.RowLimit = int32(n)
	}
	if v := q.Get("before_id"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			httpx.ValidationError(w, map[string]string{"before_id": "invalid"})
			return
		}
		params.BeforeID = pgtype.Int8{Int64: n, Valid: true}
	}
	if v := q.Get("entity_type"); v != "" {
		params.EntityType = pgtype.Text{String: v, Valid: true}
	}
	if v := q.Get("entity_id"); v != "" {
		params.EntityID = pgtype.Text{String: v, Valid: true}
	}
	rows, err := a.q.ListAuditLogs(r.Context(), params)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	out := make([]auditView, len(rows))
	for i, row := range rows {
		v := auditView{ID: row.ID, ActorKind: row.ActorKind, Action: row.Action, EntityType: row.EntityType,
			EntityID: row.EntityID, Details: rawJSON(row.Details), CreatedAt: row.CreatedAt.Time}
		if row.ActorUserID.Valid {
			id := row.ActorUserID.Int64
			v.ActorUserID = &id
		}
		if row.Ip != nil {
			s := row.Ip.String()
			v.IP = &s
		}
		out[i] = v
	}
	var next *int64
	if len(rows) == int(params.RowLimit) {
		last := rows[len(rows)-1].ID
		next = &last
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": out, "next_before_id": next})
}

func addrPtr(a netip.Addr) *netip.Addr {
	if !a.IsValid() {
		return nil
	}
	return &a
}
