// Package orders implements public ordering, tracking and the staff order lifecycle
// (docs/WORKFLOWS.md §0–§1, docs/DATABASE.md §2.8–2.9, §3.1).
//
// Concurrency contract:
//   - Creation runs under the global LOCK_ORDER_CREATE advisory lock, and the idempotency replay is
//     checked first, so retries never fail on hours, caps, prices or stock (F3).
//   - Every other order mutation starts with SELECT … FOR UPDATE on the order row (F1); status
//     UPDATEs still carry a WHERE guard on the expected status.
//   - Lock order: advisory lock → order row → products (share) → balances (ascending product id).
package orders

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/audit"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/config"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/inventory"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/store"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/textutil"
)

// Order statuses and cancel reasons.
const (
	StatusPending   = "PENDING"
	StatusPreparing = "PREPARING"
	StatusReady     = "READY"
	StatusCompleted = "COMPLETED"
	StatusCancelled = "CANCELLED"

	ReasonCustomer = "CUSTOMER"
	ReasonStaff    = "STAFF"
	ReasonExpired  = "EXPIRED"
)

// Limits from docs/API-CONTRACT.md §3.
const (
	MaxLines        = 15
	MaxLineQuantity = 20
	TrackingTTL     = 24 * time.Hour
	expiryBatch     = 100
)

var (
	ErrNotFound          = errors.New("order not found")
	ErrIdempotencyReused = errors.New("idempotency key reused with a different request")
	ErrOrderingClosed    = errors.New("ordering is closed")
	ErrOrderingBusy      = errors.New("too many pending orders")
	ErrTooManyOpenOrders = errors.New("too many open orders from this network")
	ErrOrderExpired      = errors.New("order expired before it was accepted")
	ErrPaymentState      = errors.New("order holds money")
)

// ValidationError lists invalid fields.
type ValidationError map[string]string

func (v ValidationError) Error() string { return "invalid order input" }

// IllegalTransitionError reports the order's current status.
type IllegalTransitionError struct{ Current string }

func (e IllegalTransitionError) Error() string { return "illegal transition from " + e.Current }

// InsufficientStockError lists products without enough available stock.
type InsufficientStockError struct{ ProductIDs []int64 }

func (e InsufficientStockError) Error() string { return "insufficient stock" }

// UnavailableError lists inactive, sold-out or unknown products.
type UnavailableError struct{ ProductIDs []int64 }

func (e UnavailableError) Error() string { return "product unavailable" }

// PriceChange is one line whose price differs from what the customer saw.
type PriceChange struct {
	ProductID            int64 `json:"product_id"`
	CurrentUnitPriceRial int64 `json:"current_unit_price_rial"`
}

// PriceChangedError is returned when expected prices are stale (F13).
type PriceChangedError struct{ Items []PriceChange }

func (e PriceChangedError) Error() string { return "price changed" }

// Config holds the ordering policy.
type Config struct {
	PendingTTL          time.Duration
	MaxPending          int64
	MaxPendingPerClient int64
	Hours               config.OrderingHours
	Location            *time.Location
	Secret              []byte // APP_SECRET_KEY: derives tracking tokens and client keys
}

// Service holds order operations.
type Service struct {
	pool   *pgxpool.Pool
	cfg    Config
	closed atomic.Bool // kill switch (in memory, owner decision O4)
	now    func() time.Time
}

// New returns an order service.
func New(pool *pgxpool.Pool, cfg Config) *Service {
	return &Service{pool: pool, cfg: cfg, now: time.Now}
}

// SetClock replaces the clock (tests).
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// Detail is an order with its lines. Token is set only when returned to the creating customer.
type Detail struct {
	Order store.Order
	Items []store.OrderItem
	Token string
}

// ---- tokens & keys ----

func (s *Service) mac(prefix, v string) []byte {
	m := hmac.New(sha256.New, s.cfg.Secret)
	m.Write([]byte(prefix + v))
	return m.Sum(nil)
}

