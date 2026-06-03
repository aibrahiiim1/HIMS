package operations

import "testing"

func TestComputeStockStatus(t *testing.T) {
	cases := []struct {
		qty, min int
		want     StockStatus
	}{
		{0, 0, StockOut}, // empty even with zero threshold
		{0, 5, StockOut}, // empty
		{3, 5, StockLow}, // below threshold
		{5, 5, StockLow}, // at threshold
		{6, 5, StockOK},  // above threshold
		{10, 0, StockOK}, // stocked, no threshold set
	}
	for _, c := range cases {
		if got := ComputeStockStatus(c.qty, c.min); got != c.want {
			t.Errorf("ComputeStockStatus(%d,%d) = %v; want %v", c.qty, c.min, got, c.want)
		}
	}
}
