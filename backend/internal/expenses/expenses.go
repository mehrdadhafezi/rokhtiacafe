// Package expenses records operational expenses (docs/DATABASE.md §2.11). Expenses are never
// deleted: a mistaken entry is voided with a reason and re-entered (docs/WORKFLOWS.md §6).
package expenses

import (
	"context"
	"errors"
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

// Categories from docs/DATABASE.md §2.11. Ingredients and supplies for made-to-order drinks are
// recorded here; stock bought for TRACKED products is a purchase, not an expense.
var Categories = map[string]bool{
	"INGREDIENTS": true, "SUPPLIES": true, "RENT": true, "UTILITIES": true,
	"SALARY": true, "MAINTENANCE": true, "OTHER": true,
}

// MaxAmountRial bounds a single expense (100 billion toman).
const MaxAmountRial = 1_000_000_000_000

var (
	ErrNotFound          = errors.New("expense not found")
	ErrIdempotencyReused = errors.New("idempotency key reused with a different request")
)

// ValidationError lists invalid fields.
type ValidationError map[string]string

func (v ValidationError) Error() string { return "invalid expense input" }

// Service holds expense operations.
type Service struct {
	Pool     *pgxpool.Pool
	Location *time.Location
}

// Input is a new expense.
type Input struct {
	Category    string
	AmountRial  int64
	ExpenseDate string // YYYY-MM-DD business date
	Description string
}

// ParseDate parses a business date into a timezone-independent SQL date.
func ParseDate(s string) (pgtype.Date, bool) {
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return pgtype.Date{}, false
	}
	return pgtype.Date{Time: d, Valid: true}, true
}

// Create records an expense. Replaying the same key with the same input returns the original.
func (s *Service) Create(ctx context.Context, actor audit.Actor, key pgtype.UUID, in Input) (store.Expense, bool, error) {
	bad := ValidationError{}
	if !Categories[in.Category] {
		bad["category"] = "INGREDIENTS, SUPPLIES, RENT, UTILITIES, SALARY, MAINTENANCE or OTHER"
	}
	if in.AmountRial <= 0 || in.AmountRial > MaxAmountRial {
		bad["amount_rial"] = "out of range"
	}
	date, ok := ParseDate(in.ExpenseDate)
	if !ok {
		bad["expense_date"] = "YYYY-MM-DD"
	} else if in.ExpenseDate > time.Now().In(s.Location).Format(time.DateOnly) {
		bad["expense_date"] = "in the future"
	}
	in.Description = textutil.Line(in.Description)
	if n := textutil.Len(in.Description); n < 1 || n > 200 {
		bad["description"] = "required, at most 200 characters"
	}
	if len(bad) > 0 {
		return store.Expense{}, false, bad
	}

	q := store.New(s.Pool)
	replay := func() (store.Expense, bool, error) {
		e, err := q.GetExpenseByIdempotencyKey(ctx, key)
		if err != nil {
			return e, false, err
		}
		if e.Category != in.Category || e.AmountRial != in.AmountRial || e.Description != in.Description ||
			e.ExpenseDate.Time.Format(time.DateOnly) != in.ExpenseDate {
			return e, false, ErrIdempotencyReused
		}
		return e, true, nil
	}
	if _, err := q.GetExpenseByIdempotencyKey(ctx, key); err == nil {
		return replay()
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return store.Expense{}, false, err
	}

	var e store.Expense
	err := store.InTx(ctx, s.Pool, func(q *store.Queries) error {
		var err error
		e, err = q.CreateExpense(ctx, store.CreateExpenseParams{Category: in.Category, AmountRial: in.AmountRial,
			ExpenseDate: date, Description: in.Description, IdempotencyKey: key, CreatedBy: actor.UserID})
		if err != nil {
			return err
		}
		return audit.Record(ctx, q, actor, audit.Entry{Action: "expense.create", EntityType: "expense",
			EntityID: strconv.FormatInt(e.ID, 10), Details: map[string]any{"category": e.Category, "amount_rial": e.AmountRial}})
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return replay() // a concurrent request with the same key won
	}
	return e, false, err
}

// Void marks an expense void with a reason; repeating it is a no-op.
func (s *Service) Void(ctx context.Context, actor audit.Actor, id int64, reason string) (store.Expense, error) {
	reason = textutil.Line(reason)
	if n := textutil.Len(reason); n < 1 || n > 200 {
		return store.Expense{}, ValidationError{"reason": "required, at most 200 characters"}
	}
	var e store.Expense
	err := store.InTx(ctx, s.Pool, func(q *store.Queries) error {
		var err error
		e, err = q.GetExpenseForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil || e.VoidedAt.Valid {
			return err
		}
		e, err = q.VoidExpense(ctx, store.VoidExpenseParams{ID: id, VoidReason: pgtype.Text{String: reason, Valid: true},
			VoidedBy: pgtype.Int8{Int64: actor.UserID, Valid: actor.Kind == audit.KindUser}})
		if err != nil {
			return err
		}
		return audit.Record(ctx, q, actor, audit.Entry{Action: "expense.void", EntityType: "expense",
			EntityID: strconv.FormatInt(id, 10), Details: map[string]any{"amount_rial": e.AmountRial, "reason": reason}})
	})
	return e, err
}

// List returns expenses, newest first.
func (s *Service) List(ctx context.Context, p store.ListExpensesParams) ([]store.Expense, error) {
	return store.New(s.Pool).ListExpenses(ctx, p)
}
