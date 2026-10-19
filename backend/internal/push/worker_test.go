package push

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/database"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/migrate"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/store"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/testdb"
)

type fx struct {
	t    *testing.T
	ctx  context.Context
	pool *pgxpool.Pool
	user int64
}

func setup(t *testing.T) *fx {
	t.Helper()
	db := testdb.New(t)
	m, err := migrate.New(db.OwnerURL, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
	f := &fx{t: t, ctx: ctx, pool: pool}
	f.must(pool.QueryRow(ctx, `INSERT INTO users (username, display_name, password_hash, role) VALUES ('admin','A','x','ADMIN') RETURNING id`).Scan(&f.user))
	return f
}

func (f *fx) must(err error) {
	f.t.Helper()
	if err != nil {
		f.t.Fatal(err)
	}
}

// keys returns a real P-256 public key and auth secret, as a browser would send them.
func keys(t *testing.T) (p256dh, auth string) {
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	a := make([]byte, 16)
	_, _ = rand.Read(a)
	return base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()), base64.RawURLEncoding.EncodeToString(a)
}

func (f *fx) subscription(endpoint string) int64 {
	p, a := keys(f.t)
	var id int64
	f.must(f.pool.QueryRow(f.ctx, `INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth) VALUES ($1,$2,$3,$4) RETURNING id`,
		f.user, endpoint, p, a).Scan(&id))
	return id
}

func (f *fx) event(sub int64, key string) int64 {
	var id int64
	f.must(f.pool.QueryRow(f.ctx, `INSERT INTO notification_events (event_type, dedup_key, subscription_id, payload)
		VALUES ('TEST', $1, $2, '{"title":"t"}') RETURNING id`, key, sub).Scan(&id))
	return id
}

type eventState struct {
	Status    string
	Attempts  int32
	Next      time.Time
	LastError *string
	SubID     *int64
}

func (f *fx) state(id int64) eventState {
	var s eventState
	f.must(f.pool.QueryRow(f.ctx, `SELECT status, attempts, next_attempt_at, last_error, subscription_id FROM notification_events WHERE id = $1`, id).
		Scan(&s.Status, &s.Attempts, &s.Next, &s.LastError, &s.SubID))
	return s
}

func (f *fx) makeDue(id int64) {
	_, err := f.pool.Exec(f.ctx, `UPDATE notification_events SET next_attempt_at = now() - interval '1 second' WHERE id = $1`, id)
	f.must(err)
}

// fakeSender answers with a fixed status and records what it was asked to send.
type fakeSender struct {
	mu       sync.Mutex
	status   int
	retry    time.Duration
	err      error
	sent     map[string]int // endpoint → sends
	topics   []string
	block    chan struct{} // when set, Send waits on it
	inFlight atomic.Int32
	maxSeen  atomic.Int32
	onSend   func()
}

