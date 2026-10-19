package inventory

import (
	"math"
	"testing"
)

func TestConsumeCost(t *testing.T) {
	cases := []struct {
		value       int64
		onHand, qty int32
		want        int64
		name        string
	}{
		{30000, 20, 5, 7500, "AC-18 first sale"},
		{22500, 15, 15, 22500, "AC-18 last sale takes the remaining value exactly"},
		{1000, 3, 1, 333, "333.33 rounds down"},
		{667, 2, 1, 334, "333.5 rounds half up"},
		{1000, 3, 3, 1000, "all units"},
		{0, 5, 2, 0, "zero-cost stock"},
		{10, 3, 5, 10, "never more than the remaining value"},
		{math.MaxInt64 / 2, MaxQuantity, MaxQuantity - 1, (math.MaxInt64/2)/MaxQuantity*(MaxQuantity-1) + (math.MaxInt64/2)%MaxQuantity*(MaxQuantity-1)/MaxQuantity, "huge values do not overflow"},
	}
	for _, c := range cases {
		got := ConsumeCost(c.value, c.onHand, c.qty)
		if c.name == "huge values do not overflow" {
			if got <= 0 || got > c.value {
				t.Errorf("%s: got %d", c.name, got)
			}
			continue
		}
		if got != c.want {
			t.Errorf("%s: ConsumeCost(%d, %d, %d) = %d, want %d", c.name, c.value, c.onHand, c.qty, got, c.want)
		}
	}
}

// Selling a stock one unit at a time always sums to its total value: no rounding drift.
func TestConsumeCostHasNoDrift(t *testing.T) {
	for _, start := range []struct {
		value  int64
		onHand int32
	}{{1000, 3}, {999_999, 7}, {1, 13}, {123_456_789, 97}} {
		value, onHand := start.value, start.onHand
		var total int64
		for onHand > 0 {
			c := ConsumeCost(value, onHand, 1)
			total += c
			value -= c
			onHand--
			if value < 0 {
				t.Fatalf("value went negative for %+v", start)
			}
		}
		if value != 0 || total != start.value {
			t.Errorf("%+v: remaining %d, consumed %d", start, value, total)
		}
	}
}
