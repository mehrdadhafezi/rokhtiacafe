package api_test

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/api"
)

func browserKeys(t *testing.T) map[string]string {
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	a := make([]byte, 16)
	_, _ = rand.Read(a)
	return map[string]string{
		"p256dh": base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()),
		"auth":   base64.RawURLEncoding.EncodeToString(a),
	}
}

func TestPushSubscriptionsAdminOnly(t *testing.T) {
	e := newEnv(t)
	e.createUser("barista", "OPERATOR", "barista-pass-1")
	op := e.newClient().login("barista", "barista-pass-1")
	sub := map[string]any{"endpoint": "https://fcm.googleapis.com/fcm/send/op", "keys": browserKeys(t)}
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/v1/admin/push/vapid-public-key", nil},
		{http.MethodPost, "/api/v1/admin/push/subscriptions", sub},
		{http.MethodDelete, "/api/v1/admin/push/subscriptions", map[string]any{"endpoint": "x"}},
		{http.MethodPost, "/api/v1/admin/push/test", nil},
	} {
		resp, body := op.do(c.method, c.path, c.body)
		expect(t, resp, body, http.StatusForbidden, "FORBIDDEN")
	}
	resp, body := e.newClient().do(http.MethodGet, "/api/v1/admin/push/vapid-public-key", nil)
	expect(t, resp, body, http.StatusUnauthorized, "UNAUTHENTICATED")
	var n int
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM push_subscriptions`).Scan(&n); err != nil || n != 0 {
		t.Errorf("subscriptions stored: %d %v", n, err)
	}
}

// R-F11-1: unsafe endpoints are rejected with 422 before anything is stored.
func TestPushSubscribeRejectsUnsafeEndpoints(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	keys := browserKeys(t)
	for _, endpoint := range []string{
		"https://127.0.0.1/push",
		"https://169.254.169.254/latest/meta-data/",
		"http://fcm.googleapis.com/fcm/send/x",
		"https://attacker.example/fcm/send/x",
		"https://fcm.googleapis.com:8443/x",
		"https://" + strings.Repeat("a", 2100) + ".fcm.googleapis.com/",
	} {
		resp, body := admin.do(http.MethodPost, "/api/v1/admin/push/subscriptions", map[string]any{"endpoint": endpoint, "keys": keys})
		expect(t, resp, body, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
	}
	// Malformed keys are rejected too (they could never be encrypted for).
	for _, bad := range []map[string]string{
		{"p256dh": "short", "auth": keys["auth"]},
		{"p256dh": keys["p256dh"], "auth": base64.RawURLEncoding.EncodeToString(make([]byte, 8))},
	} {
		resp, body := admin.do(http.MethodPost, "/api/v1/admin/push/subscriptions",
			map[string]any{"endpoint": "https://fcm.googleapis.com/fcm/send/k", "keys": bad})
		expect(t, resp, body, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
	}
	var n int
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM push_subscriptions`).Scan(&n); err != nil || n != 0 {
		t.Errorf("unsafe subscriptions stored: %d %v", n, err)
	}
}

func TestPushSubscribeUnsubscribeAndTest(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	e.createUser("owner2", "ADMIN", "owner2-pass-1")
	other := e.newClient().login("owner2", "owner2-pass-1")

	resp, body := admin.do(http.MethodGet, "/api/v1/admin/push/vapid-public-key", nil)
	expect(t, resp, body, http.StatusOK, "")
	if body["key"] != "test-vapid-public-key" {
		t.Errorf("key = %v", body["key"])
	}

	// No subscription yet: the test notification has nowhere to go.
	resp, body = admin.do(http.MethodPost, "/api/v1/admin/push/test", nil)
	expect(t, resp, body, http.StatusConflict, "CONFLICT")

	endpoint := "https://fcm.googleapis.com/fcm/send/device-1"
	sub := map[string]any{"endpoint": endpoint, "keys": browserKeys(t)}
	resp, body = admin.do(http.MethodPost, "/api/v1/admin/push/subscriptions", sub)
	expect(t, resp, body, http.StatusOK, "")
	resp, body = admin.do(http.MethodPost, "/api/v1/admin/push/subscriptions", sub) // re-subscribe: upsert
	expect(t, resp, body, http.StatusOK, "")
	var subs, audits int
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM push_subscriptions`).Scan(&subs); err != nil || subs != 1 {
		t.Fatalf("subscriptions = %d %v", subs, err)
	}
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM audit_logs WHERE action = 'push.subscribe' AND NOT details::text LIKE '%device-1%'`).Scan(&audits); err != nil || audits != 2 {
		t.Errorf("audit rows without the endpoint = %d %v", audits, err)
	}

	resp, body = admin.do(http.MethodPost, "/api/v1/admin/push/test", nil)
	expect(t, resp, body, http.StatusAccepted, "")
	var events int
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM notification_events WHERE event_type = 'TEST' AND status = 'PENDING'`).Scan(&events); err != nil || events != 1 {
		t.Errorf("queued test events = %d %v", events, err)
	}

	// Another admin cannot remove someone else's device.
	resp, _ = other.do(http.MethodDelete, "/api/v1/admin/push/subscriptions", map[string]any{"endpoint": endpoint})
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("delete by other admin = %d", resp.StatusCode)
	}
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM push_subscriptions`).Scan(&subs); err != nil || subs != 1 {
		t.Errorf("other admin removed the subscription")
	}
	resp, _ = admin.do(http.MethodDelete, "/api/v1/admin/push/subscriptions", map[string]any{"endpoint": endpoint})
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("delete = %d", resp.StatusCode)
	}
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM push_subscriptions`).Scan(&subs); err != nil || subs != 0 {
		t.Errorf("subscription not removed")
	}
	// The queued event survives with its subscription cleared and will be FAILED by the worker.
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM notification_events WHERE subscription_id IS NULL`).Scan(&events); err != nil || events != 1 {
		t.Errorf("orphaned event = %d %v", events, err)
	}
}

func TestPushNotConfigured(t *testing.T) {
	e := newEnv(t, func(d *api.Deps) { d.Push, d.VAPIDPublicKey = nil, "" })
	admin := e.newClient().login("admin", adminPassword)
	resp, body := admin.do(http.MethodGet, "/api/v1/admin/push/vapid-public-key", nil)
	expect(t, resp, body, http.StatusServiceUnavailable, "PUSH_NOT_CONFIGURED")
	resp, body = admin.do(http.MethodPost, "/api/v1/admin/push/subscriptions",
		map[string]any{"endpoint": "https://fcm.googleapis.com/fcm/send/x", "keys": browserKeys(t)})
	expect(t, resp, body, http.StatusServiceUnavailable, "PUSH_NOT_CONFIGURED")
}