func (s *fakeSender) Send(ctx context.Context, m Message) (Result, error) {
	n := s.inFlight.Add(1)
	defer s.inFlight.Add(-1)
	for {
		old := s.maxSeen.Load()
		if n <= old || s.maxSeen.CompareAndSwap(old, n) {
			break
		}
	}
	if s.onSend != nil {
		s.onSend()
	}
	if s.block != nil {
		select {
		case <-s.block:
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}
	s.mu.Lock()
	if s.sent == nil {
		s.sent = map[string]int{}
	}
	s.sent[m.Endpoint]++
	s.topics = append(s.topics, m.Topic)
	s.mu.Unlock()
	return Result{Status: s.status, RetryAfter: s.retry}, s.err
}

func (f *fx) worker(s Sender) *Worker {
	return &Worker{Pool: f.pool, Sender: s, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Validate: func(e string) error { return ValidateEndpoint(e, testAllow) }}
}

const ep = "https://fcm.googleapis.com/fcm/send/"

func TestDeliveryOutcomes(t *testing.T) {
	f := setup(t)

	// 2xx → SENT; the subscription records the success.
	sub := f.subscription(ep + "ok")
	_, err := f.pool.Exec(f.ctx, `UPDATE push_subscriptions SET failure_count = 3 WHERE id = $1`, sub)
	f.must(err)
	ok := f.event(sub, "k1")
	w := f.worker(&fakeSender{status: 201})
	if _, err := w.Tick(f.ctx); err != nil {
		t.Fatal(err)
	}
	if s := f.state(ok); s.Status != "SENT" || s.Attempts != 1 {
		t.Errorf("2xx: %+v", s)
	}
	var fails int
	var success pgtype.Timestamptz
	f.must(f.pool.QueryRow(f.ctx, `SELECT failure_count, last_success_at FROM push_subscriptions WHERE id = $1`, sub).Scan(&fails, &success))
	if fails != 0 || !success.Valid {
		t.Errorf("subscription after success: failures=%d last_success=%v", fails, success)
	}

	// AC-40: 410 → GONE and the subscription is deleted.
	gone := f.subscription(ep + "gone")
	goneEv := f.event(gone, "k2")
	if _, err := f.worker(&fakeSender{status: 410}).Tick(f.ctx); err != nil {
		t.Fatal(err)
	}
	if s := f.state(goneEv); s.Status != "GONE" || s.SubID != nil {
		t.Errorf("410: %+v", s)
	}

	// Other 4xx → FAILED at once, failure counted.
	bad := f.subscription(ep + "bad")
	badEv := f.event(bad, "k3")
	if _, err := f.worker(&fakeSender{status: 400}).Tick(f.ctx); err != nil {
		t.Fatal(err)
	}
	f.must(f.pool.QueryRow(f.ctx, `SELECT failure_count FROM push_subscriptions WHERE id = $1`, bad).Scan(&fails))
	if s := f.state(badEv); s.Status != "FAILED" || fails != 1 {
		t.Errorf("400: %+v failures=%d", s, fails)
	}
}

// AC-40: 500 schedules retries with backoff (Retry-After honoured when larger); after the
// fifth attempt the event is FAILED — no infinite retry loop.
func TestRetryBackoffAndLimit(t *testing.T) {
	f := setup(t)
	sub := f.subscription(ep + "flaky")
	ev := f.event(sub, "k")
	s := &fakeSender{status: 503}
	w := f.worker(s)
	for attempt := 1; attempt <= MaxAttempts; attempt++ {
		before := time.Now()
		if attempt == 2 {
			s.retry = 30 * time.Minute // larger than the 5 min backoff
		} else {
			s.retry = 0
		}
		if _, err := w.Tick(f.ctx); err != nil {
			t.Fatal(err)
		}
		st := f.state(ev)
		if st.Attempts != int32(attempt) {
			t.Fatalf("attempt %d: attempts=%d", attempt, st.Attempts)
		}
		if attempt < MaxAttempts {
			want := Backoff[attempt-1]
			if attempt == 2 {
				want = 30 * time.Minute
			}
			if st.Status != "PENDING" || st.Next.Before(before.Add(want-5*time.Second)) || st.Next.After(time.Now().Add(want+5*time.Second)) {
				t.Fatalf("attempt %d: %+v (want next in %v)", attempt, st, want)
			}
			if n, _ := w.Tick(f.ctx); n != 0 {
				t.Fatalf("attempt %d: claimed again before its backoff", attempt)
			}
			f.makeDue(ev)
		} else if st.Status != "FAILED" {
			t.Fatalf("after %d attempts: %+v", attempt, st)
		}
	}
	f.makeDue(ev)
	if n, _ := w.Tick(f.ctx); n != 0 {
		t.Error("a FAILED event was claimed again")
	}

	// A network error (no HTTP answer) is retried too.
	ev2 := f.event(sub, "k-net")
	if _, err := f.worker(&fakeSender{err: fmt.Errorf("dial timeout")}).Tick(f.ctx); err != nil {
		t.Fatal(err)
	}
	if st := f.state(ev2); st.Status != "PENDING" || st.LastError == nil || !strings.Contains(*st.LastError, "dial timeout") {
		t.Errorf("network error: %+v", st)
	}
}

// R-F5-1: a worker that crashed after claiming is taken over once the lease expires.
func TestCrashedClaimIsReclaimed(t *testing.T) {
	f := setup(t)
	ev := f.event(f.subscription(ep+"x"), "k")
	q := store.New(f.pool)
	claimed, err := q.ClaimNotificationEvents(f.ctx, store.ClaimNotificationEventsParams{
		ClaimToken: newToken(), Lease: pgtype.Interval{Microseconds: time.Minute.Microseconds(), Valid: true}, MaxAttempts: MaxAttempts, Batch: 10,
	})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: %v %d", err, len(claimed))
	}
	// …the worker dies here. Before the lease ends nobody else may take the event.
	s := &fakeSender{status: 201}
	if n, _ := f.worker(s).Tick(f.ctx); n != 0 {
		t.Fatal("event claimed while its lease was still valid")
	}
	_, err = f.pool.Exec(f.ctx, `UPDATE notification_events SET lease_until = now() - interval '1 second' WHERE id = $1`, ev)
	f.must(err)
	if n, _ := f.worker(s).Tick(f.ctx); n != 1 {
		t.Fatal("expired lease was not reclaimed")
	}
	if st := f.state(ev); st.Status != "SENT" || st.Attempts != 2 {
		t.Errorf("after takeover: %+v", st)
	}
}

