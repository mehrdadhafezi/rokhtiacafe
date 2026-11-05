package orders

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"testing"
)

// legacyRequestHash is requestHash exactly as it was before customer accounts (C1/C2 baseline).
func legacyRequestHash(in CreateInput) []byte {
	b, _ := json.Marshal(struct {
		Label string `json:"l"`
		Note  string `json:"n"`
		Items []Item `json:"i"`
	}{in.Label, in.Note, in.Items})
	h := sha256.Sum256(b)
	return h[:]
}

// Guest orders must hash byte-for-byte as before, so retries of orders created by the previous
// binary still replay instead of failing with IDEMPOTENCY_KEY_REUSED.
func TestGuestRequestHashIsUnchanged(t *testing.T) {
	in := CreateInput{Label: "سارا", Note: "بدون شکر", Items: []Item{{ProductID: 3, Quantity: 2, ExpectedUnitPriceRial: 450000}}}
	if !bytes.Equal(requestHash(in), legacyRequestHash(in)) {
		t.Fatal("guest request hash changed")
	}
	in.CustomerID = 7
	if bytes.Equal(requestHash(in), legacyRequestHash(in)) {
		t.Fatal("customer id is not part of the request identity")
	}
	other := in
	other.CustomerID = 8
	if bytes.Equal(requestHash(in), requestHash(other)) {
		t.Fatal("two customers produce the same request hash")
	}
}
