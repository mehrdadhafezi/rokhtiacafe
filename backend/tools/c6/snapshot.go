package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

// snapshot is a content-addressed picture of schema rokh: one md5 per row (keyed by primary key),
// money and stock sums and invariant counters. It holds no plaintext rows, so it is safe as CI
// evidence (no names, phones, hashes or tokens).
type snapshot struct {
	Identity   identity                     `json:"identity"`
	Migrations []int64                      `json:"migrations"`
	Rows       map[string]map[string]string `json:"rows"` // table → primary key → md5(row)
	Sums       map[string]int64             `json:"sums"`
}

var sumQueries = map[string]string{
	"orders":                       "SELECT count(*) FROM orders",
	"orders_completed":             "SELECT count(*) FROM orders WHERE status = 'COMPLETED'",
	"orders_cancelled":             "SELECT count(*) FROM orders WHERE status = 'CANCELLED'",
	"orders_open":                  "SELECT count(*) FROM orders WHERE status IN ('PENDING','PREPARING','READY')",
	"orders_total_rial":            "SELECT coalesce(sum(total_rial),0) FROM orders",
	"orders_paid_rial":             "SELECT coalesce(sum(paid_rial),0) FROM orders",
	"orders_refunded_rial":         "SELECT coalesce(sum(refunded_rial),0) FROM orders",
	"completed_gross_rial":         "SELECT coalesce(sum(total_rial),0) FROM orders WHERE status = 'COMPLETED'",
	"payments_received_rial":       "SELECT coalesce(sum(amount_rial),0) FROM payments WHERE kind = 'PAYMENT' AND voided_at IS NULL",
	"refunds_recorded_rial":        "SELECT coalesce(sum(amount_rial),0) FROM payments WHERE kind = 'REFUND' AND voided_at IS NULL",
	"voided_payments":              "SELECT count(*) FROM payments WHERE voided_at IS NOT NULL",
	"stock_on_hand":                "SELECT coalesce(sum(on_hand),0) FROM inventory_balances",
	"stock_reserved":               "SELECT coalesce(sum(reserved),0) FROM inventory_balances",
	"inventory_movements":          "SELECT count(*) FROM inventory_movements",
	"purchases_rial":               "SELECT coalesce(sum(quantity * unit_cost_rial),0) FROM purchase_entries WHERE voided_at IS NULL",
	"expenses_rial":                "SELECT coalesce(sum(amount_rial),0) FROM expenses WHERE voided_at IS NULL",
	"audit_logs":                   "SELECT count(*) FROM audit_logs",
	"staff_users":                  "SELECT count(*) FROM users",
	"violation_negative_stock":     "SELECT count(*) FROM inventory_balances WHERE on_hand < 0 OR reserved < 0 OR reserved > on_hand",
	"violation_money_on_pending":   "SELECT count(*) FROM orders WHERE status = 'PENDING' AND paid_rial <> 0",
	"violation_paid_vs_ledger":     "SELECT count(*) FROM orders o WHERE o.paid_rial <> (SELECT coalesce(sum(amount_rial),0) FROM payments p WHERE p.order_id = o.id AND p.kind = 'PAYMENT' AND p.voided_at IS NULL)",
	"violation_refunded_vs_ledger": "SELECT count(*) FROM orders o WHERE o.refunded_rial <> (SELECT coalesce(sum(amount_rial),0) FROM payments p WHERE p.order_id = o.id AND p.kind = 'REFUND' AND p.voided_at IS NULL)",
}

// Only present after migration 00008.
var customerSumQueries = map[string]string{
	"customers":                    "SELECT count(*) FROM customers",
	"customer_order_links":         "SELECT count(*) FROM customer_order_links",
	"customer_events":              "SELECT count(*) FROM customer_events",
	"violation_link_without_event": "SELECT count(*) FROM customer_order_links l WHERE NOT EXISTS (SELECT 1 FROM customer_events e WHERE e.order_id = l.order_id AND e.event_type = 'ORDER_CREATED' AND e.customer_id = l.customer_id)",
}

func cmdSnapshot(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	db := fs.String("db", "", "database URL (superuser or owner)")
	out := fs.String("out", "", "output JSON file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	conn, id, err := connect(ctx, *db)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "SET search_path = rokh"); err != nil {
		return err
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY"); err != nil {
		return err
	}
	s := snapshot{Identity: id, Rows: map[string]map[string]string{}, Sums: map[string]int64{}}

	rows, err := tx.Query(ctx, "SELECT version_id FROM goose_db_version WHERE is_applied AND version_id > 0 ORDER BY version_id")
	if err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return err
		}
		s.Migrations = append(s.Migrations, v)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// Every base table of schema rokh with its primary-key expression.
	type table struct{ name, pk string }
	var tables []table
	rows, err = tx.Query(ctx, `
SELECT c.relname,
       coalesce((SELECT string_agg(format('%I::text', a.attname), ' || ''/'' || ' ORDER BY k.ord)
                 FROM pg_index i
                 CROSS JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS k(attnum, ord)
                 JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = k.attnum
                 WHERE i.indrelid = c.oid AND i.indisprimary), '') AS pk
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'rokh' AND c.relkind = 'r' ORDER BY 1`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var t table
		if err := rows.Scan(&t.name, &t.pk); err != nil {
			return err
		}
		tables = append(tables, t)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, t := range tables {
		key := t.pk
		if key == "" { // no primary key: the row itself identifies it (duplicates collapse, counted below)
			key = "md5(t::text)"
		}
		q := fmt.Sprintf("SELECT %s, md5(t::text) FROM rokh.%s t", key, quoteIdent(t.name))
		r, err := tx.Query(ctx, q)
		if err != nil {
			return fmt.Errorf("%s: %w", t.name, err)
		}
		m := map[string]string{}
		for r.Next() {
			var k, h string
			if err := r.Scan(&k, &h); err != nil {
				return err
			}
			m[k] = h
		}
		if err := r.Err(); err != nil {
			return err
		}
		s.Rows[t.name] = m
	}

	queries := sumQueries
	if _, ok := s.Rows["customers"]; ok {
		queries = map[string]string{}
		for k, v := range sumQueries {
			queries[k] = v
		}
		for k, v := range customerSumQueries {
			queries[k] = v
		}
	}
	for name, q := range queries {
		var v int64
		if err := tx.QueryRow(ctx, q).Scan(&v); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		s.Sums[name] = v
	}

	b, err := json.MarshalIndent(s, "", " ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, b, 0o600); err != nil {
		return err
	}
	fmt.Printf("snapshot %s: %s on %s:%d (PostgreSQL %s), migrations %v, %d tables\n",
		*out, id.Database, id.Addr, id.Port, id.Version, s.Migrations, len(s.Rows))
	keys := make([]string, 0, len(s.Sums))
	for k := range s.Sums {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("  %-30s %d\n", k, s.Sums[k])
	}
	for _, k := range keys {
		if strings.HasPrefix(k, "violation_") && s.Sums[k] != 0 {
			return fmt.Errorf("invariant %s violated by %d rows", k, s.Sums[k])
		}
	}
	return nil
}

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func loadSnapshot(path string) (snapshot, error) {
	var s snapshot
	b, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, err
	}
	if s.Rows == nil {
		return s, errors.New(path + ": not a snapshot")
	}
	return s, nil
}
