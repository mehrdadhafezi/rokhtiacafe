package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/auth"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/httpx"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/push"
)

// pushEnabled answers 503 PUSH_NOT_CONFIGURED when the server has no VAPID keys.
func (a *API) pushEnabled(w http.ResponseWriter) bool {
	if a.d.Push == nil || a.d.VAPIDPublicKey == "" {
		httpx.Error(w, http.StatusServiceUnavailable, "PUSH_NOT_CONFIGURED", "web push is not configured on this server")
		return false
	}
	return true
}

// GET /admin/push/vapid-public-key
func (a *API) vapidPublicKey(w http.ResponseWriter, r *http.Request) {
	if !a.pushEnabled(w) {
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"key": a.d.VAPIDPublicKey})
}

// POST /admin/push/subscriptions {endpoint, keys: {p256dh, auth}} — upsert, bound to the caller.
func (a *API) pushSubscribe(w http.ResponseWriter, r *http.Request) {
	if !a.pushEnabled(w) {
		return
	}
	var in struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	p := auth.FromContext(r.Context())
	sub, err := a.d.Push.Subscribe(r.Context(), a.actor(r), p.UserID, push.SubscribeInput{
		Endpoint: in.Endpoint, P256dh: in.Keys.P256dh, Auth: in.Keys.Auth, UserAgent: r.UserAgent(),
	})
	var bad push.ValidationError
	if errors.As(err, &bad) {
		httpx.ValidationError(w, bad)
		return
	}
	if err != nil {
		a.internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"id": sub.ID, "created_at": sub.CreatedAt.Time})
}

// DELETE /admin/push/subscriptions {endpoint} — removes the caller's subscription (idempotent).
func (a *API) pushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Endpoint string `json:"endpoint"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	if a.d.Push == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := a.d.Push.Unsubscribe(r.Context(), a.actor(r), auth.FromContext(r.Context()).UserID, in.Endpoint); err != nil {
		a.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /admin/push/test — queues a TEST notification to each of the caller's subscriptions.
func (a *API) pushTest(w http.ResponseWriter, r *http.Request) {
	if !a.pushEnabled(w) {
		return
	}
	n, err := a.d.Push.SendTest(r.Context(), auth.FromContext(r.Context()).UserID, time.Now())
	if errors.Is(err, push.ErrNoSubscription) {
		httpx.Error(w, http.StatusConflict, "CONFLICT", "no push subscription for this user")
		return
	}
	if err != nil {
		a.internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusAccepted, map[string]int{"queued": n})
}
