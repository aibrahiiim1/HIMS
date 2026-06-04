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
			m.CollectionStatus = "never"
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
	case m.CollectionStatus == "never":
		// Checks exist but the sweep has never run — not healthy.
		m.Status = "warning"
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

// ---- Infrastructure health score (aggregate of all health sections) --------

type sectionHealth struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Score    int    `json:"score"`
	Included bool   `json:"included"`
	Reason   string `json:"reason"`
}
type alertHealthDTO struct {
	Status       string  `json:"status"`
	OpenCritical int     `json:"open_critical"`
	OpenWarning  int     `json:"open_warning"`
	Acknowledged int     `json:"acknowledged"`
	Unresolved   int     `json:"unresolved"`
	LastAlertAt  *string `json:"last_alert_at"`
	ActiveRules  int     `json:"active_rules"`
}
type infraOverall struct {
	Score          int      `json:"score"`
	Status         string   `json:"status"`
	Confidence     string   `json:"confidence"`
	LimitedReasons []string `json:"limited_reasons"`
}

func scoreForStatus(s string) int {
	switch s {
	case "healthy":
		return 100
	case "warning":
		return 65
	case "critical":
		return 25
	default:
		return 0
	}
}

// securitySection derives the Security health section from the live cipher +
// credential metadata — the same real signals the Encryption status uses.
func (s *Server) securitySection(ctx context.Context) sectionHealth {
	encN, _ := s.queries.CountEncryptedCredentials(ctx)
	reentry, _ := s.queries.CountCredentialsNeedingReentry(ctx)
	if s.cipher == nil {
		if encN > 0 {
			return sectionHealth{Name: "Security", Status: "critical", Reason: "Encryption key missing; credential secrets are locked."}
		}
		return sectionHealth{Name: "Security", Status: "unknown", Reason: "No encryption key configured yet."}
	}
	if und, err := s.queries.CountUndecryptableCredentials(ctx, s.cipher.KeyID()); err == nil && und > 0 {
		return sectionHealth{Name: "Security", Status: "critical", Reason: "Credentials sealed with a different key cannot be decrypted."}
	}
	if meta, err := s.queries.GetEncryptionMetadata(ctx); err == nil && meta.Fingerprint != "" && meta.Fingerprint != s.cipher.Fingerprint() {
		return sectionHealth{Name: "Security", Status: "warning", Reason: "Loaded key fingerprint does not match the recorded fingerprint."}
	}
	if reentry > 0 {
		return sectionHealth{Name: "Security", Status: "warning", Reason: "Some credentials need their secret re-entered."}
	}
	return sectionHealth{Name: "Security", Status: "healthy"}
}

func (s *Server) alertHealth(ctx context.Context) (alertHealthDTO, bool) {
	a := alertHealthDTO{Status: "unknown"}
	alerts, err := s.queries.ListAlerts(ctx)
	if err != nil {
		return a, false
	}
	var newest time.Time
	for _, al := range alerts {
		if al.Status != "resolved" {
			a.Unresolved++
		}
		switch {
		case al.Status == "acknowledged":
			a.Acknowledged++
		case al.Status == "open" && al.Severity == "critical":
			a.OpenCritical++
		case al.Status == "open" && al.Severity == "warning":
			a.OpenWarning++
		}
		if al.OpenedAt.After(newest) {
			newest = al.OpenedAt
		}
	}
	a.LastAlertAt = iso(newest)
	if rules, rerr := s.queries.ListAlertRules(ctx); rerr == nil {
		for _, r := range rules {
			if r.Enabled {
				a.ActiveRules++
			}
		}
	}
	switch {
	case a.OpenCritical > 0:
		a.Status = "critical"
	case a.OpenWarning > 0:
		a.Status = "warning"
	default:
		a.Status = "healthy"
	}
	return a, true
}

func (s *Server) infrastructureHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	al, _ := s.alertHealth(ctx)
	sections := []sectionHealth{
		s.securitySection(ctx),
		{Name: "Discovery", Status: s.discoveryHealth(r).Status},
		{Name: "Monitoring", Status: s.monitoringHealth(r).Status},
		{Name: "Topology", Status: s.topologyHealth(ctx).Status},
		{Name: "Alert Health", Status: al.Status},
	}
	sum, n := 0, 0
	reasons := []string{}
	for i := range sections {
		sections[i].Score = scoreForStatus(sections[i].Status)
		sections[i].Included = sections[i].Status != "unknown" && sections[i].Status != ""
		if sections[i].Included {
			sum += sections[i].Score
			n++
		} else {
			rsn := sections[i].Reason
			if rsn == "" {
				rsn = sections[i].Name + " not collected yet"
			}
			reasons = append(reasons, rsn)
		}
	}
	overall := infraOverall{Confidence: "high", LimitedReasons: reasons}
	if n == 0 {
		overall.Status, overall.Confidence = "unknown", "unknown"
	} else {
		overall.Score = int(float64(sum)/float64(n) + 0.5)
		if len(reasons) > 0 {
			overall.Confidence = "limited"
		}
		switch {
		case overall.Score >= 90:
			overall.Status = "excellent"
		case overall.Score >= 75:
			overall.Status = "good"
		case overall.Score >= 50:
			overall.Status = "needs_attention"
		default:
			overall.Status = "critical"
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"overall": overall, "sections": sections, "alerts": al})
}
