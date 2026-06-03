package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/coralsearesorts/hims/internal/operations"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// ---- Spare parts -----------------------------------------------------------

// sparePartDTO enriches a part row with its computed stock status.
type sparePartDTO struct {
	db.SparePart
	StockStatus string `json:"stock_status"`
}

func toSparePartDTO(p db.SparePart) sparePartDTO {
	return sparePartDTO{
		SparePart:   p,
		StockStatus: string(operations.ComputeStockStatus(int(p.Quantity), int(p.MinQuantity))),
	}
}

type sparePartReq struct {
	Name        string  `json:"name"`
	SKU         *string `json:"sku"`
	Category    string  `json:"category"`
	LocationID  *string `json:"location_id"`
	Quantity    int32   `json:"quantity"`
	MinQuantity int32   `json:"min_quantity"`
	UnitCost    float64 `json:"unit_cost"`
	Notes       *string `json:"notes"`
}

func (s *Server) listSpareParts(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListSpareParts(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]sparePartDTO, len(rows))
	for i, p := range rows {
		out[i] = toSparePartDTO(p)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) listLowStockParts(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListLowStockParts(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]sparePartDTO, len(rows))
	for i, p := range rows {
		out[i] = toSparePartDTO(p)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createSparePart(w http.ResponseWriter, r *http.Request) {
	var req sparePartReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	p, err := s.queries.CreateSparePart(r.Context(), db.CreateSparePartParams{
		Name:        req.Name,
		Sku:         req.SKU,
		Category:    orDefault(req.Category, "other"),
		LocationID:  parseUUIDPtr(req.LocationID),
		Quantity:    req.Quantity,
		MinQuantity: req.MinQuantity,
		UnitCost:    req.UnitCost,
		Notes:       req.Notes,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toSparePartDTO(p))
}

func (s *Server) updateSparePart(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req sparePartReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	p, err := s.queries.UpdateSparePart(ctx, db.UpdateSparePartParams{
		ID:          id,
		Name:        req.Name,
		Sku:         req.SKU,
		Category:    orDefault(req.Category, "other"),
		LocationID:  parseUUIDPtr(req.LocationID),
		MinQuantity: req.MinQuantity,
		UnitCost:    req.UnitCost,
		Notes:       req.Notes,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toSparePartDTO(p))
}

type adjustStockReq struct {
	Quantity int32 `json:"quantity"`
}

func (s *Server) adjustSparePartStock(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req adjustStockReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Quantity < 0 {
		http.Error(w, "quantity must be >= 0", http.StatusBadRequest)
		return
	}
	p, err := s.queries.AdjustSparePartStock(ctx, db.AdjustSparePartStockParams{ID: id, Quantity: req.Quantity})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toSparePartDTO(p))
}

func (s *Server) deleteSparePart(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if err := s.queries.DeleteSparePart(ctx, id); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- Work-order parts (consumption) ---------------------------------------

type consumePartReq struct {
	SparePartID *string `json:"spare_part_id"` // nil → free-text part (no stock)
	Description string  `json:"description"`
	Quantity    int32   `json:"quantity"`
	UnitCost    float64 `json:"unit_cost"` // used only for free-text parts
}

func (s *Server) addWorkOrderPart(w http.ResponseWriter, r *http.Request) {
	ctx, woID, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req consumePartReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Quantity <= 0 {
		http.Error(w, "quantity must be > 0", http.StatusBadRequest)
		return
	}
	partID := parseUUIDPtr(req.SparePartID)
	if partID == nil {
		// Free-text part: record without touching stock.
		desc := req.Description
		if desc == "" {
			http.Error(w, "description is required for a non-stock part", http.StatusBadRequest)
			return
		}
		row, err := s.queries.AddFreeWorkOrderPart(ctx, db.AddFreeWorkOrderPartParams{
			WorkOrderID: woID, Description: desc, Quantity: req.Quantity, UnitCost: req.UnitCost,
		})
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, row)
		return
	}
	// Stock part: atomically decrement + record. ErrNoRows ⇒ insufficient stock.
	row, err := s.queries.ConsumePartToWorkOrder(ctx, db.ConsumePartToWorkOrderParams{
		WorkOrderID: woID,
		ID:          *partID,
		Quantity:    req.Quantity,
		Description: req.Description,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "insufficient stock for the requested quantity", http.StatusConflict)
			return
		}
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

func (s *Server) listWorkOrderParts(w http.ResponseWriter, r *http.Request) {
	ctx, woID, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	rows, err := s.queries.ListWorkOrderParts(ctx, woID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// ---- Purchases -------------------------------------------------------------

type purchaseReq struct {
	Description string  `json:"description"`
	Vendor      *string `json:"vendor"`
	Category    string  `json:"category"`
	LocationID  *string `json:"location_id"`
	SystemID    *string `json:"system_id"`
	DeviceID    *string `json:"device_id"`
	Amount      float64 `json:"amount"`
	PurchasedAt *string `json:"purchased_at"` // YYYY-MM-DD; defaults to today
	InvoiceRef  *string `json:"invoice_ref"`
	Notes       *string `json:"notes"`
}

func (s *Server) listPurchases(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListPurchases(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) createPurchase(w http.ResponseWriter, r *http.Request) {
	var req purchaseReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Description == "" {
		http.Error(w, "description is required", http.StatusBadRequest)
		return
	}
	when := time.Now().UTC()
	if d := parseDatePtr(req.PurchasedAt); d != nil {
		when = *d
	}
	p, err := s.queries.CreatePurchase(r.Context(), db.CreatePurchaseParams{
		Description: req.Description,
		Vendor:      req.Vendor,
		Category:    orDefault(req.Category, "other"),
		LocationID:  parseUUIDPtr(req.LocationID),
		SystemID:    parseUUIDPtr(req.SystemID),
		DeviceID:    parseUUIDPtr(req.DeviceID),
		Amount:      req.Amount,
		PurchasedAt: when,
		InvoiceRef:  req.InvoiceRef,
		Notes:       req.Notes,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) deletePurchase(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if err := s.queries.DeletePurchase(ctx, id); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- Expenses (aggregation over the purchases ledger) ---------------------

func (s *Server) expensesByCategory(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ExpensesByCategory(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) expensesByLocation(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ExpensesByLocation(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}
