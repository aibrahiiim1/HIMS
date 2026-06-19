package api

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/google/uuid"
)

// Consolidated operational coverage reports: Management, Alerts, and Virtualization sections,
// computed live from the same read models the rest of the app uses (no fake/placeholder counts).
// Data Quality reuses the existing /data-quality endpoint. CSV export via /reports/coverage/export.

type covCount struct {
	Name      string `json:"name"`
	Total     int    `json:"total"`
	Managed   int    `json:"managed"`
	Unmanaged int    `json:"unmanaged"`
}

type covRef struct {
	ID     string `json:"id,omitempty"`
	Name   string `json:"name"`
	IP     string `json:"ip,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// coverageReport (GET /reports/coverage) returns the management / alerts / virtualization sections.
func (s *Server) coverageReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	writeJSON(w, http.StatusOK, map[string]any{
		"management":     s.managementCoverage(ctx),
		"alerts":         s.alertCoverage(ctx),
		"virtualization": s.virtualizationCoverage(ctx),
		"generated_at":   time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) managementCoverage(ctx context.Context) map[string]any {
	devs, _ := s.queries.ListAllDevices(ctx)
	devs = s.scopeDevices(ctx, devs)
	maps, _ := s.buildStatusMaps(ctx)
	siteName := map[uuid.UUID]string{}
	if locs, e := s.queries.ListLocations(ctx); e == nil {
		for _, l := range locs {
			siteName[l.ID] = l.Name
		}
	}
	byState := map[string]int{}
	cat := map[string]*covCount{}
	role := map[string]*covCount{}
	site := map[string]*covCount{}
	bump := func(m map[string]*covCount, k string, managed bool) {
		if k == "" {
			k = "(none)"
		}
		c := m[k]
		if c == nil {
			c = &covCount{Name: k}
			m[k] = c
		}
		c.Total++
		if managed {
			c.Managed++
		} else {
			c.Unmanaged++
		}
	}
	total := 0
	for _, d := range devs {
		st := maps.statusFor(d)
		total++
		byState[st.Management]++
		managed := st.Management == MgmtManaged
		bump(cat, d.Category, managed)
		if rl := maps.serverRole(d); rl != "" {
			bump(role, rl, managed)
		}
		sn := "(unassigned)"
		if d.LocationID != nil {
			if n, ok := siteName[*d.LocationID]; ok {
				sn = n
			}
		}
		bump(site, sn, managed)
	}
	return map[string]any{
		"total":       total,
		"managed":     byState[MgmtManaged],
		"by_state":    byState,
		"by_category": sortedCov(cat),
		"by_role":     sortedCov(role),
		"by_site":     sortedCov(site),
	}
}

func sortedCov(m map[string]*covCount) []covCount {
	out := make([]covCount, 0, len(m))
	for _, c := range m {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Total > out[j].Total })
	return out
}

func (s *Server) alertCoverage(ctx context.Context) map[string]any {
	rows, _ := s.queries.ListAlertsEnriched(ctx)
	bySev := map[string]int{}
	byKind := map[string]int{}
	byCond := map[string]int{}
	stale, acked := 0, 0
	perDevice := map[uuid.UUID]int{}
	openDevice := map[uuid.UUID]bool{}
	cut := time.Now().Add(-24 * time.Hour)
	for _, a := range rows {
		if a.DeviceID != nil {
			perDevice[*a.DeviceID]++
		}
		if a.Status == "resolved" {
			continue
		}
		bySev[a.Severity]++
		kind := "state"
		if a.CheckID != nil {
			kind = "check"
		}
		byKind[kind]++
		byCond[a.Condition]++
		if a.Status == "acknowledged" {
			acked++
		}
		if a.OpenedAt.Before(cut) {
			stale++
		}
		if a.DeviceID != nil {
			openDevice[*a.DeviceID] = true
		}
	}
	// Down-but-not-alerted: a device whose reachability is offline yet has no open alert,
	// with the honest reason (below the rule's min_failures, or no matching enabled rule).
	devs, _ := s.queries.ListAllDevices(ctx)
	devs = s.scopeDevices(ctx, devs)
	cfByDevice := map[uuid.UUID]int32{}
	if checks, e := s.queries.ListEnabledChecksWithDevice(ctx); e == nil {
		for _, c := range checks {
			if c.LastStatus == "down" && c.ConsecutiveFailures > cfByDevice[c.DeviceID] {
				cfByDevice[c.DeviceID] = c.ConsecutiveFailures
			}
		}
	}
	var downNotAlerted []covRef
	for _, d := range devs {
		if reachabilityFromStatus(d.Status) != ReachOffline || openDevice[d.ID] {
			continue
		}
		reason := "no enabled rule matched / suppressed"
		if cf := cfByDevice[d.ID]; cf > 0 && cf < 2 {
			reason = fmt.Sprintf("below alert threshold (%d consecutive failures)", cf)
		}
		downNotAlerted = append(downNotAlerted, covRef{ID: d.ID.String(), Name: d.Name, IP: addrStr(d.PrimaryIp), Detail: reason})
		if len(downNotAlerted) >= 100 {
			break
		}
	}
	// Flapping: devices with many alert rows (open+resolved cycles).
	var flapping []covRef
	devByID := map[uuid.UUID]string{}
	for _, d := range devs {
		devByID[d.ID] = d.Name
	}
	for id, n := range perDevice {
		if n >= 3 {
			flapping = append(flapping, covRef{ID: id.String(), Name: devByID[id], Detail: fmt.Sprintf("%d alerts", n)})
		}
	}
	sort.Slice(flapping, func(i, j int) bool { return flapping[i].Detail > flapping[j].Detail })
	rulesEnabled, rulesDisabled := 0, 0
	if rs, e := s.queries.ListAlertRules(ctx); e == nil {
		for _, r := range rs {
			if r.Enabled {
				rulesEnabled++
			} else {
				rulesDisabled++
			}
		}
	}
	return map[string]any{
		"by_severity":      bySev,
		"by_kind":          byKind,
		"by_condition":     byCond,
		"stale":            stale,
		"acked_unresolved": acked,
		"down_not_alerted": downNotAlerted,
		"flapping":         flapping,
		"rules_enabled":    rulesEnabled,
		"rules_disabled":   rulesDisabled,
	}
}

func (s *Server) virtualizationCoverage(ctx context.Context) map[string]any {
	devs, _ := s.queries.ListAllDevices(ctx)
	maps, _ := s.buildStatusMaps(ctx)
	vmCounts := map[uuid.UUID][3]int{}
	if cs, e := s.queries.VMCountsByHost(ctx); e == nil {
		for _, c := range cs {
			vmCounts[c.HostDeviceID] = [3]int{int(c.Total), int(c.Running), int(c.Stopped)}
		}
	}
	health := map[uuid.UUID]string{}
	healthAt := map[uuid.UUID]time.Time{}
	if hs, e := s.queries.ListAllCollectionHealth(ctx); e == nil {
		for _, h := range hs {
			health[h.DeviceID] = h.Status
			healthAt[h.DeviceID] = h.CollectedAt
		}
	}
	esxi, hyperv, staleHosts := 0, 0, 0
	var hosts []map[string]any
	staleCut := time.Now().Add(-24 * time.Hour)
	for _, d := range devs {
		if d.Category != string(domain.CatVirtualHost) {
			continue
		}
		typ := hypervisorTypeOf(d, maps.hvType)
		if typ == "hyperv" {
			hyperv++
		} else {
			esxi++
		}
		vc := vmCounts[d.ID]
		hst := health[d.ID]
		stale := false
		if at, ok := healthAt[d.ID]; ok && at.Before(staleCut) {
			stale = true
		}
		if hst == "failed" || stale {
			staleHosts++
		}
		hosts = append(hosts, map[string]any{
			"id": d.ID.String(), "name": d.Name, "ip": addrStr(d.PrimaryIp), "type": typ,
			"vm_count": vc[0], "running": vc[1], "stopped": vc[2], "health": orDefault(hst, "none"),
		})
	}
	var dsWarn, dsCrit int
	if ds, e := s.queries.ListAllDatastores(ctx); e == nil {
		for _, d := range ds {
			cap := deref64(d.CapacityBytes)
			if cap <= 0 {
				continue
			}
			freePct := float64(deref64(d.FreeBytes)) / float64(cap) * 100
			if freePct < 10 {
				dsCrit++
			} else if freePct < 20 {
				dsWarn++
			}
		}
	}
	vmTotal, vmLinked := 0, 0
	if sumr, e := s.queries.VMLinkSummary(ctx); e == nil {
		vmTotal, vmLinked = int(sumr.Total), int(sumr.Linked)
	}
	sort.Slice(hosts, func(i, j int) bool { return hosts[i]["ip"].(string) < hosts[j]["ip"].(string) })
	return map[string]any{
		"esxi_hosts": esxi, "hyperv_hosts": hyperv,
		"vms_total": vmTotal, "vms_linked": vmLinked, "vms_unlinked": vmTotal - vmLinked,
		"datastores_warn": dsWarn, "datastores_crit": dsCrit, "stale_virt_hosts": staleHosts,
		"hosts": hosts,
	}
}

// coverageExport (GET /reports/coverage/export?section=...&format=csv) — CSV export per section.
func (s *Server) coverageExport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	section := r.URL.Query().Get("section")
	var header []string
	var rows [][]string
	switch section {
	case "management":
		m := s.managementCoverage(ctx)
		header = []string{"dimension", "value", "total", "managed", "unmanaged"}
		for st, n := range m["by_state"].(map[string]int) {
			rows = append(rows, []string{"state", st, strconv.Itoa(n), "", ""})
		}
		for _, dim := range []string{"by_category", "by_role", "by_site"} {
			for _, c := range m[dim].([]covCount) {
				rows = append(rows, []string{dim, c.Name, strconv.Itoa(c.Total), strconv.Itoa(c.Managed), strconv.Itoa(c.Unmanaged)})
			}
		}
	case "virtualization":
		v := s.virtualizationCoverage(ctx)
		header = []string{"host", "ip", "type", "vm_count", "running", "stopped", "health"}
		for _, h := range v["hosts"].([]map[string]any) {
			rows = append(rows, []string{str(h["name"]), str(h["ip"]), str(h["type"]), str(h["vm_count"]), str(h["running"]), str(h["stopped"]), str(h["health"])})
		}
	case "alerts":
		a := s.alertCoverage(ctx)
		header = []string{"metric", "key", "count"}
		for k, n := range a["by_severity"].(map[string]int) {
			rows = append(rows, []string{"severity", k, strconv.Itoa(n)})
		}
		for k, n := range a["by_condition"].(map[string]int) {
			rows = append(rows, []string{"condition", k, strconv.Itoa(n)})
		}
		rows = append(rows, []string{"stale", "open>24h", strconv.Itoa(a["stale"].(int))})
		rows = append(rows, []string{"acked_unresolved", "", strconv.Itoa(a["acked_unresolved"].(int))})
		for _, d := range a["down_not_alerted"].([]covRef) {
			rows = append(rows, []string{"down_not_alerted", d.Name + " " + d.IP, d.Detail})
		}
	default:
		http.Error(w, "unknown section (management|virtualization|alerts)", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "coverage-"+section+".csv"))
	cw := csv.NewWriter(w)
	_ = cw.Write(header)
	_ = cw.WriteAll(rows)
	cw.Flush()
}

func str(v any) string { return fmt.Sprintf("%v", v) }
