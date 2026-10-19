package push

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/store"
)

// Outbox limits (docs/ARCHITECTURE.md §2.5, WORKFLOWS §4.2).
const (
	MaxAttempts       = 5
	DefaultBatch      = 10
	DefaultConcurrent = 4
	DefaultLease      = 60 * time.Second
	EventMaxAge       = 24 * time.Hour
	// A subscription that keeps failing permanently is dropped.
	MaxSubscriptionFailures = 10
)

// Backoff before attempt n+1 after a retryable failure of attempt n (1-based).
var Backoff = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour}

// Worker delivers the notification outbox. It never holds a database transaction during
// network I/O: claim (short tx) → send → finalize by claim token (short tx).
type Worker struct {
	Pool   *pgxpool.Pool
	Sender Sender
	// Validate re-checks the endpoint before every send (the allowlist may have changed).
	Validate   func(endpoint string) error
	Logger     *slog.Logger
	Batch      int
	Concurrent int
	Lease      time.Duration
	Now        func() time.Time
}

func (w *Worker) defaults() {
	if w.Batch <= 0 {
		w.Batch = DefaultBatch
	}
	if w.Concurrent <= 0 {
		w.Concurrent = DefaultConcurrent
	}
	if w.Lease <= 0 {
		w.Lease = DefaultLease
	}
	if w.Now == nil {
		w.Now = time.Now
	}
	if w.Logger == nil {
		w.Logger = slog.Default()
	}
}

// Tick claims up to Batch due events and delivers them; it returns how many were claimed.
func (w *Worker) Tick(ctx context.Context) (int, error) {
	w.defaults()
	q := store.New(w.Pool)
	if _, err := q.ExpireStaleNotificationClaims(ctx, MaxAttempts); err != nil {
		return 0, fmt.Errorf("expire stale claims: %w", err)
	}
	token := newToken()
	events, err := q.ClaimNotificationEvents(ctx, store.ClaimNotificationEventsParams{
		ClaimToken:  token,
		Lease:       pgtype.Interval{Microseconds: w.Lease.Microseconds(), Valid: true},
		MaxAttempts: MaxAttempts,
		Batch:       int32(w.Batch),
	})
	if err != nil {
		return 0, fmt.Errorf("claim: %w", err)
	}
	sem := make(chan struct{}, w.Concurrent)
	var wg sync.WaitGroup
	for _, ev := range events {
		wg.Add(1)
		sem <- struct{}{}
		go func(ev store.ClaimNotificationEventsRow) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := w.deliver(ctx, token, ev); err != nil && ctx.Err() == nil {
				w.Logger.Warn("push delivery bookkeeping failed", "event_id", ev.ID, "err", err)
			}
		}(ev)
	}
	wg.Wait()
	return len(events), nil
}

// Run ticks every interval until ctx ends; a full batch is followed immediately by another tick.
func (w *Worker) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		n, err := w.Tick(ctx)
		if err != nil && ctx.Err() == nil {
			w.Logger.Warn("push outbox tick failed", "err", err)
		}
		if n >= w.Batch && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

type outcome struct {
	status    string // SENT, PENDING (retry), FAILED, GONE
	next      time.Time
	lastError string
}

func (w *Worker) deliver(ctx context.Context, token pgtype.UUID, ev store.ClaimNotificationEventsRow) error {
	q := store.New(w.Pool)
	now := w.Now()
	var sub store.PushSubscription
	var o outcome
	switch {
	case !ev.SubscriptionID.Valid:
		o = outcome{status: "FAILED", lastError: "subscription removed"}
	case now.Sub(ev.CreatedAt.Time) > EventMaxAge:
		o = outcome{status: "FAILED", lastError: "older than 24 hours"}
	default:
		var err error
		sub, err = q.GetPushSubscription(ctx, ev.SubscriptionID.Int64)
		if errors.Is(err, pgx.ErrNoRows) {
			o = outcome{status: "FAILED", lastError: "subscription removed"}
			break
		}
		if err != nil {
			return err
		}
		if w.Validate != nil {
			if err := w.Validate(sub.Endpoint); err != nil {
				o = outcome{status: "FAILED", lastError: truncate(err.Error())}
				break
			}
		}
		o = w.send(ctx, ev, sub)
	}
	return w.finalize(ctx, token, ev, sub, o)
}

