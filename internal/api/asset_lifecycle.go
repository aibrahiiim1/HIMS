package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/coralsearesorts/hims/internal/operations"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/jackc/pgx/v5"
)

// Asset Lifecycle (#18): per-device warranty / EOL / owner / cost, with
// warranty + EOL statuses derived at read time from the dates (reusing the
// license-status logic). Maintenance history is the device's linked work
// orders (GET /devices/{id}/work-orders).

func dateStr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}

// lifecycleStatuses derives warranty + EOL status from the dates.
func lifecycleStatuses(warranty, eol *time.Time, now time.Time) (string, string) {
	return string(operations.ComputeLicenseStatus(warranty, now)),
		string(operations.ComputeLicenseStatus(eol, now))
}

type lifecycleDTO struct {
	DeviceID       string  `json:"device_id"`
	DeviceName     string  `json:"device_name,omitempty"`
	Category       string  `json:"category,omitempty"`
	PrimaryIP      string  `json:"primary_ip,omitempty"`
	Owner          string  `json:"owner"`
	Supplier       string  `json:"supplier"`
	PurchaseDate   string  `json:"purchase_date"`
	WarrantyExpiry string  `json:"warranty_expiry"`
	EolDate        string  `json:"eol_date"`
	Cost           float64 `json:"cost"`
	Notes          string  `json:"notes"`
	WarrantyStatus string  `json:"warranty_status"`
	EolStatus      string  `json:"eol_status"`
}

// getDeviceLifecycle handles GET /devices/{id}/lifecycle. Returns an empty
// (zero) lifecycle with statuses "unknown" when none is recorded yet.
func (s *Server) getDeviceLifecycle(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	now := time.Now().UTC()
	row, err := s.queries.GetDeviceLifecycle(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		ws, es := lifecycleStatuses(nil, nil, now)
		writeJSON(w, http.StatusOK, lifecycleDTO{DeviceID: id.String(), WarrantyStatus: ws, EolStatus: es})
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	ws, es := lifecycleStatuses(row.WarrantyExpiry, row.EolDate, now)
	writeJSON(w, http.StatusOK, lifecycleDTO{
		DeviceID: row.DeviceID.String(), Owner: row.Owner, Supplier: row.Supplier,
		PurchaseDate: dateStr(row.PurchaseDate), WarrantyExpiry: dateStr(row.WarrantyExpiry),
		EolDate: dateStr(row.EolDate), Cost: row.Cost, Notes: row.Notes,
		WarrantyStatus: ws, EolStatus: es,
	})
}

type putLifecycleReq struct {
	Owner          string  `json:"owner"`
	Supplier       string  `json:"supplier"`
	PurchaseDate   *string `json:"purchase_date"`
	WarrantyExpiry *string `json:"warranty_expiry"`
	EolDate        *string `json:"eol_date"`
	Cost           float64 `json:"cost"`
	Notes          string  `json:"notes"`
}

// putDeviceLifecycle handles PUT /devices/{id}/lifecycle (upsert).
func (s *Server) putDeviceLifecycle(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	var req putLifecycleReq
	if !decodeJSON(w, r, &req) {
		return
	}
	row, err := s.queries.UpsertDeviceLifecycle(ctx, db.UpsertDeviceLifecycleParams{
		DeviceID: id, Owner: req.Owner, Supplier: req.Supplier,
		PurchaseDate: parseDatePtr(req.PurchaseDate), WarrantyExpiry: parseDatePtr(req.WarrantyExpiry),
		EolDate: parseDatePtr(req.EolDate), Cost: req.Cost, Notes: req.Notes,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "inventory", "asset.lifecycle.update", "device", id.String(), "Updated asset lifecycle", nil)
	now := time.Now().UTC()
	ws, es := lifecycleStatuses(row.WarrantyExpiry, row.EolDate, now)
	writeJSON(w, http.StatusOK, lifecycleDTO{
		DeviceID: row.DeviceID.String(), Owner: row.Owner, Supplier: row.Supplier,
		PurchaseDate: dateStr(row.PurchaseDate), WarrantyExpiry: dateStr(row.WarrantyExpiry),
		EolDate: dateStr(row.EolDate), Cost: row.Cost, Notes: row.Notes,
		WarrantyStatus: ws, EolStatus: es,
	})
}

// assetLifecycleRegister handles GET /assets/lifecycle — the tracked-asset
// register with computed statuses + a rollup summary.
func (s *Server) assetLifecycleRegister(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListAssetLifecycle(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	now := time.Now().UTC()
	out := make([]lifecycleDTO, len(rows))
	var totalCost float64
	summary := map[string]int{"in_warranty": 0, "warranty_expiring": 0, "warranty_expired": 0, "eol": 0, "eol_approaching": 0}
	for i, row := range rows {
		ws, es := lifecycleStatuses(row.WarrantyExpiry, row.EolDate, now)
		ip := ""
		if row.PrimaryIp != nil {
			ip = row.PrimaryIp.String()
		}
		out[i] = lifecycleDTO{
			DeviceID: row.DeviceID.String(), DeviceName: row.DeviceName, Category: row.Category,
			PrimaryIP: ip, Owner: row.Owner, Supplier: row.Supplier, PurchaseDate: dateStr(row.PurchaseDate),
			WarrantyExpiry: dateStr(row.WarrantyExpiry), EolDate: dateStr(row.EolDate), Cost: row.Cost,
			Notes: row.Notes, WarrantyStatus: ws, EolStatus: es,
		}
		totalCost += row.Cost
		switch ws {
		case "expired":
			summary["warranty_expired"]++
		case "active", "unknown":
			summary["in_warranty"]++
		default:
			summary["warranty_expiring"]++
		}
		switch es {
		case "expired":
			summary["eol"]++
		case "active", "unknown":
		default:
			summary["eol_approaching"]++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"assets":     out,
		"total":      len(out),
		"total_cost": totalCost,
		"summary":    summary,
	})
}
