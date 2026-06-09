package api

import (
	"net/http"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// Analytics endpoints power the enterprise dashboards with real historical
// trend data derived from monitoring_samples (reachability time-series) and
// alerts. Percentages are computed here from raw counts; latency / MTTA / MTTR
// are emitted as null when there is no underlying data (never a fake 0).

// analyticsWindow maps a UI window token to a date_trunc granularity + a
// Postgres interval string. Unknown tokens fall back to the 24h view.
func analyticsWindow(w string) (token, bucket, interval string) {
	switch w {
	case "1h":
		return "1h", "minute", "1 hour"
	case "7d":
		return "7d", "day", "7 days"
	case "30d":
		return "30d", "day", "30 days"
	default:
		return "24h", "hour", "24 hours"
	}
}

// pct returns 100*part/whole rounded to 2 decimals, or 0 when whole is 0.
func pct(part, whole int64) float64 {
	if whole <= 0 {
		return 0
	}
	return float64(int64(float64(part)/float64(whole)*10000+0.5)) / 100
}

// nz returns a pointer to v, or nil when v <= 0 (so "no data" reads as null in
// JSON rather than a misleading zero for latency / MTTA / MTTR).
func nzf(v float64) *float64 {
	if v <= 0 {
		return nil
	}
	return &v
}

type availabilityPoint struct {
	TS           string   `json:"ts"`
	Total        int64    `json:"total"`
	Up           int64    `json:"up"`
	Down         int64    `json:"down"`
	Warning      int64    `json:"warning"`
	UptimePct    float64  `json:"uptime_pct"`
	AvgLatencyMs *float64 `json:"avg_latency_ms"`
	P95LatencyMs *float64 `json:"p95_latency_ms"`
}

type availabilitySummary struct {
	Samples      int64    `json:"samples"`
	Up           int64    `json:"up"`
	Down         int64    `json:"down"`
	Warning      int64    `json:"warning"`
	Devices      int64    `json:"devices"`
	UptimePct    float64  `json:"uptime_pct"`
	AvgLatencyMs *float64 `json:"avg_latency_ms"`
	P95LatencyMs *float64 `json:"p95_latency_ms"`
}

// analyticsAvailability handles GET /analytics/availability?window=24h|7d|30d.
// Fleet-wide reachability over time + a window rollup, from monitoring_samples.
func (s *Server) analyticsAvailability(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token, bucket, interval := analyticsWindow(r.URL.Query().Get("window"))

	series, err := s.queries.FleetAvailabilitySeries(ctx, db.FleetAvailabilitySeriesParams{Column1: bucket, Column2: interval})
	if err != nil {
		writeErr(w, err)
		return
	}
	sum, err := s.queries.FleetAvailabilitySummary(ctx, interval)
	if err != nil {
		writeErr(w, err)
		return
	}

	points := make([]availabilityPoint, 0, len(series))
	for _, p := range series {
		points = append(points, availabilityPoint{
			TS:           p.Bucket.UTC().Format("2006-01-02T15:04:05Z07:00"),
			Total:        p.Total,
			Up:           p.Up,
			Down:         p.Down,
			Warning:      p.Warning,
			UptimePct:    pct(p.Up, p.Total),
			AvgLatencyMs: nzf(p.AvgLatencyMs),
			P95LatencyMs: nzf(p.P95LatencyMs),
		})
	}
	out := map[string]any{
		"window": token,
		"bucket": bucket,
		"summary": availabilitySummary{
			Samples:      sum.Total,
			Up:           sum.Up,
			Down:         sum.Down,
			Warning:      sum.Warning,
			Devices:      sum.Devices,
			UptimePct:    pct(sum.Up, sum.Total),
			AvgLatencyMs: nzf(sum.AvgLatencyMs),
			P95LatencyMs: nzf(sum.P95LatencyMs),
		},
		"series": points,
	}
	writeJSON(w, http.StatusOK, out)
}

type deviceUptimeRow struct {
	DeviceID     string   `json:"device_id"`
	Name         string   `json:"name"`
	PrimaryIP    *string  `json:"primary_ip"`
	Category     string   `json:"category"`
	Samples      int64    `json:"samples"`
	Up           int64    `json:"up"`
	UptimePct    float64  `json:"uptime_pct"`
	AvgLatencyMs *float64 `json:"avg_latency_ms"`
	MaxLatencyMs *float64 `json:"max_latency_ms"`
	Flaps        int64    `json:"flaps"`
}

// analyticsDeviceUptime handles GET /analytics/device-uptime?window=24h|7d|30d.
// Per-device availability over the window, worst-first (for worst-performers +
// flapping lists). Scoped to the requester's site like the rest of inventory.
func (s *Server) analyticsDeviceUptime(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_, _, interval := analyticsWindow(r.URL.Query().Get("window"))

	rows, err := s.queries.DeviceUptimeRanking(ctx, interval)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]deviceUptimeRow, 0, len(rows))
	for _, d := range rows {
		row := deviceUptimeRow{
			DeviceID:     d.DeviceID.String(),
			Name:         d.Name,
			Category:     d.Category,
			Samples:      d.Samples,
			Up:           d.Up,
			UptimePct:    pct(d.Up, d.Samples),
			AvgLatencyMs: nzf(d.AvgLatencyMs),
			MaxLatencyMs: nzf(d.MaxLatencyMs),
			Flaps:        d.Flaps,
		}
		if d.PrimaryIp != nil && d.PrimaryIp.IsValid() {
			ip := d.PrimaryIp.String()
			row.PrimaryIP = &ip
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, out)
}

// analyticsAlerts handles GET /analytics/alerts?window=7d|30d. Alert volume +
// MTTA/MTTR + opened-per-bucket trend. MTTA/MTTR are null until acknowledged /
// resolved alerts exist, so the UI shows an honest empty state rather than 0.
func (s *Server) analyticsAlerts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token, bucket, interval := analyticsWindow(r.URL.Query().Get("window"))

	sum, err := s.queries.AlertAnalyticsSummary(ctx, interval)
	if err != nil {
		writeErr(w, err)
		return
	}
	series, err := s.queries.AlertOpenedSeries(ctx, db.AlertOpenedSeriesParams{Column1: bucket, Column2: interval})
	if err != nil {
		writeErr(w, err)
		return
	}
	points := make([]map[string]any, 0, len(series))
	for _, p := range series {
		points = append(points, map[string]any{
			"ts":       p.Bucket.UTC().Format("2006-01-02T15:04:05Z07:00"),
			"opened":   p.Opened,
			"critical": p.Critical,
			"warning":  p.Warning,
		})
	}
	out := map[string]any{
		"window":        token,
		"bucket":        bucket,
		"opened":        sum.Opened,
		"resolved":      sum.Resolved,
		"open_now":      sum.OpenNow,
		"open_critical": sum.OpenCritical,
		"mtta_seconds":  nzf(sum.MttaSeconds),
		"mttr_seconds":  nzf(sum.MttrSeconds),
		"series":        points,
	}
	writeJSON(w, http.StatusOK, out)
}
