package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/coralsearesorts/hims/internal/reports"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/go-chi/chi/v5"
)

// Reports Pro (#21): server-authoritative report assembly + multi-format export
// (CSV / XLSX). The same Report can be downloaded or emailed by the scheduler.

func ipStr(d db.Device) string {
	if d.PrimaryIp == nil {
		return ""
	}
	return d.PrimaryIp.String()
}

// countBy tallies items by a key function into a sorted (desc by count) sheet.
func countByRows(values []string) [][]string {
	counts := map[string]int{}
	for _, v := range values {
		if v == "" {
			v = "(unset)"
		}
		counts[v]++
	}
	type kv struct {
		k string
		n int
	}
	list := make([]kv, 0, len(counts))
	for k, n := range counts {
		list = append(list, kv{k, n})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].n != list[j].n {
			return list[i].n > list[j].n
		}
		return list[i].k < list[j].k
	})
	rows := make([][]string, len(list))
	for i, e := range list {
		rows[i] = []string{e.k, fmt.Sprintf("%d", e.n)}
	}
	return rows
}

func (s *Server) inventorySheets(ctx context.Context) ([]reports.Sheet, error) {
	devs, err := s.queries.ListAllDevices(ctx)
	if err != nil {
		return nil, err
	}
	deviceRows := make([][]string, len(devs))
	var cats, vendors, statuses []string
	for i, d := range devs {
		deviceRows[i] = []string{d.Name, ipStr(d), derefStr(d.Vendor), derefStr(d.Model), d.Category, d.Status, derefStr(d.Driver)}
		cats = append(cats, d.Category)
		vendors = append(vendors, derefStr(d.Vendor))
		statuses = append(statuses, d.Status)
	}
	return []reports.Sheet{
		{Name: "Devices", Headers: []string{"Name", "IP", "Vendor", "Model", "Category", "Status", "Driver"}, Rows: deviceRows},
		{Name: "By Category", Headers: []string{"Category", "Count"}, Rows: countByRows(cats)},
		{Name: "By Vendor", Headers: []string{"Vendor", "Count"}, Rows: countByRows(vendors)},
		{Name: "By Status", Headers: []string{"Status", "Count"}, Rows: countByRows(statuses)},
	}, nil
}

func (s *Server) availabilitySheets(ctx context.Context) ([]reports.Sheet, error) {
	rows, err := s.queries.MonitoringStatusOverview(ctx)
	if err != nil {
		return nil, err
	}
	out := make([][]string, len(rows))
	for i, r := range rows {
		out[i] = []string{r.Status, fmt.Sprintf("%d", r.Count)}
	}
	return []reports.Sheet{
		{Name: "Availability", Headers: []string{"Device Reachability", "Devices"}, Rows: out},
	}, nil
}

func (s *Server) vendorSheets(ctx context.Context) ([]reports.Sheet, error) {
	devs, err := s.queries.ListAllDevices(ctx)
	if err != nil {
		return nil, err
	}
	vendors := make([]string, len(devs))
	for i, d := range devs {
		vendors[i] = derefStr(d.Vendor)
	}
	return []reports.Sheet{
		{Name: "By Vendor", Headers: []string{"Vendor", "Count"}, Rows: countByRows(vendors)},
	}, nil
}

// buildReport assembles the named report from live data.
func (s *Server) buildReport(ctx context.Context, kind string, now time.Time) (reports.Report, error) {
	r := reports.Report{Generated: now}
	add := func(fn func(context.Context) ([]reports.Sheet, error)) error {
		sh, err := fn(ctx)
		if err != nil {
			return err
		}
		r.Sheets = append(r.Sheets, sh...)
		return nil
	}
	switch kind {
	case "inventory":
		r.Title = "Inventory Report"
		return r, add(s.inventorySheets)
	case "availability":
		r.Title = "Availability Report"
		return r, add(s.availabilitySheets)
	case "vendors":
		r.Title = "Vendor Report"
		return r, add(s.vendorSheets)
	case "all":
		r.Title = "Full Inventory & Health Report"
		if err := add(s.inventorySheets); err != nil {
			return r, err
		}
		if err := add(s.availabilitySheets); err != nil {
			return r, err
		}
		return r, nil
	default:
		return r, errBadRequest("unknown report type (inventory|availability|vendors|all)")
	}
}

// exportReport handles GET /reports/{type}/export?format=xlsx|csv — builds the
// report from live data and streams it as a downloadable file.
func (s *Server) exportReport(w http.ResponseWriter, r *http.Request) {
	kind := chi.URLParam(r, "type")
	format := strings.ToLower(r.URL.Query().Get("format"))
	if format == "" {
		format = "xlsx"
	}
	rep, err := s.buildReport(r.Context(), kind, time.Now().UTC())
	if err != nil {
		if _, ok := err.(*badRequest); ok {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeErr(w, err)
		return
	}
	stamp := time.Now().UTC().Format("20060102-1504")
	switch format {
	case "csv":
		b, err := rep.CSV()
		if err != nil {
			writeErr(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", kind+"-"+stamp+".csv"))
		_, _ = w.Write(b)
	case "xlsx":
		b, err := rep.XLSX()
		if err != nil {
			writeErr(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", kind+"-"+stamp+".xlsx"))
		_, _ = w.Write(b)
	default:
		http.Error(w, "format must be xlsx or csv", http.StatusBadRequest)
	}
}
