package operations

// StockStatus is a spare part's inventory health.
type StockStatus string

const (
	StockOut StockStatus = "out" // nothing on hand
	StockLow StockStatus = "low" // at or below the reorder threshold
	StockOK  StockStatus = "ok"  // comfortably above threshold
)

// ComputeStockStatus classifies on-hand quantity against its reorder
// threshold. Zero is "out" even if min is also zero (you can't fit a part you
// don't have); at-or-below a positive threshold is "low"; otherwise "ok".
func ComputeStockStatus(quantity, minQuantity int) StockStatus {
	if quantity <= 0 {
		return StockOut
	}
	if quantity <= minQuantity {
		return StockLow
	}
	return StockOK
}
