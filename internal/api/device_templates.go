package api

import (
	"encoding/json"
	"net/http"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

// Device Templates Engine (#8). A template is a reusable monitoring profile —
// a set of checks (TCP reachability / SNMP scalar OIDs) and default alert rules
// — that an operator can APPLY to many devices at once. The structured profile
// is stored in the existing device_templates.monitoring_rules JSONB column
// (extends the existing encoding rather than adding new columns); applying it
// materialises real monitoring_checks + alert_rules via the same idempotent
// queries the monitoring/alerting engines already use.

// templateCheck is one monitor in a template. kind "tcp" dials Port; kind
// "snmp" polls OID as a scalar. Label is operator-facing only.
type templateCheck struct {
	Kind            string `json:"kind"`
	Label           string `json:"label"`
	Port            int    `json:"port"`
	OID             string `json:"oid"`
	IntervalSeconds int    `json:"interval_seconds"`
	DownThreshold   int    `json:"down_threshold"`
}

// templateAlert is one default alert rule in a template. Alert rules are
// category-scoped (not per-device), so they are created once per apply.
type templateAlert struct {
	Name              string `json:"name"`
	TriggerStatus     string `json:"trigger_status"`
	MinFailures       int    `json:"min_failures"`
	Severity          string `json:"severity"`
	AutoWorkOrder     bool   `json:"auto_work_order"`
	WorkOrderPriority string `json:"work_order_priority"`
}

// templateBody is the structured profile carried in monitoring_rules JSONB.
type templateBody struct {
	Checks []templateCheck `json:"checks"`
	Alerts []templateAlert `json:"alerts"`
}

// valid reports whether a check is well-formed enough to materialise.
func (c templateCheck) valid() bool {
	switch c.Kind {
	case "tcp":
		return c.Port > 0 && c.Port <= 65535
	case "snmp":
		return c.OID != ""
	default:
		return false
	}
}

// matchesExisting reports whether an already-registered check on the device is
// the same monitor as this template check (so apply is idempotent and never
// duplicates). TCP matches on port; SNMP matches on OID.
func (c templateCheck) matchesExisting(e db.MonitoringCheck) bool {
	if e.Kind != c.Kind {
		return false
	}
	switch c.Kind {
	case "tcp":
		return e.TargetPort != nil && int(*e.TargetPort) == c.Port
	case "snmp":
		return e.Oid != nil && *e.Oid == c.OID
	default:
		return false
	}
}

// planChecks decides which template checks need creating on a device given the
// device's existing checks. It is pure (no I/O) so it can be unit-tested:
// invalid checks are dropped, checks already present are skipped, the rest are
// returned for creation.
func planChecks(existing []db.MonitoringCheck, checks []templateCheck) (toCreate []templateCheck, skipped int) {
	for _, c := range checks {
		if !c.valid() {
			skipped++
			continue
		}
		already := false
		for _, e := range existing {
			if c.matchesExisting(e) {
				already = true
				break
			}
		}
		if already {
			skipped++
			continue
		}
		toCreate = append(toCreate, c)
	}
	return toCreate, skipped
}

// withDefaults fills sane schedule defaults so a sparse template still produces
// valid monitoring_checks rows.
func (c templateCheck) withDefaults() templateCheck {
	if c.IntervalSeconds < 10 {
		c.IntervalSeconds = 60
	}
	if c.DownThreshold < 1 {
		c.DownThreshold = 2
	}
	return c
}

type applyTemplateReq struct {
	DeviceIDs []string `json:"device_ids"` // explicit targets; if empty, falls back to the template's device_type category
}

type applyTemplateResp struct {
	Devices       int      `json:"devices"`
	ChecksCreated int      `json:"checks_created"`
	ChecksSkipped int      `json:"checks_skipped"`
	AlertsCreated int      `json:"alerts_created"`
	AlertsSkipped int      `json:"alerts_skipped"`
	Warnings      []string `json:"warnings"`
}

// applyDeviceTemplate handles POST /device-templates/{id}/apply — materialises
// the template's checks onto the target devices and ensures its alert rules
// exist. Idempotent: re-applying creates nothing new.
func (s *Server) applyDeviceTemplate(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	var req applyTemplateReq
	if !decodeJSON(w, r, &req) {
		return
	}
	tmpl, err := s.queries.GetDeviceTemplate(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	var body templateBody
	if len(tmpl.MonitoringRules) > 0 {
		// A legacy free-form blob that isn't our shape simply yields no
		// checks/alerts rather than erroring.
		_ = json.Unmarshal(tmpl.MonitoringRules, &body)
	}

	// Resolve target devices: explicit IDs, else the template's category.
	var deviceIDs []uuid.UUID
	resp := applyTemplateResp{Warnings: []string{}}
	if len(req.DeviceIDs) > 0 {
		for _, s := range req.DeviceIDs {
			if u, err := uuid.Parse(s); err == nil {
				deviceIDs = append(deviceIDs, u)
			}
		}
	} else if tmpl.DeviceType != "" {
		rows, err := s.queries.ListDevicesByCategory(r.Context(), tmpl.DeviceType)
		if err != nil {
			writeErr(w, err)
			return
		}
		for _, d := range rows {
			deviceIDs = append(deviceIDs, d.ID)
		}
	}
	if len(deviceIDs) == 0 {
		http.Error(w, "no target devices: pass device_ids, or set the template's device_type to a category with devices", http.StatusBadRequest)
		return
	}

	if len(body.Checks) == 0 {
		resp.Warnings = append(resp.Warnings, "template defines no checks")
	}

	for _, did := range deviceIDs {
		existing, err := s.queries.ListMonitoringChecksByDevice(r.Context(), did)
		if err != nil {
			writeErr(w, err)
			return
		}
		toCreate, skipped := planChecks(existing, body.Checks)
		resp.ChecksSkipped += skipped
		for _, c := range toCreate {
			c = c.withDefaults()
			var port *int32
			var oid *string
			if c.Kind == "tcp" {
				p := int32(c.Port)
				port = &p
			} else {
				o := c.OID
				oid = &o
			}
			if _, err := s.queries.UpsertMonitoringCheck(r.Context(), db.UpsertMonitoringCheckParams{
				DeviceID: did, Kind: c.Kind, TargetPort: port, Oid: oid,
				IntervalSeconds: int32(c.IntervalSeconds), DownThreshold: int32(c.DownThreshold), Enabled: true,
			}); err != nil {
				writeErr(w, err)
				return
			}
			resp.ChecksCreated++
		}
	}
	resp.Devices = len(deviceIDs)

	// Alert rules are category-scoped → created once per apply, deduped by name.
	if len(body.Alerts) > 0 {
		existingRules, err := s.queries.ListAlertRules(r.Context())
		if err != nil {
			writeErr(w, err)
			return
		}
		seen := make(map[string]bool, len(existingRules))
		for _, rl := range existingRules {
			seen[rl.Name] = true
		}
		var category *string
		if tmpl.DeviceType != "" {
			dt := tmpl.DeviceType
			category = &dt
		}
		for _, a := range body.Alerts {
			if a.Name == "" || seen[a.Name] {
				resp.AlertsSkipped++
				continue
			}
			trigger := a.TriggerStatus
			if trigger != "down" && trigger != "warning" {
				trigger = "down"
			}
			sev := a.Severity
			if sev != "info" && sev != "warning" && sev != "critical" {
				sev = "warning"
			}
			prio := a.WorkOrderPriority
			if prio != "low" && prio != "medium" && prio != "high" && prio != "critical" {
				prio = "high"
			}
			if _, err := s.queries.CreateAlertRule(r.Context(), db.CreateAlertRuleParams{
				Name: a.Name, TriggerStatus: trigger, MinFailures: int32(a.MinFailures),
				DeviceCategory: category, Severity: sev, AutoWorkOrder: a.AutoWorkOrder,
				WorkOrderPriority: prio, Enabled: true, EscalateAfterMinutes: 0,
			}); err != nil {
				writeErr(w, err)
				return
			}
			seen[a.Name] = true
			resp.AlertsCreated++
		}
	}

	s.audit(r, "config", "template.apply", "device_template", id.String(),
		"Applied template "+tmpl.Name, map[string]any{
			"devices": resp.Devices, "checks_created": resp.ChecksCreated, "alerts_created": resp.AlertsCreated,
		})
	writeJSON(w, http.StatusOK, resp)
}
