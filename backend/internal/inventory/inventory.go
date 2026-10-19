// Package inventory maintains product-level stock: balances, the append-only movement ledger,
// purchases with exact voids, manual adjustments and the moving weighted average cost
// (docs/DATABASE.md §5.1, docs/WORKFLOWS.md §2).
//
// Every on_hand change happens in one transaction that locks the balance row, writes exactly one
// inventory_movements row and updates the balance to the movement's *_after values, so the
// balance always equals the ledger sums (INV-4).
package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/audit"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/store"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/textutil"
)

// Movement types and adjustment reason codes (docs/DATABASE.md §2.7).
const (
	TypePurchase      = "PURCHASE"
	TypePurchaseVoid  = "PURCHASE_VOID"
	TypeAdjustmentIn  = "ADJUSTMENT_IN"
	TypeAdjustmentOut = "ADJUSTMENT_OUT"

	ReasonOpeningBalance  = "OPENING_BALANCE"
	ReasonCountCorrection = "COUNT_CORRECTION"
	ReasonReturnToStock   = "RETURN_TO_STOCK"
	ReasonDamaged         = "DAMAGED"
	ReasonExpired         = "EXPIRED"
)

var (
	ErrNotFound          = errors.New("not found")
	ErrInsufficientStock = errors.New("insufficient available stock")
	ErrPurchaseConsumed  = errors.New("stock has left since this purchase; it cannot be voided exactly")
	ErrIdempotencyReused = errors.New("idempotency key reused with a different request")
)

// ValidationError lists invalid fields.
type ValidationError map[string]string

func (v ValidationError) Error() string { return "invalid inventory input" }

// Limits keep every product of quantity × cost far inside int64.
const (
	MaxQuantity     = 100_000
	MaxUnitCostRial = 1_000_000_000_000 // 100 billion toman
)

// Service holds inventory operations.
type Service struct {
	Pool     *pgxpool.Pool
	Location *time.Location // business timezone, for purchase dates
}

// ConsumeCost is the moving-average cost of taking qty out of a stock of onHand units worth
// value rials: round_half_up(value × qty / onHand). When qty == onHand it returns value exactly,
// so the value reaches zero together with the stock. Exact integer arithmetic, no overflow.
func ConsumeCost(value int64, onHand, qty int32) int64 {
	if qty <= 0 || onHand <= 0 {
		return 0
	}
	if qty >= onHand {
		return value
	}
	num := new(big.Int).Mul(big.NewInt(value), big.NewInt(int64(qty)))
	num.Mul(num, big.NewInt(2)).Add(num, big.NewInt(int64(onHand)))
	den := big.NewInt(2 * int64(onHand))
	return num.Quo(num, den).Int64()
}

// ---- ledger core ----

type change struct {
	productID int64
	typ       string
	reason    pgtype.Text
	qty       int32 // always positive; direction comes from typ
	unitCost  int64 // inflows: value added per unit
	exact     int64 // PURCHASE_VOID: exact value removed
	purchase  pgtype.Int8
	note      pgtype.Text
	actor     audit.Actor
	key       pgtype.UUID
}

func isInflow(typ string) bool { return typ == TypePurchase || typ == TypeAdjustmentIn }

// lockTracked locks the product (share) and its balance (update), in the documented order.
func lockTracked(ctx context.Context, q *store.Queries, productID int64) (store.Product, store.InventoryBalance, error) {
	p, err := q.GetProductForShare(ctx, productID)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, store.InventoryBalance{}, ErrNotFound
	}
	if err != nil {
		return p, store.InventoryBalance{}, err
	}
	if p.InventoryMode != "TRACKED" {
		return p, store.InventoryBalance{}, ValidationError{"product_id": "not a TRACKED product"}
	}
	if err := q.EnsureInventoryBalance(ctx, productID); err != nil {
		return p, store.InventoryBalance{}, err
	}
	b, err := q.GetInventoryBalanceForUpdate(ctx, productID)
	return p, b, err
}

