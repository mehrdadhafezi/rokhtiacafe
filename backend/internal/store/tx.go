package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Advisory lock keys (docs/DATABASE.md §3.1). At most one is taken per transaction.
const (
	LockOrderCreate     int64 = 0x524b_0001 // "RK" 1
	LockAdminMembership int64 = 0x524b_0002 // "RK" 2
)

// InTx runs fn inside a READ COMMITTED transaction and commits if fn returns nil.
func InTx(ctx context.Context, pool *pgxpool.Pool, fn func(q *Queries) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	if err := fn(New(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

var _ DBTX = pgx.Tx(nil)
