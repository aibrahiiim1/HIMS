package api

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// Operational health aggregation for the Dashboard. Every value is derived
// from real tables; metrics with no backing data are returned as null so the
// UI can show "Not collected yet" rather than a fabricated number.

const isoFmt = "2006-01-02T15:04:05Z07:00"

func iso(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := t.Format(isoFmt)
	return &s
}
func isoP(t *time.Time) *string {
	if t == nil || t.IsZero() {
		return nil
	}
	return iso(*t)
}

type discoveryHealth struct {
	Status                 string  `json:"status"`
	LastScanAt             *string `json:"last_scan_at"`
	LastScanStatus         string  `json:"last_scan_status"`
	SuccessfulScanPercent  *int    `json:"successful_scan_percent"`
	FailedScanCount        int     `json:"failed_scan_count"`
	CredentialFailureCount *int    `json:"credential_failure_count"` // not collected as a distinct metric → null
	PendingJobCount        int     `json:"pending_job_count"`
}
type monitoringHealth struct {
	Status           string  `json:"status"`
	MonitoredDevices int64   `json:"monitored_devices"`
	OnlineDevices    int64   `json:"online_devices"`
	OfflineDevices   int64   `json:"offline_devices"`
	CriticalAlerts   int     `json:"critical_alerts"`
	WarningAlerts    int     `json:"warning_alerts"`
	LastCollectionAt *string `json:"last_collection_at"`
	CollectionStatus string  `json:"collection_status"`
}
type topologyHealth struct {
	Status                string  `json:"status"`
	MappedDevices         int     `json:"mapped_devices"`
	UnmappedDevices       int     `json:"unmapped_devices"`
	MissingNeighbors      int     `json:"missing_neighbors"`
	CoveragePercent       *int    `json:"coverage_percent"`
	LldpCdpDataAge        *string `json:"lldp_cdp_data_age"`
	LastTopologyRefreshAt *string `json:"last_topology_refresh_at"`
}

func (s *Server) operationalHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := map[string]any{
		"discovery":  s.discoveryHealth(r),
		"monitoring": s.monitoringHealth(r),
		"topology":   s.topologyHealth(ctx),
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) discoveryHealth(r *http.Request) discoveryHealth {
	ctx := r.Context()
	d := discoveryHealth{Status: "unknown", LastScanStatus: "never"}
	jobs, err := s.queries.ListDiscoveryJobs(ctx)
	if err != nil || len(jobs) == 0 {
		return d
	}
	// jobs come ordered newest-first; consider a recent window for rates.
	recent := jobs
	if len(recent) > 50 {
		recent = recent[:50]
	}
	completed, failed, running := 0, 0, 0
	for _, j := range recent {
		switch j.Status {
		case "completed":
			completed++
		case "failed":
			failed++
		case "running", "queued", "pending":
			running++
		}
	}
	d.FailedScanCount = failed
	d.PendingJobCount = running
	if fin := completed + failed; fin > 0 {
		p := int(float64(completed) / float64(fin) * 100.0)
		d.SuccessfulScanPercent = &p
	}
	last := jobs[0]
	d.LastScanAt = isoP(last.FinishedAt)
	if d.LastScanAt == nil {
		d.LastScanAt = iso(last.CreatedAt)
	}
	switch last.Status {
	case "completed":
		d.LastScanStatus = "success"
	case "failed":
		d.LastScanStatus = "failed"
	case "running", "queued", "pending":
		d.LastScanStatus = "running"
	default:
		d.LastScanStatus = last.Status
	}
	// Health: last failed or only failures → critical; some failures → warning; else healthy.
	switch {
	case last.Status == "failed" || (failed > 0 && completed == 0):
		d.Status = "critical"
	case failed > 0:
		d.Status = "warning"
	default:
		d.Status = "healthy"
	}
	return d
}

func (s *Server) monitoringHealth(r *http.Request) monitoringHealth {
	ctx := r.Context()
	m := monitoringHealth{Status: "unknown", CollectionStatus: "not configured"}
	ov, err := s.queries.MonitoringStatusOverview(ctx)
	if err != nil {
		return m
	}
	var up, down, warn, total int64
	for _, row := range ov {
		total += row.Count
		switch row.Status {
		case "up":
			up = row.Count
		case "down":
			down = row.Count
		case "warning":
			warn = row.Count
		}
	}
	m.MonitoredDevices, m.OnlineDevices, m.OfflineDevices = total, up, down
	if total == 0 {
		return m // unknown / not configured
	}
	// Last collection = newest check run.
	if checks, cerr := s.queries.ListMonitoringChecks(ctx); cerr == nil {
		var newest *time.Time
		for _, c := range checks {
			if c.LastRunAt != nil && (newest == nil || c.LastRunAt.After(*newest)) {
				newest = c.LastRunAt
			}
		}
		m.LastCollectionAt = isoP(newest)
		if newest == nil {
			m.CollectionStatus = "idle"
		} else if time.Since(*newest) > time.Hour {
			m.CollectionStatus = "stale"
		} else {
			m.CollectionStatus = "active"
		}
	}
	if alerts, aerr := s.queries.ListAlerts(ctx); aerr == nil {
		for _, a := range alerts {
			if a.Status == "resolved" {
				continue
			}
			switch a.Severity {
			case "critical":
				m.CriticalAlerts++
			case "warning":
				m.WarningAlerts++
			}
		}
	}
	// Health
	switch {
	case m.CriticalAlerts > 0 || (total > 0 && float64(down)/float64(total) > 0.25):
		m.Status = "critical"
	case m.WarningAlerts > 0 || down > 0 || warn > 0 || m.CollectionStatus == "stale":
		m.Status = "warning"
	default:
		m.Status = "healthy"
	}
	return m
}

func (s *Server) topologyHealth(ctx context.Context) topologyHealth {
	t := topologyHealth{Status: "unknown"}
	links, err := s.queries.ListAllTopologyLinks(ctx)
	if err != nil || len(links) == 0 {
		return t
	}
	mapped := map[uuid.UUID]struct{}{}
	missing := 0
	var newest time.Time
	for _, l := range links {
		mapped[l.LocalDeviceID] = struct{}{}
		if l.RemoteDeviceID != nil {
			mapped[*l.RemoteDeviceID] = struct{}{}
		} else {
			missing++
		}
		if l.LastSeenAt.After(newest) {
			newest = l.LastSeenAt
		}
	}
	t.MappedDevices = len(mapped)
	t.MissingNeighbors = missing
	t.LastTopologyRefreshAt = iso(newest)

	if devs, derr := s.queries.ListAllDevices(ctx); derr == nil {
		total := len(devs)
		if total > 0 {
			t.UnmappedDevices = total - t.MappedDevices
			if t.UnmappedDevices < 0 {
				t.UnmappedDevices = 0
			}
			cov := int(float64(t.MappedDevices) / float64(total) * 100.0)
			t.CoveragePercent = &cov
		}
	}
	if maxSeen, nerr := s.queries.MaxNeighborSeenAt(ctx); nerr == nil && !maxSeen.IsZero() {
		t.LldpCdpDataAge = iso(maxSeen)
	}
	// Health by coverage.
	cov := 0
	if t.CoveragePercent != nil {
		cov = *t.CoveragePercent
	}
	switch {
	case cov >= 70:
		t.Status = "healthy"
	case cov >= 30:
		t.Status = "warning"
	default:
		t.Status = "critical"
	}
	return t
}