func uuidString(k pgtype.UUID) string {
	b := k.Bytes
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// TrackingToken derives the customer's tracking token from the idempotency key (SECURITY §10).
// The server can re-derive it for replays, so only its hash is stored.
func (s *Service) TrackingToken(key pgtype.UUID) string {
	return base64.RawURLEncoding.EncodeToString(s.mac("trk:", uuidString(key)))
}

// HashToken is the stored form of a tracking token.
func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// ClientKey is the per-network key used for open-order caps; the raw IP is never stored.
func (s *Service) ClientKey(ip netip.Addr) []byte { return s.mac("ip:", ip.String()) }

// ---- ordering switch ----

// IsOpen reports whether public ordering accepts new orders at t.
func (s *Service) IsOpen(t time.Time) bool {
	return !s.closed.Load() && s.cfg.Hours.IsOpen(t, s.cfg.Location)
}

// SwitchOpen reports the kill-switch position (true = not switched off).
func (s *Service) SwitchOpen() bool { return !s.closed.Load() }

// SetOrderingOpen flips the kill switch and audits it.
func (s *Service) SetOrderingOpen(ctx context.Context, actor audit.Actor, open bool) error {
	return store.InTx(ctx, s.pool, func(q *store.Queries) error {
		prev := !s.closed.Swap(!open)
		return audit.Record(ctx, q, actor, audit.Entry{Action: "ordering.switch", EntityType: "ordering", EntityID: "public",
			Details: map[string]any{"open": []bool{prev, open}}})
	})
}

// ---- create ----

// Item is one requested line.
type Item struct {
	ProductID             int64 `json:"product_id"`
	Quantity              int32 `json:"quantity"`
	ExpectedUnitPriceRial int64 `json:"expected_unit_price_rial"`
}

// CreateInput is a public order request.
type CreateInput struct {
	Key      pgtype.UUID
	ClientIP netip.Addr
	Label    string
	Note     string
	Items    []Item
}

func validate(in *CreateInput) ValidationError {
	bad := ValidationError{}
	if len(in.Items) < 1 || len(in.Items) > MaxLines {
		bad["items"] = fmt.Sprintf("1-%d lines", MaxLines)
	}
	seen := map[int64]bool{}
	for i, it := range in.Items {
		switch {
		case it.ProductID <= 0:
			bad[fmt.Sprintf("items[%d].product_id", i)] = "required"
		case seen[it.ProductID]:
			bad[fmt.Sprintf("items[%d].product_id", i)] = "duplicate"
		}
		seen[it.ProductID] = true
		if it.Quantity < 1 || it.Quantity > MaxLineQuantity {
			bad[fmt.Sprintf("items[%d].quantity", i)] = fmt.Sprintf("1-%d", MaxLineQuantity)
		}
		if it.ExpectedUnitPriceRial <= 0 {
			bad[fmt.Sprintf("items[%d].expected_unit_price_rial", i)] = "required"
		}
	}
	in.Label = textutil.Line(in.Label)
	if textutil.Len(in.Label) > 40 {
		bad["customer_label"] = "at most 40 characters"
	}
	in.Note = textutil.Multiline(in.Note)
	if textutil.Len(in.Note) > 200 {
		bad["customer_note"] = "at most 200 characters"
	}
	sort.Slice(in.Items, func(i, j int) bool { return in.Items[i].ProductID < in.Items[j].ProductID })
	return bad
}

// requestHash is SHA-256 of the canonical request (items sorted by product id).
func requestHash(in CreateInput) []byte {
	b, _ := json.Marshal(struct {
		Label string `json:"l"`
		Note  string `json:"n"`
		Items []Item `json:"i"`
	}{in.Label, in.Note, in.Items})
	h := sha256.Sum256(b)
	return h[:]
}

func text(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }

// KeyExists reports whether an order with this idempotency key exists (replays skip rate limits).
func (s *Service) KeyExists(ctx context.Context, key pgtype.UUID) (bool, error) {
	return store.New(s.pool).OrderExistsByIdempotencyKey(ctx, key)
}

// Create places a public order (docs/WORKFLOWS.md §1.2). created=false means an idempotent replay
// that returns the order's current state.
func (s *Service) Create(ctx context.Context, in CreateInput) (d Detail, created bool, err error) {
	if bad := validate(&in); len(bad) > 0 {
		return d, false, bad
	}
	hash := requestHash(in)

	err = store.InTx(ctx, s.pool, func(q *store.Queries) error {
		if err := q.AdvisoryXactLock(ctx, store.LockOrderCreate); err != nil {
			return err
		}
		// Replay first: a retry of a successful order never fails on hours, caps, prices or stock.
		if existing, err := q.GetOrderByIdempotencyKey(ctx, in.Key); err == nil {
			if !bytes.Equal(existing.RequestHash, hash) {
				return ErrIdempotencyReused
			}
			d, err = loadDetail(ctx, q, existing)
			return err
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		if !s.IsOpen(s.now()) {
			return ErrOrderingClosed
		}
		clientKey := s.ClientKey(in.ClientIP)
		if n, err := q.CountPendingOrders(ctx); err != nil {
			return err
		} else if n >= s.cfg.MaxPending {
			return ErrOrderingBusy
		}
		if n, err := q.CountPendingOrdersForClient(ctx, clientKey); err != nil {
			return err
		} else if n >= s.cfg.MaxPendingPerClient {
			return ErrTooManyOpenOrders
		}

		ids := make([]int64, len(in.Items))
		for i, it := range in.Items {
			ids[i] = it.ProductID
		}
		rows, err := q.GetProductsForOrder(ctx, ids)
		if err != nil {
			return err
		}
		products := make(map[int64]store.GetProductsForOrderRow, len(rows))
		for _, p := range rows {
			products[p.ID] = p
		}
		var unavailable []int64
		var changed []PriceChange
		for _, it := range in.Items {
			p, ok := products[it.ProductID]
			if !ok || !p.IsActive || !p.IsAvailable || !p.CategoryActive {
				unavailable = append(unavailable, it.ProductID)
				continue
			}
			if p.PriceRial != it.ExpectedUnitPriceRial {
				changed = append(changed, PriceChange{ProductID: p.ID, CurrentUnitPriceRial: p.PriceRial})
			}
		}
		if len(unavailable) > 0 {
			return UnavailableError{ProductIDs: unavailable}
		}
		if len(changed) > 0 {
			return PriceChangedError{Items: changed}
		}

		var short []int64
		for _, it := range in.Items { // ascending product id (validate sorted them)
			if products[it.ProductID].InventoryMode != "TRACKED" {
				continue
			}
			ok, err := inventory.Reserve(ctx, q, it.ProductID, it.Quantity)
			if err != nil {
				return err
			}
			if !ok {
				short = append(short, it.ProductID)
			}
		}
		if len(short) > 0 {
			return InsufficientStockError{ProductIDs: short} // rolls back every reservation above
		}

		now := s.now().In(s.cfg.Location)
		bdate := pgtype.Date{Time: time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC), Valid: true}
		num, err := q.NextDisplayNumber(ctx, bdate)
		if err != nil {
			return err
		}
		var total int64
		for _, it := range in.Items {
			total += int64(it.Quantity) * products[it.ProductID].PriceRial
		}
		token := s.TrackingToken(in.Key)
		o, err := q.CreateOrder(ctx, store.CreateOrderParams{
			BusinessDate: bdate, DisplayNumber: num, TrackingTokenHash: HashToken(token), IdempotencyKey: in.Key,
			RequestHash: hash, ClientKey: clientKey, CustomerLabel: text(in.Label), CustomerNote: text(in.Note),
			TotalRial: total, PendingTtl: pgtype.Interval{Microseconds: s.cfg.PendingTTL.Microseconds(), Valid: true},
		})
		if err != nil {
			return err
		}
		d = Detail{Order: o}
		for _, it := range in.Items {
			p := products[it.ProductID]
			item, err := q.CreateOrderItem(ctx, store.CreateOrderItemParams{
				OrderID: o.ID, ProductID: p.ID, ProductName: p.Name, InventoryMode: p.InventoryMode,
				UnitPriceRial: p.PriceRial, Quantity: it.Quantity, LineTotalRial: int64(it.Quantity) * p.PriceRial,
			})
			if err != nil {
				return err
			}
			d.Items = append(d.Items, item)
		}
		created = true
		return audit.Record(ctx, q, audit.Actor{Kind: audit.KindCustomer, IP: in.ClientIP}, audit.Entry{
			Action: "order.create", EntityType: "order", EntityID: strconv.FormatInt(o.ID, 10),
			Details: map[string]any{"display_number": num, "total_rial": total, "lines": len(d.Items)}})
	})
	if err != nil {
		return Detail{}, false, err
	}
	d.Token = s.TrackingToken(in.Key)
	return d, created, nil
}

func loadDetail(ctx context.Context, q *store.Queries, o store.Order) (Detail, error) {
	items, err := q.ListOrderItems(ctx, o.ID)
	return Detail{Order: o, Items: items}, err
}

// ---- customer tracking ----

func (s *Service) byToken(ctx context.Context, q *store.Queries, token string) (store.Order, error) {
	if token == "" || len(token) > 128 {
		return store.Order{}, ErrNotFound
	}
	o, err := q.GetOrderByTrackingHash(ctx, HashToken(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return o, ErrNotFound
	}
	if err != nil {
		return o, err
	}
	if s.now().Sub(o.CreatedAt.Time) > TrackingTTL {
		return o, ErrNotFound
	}
	return o, nil
}

// Track returns the order for a tracking token (valid 24 h after creation).
func (s *Service) Track(ctx context.Context, token string) (Detail, error) {
	q := store.New(s.pool)
	o, err := s.byToken(ctx, q, token)
	if err != nil {
		return Detail{}, err
	}
	return loadDetail(ctx, q, o)
}

// CustomerCancel cancels a PENDING order by its tracking token. Cancelling an order that the
// customer already cancelled, or that has expired, returns it unchanged.
func (s *Service) CustomerCancel(ctx context.Context, token string, ip netip.Addr) (Detail, error) {
	o, err := s.byToken(ctx, store.New(s.pool), token)
	if err != nil {
		return Detail{}, err
	}
	var d Detail
	err = store.InTx(ctx, s.pool, func(q *store.Queries) error {
		o, err := q.GetOrderForUpdate(ctx, o.ID)
		if err != nil {
			return err
		}
		actor := audit.Actor{Kind: audit.KindCustomer, IP: ip}
		switch {
		case o.Status == StatusPending:
			now, err := q.DBNow(ctx)
			if err != nil {
				return err
			}
			reason := ReasonCustomer
			if !now.Time.Before(o.ExpiresAt.Time) {
				reason = ReasonExpired // past its deadline: expire rather than record a customer cancel
				actor = audit.Actor{Kind: audit.KindSystem}
			}
			o, err = s.cancelLocked(ctx, q, o, reason, "", actor, nil)
			if err != nil {
				return err
			}
		case o.Status == StatusCancelled && (o.CancelReason.String == ReasonCustomer || o.CancelReason.String == ReasonExpired):
		default:
			return IllegalTransitionError{Current: o.Status}
		}
		d, err = loadDetail(ctx, q, o)
		return err
	})
	return d, err
}

// ---- staff transitions ----

func (s *Service) withLockedOrder(ctx context.Context, id int64, fn func(q *store.Queries, o store.Order) (store.Order, error)) (Detail, error) {
	var d Detail
	err := store.InTx(ctx, s.pool, func(q *store.Queries) error {
		o, err := q.GetOrderForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if o, err = fn(q, o); err != nil {
			return err
		}
		d, err = loadDetail(ctx, q, o)
		return err
	})
	return d, err
}

func userBy(a audit.Actor) pgtype.Int8 {
	return pgtype.Int8{Int64: a.UserID, Valid: a.Kind == audit.KindUser}
}

func record(ctx context.Context, q *store.Queries, a audit.Actor, action string, o store.Order, details map[string]any) error {
	return audit.Record(ctx, q, a, audit.Entry{Action: action, EntityType: "order", EntityID: strconv.FormatInt(o.ID, 10), Details: details})
}

// Accept moves PENDING → PREPARING before the acceptance deadline. Past the deadline the order is
// expired in the same transaction and ErrOrderExpired is returned (F2).
func (s *Service) Accept(ctx context.Context, actor audit.Actor, id int64) (Detail, error) {
	expired := false
	d, err := s.withLockedOrder(ctx, id, func(q *store.Queries, o store.Order) (store.Order, error) {
		switch o.Status {
		case StatusPreparing:
			return o, nil // already accepted: idempotent double-tap
		case StatusPending:
		default:
			return o, IllegalTransitionError{Current: o.Status}
		}
		now, err := q.DBNow(ctx)
		if err != nil {
			return o, err
		}
		if !now.Time.Before(o.ExpiresAt.Time) {
			expired = true
			return s.cancelLocked(ctx, q, o, ReasonExpired, "", audit.Actor{Kind: audit.KindSystem}, nil)
		}
		n, err := q.AcceptOrder(ctx, store.AcceptOrderParams{ID: o.ID, By: userBy(actor)})
		if err != nil {
			return o, err
		}
		return n, record(ctx, q, actor, "order.accept", n, nil)
	})
	if err == nil && expired {
		return d, ErrOrderExpired
	}
	return d, err
}

// Ready moves PREPARING → READY.
func (s *Service) Ready(ctx context.Context, actor audit.Actor, id int64) (Detail, error) {
	return s.withLockedOrder(ctx, id, func(q *store.Queries, o store.Order) (store.Order, error) {
		switch o.Status {
		case StatusReady:
			return o, nil
		case StatusPreparing:
		default:
			return o, IllegalTransitionError{Current: o.Status}
		}
		n, err := q.MarkOrderReady(ctx, o.ID)
		if err != nil {
			return o, err
		}
		return n, record(ctx, q, actor, "order.ready", n, nil)
	})
}

// Complete moves READY → COMPLETED: reserved stock leaves at average cost (one SALE row per
// tracked line) and each line gets its cost snapshot (docs/WORKFLOWS.md §1.3).
func (s *Service) Complete(ctx context.Context, actor audit.Actor, id int64) (Detail, error) {
	return s.withLockedOrder(ctx, id, func(q *store.Queries, o store.Order) (store.Order, error) {
		switch o.Status {
		case StatusCompleted:
			return o, nil
		case StatusReady:
		default:
			return o, IllegalTransitionError{Current: o.Status}
		}
		items, err := q.ListOrderItems(ctx, o.ID) // ascending product id
		if err != nil {
			return o, err
		}
		var cogs int64
		for _, it := range items {
			params := store.SetOrderItemCostParams{ID: it.ID, CogsRial: pgtype.Int8{Valid: true}}
			if it.InventoryMode == "TRACKED" {
				cost, err := inventory.ConsumeReserved(ctx, q, it.ProductID, it.Quantity, inventory.TypeSale, o.ID, actor)
				if err != nil {
					return o, err
				}
				params.CogsRial.Int64 = cost
				cogs += cost
			} else {
				est, err := q.GetProductEstimate(ctx, it.ProductID)
				if err != nil {
					return o, err
				}
				if est.Valid {
					params.EstimatedCostRial = pgtype.Int8{Int64: est.Int64 * int64(it.Quantity), Valid: true}
				}
			}
			if err := q.SetOrderItemCost(ctx, params); err != nil {
				return o, err
			}
		}
		n, err := q.CompleteOrder(ctx, store.CompleteOrderParams{ID: o.ID, By: userBy(actor)})
		if err != nil {
			return o, err
		}
		return n, record(ctx, q, actor, "order.complete", n, map[string]any{"cogs_rial": cogs})
	})
}

// WasteLine marks prepared or opened units that cannot go back to stock (F7).
type WasteLine struct {
	ProductID int64 `json:"product_id"`
	Quantity  int32 `json:"quantity"`
}

// CancelInput is a staff cancellation.
type CancelInput struct {
	Note  string
	Waste []WasteLine
}

// Cancel cancels a PENDING/PREPARING/READY order. Reservations are released; wasted tracked units
// leave stock at average cost (WASTE movement, an inventory loss) and wasted untracked units get
// an estimated cost (docs/WORKFLOWS.md §1.5).
func (s *Service) Cancel(ctx context.Context, actor audit.Actor, id int64, in CancelInput) (Detail, error) {
	in.Note = textutil.Multiline(in.Note)
	if textutil.Len(in.Note) > 200 {
		return Detail{}, ValidationError{"note": "at most 200 characters"}
	}
	return s.withLockedOrder(ctx, id, func(q *store.Queries, o store.Order) (store.Order, error) {
		switch o.Status {
		case StatusCancelled:
			return o, nil
		case StatusPending, StatusPreparing, StatusReady:
		default:
			return o, IllegalTransitionError{Current: o.Status}
		}
		if len(in.Waste) > 0 && o.Status == StatusPending {
			return o, ValidationError{"waste": "only for orders already being prepared"}
		}
		return s.cancelLocked(ctx, q, o, ReasonStaff, in.Note, actor, in.Waste)
	})
}

// cancelLocked cancels an order whose row is locked by the caller.
func (s *Service) cancelLocked(ctx context.Context, q *store.Queries, o store.Order, reason, note string, actor audit.Actor, waste []WasteLine) (store.Order, error) {
	if o.PaidRial != o.RefundedRial {
		return o, ErrPaymentState // refunds of prepayments arrive with payments (T7)
	}
	items, err := q.ListOrderItems(ctx, o.ID)
	if err != nil {
		return o, err
	}
	byProduct := make(map[int64]store.OrderItem, len(items))
	for _, it := range items {
		byProduct[it.ProductID] = it
	}
	wasted := map[int64]int32{}
	for i, w := range waste {
		it, ok := byProduct[w.ProductID]
		switch {
		case !ok:
			return o, ValidationError{fmt.Sprintf("waste[%d].product_id", i): "not in this order"}
		case wasted[w.ProductID] != 0:
			return o, ValidationError{fmt.Sprintf("waste[%d].product_id", i): "duplicate"}
		case w.Quantity < 1 || w.Quantity > it.Quantity:
			return o, ValidationError{fmt.Sprintf("waste[%d].quantity", i): fmt.Sprintf("1-%d", it.Quantity)}
		}
		wasted[w.ProductID] = w.Quantity
	}

	var wasteCost int64
	for _, it := range items { // ascending product id
		w := wasted[it.ProductID]
		if it.InventoryMode == "TRACKED" {
			if w > 0 {
				cost, err := inventory.ConsumeReserved(ctx, q, it.ProductID, w, inventory.TypeWaste, o.ID, actor)
				if err != nil {
					return o, err
				}
				wasteCost += cost
			}
			if err := inventory.Release(ctx, q, it.ProductID, it.Quantity-w); err != nil {
				return o, err
			}
		}
		if w > 0 {
			params := store.SetOrderItemWasteParams{ID: it.ID, WastedQuantity: w}
			if it.InventoryMode == "UNTRACKED" {
				est, err := q.GetProductEstimate(ctx, it.ProductID)
				if err != nil {
					return o, err
				}
				if est.Valid {
					params.EstimatedCostRial = pgtype.Int8{Int64: est.Int64 * int64(w), Valid: true}
				}
			}
			if err := q.SetOrderItemWaste(ctx, params); err != nil {
				return o, err
			}
		}
	}
	n, err := q.CancelOrder(ctx, store.CancelOrderParams{ID: o.ID, Reason: text(reason), Note: text(note), By: userBy(actor)})
	if err != nil {
		return o, err
	}
	details := map[string]any{"reason": reason}
	if len(wasted) > 0 {
		details["waste"] = wasted
		details["waste_cost_rial"] = wasteCost
	}
	action := "order.cancel"
	if reason == ReasonExpired {
		action = "order.expire"
	}
	return n, record(ctx, q, actor, action, n, details)
}

// ExpireDue cancels PENDING orders past their acceptance deadline, one order per transaction
// under the same lock that selected it (F2). Safe to run from several workers at once.
func (s *Service) ExpireDue(ctx context.Context) (int, error) {
	done := 0
	for done < expiryBatch {
		found := false
		err := store.InTx(ctx, s.pool, func(q *store.Queries) error {
			id, err := q.NextExpiredPendingOrder(ctx)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			o, err := q.GetOrderForUpdate(ctx, id)
			if err != nil {
				return err
			}
			found = true
			_, err = s.cancelLocked(ctx, q, o, ReasonExpired, "", audit.Actor{Kind: audit.KindSystem}, nil)
			return err
		})
		if err != nil {
			return done, err
		}
		if !found {
			return done, nil
		}
		done++
	}
	return done, nil
}

// ---- staff reads ----

// Get returns one order with its lines.
func (s *Service) Get(ctx context.Context, id int64) (Detail, error) {
	q := store.New(s.pool)
	o, err := q.GetOrder(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, ErrNotFound
	}
	if err != nil {
		return Detail{}, err
	}
	return loadDetail(ctx, q, o)
}

// List returns orders, newest first, with their lines.
func (s *Service) List(ctx context.Context, p store.ListOrdersParams) ([]Detail, error) {
	q := store.New(s.pool)
	rows, err := q.ListOrders(ctx, p)
	if err != nil || len(rows) == 0 {
		return []Detail{}, err
	}
	ids := make([]int64, len(rows))
	for i, o := range rows {
		ids[i] = o.ID
	}
	items, err := q.ListOrderItemsForOrders(ctx, ids)
	if err != nil {
		return nil, err
	}
	byOrder := map[int64][]store.OrderItem{}
	for _, it := range items {
		byOrder[it.OrderID] = append(byOrder[it.OrderID], it)
	}
	out := make([]Detail, len(rows))
	for i, o := range rows {
		out[i] = Detail{Order: o, Items: byOrder[o.ID]}
	}
	return out, nil
}