// R-F5-2: a stale worker finalizing after its lease was re-claimed changes nothing.
func TestStaleWorkerCannotFinalize(t *testing.T) {
	f := setup(t)
	sub := f.subscription(ep + "x")
	ev := f.event(sub, "k")
	q := store.New(f.pool)
	stale := newToken()
	rows, err := q.ClaimNotificationEvents(f.ctx, store.ClaimNotificationEventsParams{
		ClaimToken: stale, Lease: pgtype.Interval{Microseconds: time.Minute.Microseconds(), Valid: true}, MaxAttempts: MaxAttempts, Batch: 10,
	})
	f.must(err)
	_, err = f.pool.Exec(f.ctx, `UPDATE notification_events SET lease_until = now() - interval '1 second' WHERE id = $1`, ev)
	f.must(err)
	if _, err := f.worker(&fakeSender{status: 201}).Tick(f.ctx); err != nil { // the new owner delivers
		t.Fatal(err)
	}
	// The stale worker now reports a permanent failure with its old token.
	w := f.worker(nil)
	w.defaults()
	subRow, _ := q.GetPushSubscription(f.ctx, sub)
	f.must(w.finalize(f.ctx, stale, rows[0], subRow, outcome{status: "FAILED", lastError: "late"}))
	var fails int
	f.must(f.pool.QueryRow(f.ctx, `SELECT failure_count FROM push_subscriptions WHERE id = $1`, sub).Scan(&fails))
	if st := f.state(ev); st.Status != "SENT" || fails != 0 {
		t.Errorf("stale finalize changed state: %+v failures=%d", st, fails)
	}
}

