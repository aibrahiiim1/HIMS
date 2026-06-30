package api

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/coralsearesorts/hims/internal/snmp"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// HPE Compaq/iLO CPQ MIB OIDs — read-only server/BMC identity + overall health over SNMP.
const (
	oidSysDescrBMC    = ".1.3.6.1.2.1.1.1.0"         // "Integrated Lights-Out 5 1.37 …"
	oidCpqProductName = ".1.3.6.1.4.1.232.2.2.4.2.0" // server model, e.g. "ProLiant DL380 Gen10"
	oidCpqSerialNum   = ".1.3.6.1.4.1.232.2.2.2.1.0" // chassis serial
	oidCpqHeCondition = ".1.3.6.1.4.1.232.6.1.3.0"   // overall server health: 2=ok 3=degraded 4=failed
)

var reILOFw = regexp.MustCompile(`(?i)(Integrated Lights-Out\s*\d*)\s+([\d.]+)`)

func cpqHealth(v int64) string {
	switch v {
	case 2:
		return "OK"
	case 3:
		return "Degraded"
	case 4:
		return "Failed"
	default:
		return "Unknown"
	}
}

// collectILOviaSNMP reads REAL HPE iLO / ProLiant identity + overall health from the CPQ MIBs
// using the device's BOUND SNMP credential, and persists it on the device row (vendor/model/
// serial/firmware) + facts (bmc.snmp_health / bmc.controller / bmc.source=snmp).
//
// It deliberately does NOT write bmc_info and never sets a Redfish/managed-via-Redfish state:
// Redfish controller inventory still requires a real Redfish (http_basic) credential, so the
// iLO stays redfish=not_collected. SNMP simply enriches the row with what the community exposes.
// Returns false if the device didn't answer SNMP / isn't an HPE iLO.
func (s *Server) collectILOviaSNMP(ctx context.Context, dev db.Device) bool {
	if dev.PrimaryIp == nil || !dev.PrimaryIp.IsValid() {
		return false
	}
	c, err := s.snmpClientForDevice(ctx, dev, "", 8*time.Second)
	if err != nil {
		return false
	}
	if err := c.Connect(ctx); err != nil {
		return false
	}
	defer c.Close()

	pdus, err := c.Get(ctx, oidSysDescrBMC, oidCpqProductName, oidCpqSerialNum, oidCpqHeCondition)
	if err != nil || len(pdus) == 0 {
		return false
	}
	byOID := map[string]snmp.PDU{}
	for _, p := range pdus {
		byOID[strings.TrimPrefix(p.OID, ".")] = p
	}
	get := func(oid string) snmp.PDU { return byOID[strings.TrimPrefix(oid, ".")] }

	model := strings.TrimSpace(snmp.PDUString(get(oidCpqProductName)))
	serial := strings.TrimSpace(snmp.PDUString(get(oidCpqSerialNum)))
	controller, fw := "", ""
	if m := reILOFw.FindStringSubmatch(snmp.PDUString(get(oidSysDescrBMC))); m != nil {
		controller = strings.TrimSpace(m[1]) // "Integrated Lights-Out 5"
		fw = "iLO " + m[2]                   // "iLO 1.37"
	}
	if model == "" && serial == "" && fw == "" {
		return false // not an HPE iLO over SNMP — leave it gated, never fabricate
	}

	_ = s.queries.UpdateDeviceHardwareInfo(ctx, db.UpdateDeviceHardwareInfoParams{
		ID: dev.ID, Vendor: "HPE", Model: model, Serial: serial, OsVersion: fw, Hostname: derefStr(dev.Hostname),
	})
	facts := map[string]string{"bmc.source": "snmp", "bmc.controller": controller}
	// Only write a RECOGNIZED health state (OK/Degraded/Failed). A value of 1 ("other")
	// maps to Unknown — a weak read — and must never overwrite a prior known-good/known-bad
	// value, so we leave the existing bmc.snmp_health fact (and its observed_at age) intact.
	if hv, ok := snmp.PDUInt64(get(oidCpqHeCondition)); ok {
		if h := cpqHealth(hv); h != "Unknown" {
			facts["bmc.snmp_health"] = h
		}
	}
	for k, v := range facts {
		if v != "" {
			val := v
			_ = s.queries.UpsertDeviceFact(ctx, db.UpsertDeviceFactParams{DeviceID: dev.ID, Key: k, Value: &val, Driver: "snmp"})
		}
	}
	return true
}

// collectBMCSNMP is POST /devices/{id}/collect-bmc-snmp — operator-triggered SNMP enrichment of
// an HPE iLO/BMC (identity + health from the CPQ MIBs). Honest: it never marks Redfish collected
// or claims managed-via-Redfish — that still needs a real Redfish credential.
func (s *Server) collectBMCSNMP(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	dev, err := s.queries.GetDevice(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	ip := ""
	if dev.PrimaryIp != nil {
		ip = dev.PrimaryIp.String()
	}
	if s.collectILOviaSNMP(ctx, dev) {
		s.audit(r, "inventory", "bmc_snmp_collected", "device", id.String(), "Collected HPE iLO identity/health over SNMP (Redfish still gated)", map[string]any{"ip": ip})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "detail": "iLO identity + health collected over SNMP; Redfish remains credential_required"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": false, "detail": "device did not return HPE CPQ SNMP data (no SNMP credential, unreachable, or not an iLO)"})
}
