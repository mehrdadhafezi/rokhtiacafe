package main

import (
	"errors"
	"flag"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// cmdCompare checks two snapshots:
//
//	-mode restore   AFTER is BEFORE restored and migrated forward: every table of BEFORE has exactly
//	                the same rows (same keys, same digests), tables listed in -new exist and are
//	                empty, no other table appeared, and AFTER's migrations extend BEFORE's.
//	-mode preserve  AFTER is BEFORE plus activity: every row of BEFORE is still present and
//	                unchanged, except in the -volatile tables (sessions, balances, …); -frozen
//	                tables gain no rows either; tables never disappear and migrations never change.
func cmdCompare(args []string) error {
	fs := flag.NewFlagSet("compare", flag.ContinueOnError)
	before := fs.String("before", "", "snapshot file")
	after := fs.String("after", "", "snapshot file")
	mode := fs.String("mode", "", "restore|preserve")
	newTables := fs.String("new", "", "comma-separated tables expected to appear empty (restore)")
	volatile := fs.String("volatile", "", "comma-separated tables whose existing rows may change (preserve)")
	frozen := fs.String("frozen", "", "comma-separated tables that must not change at all, not even new rows (preserve)")
	label := fs.String("label", "", "name printed with each result")
	if err := fs.Parse(args); err != nil {
		return err
	}
	b, err := loadSnapshot(*before)
	if err != nil {
		return err
	}
	a, err := loadSnapshot(*after)
	if err != nil {
		return err
	}
	list := func(s string) []string {
		if s == "" {
			return nil
		}
		return strings.Split(s, ",")
	}
	failures := 0
	report := func(ok bool, format string, v ...any) {
		status := "PASS"
		if !ok {
			status = "FAIL"
			failures++
		}
		fmt.Printf("%s  %s%s\n", status, prefix(*label), fmt.Sprintf(format, v...))
	}

	tables := make([]string, 0, len(b.Rows))
	for t := range b.Rows {
		tables = append(tables, t)
	}
	sort.Strings(tables)

	switch *mode {
	case "restore":
		for _, t := range tables {
			if t == "goose_db_version" {
				continue
			}
			changed, missing, added := diffRows(b.Rows[t], a.Rows[t])
			report(changed == 0 && missing == 0 && added == 0 && a.Rows[t] != nil,
				"%s: %d rows identical after restore + migration (changed %d, missing %d, added %d)", t, len(b.Rows[t]), changed, missing, added)
		}
		expected := list(*newTables)
		for t, rows := range a.Rows {
			if _, ok := b.Rows[t]; ok || t == "goose_db_version" {
				continue
			}
			report(slices.Contains(expected, t) && len(rows) == 0, "new table %s exists and is empty (%d rows)", t, len(rows))
		}
		for _, t := range expected {
			_, ok := a.Rows[t]
			report(ok, "expected new table %s present", t)
		}
		report(len(a.Migrations) >= len(b.Migrations) && slices.Equal(a.Migrations[:len(b.Migrations)], b.Migrations),
			"migrations %v extend %v", a.Migrations, b.Migrations)
	case "preserve":
		vol := list(*volatile)
		for _, t := range tables {
			if t == "goose_db_version" {
				continue
			}
			if a.Rows[t] == nil {
				report(false, "%s disappeared", t)
				continue
			}
			changed, missing, added := diffRows(b.Rows[t], a.Rows[t])
			if slices.Contains(vol, t) {
				fmt.Printf("INFO  %s%s: volatile (changed %d, missing %d, added %d)\n", prefix(*label), t, changed, missing, added)
				continue
			}
			if slices.Contains(list(*frozen), t) {
				report(changed == 0 && missing == 0 && added == 0, "%s: frozen, %d rows (changed %d, missing %d, added %d)", t, len(b.Rows[t]), changed, missing, added)
				continue
			}
			report(changed == 0 && missing == 0, "%s: all %d existing rows unchanged (%d new)", t, len(b.Rows[t]), added)
		}
		report(slices.Equal(a.Migrations, b.Migrations), "migrations unchanged %v", a.Migrations)
	default:
		return errors.New("-mode must be restore or preserve")
	}
	keys := make([]string, 0, len(a.Sums))
	for k := range a.Sums {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Printf("INFO  %ssums before → after:", prefix(*label))
	for _, k := range keys {
		if bv, ok := b.Sums[k]; !ok || bv != a.Sums[k] {
			fmt.Printf(" %s %v→%d", k, sumOr(b.Sums, k), a.Sums[k])
		}
	}
	fmt.Println()
	if failures > 0 {
		return fmt.Errorf("%d comparison(s) failed", failures)
	}
	return nil
}

func diffRows(before, after map[string]string) (changed, missing, added int) {
	for k, h := range before {
		got, ok := after[k]
		switch {
		case !ok:
			missing++
		case got != h:
			changed++
		}
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			added++
		}
	}
	return
}

func prefix(label string) string {
	if label == "" {
		return ""
	}
	return "[" + label + "] "
}

func sumOr(m map[string]int64, k string) any {
	if v, ok := m[k]; ok {
		return v
	}
	return "-"
}