// apply writes one movement and the matching balance update. b must be locked.
func apply(ctx context.Context, q *store.Queries, p store.Product, b store.InventoryBalance, c change) (store.InventoryMovement, store.InventoryBalance, error) {
	var dQty int32
	var dValue int64
	lastCost := pgtype.Int8{}
	if isInflow(c.typ) {
		dQty = c.qty
		dValue = int64(c.qty) * c.unitCost
		lastCost = pgtype.Int8{Int64: c.unitCost, Valid: c.typ == TypePurchase}
	} else {
		if b.OnHand-b.Reserved < c.qty {
			return store.InventoryMovement{}, b, ErrInsufficientStock // never eat into reservations
		}
		dQty = -c.qty
		if c.typ == TypePurchaseVoid {
			dValue = -c.exact
		} else {
			dValue = -ConsumeCost(b.InventoryValueRial, b.OnHand, c.qty)
		}
	}
	onHand, value := b.OnHand+dQty, b.InventoryValueRial+dValue
	if value < 0 || (onHand == 0 && value != 0) {
		// Unreachable when callers follow the rules; refuse rather than corrupt valuation.
		return store.InventoryMovement{}, b, fmt.Errorf("inventory: valuation would become inconsistent (product %d)", p.ID)
	}

	actor := pgtype.Int8{Int64: c.actor.UserID, Valid: c.actor.Kind == audit.KindUser}
	m, err := q.InsertMovement(ctx, store.InsertMovementParams{
		ProductID: p.ID, MovementType: c.typ, ReasonCode: c.reason, QuantityDelta: dQty, ValueDeltaRial: dValue,
		OnHandAfter: onHand, ValueAfterRial: value, PurchaseEntryID: c.purchase, Note: c.note,
		ActorUserID: actor, IdempotencyKey: c.key,
	})
	if err != nil {
		return m, b, err
	}
	nb, err := q.UpdateBalanceStock(ctx, store.UpdateBalanceStockParams{
		ProductID: p.ID, OnHand: onHand, InventoryValueRial: value, LastUnitCostRial: lastCost,
	})
	if err != nil {
		return m, b, err
	}
	if err := evaluateLowStock(ctx, q, p, nb, isInflow(c.typ)); err != nil {
		return m, nb, err
	}
	return m, nb, nil
}

