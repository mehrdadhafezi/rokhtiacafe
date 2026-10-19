package config

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

var orderingHoursRE = regexp.MustCompile(`^([0-9]{2}):([0-9]{2})-([0-9]{2}):([0-9]{2})$`)

// OrderingHours is the daily public-ordering window in the business timezone
// (docs/WORKFLOWS.md §1.2). Close < Open means the window runs past midnight.
type OrderingHours struct {
	OpenMin  int // minutes after local midnight
	CloseMin int
}

// ParseOrderingHours parses "HH:MM-HH:MM".
func ParseOrderingHours(s string) (OrderingHours, error) {
	m := orderingHoursRE.FindStringSubmatch(s)
	if m == nil {
		return OrderingHours{}, fmt.Errorf("ORDERING_HOURS must look like 08:00-23:00, got %q", s)
	}
	oh, _ := strconv.Atoi(m[1])
	om, _ := strconv.Atoi(m[2])
	ch, _ := strconv.Atoi(m[3])
	cm, _ := strconv.Atoi(m[4])
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
