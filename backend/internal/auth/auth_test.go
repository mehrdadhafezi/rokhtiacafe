package auth

import (
	"errors"
	"sort"
	"strings"
	"testing"
)

func TestHashAndVerifyPassword(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("unexpected PHC string %q", h)
	}
	if ok, err := VerifyPassword("correct horse battery", h); err != nil || !ok {
		t.Errorf("correct password: ok=%v err=%v", ok, err)
	}
	if ok, _ := VerifyPassword("correct horse batterY", h); ok {
		t.Error("wrong password accepted")
	}
	h2, _ := HashPassword("correct horse battery")
	if h == h2 {
		t.Error("salt is not random")
	}
}

func TestVerifyRejectsMalformedHashes(t *testing.T) {
	for _, bad := range []string{"", "plain", "$argon2i$v=19$m=1,t=1,p=1$AA$AA", "$argon2id$v=18$m=1,t=1,p=1$AA$AA",
		"$argon2id$v=19$m=0,t=1,p=1$AA$AA", "$argon2id$v=19$m=1,t=1,p=1$!!$AA", "$argon2id$v=19$m=1,t=1,p=1$AA$"} {
		if ok, err := VerifyPassword("x", bad); ok || err == nil {
			t.Errorf("hash %q: ok=%v err=%v", bad, ok, err)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	cases := map[string]bool{
		"short":                   false,
		"1234567890":              false, // common
		"Password123":             false, // common (case-insensitive)
		"تیاکافه-رخ-۱۴۰۵":         true,  // counted in characters, not bytes
		strings.Repeat("a", 129):  false,
		"a perfectly fine phrase": true,
		strings.Repeat("ab", 64):  true,
	}
	for pw, want := range cases {
		err := ValidatePassword(pw)
		if (err == nil) != want {
			t.Errorf("ValidatePassword(%q) = %v, want ok=%v", pw, err, want)
		}
		if err != nil && !errors.Is(err, ErrWeakPassword) {
			t.Errorf("error does not wrap ErrWeakPassword: %v", err)
		}
	}
}

// The OPERATOR column of docs/SECURITY.md §3, exactly.
func TestPermissionMatrixMatchesSecurityDoc(t *testing.T) {
	want := []string{
		"catalog.availability", "catalog.view", "dashboard.view", "inventory.view_qty",
		"orders.cancel_prepaid", "orders.transition", "orders.view", "payments.record", "self.password",
	}
	var got []string
	for _, a := range AllActions {
		if !Can(RoleAdmin, a) {
			t.Errorf("ADMIN denied %s", a)
		}
		if Can(RoleOperator, a) {
			got = append(got, string(a))
		}
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("OPERATOR may do %v\nwant          %v", got, want)
	}
	for _, denied := range []Action{ActUsersManage, ActReportsView, ActPaymentsRefund, ActPaymentsVoid,
		ActCatalogManage, ActInventoryManage, ActInventoryViewCost, ActExpensesManage, ActOrderingToggle, ActDashboardRevenue} {
		if Can(RoleOperator, denied) {
			t.Errorf("OPERATOR allowed %s", denied)
		}
	}
	if Can(RoleAdmin, "made.up") || Can("GUEST", ActOrdersView) {
		t.Error("unknown action or role must be denied")
	}
}
