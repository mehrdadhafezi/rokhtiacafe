package push

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/audit"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/store"
)

// ValidationError lists invalid fields.
type ValidationError map[string]string

func (v ValidationError) Error() string { return "invalid push subscription" }

// ErrNoSubscription means the user has no subscription to send a test notification to.
var ErrNoSubscription = errors.New("no push subscription for this user")

// Service manages subscriptions and enqueues test notifications.
type Service struct {
	Pool      *pgxpool.Pool
	Allowlist Allowlist
}

// SubscribeInput is the browser's PushSubscription (JSON form) plus its user agent.
type SubscribeInput struct {
	Endpoint  string
	P256dh    string
	Auth      string
	UserAgent string
}

// decodeKey accepts base64url with or without padding (browsers send unpadded).
func decodeKey(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

// Subscribe stores (or rebinds) the endpoint for the given ADMIN user.
func (s *Service) Subscribe(ctx context.Context, actor audit.Actor, userID int64, in SubscribeInput) (store.PushSubscription, error) {
	bad := ValidationError{}
	if err := ValidateEndpoint(in.Endpoint, s.Allowlist); err != nil {
		bad["endpoint"] = strings.TrimPrefix(err.Error(), ErrEndpoint.Error()+": ")
	}
	// P-256 public key (uncompressed point) and a 16-byte auth secret (RFC 8291).
	if k, err := decodeKey(in.P256dh); err != nil || len(k) != 65 || k[0] != 4 {
		bad["keys.p256dh"] = "invalid"
	}
	if k, err := decodeKey(in.Auth); err != nil || len(k) != 16 {
		bad["keys.auth"] = "invalid"
	}
	if len(bad) > 0 {
		return store.PushSubscription{}, bad
	}
	ua := strings.TrimSpace(in.UserAgent)
	if r := []rune(ua); len(r) > 300 {
		ua = string(r[:300])
	}
	var sub store.PushSubscription
	err := store.InTx(ctx, s.Pool, func(q *store.Queries) error {
		var err error
		sub, err = q.UpsertPushSubscription(ctx, store.UpsertPushSubscriptionParams{
			UserID: userID, Endpoint: in.Endpoint, P256dh: in.P256dh, Auth: in.Auth,
			UserAgent: pgtype.Text{String: ua, Valid: ua != ""},
		})
		if err != nil {
			return err
		}
		// Only the push-service host is audited: the full endpoint is a capability URL.
		host := ""
		if u, err := url.Parse(in.Endpoint); err == nil {
			host = u.Hostname()
		}
		return audit.Record(ctx, q, actor, audit.Entry{
			Action: "push.subscribe", EntityType: "push_subscription", EntityID: strconv.FormatInt(sub.ID, 10),
			Details: map[string]any{"host": host},
		})
	})
	return sub, err
}

// Unsubscribe removes the user's subscription for endpoint; removing a missing one is a no-op.
func (s *Service) Unsubscribe(ctx context.Context, actor audit.Actor, userID int64, endpoint string) error {
	return store.InTx(ctx, s.Pool, func(q *store.Queries) error {
		n, err := q.DeleteUserPushSubscription(ctx, store.DeleteUserPushSubscriptionParams{Endpoint: endpoint, UserID: userID})
		if err != nil || n == 0 {
			return err
		}
		return audit.Record(ctx, q, actor, audit.Entry{
			Action: "push.unsubscribe", EntityType: "user", EntityID: strconv.FormatInt(userID, 10),
		})
	})
}

// testPayload is shown by the staff service worker.
var testPayload, _ = json.Marshal(map[string]any{
	"type": "TEST", "title": "اعلان آزمایشی رخ تیا",
	"body": "اعلان‌های موجودی کم روی این دستگاه فعال است.", "url": "/admin/account",
})

// SendTest queues one TEST notification for each of the user's subscriptions.
func (s *Service) SendTest(ctx context.Context, userID int64, now time.Time) (int, error) {
	var n int
	err := store.InTx(ctx, s.Pool, func(q *store.Queries) error {
		subs, err := q.ListUserPushSubscriptions(ctx, userID)
		if err != nil {
			return err
		}
		if len(subs) == 0 {
			return ErrNoSubscription
		}
		key := "TEST:" + strconv.FormatInt(userID, 10) + ":" + strconv.FormatInt(now.UnixMicro(), 10)
		for _, id := range subs {
			if err := q.InsertNotificationEvent(ctx, store.InsertNotificationEventParams{
				EventType: "TEST", DedupKey: key, SubscriptionID: pgtype.Int8{Int64: id, Valid: true}, Payload: testPayload,
			}); err != nil {
				return err
			}
		}
		n = len(subs)
		return nil
	})
	return n, err
}
