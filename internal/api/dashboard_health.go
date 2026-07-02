package api

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
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

// fabricCategories are the topology-capable device classes — switches and
// routers (incl. ISP routers). Topology coverage, the Unmapped Devices view and
// its sidebar badge all measure over this set only; endpoints, servers, APs,
// printers, controllers etc. don't form LLDP/CDP links.
var fabricCategories = map[string]bool{"switch": true, "router": true, "isp_router": true}

// mappedDeviceIDs returns the set of device IDs that appear in at least one
// topology link (as the local or remote endpoint). A failed query yields an
// empty set (everything reads as unmapped) rather than an error — callers treat
// this as best-effort coverage data.
func (s *Server) mappedDeviceIDs(ctx context.Context) map[uuid.UUID]struct{} {
	mapped := map[uuid.UUID]struct{}{}
	links, err := s.queries.ListAllTopologyLinks(ctx)
	if err != nil {
		return mapped
	}
	for _, l := range links {
		mapped[l.LocalDeviceID] = struct{}{}
		if l.RemoteDeviceID != nil {
			mapped[*l.RemoteDeviceID] = struct{}{}
		}
	}
	return mapped
}

// isUnmappedFabric reports whether d is a topology-capable fabric device that is
// absent from every topology link — the predicate behind the Unmapped Devices
// view and its sidebar badge. Kept beside topologyHealth so coverage and the
// drill-down list always agree on what "unmapped" means.
func isUnmappedFabric(d db.Device, mapped map[uuid.UUID]struct{}) bool {
	if !fabricCategories[d.Category] {
		return false
	}
	_, ok := mapped[d.ID]
	return !ok
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
	t.MissingNeighbors = missing
	t.LastTopologyRefreshAt = iso(newest)

	// Coverage is measured over the topology-capable FABRIC only — switches and
	// routers (incl. ISP routers). Endpoints, controllers, servers, APs, printers
	// etc. don't form LLDP/CDP links, so counting them in the denominator would
	// permanently understate coverage and keep this panel stuck on "warning".
	hasFabric := false
	if devs, derr := s.queries.ListAllDevices(ctx); derr == nil {
		totalFabric, mappedFabric := 0, 0
		for _, dv := range devs {
			if !fabricCategories[dv.Category] {
				continue
			}
			totalFabric++
			if _, ok := mapped[dv.ID]; ok {
				mappedFabric++
			}
		}
		t.MappedDevices = mappedFabric
		if totalFabric > 0 {
			hasFabric = true
			t.UnmappedDevices = totalFabric - mappedFabric
			cov := int(float64(mappedFabric) / float64(totalFabric) * 100.0)
			t.CoveragePercent = &cov
		}
	}
	if maxSeen, nerr := s.queries.MaxNeighborSeenAt(ctx); nerr == nil && !maxSeen.IsZero() {
		t.LldpCdpDataAge = iso(maxSeen)
	}
	// Health by fabric coverage. With no switches/routers in inventory there is
	// nothing to map — report "unknown" rather than a misleading "critical".
	if !hasFabric {
		t.Status = "unknown"
		return t
	}
	switch cov := *t.CoveragePercent; {
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
	Name       string `json:"name"`
	Status     string `json:"status"`
	Score      int    `json:"score"`
	Included   bool   `json:"included"`
	Reason     string `json:"reason"`
	ReasonCode string `json:"reason_code"` // machine-readable driver (e.g. "critical_alerts_open")
	Link       string `json:"link"`        // real frontend route/filter to drill into this section
	Drivers    int    `json:"drivers"`     // count of contributing items (alerts/offline/etc.)
}

// infraDriver is one concrete contributor to a degraded score — a real open alert
// (never fabricated). It carries everything the card needs to render a row and a
// deep link, so the UI shows no hardcoded blockers.
type infraDriver struct {
	DeviceID    string  `json:"device_id,omitempty"`
	IP          string  `json:"ip,omitempty"`
	Name        string  `json:"name"`
	Section     string  `json:"section"`  // which health section this drives
	Severity    string  `json:"severity"` // critical | warning
	Label       string  `json:"label"`    // the alert message
	Protocol    string  `json:"protocol,omitempty"`
	Port        int     `json:"port,omitempty"`
	LastChanged *string `json:"last_changed,omitempty"`
	Link        string  `json:"link"`
}

// infraHygiene surfaces the alert-hygiene signal (open alerts with no check
// linkage) so the operator sees it without it silently affecting the score.
type infraHygiene struct {
	NullCheckIDOpen int    `json:"null_check_id_open"`
	Note            string `json:"note"`
	Link            string `json:"link"`
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
	Score            int      `json:"score"`
	Status           string   `json:"status"`
	Confidence       string   `json:"confidence"`
	ConfidenceReason string   `json:"confidence_reason"`
	LimitedReasons   []string `json:"limited_reasons"`
	Summary          string   `json:"summary"`       // one-line plain-English "why"
	CalculatedAt     string   `json:"calculated_at"` // when this was computed
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
	c := s.cipher()
	if c == nil {
		if encN > 0 {
			return sectionHealth{Name: "Security", Status: "critical", Reason: "Encryption key missing; credential secrets are locked."}
		}
		return sectionHealth{Name: "Security", Status: "unknown", Reason: "No encryption key configured yet."}
	}
	if und, err := s.queries.CountUndecryptableCredentials(ctx, c.KeyID()); err == nil && und > 0 {
		return sectionHealth{Name: "Security", Status: "critical", Reason: "Credentials sealed with a different key cannot be decrypted."}
	}
	if meta, err := s.queries.GetEncryptionMetadata(ctx); err == nil && meta.Fingerprint != "" && meta.Fingerprint != c.Fingerprint() {
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
	now := time.Now().UTC()

	// Every section's status + the metrics behind its reason come from live data.
	sec := s.securitySection(ctx)
	disc := s.discoveryHealth(r)
	mon := s.monitoringHealth(r)
	topo := s.topologyHealth(ctx)
	al, _ := s.alertHealth(ctx)

	secReason := sec.Reason
	if secReason == "" && sec.Status == "healthy" {
		secReason = "Encryption key loaded; no credential-key mismatches."
	}
	discReason, discCode := "Recent scans are succeeding.", "ok"
	switch disc.Status {
	case "critical":
		discReason, discCode = "The last discovery scan failed.", "last_scan_failed"
	case "warning":
		discReason, discCode = fmt.Sprintf("%d recent scan(s) failed.", disc.FailedScanCount), "some_scans_failed"
	}
	monReason, monCode := fmt.Sprintf("%d of %d monitored devices offline.", mon.OfflineDevices, mon.MonitoredDevices), "ok"
	if mon.CriticalAlerts > 0 {
		monReason, monCode = fmt.Sprintf("%d open critical monitoring alert(s).", mon.CriticalAlerts), "critical_alerts_open"
	} else if mon.Status == "warning" {
		monCode = "degraded"
		if mon.CollectionStatus == "stale" {
			monReason = "The monitoring sweep is stale."
		}
	}
	topoReason, topoCode := "Fabric topology coverage is healthy.", "ok"
	if topo.CoveragePercent != nil {
		topoReason = fmt.Sprintf("Fabric coverage %d%% (%d unmapped).", *topo.CoveragePercent, topo.UnmappedDevices)
	}
	if topo.Status == "warning" || topo.Status == "critical" {
		topoCode = "low_coverage"
	}
	alReason, alCode := "No open critical or warning alerts.", "ok"
	if al.OpenCritical > 0 {
		alReason, alCode = fmt.Sprintf("%d critical + %d warning alert(s) open.", al.OpenCritical, al.OpenWarning), "critical_alerts_open"
	} else if al.OpenWarning > 0 {
		alReason, alCode = fmt.Sprintf("%d warning alert(s) open.", al.OpenWarning), "warning_alerts_open"
	}

	sections := []sectionHealth{
		{Name: "Security", Status: sec.Status, Reason: secReason, ReasonCode: statusCode(sec.Status), Link: "/security/encryption"},
		{Name: "Discovery", Status: disc.Status, Reason: discReason, ReasonCode: discCode, Link: "/discovery", Drivers: disc.FailedScanCount},
		{Name: "Monitoring", Status: mon.Status, Reason: monReason, ReasonCode: monCode, Link: "/monitoring", Drivers: mon.CriticalAlerts + int(mon.OfflineDevices)},
		{Name: "Topology", Status: topo.Status, Reason: topoReason, ReasonCode: topoCode, Link: "/topology", Drivers: topo.UnmappedDevices},
		{Name: "Alert Health", Status: al.Status, Reason: alReason, ReasonCode: alCode, Link: "/alerts", Drivers: al.OpenCritical + al.OpenWarning},
	}

	sum, n := 0, 0
	limited := []string{}
	var problems []string
	for i := range sections {
		sections[i].Score = scoreForStatus(sections[i].Status)
		sections[i].Included = sections[i].Status != "unknown" && sections[i].Status != ""
		if sections[i].Included {
			sum += sections[i].Score
			n++
			if sections[i].Status != "healthy" {
				problems = append(problems, sections[i].Name)
			}
		} else {
			rsn := sections[i].Reason
			if rsn == "" {
				rsn = sections[i].Name + " not collected yet"
			}
			limited = append(limited, rsn)
		}
	}

	overall := infraOverall{Confidence: "high", LimitedReasons: limited, CalculatedAt: now.Format(isoFmt)}
	if n == 0 {
		overall.Status, overall.Confidence = "unknown", "unknown"
		overall.ConfidenceReason = "No health sections have data yet."
		overall.Summary = "Infrastructure health cannot be computed — no section has data."
	} else {
		overall.Score = int(float64(sum)/float64(n) + 0.5)
		overall.ConfidenceReason = fmt.Sprintf("Score averaged over all %d health sections, each with live data.", n)
		if len(limited) > 0 {
			overall.Confidence = "limited"
			overall.ConfidenceReason = fmt.Sprintf("%d of %d sections have no data yet, so the score is averaged over %d — treat it as indicative.", len(limited), len(sections), n)
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
		if len(problems) == 0 {
			overall.Summary = "All health sections are healthy."
		} else {
			verb := "are"
			if len(problems) == 1 {
				verb = "is"
			}
			overall.Summary = "Needs attention because " + joinAnd(problems) + " " + verb + " degraded."
		}
	}

	drivers, hygiene := s.infraDrivers(ctx)
	writeJSON(w, http.StatusOK, map[string]any{
		"overall": overall, "sections": sections, "alerts": al,
		"top_drivers": drivers, "alert_hygiene": hygiene,
	})
}

// statusCode maps a section status to a short machine-readable reason code.
func statusCode(status string) string {
	switch status {
	case "healthy":
		return "ok"
	case "warning":
		return "degraded"
	case "critical":
		return "critical"
	default:
		return "unknown"
	}
}

// joinAnd renders ["A"] → "A", ["A","B"] → "A and B", ["A","B","C"] → "A, B and C".
func joinAnd(xs []string) string {
	switch len(xs) {
	case 0:
		return ""
	case 1:
		return xs[0]
	case 2:
		return xs[0] + " and " + xs[1]
	default:
		return strings.Join(xs[:len(xs)-1], ", ") + " and " + xs[len(xs)-1]
	}
}

var alertPortRe = regexp.MustCompile(`tcp:(\d+)`)

// infraDrivers returns the REAL open alerts pulling the score down (critical
// first, longest-outstanding first) plus the alert-hygiene signal (open alerts
// with no check linkage). Nothing is fabricated — every row is a live alert.
func (s *Server) infraDrivers(ctx context.Context) ([]infraDriver, infraHygiene) {
	hyg := infraHygiene{Link: "/alerts"}
	alerts, err := s.queries.ListAlerts(ctx)
	if err != nil {
		return nil, hyg
	}
	devByID := map[uuid.UUID]db.Device{}
	if devs, derr := s.queries.ListAllDevices(ctx); derr == nil {
		for _, d := range devs {
			devByID[d.ID] = d
		}
	}
	drivers := []infraDriver{}
	for _, a := range alerts {
		if a.Status == "resolved" {
			continue
		}
		if a.CheckID == nil {
			hyg.NullCheckIDOpen++
		}
		if a.Severity != "critical" {
			continue // top drivers = the critical blockers behind Monitoring/Alert Health
		}
		d := infraDriver{Name: "(system)", Severity: a.Severity, Label: a.Message, Section: "Alert Health", Link: "/alerts"}
		if a.DeviceID != nil {
			if dev, ok := devByID[*a.DeviceID]; ok {
				d.DeviceID = dev.ID.String()
				d.Name = dev.Name
				d.Link = "/devices/" + dev.ID.String()
				if dev.PrimaryIp != nil {
					d.IP = dev.PrimaryIp.String()
				}
			}
		}
		if m := alertPortRe.FindStringSubmatch(a.Message); m != nil {
			d.Protocol = "tcp"
			d.Port, _ = strconv.Atoi(m[1])
			d.Section = "Monitoring" // a down reachability check drives Monitoring
		}
		last := a.OpenedAt
		if a.EscalatedAt != nil && a.EscalatedAt.After(last) {
			last = *a.EscalatedAt
		}
		ls := last.Format(isoFmt)
		d.LastChanged = &ls
		drivers = append(drivers, d)
	}
	// Worst-first = longest outstanding (oldest opened) at the top — most actionable.
	sort.SliceStable(drivers, func(i, j int) bool {
		return deref(drivers[i].LastChanged) < deref(drivers[j].LastChanged)
	})
	if hyg.NullCheckIDOpen > 0 {
		hyg.Note = fmt.Sprintf("%d open alert(s) have no monitoring-check linkage (state-based collection/datastore/virtualization alerts). They are resolved by their own state evaluation, not by a check recovery.", hyg.NullCheckIDOpen)
	}
	return drivers, hyg
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
