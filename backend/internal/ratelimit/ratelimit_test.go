package ratelimit

import (
	"testing"
	"time"
)

func TestBurstThenRefill(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	l := New(5, 15*time.Minute)
	l.now = func() time.Time { return now }

	for i := 0; i < 5; i++ {
		if ok, _ := l.Allow("alice"); !ok {
			t.Fatalf("attempt %d denied within burst", i+1)
		}
	}
	ok, wait := l.Allow("alice")
	if ok || wait <= 0 || wait > 3*time.Minute {
		t.Fatalf("6th attempt: ok=%v wait=%v, want denied with ~3m wait", ok, wait)
	}
	if ok, _ := l.Allow("bob"); !ok {
		t.Error("keys must be independent")
	}

	now = now.Add(3 * time.Minute)
	if ok, _ := l.Allow("alice"); !ok {
		t.Error("one token should refill after window/burst")
	}
	if ok, _ := l.Allow("alice"); ok {
		t.Error("only one token should have refilled")
	}
}

func TestIdleKeysAreSwept(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	l := New(1, time.Minute)
	l.now = func() time.Time { return now }
	l.Allow("old")
	now = now.Add(time.Hour)
	for i := 0; i < 300; i++ {
		l.Allow("new")
	}
	if _, ok := l.keys["old"]; ok {
		t.Error("idle key not removed")
	}
}
