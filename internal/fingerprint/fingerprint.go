// Package fingerprint matches device evidence (SNMP sysObjectID/sysDescr, HTTP
// Server banner, SSH banner, open ports/services) against a library of vendor
// fingerprints to suggest a vendor + device type with a confidence. It is the
// functional core behind the Vendor Fingerprint Library (#9): the built-in
// Library() seeds the operator-editable table, and Match() powers both the
// match-test tool and per-device suggestions. Pure logic, no I/O — unit-tested.
package fingerprint

import (
	"sort"
	"strings"
)

// Kinds of fingerprint pattern. Each matches a different evidence channel.
const (
	KindOID     = "oid"     // SNMP sysObjectID prefix (enterprise PEN) or exact identity
	KindHTTP    = "http"    // substring of the HTTP Server header / page title
	KindSSH     = "ssh"     // substring of the SSH identification banner
	KindService = "service" // substring of SNMP sysDescr / service banner
	KindSysName = "sysname" // substring of SNMP sysName (administrative host name)
	KindPort    = "port"    // an open TCP port number (weak signal)
)

// Print is one fingerprint rule.
type Print struct {
	Kind       string `json:"kind"`
	Pattern    string `json:"pattern"`
	Vendor     string `json:"vendor"`
	DeviceType string `json:"device_type"`
	Confidence int    `json:"confidence"`
}

// Evidence is what we observed about a device. Any field may be empty.
type Evidence struct {
	SysObjectID string `json:"sysobjectid"`
	SysDescr    string `json:"sysdescr"`
	SysName     string `json:"sysname"`
	HTTPServer  string `json:"http_server"`
	SSHBanner   string `json:"ssh_banner"`
	Ports       []int  `json:"ports"`
}

// Result is a matched fingerprint applied to the evidence.
type Result struct {
	Vendor     string `json:"vendor"`
	DeviceType string `json:"device_type"`
	Confidence int    `json:"confidence"`
	Kind       string `json:"kind"`
	Pattern    string `json:"pattern"`
}

// normOID strips a leading dot so ".1.3.6.1.4.1.9" and "1.3.6.1.4.1.9" compare
// equal, and ensures prefix matching is on dotted boundaries.
func normOID(s string) string {
	return strings.TrimPrefix(strings.TrimSpace(s), ".")
}