// evaluateLowStock implements docs/WORKFLOWS.md §4.1: start one alert episode when available
// stock reaches the threshold (one outbox row per ADMIN subscription), and re-arm only after an
// inflow lifts it back above the threshold.
func evaluateLowStock(ctx context.Context, q *store.Queries, p store.Product, b store.InventoryBalance, inflow bool) error {
	if !p.LowStockThreshold.Valid {
		return nil
	}
	available := b.OnHand - b.Reserved
	threshold := p.LowStockThreshold.Int32
	if available > threshold {
		if inflow && b.LowStockAlertedAt.Valid {
			return q.ClearLowStockAlerted(ctx, p.ID)
		}
		return nil
	}
	if b.LowStockAlertedAt.Valid {
		return nil // episode already active
	}
	at, err := q.SetLowStockAlerted(ctx, p.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	subs, err := q.ListAdminPushSubscriptions(ctx)
	if err != nil || len(subs) == 0 {
		return err
	}
	payload, _ := json.Marshal(map[string]any{
		"type": "LOW_STOCK", "product_id": p.ID, "title": "موجودی کم",
		"body": fmt.Sprintf("%s: %d عدد باقی مانده", p.Name, available), "url": "/admin/inventory?filter=low",
	})
	// Microseconds: PostgreSQL's timestamp precision, so two episodes never share a key.
	key := "LOW_STOCK:" + strconv.FormatInt(p.ID, 10) + ":" + strconv.FormatInt(at.Time.UnixMicro(), 10)
	for _, sub := range subs {
		if err := q.InsertNotificationEvent(ctx, store.InsertNotificationEventParams{
			EventType: "LOW_STOCK", DedupKey: key, SubscriptionID: pgtype.Int8{Int64: sub, Valid: true},
			ProductID: pgtype.Int8{Int64: p.ID, Valid: true}, Payload: payload,
		}); err != nil {
			return err
		}
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func text(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }

// ---- purchases ----

// PurchaseInput records stock bought for a TRACKED product.
type PurchaseInput struct {
	ProductID    int64
	Quantity     int32
	UnitCostRial int64
	PurchasedOn  string // YYYY-MM-DD, business date
	Supplier     string
	Note         string
}

func (s *Service) validatePurchase(in *PurchaseInput) (pgtype.Date, ValidationError) {
	bad := ValidationError{}
	if in.ProductID <= 0 {
		bad["product_id"] = "required"
	}
	if in.Quantity < 1 || in.Quantity > MaxQuantity {
		bad["quantity"] = fmt.Sprintf("1-%d", MaxQuantity)
	}
	if in.UnitCostRial < 0 || in.UnitCostRial > MaxUnitCostRial {
		bad["unit_cost_rial"] = "out of range"
	}
	var date pgtype.Date
	d, err := time.ParseInLocation("2006-01-02", in.PurchasedOn, s.Location)
	if err != nil {
		bad["purchased_on"] = "YYYY-MM-DD"
	} else if d.Format(time.DateOnly) > time.Now().In(s.Location).Format(time.DateOnly) {
		bad["purchased_on"] = "in the future"
	} else {
		// A calendar date, independent of timezone (stored as a SQL date).
		date = pgtype.Date{Time: time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC), Valid: true}
	}
	in.Supplier = textutil.Line(in.Supplier)
	if textutil.Len(in.Supplier) > 80 {
		bad["supplier"] = "length"
	}
	in.Note = textutil.Multiline(in.Note)
	if textutil.Len(in.Note) > 300 {
		bad["note"] = "length"
	}
	return date, bad
}

func samePurchase(p store.PurchaseEntry, in PurchaseInput, date pgtype.Date) bool {
	return p.ProductID == in.ProductID && p.Quantity == in.Quantity && p.UnitCostRial == in.UnitCostRial &&
		p.PurchasedOn.Time.Format(time.DateOnly) == date.Time.Format(time.DateOnly) &&
		p.Supplier.String == in.Supplier && p.Note.String == in.Note
}

// Purchase adds stock at the given unit cost. Replaying the same key with the same input returns
// the original purchase (replay=true) and changes nothing.
func (s *Service) Purchase(ctx context.Context, actor audit.Actor, key pgtype.UUID, in PurchaseInput) (store.PurchaseEntry, bool, error) {
	date, bad := s.validatePurchase(&in)
	if len(bad) > 0 {
		return store.PurchaseEntry{}, false, bad
	}
	replay := func() (store.PurchaseEntry, bool, error) {
		p, err := store.New(s.Pool).GetPurchaseByIdempotencyKey(ctx, key)
		if err != nil {
			return p, false, err
		}
		if !samePurchase(p, in, date) {
			return p, false, ErrIdempotencyReused
		}
		return p, true, nil
	}
	if _, err := store.New(s.Pool).GetPurchaseByIdempotencyKey(ctx, key); err == nil {
		return replay()
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return store.PurchaseEntry{}, false, err
	}

	var pe store.PurchaseEntry
	err := store.InTx(ctx, s.Pool, func(q *store.Queries) error {
		prod, bal, err := lockTracked(ctx, q, in.ProductID)
		if errors.Is(err, ErrNotFound) {
			return ValidationError{"product_id": "unknown"}
		}
		if err != nil {
			return err
		}
		pe, err = q.CreatePurchase(ctx, store.CreatePurchaseParams{
			ProductID: in.ProductID, Quantity: in.Quantity, UnitCostRial: in.UnitCostRial,
			TotalCostRial: int64(in.Quantity) * in.UnitCostRial, Supplier: text(in.Supplier), PurchasedOn: date,
			Note: text(in.Note), IdempotencyKey: key, CreatedBy: actor.UserID,
		})
		if err != nil {
			return err
		}
		if _, _, err := apply(ctx, q, prod, bal, change{productID: prod.ID, typ: TypePurchase, qty: in.Quantity,
			unitCost: in.UnitCostRial, purchase: pgtype.Int8{Int64: pe.ID, Valid: true}, actor: actor}); err != nil {
			return err
		}
		return audit.Record(ctx, q, actor, audit.Entry{Action: "purchase.create", EntityType: "purchase",
			EntityID: strconv.FormatInt(pe.ID, 10), Details: map[string]any{
				"product_id": in.ProductID, "quantity": in.Quantity, "unit_cost_rial": in.UnitCostRial}})
	})
	if isUniqueViolation(err) {
		return replay() // a concurrent request with the same key won
	}
	return pe, false, err
}

// VoidPurchase reverses a purchase exactly (F4): allowed only if nothing has left this product's
// stock since the purchase and the quantity is still available. Repeating it is a no-op.
func (s *Service) VoidPurchase(ctx context.Context, actor audit.Actor, purchaseID int64, reason string) (store.PurchaseEntry, error) {
	reason = textutil.Line(reason)
	if n := textutil.Len(reason); n < 1 || n > 200 {
		return store.PurchaseEntry{}, ValidationError{"reason": "required, at most 200 characters"}
	}
	var pe store.PurchaseEntry
	err := store.InTx(ctx, s.Pool, func(q *store.Queries) error {
		var err error
		pe, err = q.GetPurchaseForUpdate(ctx, purchaseID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if pe.VoidedAt.Valid {
			return nil // state-idempotent
		}
		prod, bal, err := lockTracked(ctx, q, pe.ProductID)
		if err != nil {
			return err
		}
		pm, err := q.GetPurchaseMovement(ctx, pgtype.Int8{Int64: pe.ID, Valid: true})
		if err != nil {
			return fmt.Errorf("purchase %d has no ledger row: %w", pe.ID, err)
		}
		consumed, err := q.HasOutflowAfter(ctx, store.HasOutflowAfterParams{ProductID: pe.ProductID, AfterID: pm.ID})
		if err != nil {
			return err
		}
		if consumed {
			return ErrPurchaseConsumed
		}
		if _, _, err := apply(ctx, q, prod, bal, change{productID: prod.ID, typ: TypePurchaseVoid, qty: pe.Quantity,
			exact: pe.TotalCostRial, purchase: pgtype.Int8{Int64: pe.ID, Valid: true}, actor: actor}); err != nil {
			return err
		}
		pe, err = q.VoidPurchase(ctx, store.VoidPurchaseParams{ID: pe.ID, VoidReason: text(reason),
			VoidedBy: pgtype.Int8{Int64: actor.UserID, Valid: actor.Kind == audit.KindUser}})
		if err != nil {
			return err
		}
		return audit.Record(ctx, q, actor, audit.Entry{Action: "purchase.void", EntityType: "purchase",
			EntityID: strconv.FormatInt(pe.ID, 10), Details: map[string]any{"reason": reason}})
	})
	return pe, err
}

// ---- adjustments ----

// AdjustInput is a manual stock correction (docs/WORKFLOWS.md §6).
type AdjustInput struct {
	Direction    string // IN or OUT
	Quantity     int32
	UnitCostRial *int64 // required for IN, forbidden for OUT
	ReasonCode   string
	Note         string
}

var reasonsIn = map[string]bool{ReasonOpeningBalance: true, ReasonCountCorrection: true, ReasonReturnToStock: true}
var reasonsOut = map[string]bool{ReasonCountCorrection: true, ReasonDamaged: true, ReasonExpired: true}

func validateAdjust(in *AdjustInput) (string, ValidationError) {
	bad := ValidationError{}
	typ := ""
	switch in.Direction {
	case "IN":
		typ = TypeAdjustmentIn
		if !reasonsIn[in.ReasonCode] {
			bad["reason_code"] = "OPENING_BALANCE, COUNT_CORRECTION or RETURN_TO_STOCK"
		}
		if in.UnitCostRial == nil {
			bad["unit_cost_rial"] = "required for IN"
		} else if *in.UnitCostRial < 0 || *in.UnitCostRial > MaxUnitCostRial {
			bad["unit_cost_rial"] = "out of range"
		}
	case "OUT":
		typ = TypeAdjustmentOut
		if !reasonsOut[in.ReasonCode] {
			bad["reason_code"] = "COUNT_CORRECTION, DAMAGED or EXPIRED"
		}
		if in.UnitCostRial != nil {
			bad["unit_cost_rial"] = "OUT uses the current average cost"
		}
	default:
		bad["direction"] = "IN or OUT"
	}
	if in.Quantity < 1 || in.Quantity > MaxQuantity {
		bad["quantity"] = fmt.Sprintf("1-%d", MaxQuantity)
	}
	in.Note = textutil.Multiline(in.Note)
	if n := textutil.Len(in.Note); n < 1 || n > 300 {
		bad["note"] = "required, at most 300 characters"
	}
	return typ, bad
}

func sameAdjustment(m store.InventoryMovement, productID int64, typ string, in AdjustInput) bool {
	if m.ProductID != productID || m.MovementType != typ || m.ReasonCode.String != in.ReasonCode || m.Note.String != in.Note {
		return false
	}
	qty := m.QuantityDelta
	if qty < 0 {
		qty = -qty
	}
	if qty != in.Quantity {
		return false
	}
	return typ != TypeAdjustmentIn || m.ValueDeltaRial == int64(in.Quantity)*(*in.UnitCostRial)
}

// Adjust records a manual stock correction. IN adds stock at the given unit cost; OUT removes
// available stock at the current average cost. Same-key replays return the original movement.
func (s *Service) Adjust(ctx context.Context, actor audit.Actor, key pgtype.UUID, productID int64, in AdjustInput) (store.InventoryMovement, bool, error) {
	typ, bad := validateAdjust(&in)
	if len(bad) > 0 {
		return store.InventoryMovement{}, false, bad
	}
	replay := func() (store.InventoryMovement, bool, error) {
		m, err := store.New(s.Pool).GetMovementByIdempotencyKey(ctx, key)
		if err != nil {
			return m, false, err
		}
		if !sameAdjustment(m, productID, typ, in) {
			return m, false, ErrIdempotencyReused
		}
		return m, true, nil
	}
	if _, err := store.New(s.Pool).GetMovementByIdempotencyKey(ctx, key); err == nil {
		return replay()
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return store.InventoryMovement{}, false, err
	}

	var m store.InventoryMovement
	err := store.InTx(ctx, s.Pool, func(q *store.Queries) error {
		prod, bal, err := lockTracked(ctx, q, productID)
		if err != nil {
			return err
		}
		c := change{productID: productID, typ: typ, reason: text(in.ReasonCode), qty: in.Quantity,
			note: text(in.Note), actor: actor, key: key}
		if in.UnitCostRial != nil {
			c.unitCost = *in.UnitCostRial
		}
		m, _, err = apply(ctx, q, prod, bal, c)
		if err != nil {
			return err
		}
		return audit.Record(ctx, q, actor, audit.Entry{Action: "inventory.adjust", EntityType: "product",
			EntityID: strconv.FormatInt(productID, 10), Details: map[string]any{
				"movement_id": m.ID, "type": typ, "quantity": in.Quantity, "reason_code": in.ReasonCode,
				"value_delta_rial": m.ValueDeltaRial}})
	})
	if isUniqueViolation(err) {
		return replay()
	}
	return m, false, err
}

// ---- reads ----

// ListInventory returns every TRACKED product with its balance.
func (s *Service) ListInventory(ctx context.Context) ([]store.ListInventoryRow, error) {
	return store.New(s.Pool).ListInventory(ctx)
}

// ListMovements returns a product's ledger, newest first.
func (s *Service) ListMovements(ctx context.Context, productID int64, beforeID pgtype.Int8, limit int32) ([]store.InventoryMovement, error) {
	if _, err := store.New(s.Pool).GetProduct(ctx, productID); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	return store.New(s.Pool).ListMovements(ctx, store.ListMovementsParams{ProductID: productID, BeforeID: beforeID, RowLimit: limit})
}

// ListPurchases returns purchases, newest first.
func (s *Service) ListPurchases(ctx context.Context, p store.ListPurchasesParams) ([]store.PurchaseEntry, error) {
	return store.New(s.Pool).ListPurchases(ctx, p)
}