func (w *Worker) send(ctx context.Context, ev store.ClaimNotificationEventsRow, sub store.PushSubscription) outcome {
	sendCtx, cancel := context.WithTimeout(ctx, SendTimeout)
	defer cancel()
	res, err := w.Sender.Send(sendCtx, Message{
		Endpoint: sub.Endpoint, P256dh: sub.P256dh, Auth: sub.Auth, Payload: ev.Payload, Topic: topic(ev),
	})
	switch {
	case err != nil:
		return w.retry(ev, 0, "send: "+err.Error())
	case res.Status >= 200 && res.Status < 300:
		return outcome{status: "SENT"}
	case res.Status == 404 || res.Status == 410:
		return outcome{status: "GONE", lastError: fmt.Sprintf("HTTP %d: subscription expired", res.Status)}
	case res.Status == 429 || res.Status >= 500:
		return w.retry(ev, res.RetryAfter, fmt.Sprintf("HTTP %d", res.Status))
	default:
		return outcome{status: "FAILED", lastError: fmt.Sprintf("HTTP %d", res.Status)}
	}
}

// retry schedules the next attempt, or fails the event once the attempts are used up.
func (w *Worker) retry(ev store.ClaimNotificationEventsRow, retryAfter time.Duration, msg string) outcome {
	if ev.Attempts >= MaxAttempts {
		return outcome{status: "FAILED", lastError: truncate(msg + " (no attempts left)")}
	}
	wait := Backoff[min(int(ev.Attempts), len(Backoff))-1]
	if retryAfter > wait {
		wait = retryAfter
	}
	return outcome{status: "PENDING", next: w.Now().Add(wait), lastError: truncate(msg)}
}

// finalize records the outcome only if this worker still owns the claim; a stale worker whose
// lease was taken over changes nothing (R-F5-2).
func (w *Worker) finalize(ctx context.Context, token pgtype.UUID, ev store.ClaimNotificationEventsRow, sub store.PushSubscription, o outcome) error {
	return store.InTx(ctx, w.Pool, func(q *store.Queries) error {
		var n int64
		var err error
		text := pgtype.Text{String: o.lastError, Valid: o.lastError != ""}
		switch o.status {
		case "SENT":
			n, err = q.MarkNotificationSent(ctx, store.MarkNotificationSentParams{ID: ev.ID, ClaimToken: token})
		case "PENDING":
			n, err = q.MarkNotificationRetry(ctx, store.MarkNotificationRetryParams{
				ID: ev.ID, ClaimToken: token, LastError: text,
				NextAttemptAt: pgtype.Timestamptz{Time: o.next, Valid: true},
			})
		default:
			n, err = q.MarkNotificationFinal(ctx, store.MarkNotificationFinalParams{ID: ev.ID, ClaimToken: token, Status: o.status, LastError: text})
		}
		if err != nil || n == 0 || sub.ID == 0 {
			return err
		}
		switch o.status {
		case "SENT":
			return q.MarkPushSubscriptionSuccess(ctx, sub.ID)
		case "GONE":
			return q.DeletePushSubscription(ctx, sub.ID)
		case "FAILED":
			failures, err := q.IncrementPushSubscriptionFailures(ctx, sub.ID)
			if err != nil || failures < MaxSubscriptionFailures {
				return err
			}
			return q.DeletePushSubscription(ctx, sub.ID)
		}
		return nil
	})
}

// topic lets the push service replace an undelivered duplicate (e.g. after a worker crash).
func topic(ev store.ClaimNotificationEventsRow) string {
	if ev.EventType == "LOW_STOCK" && ev.ProductID.Valid {
		return fmt.Sprintf("low-stock-%d", ev.ProductID.Int64)
	}
	return ""
}

func truncate(s string) string {
	r := []rune(s)
	if len(r) > 300 {
		return string(r[:300])
	}
	return s
}

func newToken() pgtype.UUID {
	var u pgtype.UUID
	_, _ = rand.Read(u.Bytes[:])
	u.Bytes[6] = (u.Bytes[6] & 0x0f) | 0x40
	u.Bytes[8] = (u.Bytes[8] & 0x3f) | 0x80
	u.Valid = true
	return u
}

// Cleanup deletes delivered, failed and gone events older than 30 days.
func Cleanup(ctx context.Context, pool *pgxpool.Pool, now time.Time) (int64, error) {
	return store.New(pool).DeleteOldNotificationEvents(ctx, pgtype.Timestamptz{Time: now.Add(-30 * 24 * time.Hour), Valid: true})
}