// matches reports whether a single print matches the evidence.
func (p Print) matches(ev Evidence) bool {
	switch p.Kind {
	case KindOID:
		oid := normOID(ev.SysObjectID)
		pat := normOID(p.Pattern)
		if oid == "" || pat == "" {
			return false
		}
		// Prefix match on a dotted boundary (so 1.3.6.1.4.1.9 matches
		// 1.3.6.1.4.1.9.1.516 but not 1.3.6.1.4.1.99).
		return oid == pat || strings.HasPrefix(oid, pat+".")
	case KindHTTP:
		return ev.HTTPServer != "" && containsFold(ev.HTTPServer, p.Pattern)
	case KindSSH:
		return ev.SSHBanner != "" && containsFold(ev.SSHBanner, p.Pattern)
	case KindService:
		return ev.SysDescr != "" && containsFold(ev.SysDescr, p.Pattern)
	case KindSysName:
		return ev.SysName != "" && containsFold(ev.SysName, p.Pattern)
	case KindPort:
		want := strings.TrimSpace(p.Pattern)
		for _, port := range ev.Ports {
			if itoa(port) == want {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// Match returns every matching print as a Result, ranked by confidence
// (highest first); ties keep OID > service > http > ssh > port ordering so the
// strongest evidence channel wins a tie.
func Match(ev Evidence, lib []Print) []Result {
	var out []Result
	for _, p := range lib {
		if p.matches(ev) {
			out = append(out, Result{Vendor: p.Vendor, DeviceType: p.DeviceType, Confidence: p.Confidence, Kind: p.Kind, Pattern: p.Pattern})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Confidence != out[j].Confidence {
			return out[i].Confidence > out[j].Confidence
		}
		return kindRank(out[i].Kind) < kindRank(out[j].Kind)
	})
	return out
}

// ModelFromSysDescr pulls a product model out of an SNMP sysDescr that uses the
// common "Vendor Product - Model, version…" shape, e.g. Extreme's
// "…ExtremeCloud IQ Controller - VE6120 Medium, System Version 10.05…" → "VE6120
// Medium". Returns "" when no clear model segment is present.
func ModelFromSysDescr(d string) string {
	d = strings.TrimSpace(d)
	i := strings.Index(d, " - ")
	if i < 0 {
		return ""
	}
	m := strings.TrimSpace(d[i+3:])
	if j := strings.IndexByte(m, ','); j >= 0 {
		m = strings.TrimSpace(m[:j])
	}
	if m == "" || len(m) > 64 {
		return ""
	}
	return m
}

// CanonicalCategory maps a fingerprint device_type token to HIMS's canonical
// device-category string. Most tokens are already canonical category names; the
// exceptions are the broad "wireless" → wireless_controller and "voip" → pbx.
// An empty token returns "" (caller treats that as "no category override").
func CanonicalCategory(deviceType string) string {
	switch deviceType {
	case "":
		return ""
	case "wireless":
		return "wireless_controller"
	case "voip":
		return "pbx"
	default:
		return deviceType
	}
}

func kindRank(k string) int {
	switch k {
	case KindOID:
		return 0
	case KindService:
		return 1
	case KindSysName:
		return 2
	case KindHTTP:
		return 3
	case KindSSH:
		return 4
	default:
		return 5
	}
}

func containsFold(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// Library returns the comprehensive built-in fingerprint set: real IANA private
// enterprise numbers (sysObjectID prefixes), common HTTP/SSH banners, sysDescr
// keywords and a few diagnostic ports, spanning network, compute, firewall,
// wireless, camera, printer, voice and UPS classes. Operators import this once
// (idempotent) and then extend it.
func Library() []Print {
	return []Print{
		// --- Product-specific sysObjectID / sysDescr (exact identity) ---
		// These outrank generic enterprise-PEN prefixes AND driver fingerprints: a
		// device whose enterprise OID would otherwise read as "switch" is correctly
		// identified by its product. Enterprise prefix → VENDOR; product OID /
		// sysDescr → CATEGORY + MODEL (req #8: generic vendor must not force switch).
		{KindOID, "1.3.6.1.4.1.1916.2.284", "Extreme Networks", "wireless_controller", 95}, // ExtremeCloud IQ Controller VE6120
		{KindService, "ExtremeCloud IQ Controller", "Extreme Networks", "wireless_controller", 92},
		{KindService, "ExtremeCloud", "Extreme Networks", "wireless_controller", 80},

		// --- SNMP sysObjectID enterprise prefixes (PEN) ---
		{KindOID, "1.3.6.1.4.1.9", "Cisco", "switch", 80},
		{KindOID, "1.3.6.1.4.1.9.1", "Cisco", "switch", 82},
		{KindOID, "1.3.6.1.4.1.9.6.1", "Cisco", "switch", 78}, // Cisco SMB / Small Business
		{KindOID, "1.3.6.1.4.1.11", "Aruba/HPE", "switch", 78},
		{KindOID, "1.3.6.1.4.1.14823", "Aruba", "wireless", 80},
		{KindOID, "1.3.6.1.4.1.2011", "Huawei", "switch", 80},
		{KindOID, "1.3.6.1.4.1.12356", "Fortinet", "firewall", 85},
		{KindOID, "1.3.6.1.4.1.2636", "Juniper", "switch", 80},
		{KindOID, "1.3.6.1.4.1.1916", "Extreme", "switch", 82},
		{KindOID, "1.3.6.1.4.1.30065", "Arista", "switch", 82},
		{KindOID, "1.3.6.1.4.1.25461", "Palo Alto", "firewall", 85},
		{KindOID, "1.3.6.1.4.1.674", "Dell", "server", 72},
		{KindOID, "1.3.6.1.4.1.14988", "MikroTik", "router", 80},
		{KindOID, "1.3.6.1.4.1.41112", "Ubiquiti", "wireless", 78},
		{KindOID, "1.3.6.1.4.1.4526", "Netgear", "switch", 70},
		{KindOID, "1.3.6.1.4.1.1588", "Brocade", "switch", 75},
		{KindOID, "1.3.6.1.4.1.6876", "VMware", "virtual_host", 85},
		{KindOID, "1.3.6.1.4.1.8072", "Net-SNMP (Linux)", "server", 65},
		{KindOID, "1.3.6.1.4.1.311", "Microsoft", "server", 68},
		{KindOID, "1.3.6.1.4.1.318", "APC", "ups", 85},
		{KindOID, "1.3.6.1.4.1.39165", "Hikvision", "camera", 82},
		{KindOID, "1.3.6.1.4.1.368", "Axis", "camera", 82},
		{KindOID, "1.3.6.1.4.1.6574", "Synology", "server", 78},
		{KindOID, "1.3.6.1.4.1.367", "Ricoh", "printer", 80},
		{KindOID, "1.3.6.1.4.1.11.2.3.9", "HP", "printer", 80},
		{KindOID, "1.3.6.1.4.1.1602", "Canon", "printer", 80},
		{KindOID, "1.3.6.1.4.1.13885", "Polycom", "voip", 78},

		// --- SNMP sysDescr / service keywords ---
		{KindService, "Cisco IOS", "Cisco", "switch", 75},
		{KindService, "Adaptive Security Appliance", "Cisco", "firewall", 80},
		{KindService, "ProCurve", "HPE", "switch", 75},
		{KindService, "Aruba", "Aruba", "switch", 72},
		{KindService, "FortiGate", "Fortinet", "firewall", 85},
		{KindService, "FortiOS", "Fortinet", "firewall", 82},
		{KindService, "Huawei Versatile Routing Platform", "Huawei", "switch", 78},
		{KindService, "ExtremeXOS", "Extreme", "switch", 82},
		{KindService, "JUNOS", "Juniper", "switch", 80},
		{KindService, "Arista Networks", "Arista", "switch", 82},
		{KindService, "VMware ESXi", "VMware", "virtual_host", 85},
		{KindService, "Windows", "Microsoft", "server", 62},
		{KindService, "Linux", "Linux", "server", 55},
		{KindService, "RouterOS", "MikroTik", "router", 80},
		// Bare vendor-name fallbacks (low confidence) — these catch the
		// truncated sysDescr / vendor string HIMS persists today, so a
		// per-device suggestion still resolves when only the vendor is known.
		// Specific product patterns above always outrank these.
		{KindService, "Fortinet", "Fortinet", "firewall", 68},
		{KindService, "Cisco", "Cisco", "switch", 50},
		{KindService, "Huawei", "Huawei", "switch", 58},
		{KindService, "Extreme", "Extreme", "switch", 58},
		{KindService, "VMware", "VMware", "virtual_host", 70},
		{KindService, "Hikvision", "Hikvision", "camera", 70},
		{KindService, "Axis", "Axis", "camera", 65},
		{KindService, "APC", "APC", "ups", 70},
		{KindService, "Ubiquiti", "Ubiquiti", "wireless", 62},
		{KindService, "MikroTik", "MikroTik", "router", 70},

		// --- HTTP Server header / title ---
		{KindHTTP, "Microsoft-IIS", "Microsoft", "server", 60},
		{KindHTTP, "Apache", "Apache", "server", 45},
		{KindHTTP, "nginx", "nginx", "server", 45},
		{KindHTTP, "FortiGate", "Fortinet", "firewall", 80},
		{KindHTTP, "App-webs", "Hikvision", "camera", 70}, // Hikvision embedded web
		{KindHTTP, "DNVRS-Webs", "Hikvision", "nvr", 72},
		{KindHTTP, "GoAhead-Webs", "Embedded", "camera", 55},
		{KindHTTP, "Boa", "Embedded", "camera", 50},
		{KindHTTP, "RomPager", "Embedded", "router", 50},
		{KindHTTP, "HP HTTP Server", "HP", "printer", 65},

		// --- SSH identification banner ---
		{KindSSH, "Cisco", "Cisco", "switch", 70},
		{KindSSH, "ROSSSH", "MikroTik", "router", 75},
		{KindSSH, "dropbear", "Embedded", "server", 40},
		{KindSSH, "OpenSSH", "Generic", "server", 30},

		// --- Open ports (weak, last-resort signals) ---
		{KindPort, "9100", "Generic", "printer", 55},
		{KindPort, "554", "Generic", "camera", 50},
		{KindPort, "5060", "Generic", "voip", 50},
	}
}
