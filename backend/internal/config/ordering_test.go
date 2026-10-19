package config

import (
	"testing"
	"time"
)

func TestOrderingHours(t *testing.T) {
	tehran, _ := time.LoadLocation("Asia/Tehran")
	at := func(h, m int) time.Time { return time.Date(2026, 10, 19, h, m, 0, 0, tehran) }

	day, err := ParseOrderingHours("08:00-23:00")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		t    time.Time
		open bool
	}{{at(7, 59), false}, {at(8, 0), true}, {at(22, 59), true}, {at(23, 0), false}} {
		if got := day.IsOpen(c.t, tehran); got != c.open {
			t.Errorf("08-23 at %s: open=%v", c.t.Format("15:04"), got)
		}
	}
	// The check uses business time even when the clock is UTC: 05:00 UTC = 08:30 Tehran.
	if !day.IsOpen(time.Date(2026, 10, 19, 5, 0, 0, 0, time.UTC), tehran) {
		t.Error("timezone not applied")
	}
	if day.Opens() != "08:00" || day.Closes() != "23:00" {
		t.Errorf("format: %s-%s", day.Opens(), day.Closes())
	}

	night, err := ParseOrderingHours("18:30-02:00")
	if err != nil {
		t.Fatal(err)
	}
	if !night.IsOpen(at(1, 0), tehran) || !night.IsOpen(at(19, 0), tehran) || night.IsOpen(at(12, 0), tehran) {
		t.Error("overnight window wrong")
	}

	for _, bad := range []string{"", "8-23", "25:00-23:00", "08:60-23:00", "10:00-10:00", "24:30-01:00",
		"08:00-23:000", "08:00-23:00junk", " 08:00-23:00", "8:00-23:00", "08:00 - 23:00", "-8:00-23:00"} {
		if _, err := ParseOrderingHours(bad); err == nil {
			t.Errorf("ParseOrderingHours(%q) accepted", bad)
		}
	}
}