// R-F5-3: no transaction is open while the HTTP request is in flight.
func TestNoTransactionDuringSend(t *testing.T) {
	f := setup(t)
	f.event(f.subscription(ep+"x"), "k")
	var dbName string
	f.must(f.pool.QueryRow(f.ctx, `SELECT current_database()`).Scan(&dbName))
	block := make(chan struct{})
	var idleInTx int
	var probeErr error
	s := &fakeSender{status: 201, block: block, onSend: func() {
		go func() {
			defer close(block)
			time.Sleep(300 * time.Millisecond) // the send is "in flight"
			probeErr = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM pg_stat_activity
				WHERE datname = $1 AND pid <> pg_backend_pid() AND state LIKE 'idle in transaction%'`, dbName).Scan(&idleInTx)
		}()
	}}
	if _, err := f.worker(s).Tick(f.ctx); err != nil {
		t.Fatal(err)
	}
	if probeErr != nil || idleInTx != 0 {
		t.Errorf("transactions open during send: %d (%v)", idleInTx, probeErr)
	}
}

// R-F5-4: at most 10 claimed per tick and 4 sends at once.
func TestBatchAndConcurrencyLimits(t *testing.T) {
	f := setup(t)
	for i := range 15 {
		f.event(f.subscription(fmt.Sprintf("%sbulk-%d", ep, i)), "k")
	}
	block := make(chan struct{})
	s := &fakeSender{status: 201, block: block}
	go func() { time.Sleep(300 * time.Millisecond); close(block) }()
	n, err := f.worker(s).Tick(f.ctx)
	if err != nil || n != DefaultBatch {
		t.Fatalf("claimed %d (%v), want %d", n, err, DefaultBatch)
	}
	if m := s.maxSeen.Load(); m > DefaultConcurrent || m < 2 {
		t.Errorf("max concurrent sends = %d, want 2..%d", m, DefaultConcurrent)
	}
}

// Duplicate protection: concurrent workers deliver every event exactly once.
func TestConcurrentWorkersDeliverOnce(t *testing.T) {
	f := setup(t)
	const events = 30
	for i := range events {
		f.event(f.subscription(fmt.Sprintf("%sdup-%d", ep, i)), "k")
	}
	s := &fakeSender{status: 201}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := f.worker(s)
			for {
				n, err := w.Tick(f.ctx)
				if err != nil {
					t.Error(err)
					return
				}
				if n == 0 {
					return
				}
			}
		}()
	}
	wg.Wait()
	if len(s.sent) != events {
		t.Errorf("delivered to %d endpoints, want %d", len(s.sent), events)
	}
	for e, n := range s.sent {
		if n != 1 {
			t.Errorf("%s delivered %d times", e, n)
		}
	}
	var sent int
	f.must(f.pool.QueryRow(f.ctx, `SELECT count(*) FROM notification_events WHERE status = 'SENT' AND attempts = 1`).Scan(&sent))
	if sent != events {
		t.Errorf("SENT rows with one attempt = %d", sent)
	}
}

// The allowlist is re-checked before every send; old events are not delivered.
func TestSendTimeChecks(t *testing.T) {
	f := setup(t)
	s := &fakeSender{status: 201}
	unsafe := f.subscription("https://fcm.googleapis.com/fcm/send/x")
	ev := f.event(unsafe, "k")
	w := f.worker(s)
	w.Validate = func(string) error { return fmt.Errorf("%w: host removed from allowlist", ErrEndpoint) }
	if _, err := w.Tick(f.ctx); err != nil {
		t.Fatal(err)
	}
	if st := f.state(ev); st.Status != "FAILED" || len(s.sent) != 0 {
		t.Errorf("disallowed endpoint: %+v sent=%d", st, len(s.sent))
	}

	old := f.event(f.subscription(ep+"old"), "k-old")
	_, err := f.pool.Exec(f.ctx, `UPDATE notification_events SET created_at = now() - interval '25 hours' WHERE id = $1`, old)
	f.must(err)
	if _, err := f.worker(s).Tick(f.ctx); err != nil {
		t.Fatal(err)
	}
	if st := f.state(old); st.Status != "FAILED" || len(s.sent) != 0 {
		t.Errorf("event older than 24h: %+v", st)
	}
}

func TestLowStockTopicAndSubscriptionDrop(t *testing.T) {
	f := setup(t)
	var product int64
	f.must(f.pool.QueryRow(f.ctx, `INSERT INTO categories (name) VALUES ('c') RETURNING id`).Scan(new(int64)))
	f.must(f.pool.QueryRow(f.ctx, `INSERT INTO products (category_id, name, price_rial, inventory_mode, low_stock_threshold)
		VALUES (1, 'Cake', 50000, 'TRACKED', 2) RETURNING id`).Scan(&product))
	sub := f.subscription(ep + "ls")
	_, err := f.pool.Exec(f.ctx, `INSERT INTO notification_events (event_type, dedup_key, subscription_id, product_id, payload)
		VALUES ('LOW_STOCK', 'LOW_STOCK:1:1', $1, $2, '{}')`, sub, product)
	f.must(err)
	s := &fakeSender{status: 201}
	if _, err := f.worker(s).Tick(f.ctx); err != nil {
		t.Fatal(err)
	}
	if len(s.topics) != 1 || s.topics[0] != fmt.Sprintf("low-stock-%d", product) {
		t.Errorf("topic = %v", s.topics)
	}

	// A subscription that keeps failing permanently is removed after 10 failures.
	_, err = f.pool.Exec(f.ctx, `UPDATE push_subscriptions SET failure_count = $1 WHERE id = $2`, MaxSubscriptionFailures-1, sub)
	f.must(err)
	f.event(sub, "k-final")
	if _, err := f.worker(&fakeSender{status: 403}).Tick(f.ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	f.must(f.pool.QueryRow(f.ctx, `SELECT count(*) FROM push_subscriptions WHERE id = $1`, sub).Scan(&n))
	if n != 0 {
		t.Error("subscription with 10 failures was kept")
	}
}

func TestCleanupKeepsRecentAndPending(t *testing.T) {
	f := setup(t)
	sub := f.subscription(ep + "c")
	oldSent, oldPending, recent := f.event(sub, "a"), f.event(sub, "b"), f.event(sub, "c")
	_, err := f.pool.Exec(f.ctx, `UPDATE notification_events SET status = 'SENT', sent_at = now(), created_at = now() - interval '31 days' WHERE id = $1;`, oldSent)
	f.must(err)
	_, err = f.pool.Exec(f.ctx, `UPDATE notification_events SET created_at = now() - interval '31 days' WHERE id = $1`, oldPending)
	f.must(err)
	_, err = f.pool.Exec(f.ctx, `UPDATE notification_events SET status = 'SENT', sent_at = now() WHERE id = $1`, recent)
	f.must(err)
	n, err := Cleanup(f.ctx, f.pool, time.Now())
	if err != nil || n != 1 {
		t.Fatalf("deleted %d (%v), want 1", n, err)
	}
}

// The real sender produces an RFC 8291/8292 request (encrypted body, VAPID auth, TTL, urgency,
// topic) — checked against a local HTTPS server standing in for a push service.
func TestWebPushSenderRequest(t *testing.T) {
	var got *http.Request
	var body []byte
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	p, a := keys(t)
	res, err := WebPushSender{Client: srv.Client(), PublicKey: pub, PrivateKey: priv, Subject: "mailto:owner@example.com"}.
		Send(context.Background(), Message{Endpoint: srv.URL + "/push/abc", P256dh: p, Auth: a, Payload: []byte(`{"title":"سلام"}`), Topic: "low-stock-7"})
	if err != nil || res.Status != 201 || res.RetryAfter != 7*time.Second {
		t.Fatalf("send: %+v %v", res, err)
	}
	h := got.Header
	if h.Get("TTL") != "21600" || h.Get("Urgency") != "high" || h.Get("Topic") != "low-stock-7" ||
		h.Get("Content-Encoding") != "aes128gcm" || !strings.HasPrefix(h.Get("Authorization"), "vapid t=") {
		t.Errorf("headers: %v", h)
	}
	if strings.Contains(string(body), "سلام") || len(body) < 100 {
		t.Error("payload was not encrypted")
	}
	if !strings.Contains(h.Get("Authorization"), "k="+strings.TrimRight(pub, "=")) {
		t.Error("VAPID public key missing from Authorization")
	}
}
