package config

import (
	"fmt"
	"time"
)

// OrderingHours is the daily public-ordering window in the business timezone
// (docs/WORKFLOWS.md §1.2). Close < Open means the window runs past midnight.
type OrderingHours struct {
	OpenMin  int // minutes after local midnight
	CloseMin int
}

// ParseOrderingHours parses "HH:MM-HH:MM".
func ParseOrderingHours(s string) (OrderingHours, error) {
	var oh, om, ch, cm int
	if _, err := fmt.Sscanf(s, "%d:%d-%d:%d", &oh, &om, &ch, &cm); err != nil {
		return OrderingHours{}, fmt.Errorf("ORDERING_HOURS must look like 08:00-23:00, got %q", s)
	}
	for _, v := range [][2]int{{oh, om}, {ch, cm}} {
		if v[0] < 0 || v[0] > 24 || v[1] < 0 || v[1] > 59 || (v[0] == 24 && v[1] != 0) {
			return OrderingHours{}, fmt.Errorf("ORDERING_HOURS has an invalid time in %q", s)
		}
	}
	h := OrderingHours{OpenMin: oh*60 + om, CloseMin: ch*60 + cm}
	if h.OpenMin == h.CloseMin {
		return OrderingHours{}, fmt.Errorf("ORDERING_HOURS open and close are equal in %q", s)
	}
	return h, nil
}

// IsOpen reports whether t (converted to loc) falls inside the window.
func (h OrderingHours) IsOpen(t time.Time, loc *time.Location) bool {
	lt := t.In(loc)
	m := lt.Hour()*60 + lt.Minute()
	if h.OpenMin < h.CloseMin {
		return m >= h.OpenMin && m < h.CloseMin
	}
	return m >= h.OpenMin || m < h.CloseMin
}

// Opens and Closes format the window ends as HH:MM.
func (h OrderingHours) Opens() string  { return hhmm(h.OpenMin) }
func (h OrderingHours) Closes() string { return hhmm(h.CloseMin) }

func hhmm(m int) string { return fmt.Sprintf("%02d:%02d", m/60, m%60) }
