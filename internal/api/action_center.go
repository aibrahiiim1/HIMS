package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

// Action Center: turns the live operational backlog into actionable remediation queues. Every
// queue is built from CURRENT derived state (not stale DQ history), so "active" issues are
// separated from historical ones. Each row carries a recommended action + whether it is
// auto-actionable (re-collect/retry), needs credential work, or needs host-side work. Snoozed
// devices are excluded. No fake fixes: actions reuse the real collect/test endpoints.

type acRow struct {
	DeviceID    string `json:"device_id"`
	Name        string `json:"name"`
	IP          string `json:"ip,omitempty"`
	ServerRole  string `json:"server_role,omitempty"`
	Category    string `json:"category,omitempty"`
	Reason      string `json:"reason"`
	Recommended string `json:"recommended_action"`
	LastSuccess string `json:"last_success,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	Superseded  bool   `json:"superseded,omitempty"`
}
type acQueue struct {
	Key            string  `json:"key"`
	Label          string  `json:"label"`
	Description    string  `json:"description"`
	ActionKind     string  `json:"action_kind"` // recollect | credential | host_fix | agent | info
	AutoActionable bool    `json:"auto_actionable"`
	Severity       string  `json:"severity"`
	Count          int     `json:"count"`
	Rows           []acRow `json:"rows"`
}

func (s *Server) actionCenter(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	devs, _ := s.queries.ListAllDevices(ctx)
	devs = s.scopeDevices(ctx, devs)
	maps, _ := s.buildStatusMaps(ctx)

	snoozed := map[string]bool{} // device|issue
	if rows, e := s.queries.ListActiveSnoozes(ctx); e == nil {
		for _, sn := range rows {
			snoozed[sn.DeviceID.String()+"|"+sn.IssueKey] = true
		}
	}
	recency := map[uuid.UUID]time.Time{}
	if rows, e := s.queries.DeviceCollectionRecency(ctx); e == nil {
		for _, x := range rows {
			recency[x.DeviceID] = x.LastSuccess
		}
	}
	type jobInfo struct {
		status, category, errs string
		finished               time.Time
		agent                  string
	}
	jobs := map[uuid.UUID]jobInfo{}
	if rows, e := s.queries.LatestCollectJobs(ctx); e == nil {
		for _, j := range rows {
			if j.DeviceID == nil {
				continue
			}
			ji := jobInfo{status: j.Status, category: j.Category, errs: j.Error, agent: derefStr(j.AgentName)}
			if j.FinishedAt != nil {
				ji.finished = *j.FinishedAt
			}
			jobs[*j.DeviceID] = ji
		}
	}
	deepCat := map[string]bool{"server": true, "endpoint": true, "virtual_host": true, "virtual_machine": true}

	q := map[string]*acQueue{}
	def := func(key, label, desc, kind, sev string, auto bool) *acQueue {
		x := &acQueue{Key: key, Label: label, Description: desc, ActionKind: kind, Severity: sev, AutoActionable: auto}
		q[key] = x
		return x
	}
	staleQ := def("stale_collection", "Stale collection", "Managed deep-collected hosts with no successful collection recently.", "recollect", "warning", true)
	relayQ := def("relay_job_failed", "Relay job failed", "Latest agent collection failed and no newer success exists.", "recollect", "warning", true)
	credQ := def("credential_failed", "Credential failed", "Every applicable credential was cleanly rejected — needs a correct credential.", "credential", "warning", false)
	authQ := def("not_authorized", "Not authorized (WMI/DCOM)", "A credential authenticates but the host denies WMI/DCOM access — host-side fix.", "host_fix", "warning", false)
	agentQ := def("needs_agent", "Requires site agent", "Needs a site Relay Agent to collect (legacy WSMan / WMI-DCOM).", "agent", "warning", false)
	webQ := def("web_authenticated", "Web-authenticated only", "A web/identity credential works but no deep OS/hypervisor collection exists.", "recollect", "info", true)
	add := func(qq *acQueue, d db.Device, issueKey, reason, rec string) {
		if snoozed[d.ID.String()+"|"+issueKey] {
			return
		}
		row := acRow{DeviceID: d.ID.String(), Name: d.Name, IP: addrStr(d.PrimaryIp), ServerRole: maps.serverRole(d), Category: d.Category, Reason: reason, Recommended: rec}
		if t, ok := recency[d.ID]; ok && t.Year() > 2000 {
			row.LastSuccess = t.Format(time.RFC3339)
		}
		if ji, ok := jobs[d.ID]; ok && ji.errs != "" {
			row.LastError = truncErr(ji.errs)
		}
		qq.Rows = append(qq.Rows, row)
		qq.Count++
	}

	credHistResolved := 0
	for _, d := range devs {
		st := maps.statusFor(d)
		// Historical context: a device with auth-rejection history that is now managed.
		if cs, ok := maps.cred[d.ID]; ok && cs.authRejected && st.Management == MgmtManaged {
			credHistResolved++
		}
		switch st.Management {
		case MgmtCredentialFailed:
			add(credQ, d, "credential_failed", "all applicable credentials rejected", "Update the credential, then Test / Re-collect.")
		case MgmtNotAuthorized:
			add(authQ, d, "not_authorized", "authenticated but WMI/DCOM access denied", "Grant the account WMI/DCOM rights on the host (see remediation checklist), then re-scan.")
		case MgmtNeedsAgent:
			rec := "Install/assign a Relay Agent to this site."
			if d.LocationID != nil && maps.anySites[*d.LocationID] {
				if maps.onlineSites[*d.LocationID] {
					rec = "Agent online at this site — Re-collect to route via the agent."
				} else {
					rec = "Site agent is offline — bring it online, then Re-collect."
				}
			}
			add(agentQ, d, "needs_agent", "needs a site agent for deep collection", rec)
		case MgmtWebAuthenticated:
			add(webQ, d, "web_authenticated", "web/identity credential works; no deep collection", "Add a deep (WinRM/SSH/SNMP) credential if inventory is needed, then Re-collect.")
		}
		// Stale collection — managed deep host with old last_success.
		if st.Management == MgmtManaged && deepCat[d.Category] {
			if t, ok := recency[d.ID]; ok && t.Year() > 2000 && time.Since(t) > 24*time.Hour {
				add(staleQ, d, "stale_collection", "no successful collection in "+humanAge(time.Since(t)), "Re-collect; if intentionally offline/unsupported, Snooze.")
			}
		}
		// Relay job failed and not superseded by a later success.
		if ji, ok := jobs[d.ID]; ok && ji.status == "failed" {
			last := recency[d.ID]
			superseded := !ji.finished.IsZero() && last.After(ji.finished)
			if !superseded && !snoozed[d.ID.String()+"|relay_job_failed"] {
				row := acRow{DeviceID: d.ID.String(), Name: d.Name, IP: addrStr(d.PrimaryIp), ServerRole: maps.serverRole(d), Category: d.Category,
					Reason: "agent job failed: " + ji.category, Recommended: "Retry / Re-collect.", LastError: truncErr(ji.errs)}
				if last.Year() > 2000 {
					row.LastSuccess = last.Format(time.RFC3339)
				}
				relayQ.Rows = append(relayQ.Rows, row)
				relayQ.Count++
			}
		}
	}

	// Virtualization issues (counts + context; VM/datastore-level, not per-device actions).
	vmTotal, vmLinked := 0, 0
	if sumr, e := s.queries.VMLinkSummary(ctx); e == nil {
		vmTotal, vmLinked = int(sumr.Total), int(sumr.Linked)
	}
	dsWarn, dsCrit := 0, 0
	if ds, e := s.queries.ListAllDatastores(ctx); e == nil {
		for _, d := range ds {
			cap := deref64(d.CapacityBytes)
			if cap <= 0 {
				continue
			}
			pct := float64(deref64(d.FreeBytes)) / float64(cap) * 100
			if pct < 10 {
				dsCrit++
			} else if pct < 20 {
				dsWarn++
			}
		}
	}

	order := []string{"credential_failed", "not_authorized", "needs_agent", "relay_job_failed", "stale_collection", "web_authenticated"}
	queues := make([]acQueue, 0, len(order))
	for _, k := range order {
		queues = append(queues, *q[k])
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"generated_at": time.Now().UTC().Format(time.RFC3339),
		"queues":       queues,
		"virtualization": map[string]any{
			"vms_unlinked": vmTotal - vmLinked, "vms_total": vmTotal,
			"datastores_warn": dsWarn, "datastores_crit": dsCrit,
		},
		"historical": map[string]any{
			"credential_failed_resolved": credHistResolved, // had auth-failure history but now managed
		},
	})
}

func truncErr(s string) string {
	s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
	if len(s) > 160 {
		return s[:160] + "…"
	}
	return s
}

// snoozeRemediation (POST /action-center/snooze) mutes one device+issue for N days (operator).
func (s *Server) snoozeRemediation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeviceID string `json:"device_id"`
		IssueKey string `json:"issue_key"`
		Days     int    `json:"days"`
		Reason   string `json:"reason"`
		Unsnooze bool   `json:"unsnooze"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	id, err := uuid.Parse(req.DeviceID)
	if err != nil {
		http.Error(w, "invalid device_id", http.StatusBadRequest)
		return
	}
	if req.Unsnooze {
		_ = s.queries.DeleteSnooze(r.Context(), db.DeleteSnoozeParams{DeviceID: id, IssueKey: req.IssueKey})
		writeJSON(w, http.StatusOK, map[string]any{"unsnoozed": true})
		return
	}
	days := req.Days
	if days <= 0 {
		days = 7
	}
	actor := alertActor(r)
	_ = s.queries.UpsertSnooze(r.Context(), db.UpsertSnoozeParams{
		DeviceID: id, IssueKey: req.IssueKey, Reason: req.Reason,
		Until: time.Now().Add(time.Duration(days) * 24 * time.Hour), CreatedBy: actor,
	})
	writeJSON(w, http.StatusOK, map[string]any{"snoozed": true, "days": days})
}
