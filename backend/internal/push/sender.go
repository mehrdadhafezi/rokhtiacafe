package push

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// Message is one encrypted push to one subscription.
type Message struct {
	Endpoint string
	P256dh   string
	Auth     string
	Payload  []byte
	Topic    string // collapses undelivered duplicates at the push service ("" = none)
}

// Result is the push service's answer.
type Result struct {
	Status     int
	RetryAfter time.Duration
}

// Sender delivers one message. Network failures return an error; HTTP answers return a Result.
type Sender interface {
	Send(ctx context.Context, m Message) (Result, error)
}

// TTL keeps an undelivered notification at the push service for 6 hours (WORKFLOWS §4.2).
const TTL = 21600

// maxResponseBody is how much of a push service response is read before it is discarded.
const maxResponseBody = 4 << 10

// WebPushSender encrypts with the subscription keys and signs with the VAPID key pair.
type WebPushSender struct {
	Client     webpush.HTTPClient
	PublicKey  string
	PrivateKey string
	Subject    string // "mailto:…" or "https://…"
}

// Send implements Sender.
func (s WebPushSender) Send(ctx context.Context, m Message) (Result, error) {
	resp, err := webpush.SendNotificationWithContext(ctx, m.Payload,
		&webpush.Subscription{Endpoint: m.Endpoint, Keys: webpush.Keys{P256dh: m.P256dh, Auth: m.Auth}},
		&webpush.Options{
			HTTPClient: s.Client,
			// The library adds "mailto:" itself unless the subject is an https URL.
			Subscriber:      strings.TrimPrefix(s.Subject, "mailto:"),
			Topic:           m.Topic,
			TTL:             TTL,
			Urgency:         webpush.UrgencyHigh,
			VAPIDPublicKey:  s.PublicKey,
			VAPIDPrivateKey: s.PrivateKey,
		})
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBody))
	return Result{Status: resp.StatusCode, RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())}, nil
}

// parseRetryAfter reads delta-seconds or an HTTP date; unknown values mean "no hint".
func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}
