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

// Exclusion is a negative condition on a Print: when the evidence matches ANY of
// a rule's exclusions, the rule does NOT fire — even if its positive pattern
// matched. This is how a broad/shared signal is corrected in DATA instead of
// hardcoded driver bails: e.g. "HP enterprise OID .1.3.6.1.4.1.11 = switch,
// EXCEPT the JetDirect printer subtree .11.2.3.9 or a 'jetdirect'/'laserjet'
// sysDescr = NOT a switch (it's a printer, classified by its own rule)". Kind +
// Pattern use the same channels/semantics as a positive Print match.
type Exclusion struct {
	Kind    string `json:"kind"`
	Pattern string `json:"pattern"`
}

// Print is one fingerprint rule. Model is an OPTIONAL explicit product model the
// rule stamps when it wins (e.g. "VE6120 Medium"); built-in catalog entries leave
// it empty and let the model be derived from sysDescr instead. Exclusions are
// negative conditions that suppress the rule (see Exclusion).
type Print struct {
	Kind       string      `json:"kind"`
	Pattern    string      `json:"pattern"`
	Vendor     string      `json:"vendor"`
	DeviceType string      `json:"device_type"`
	Confidence int         `json:"confidence"`
	Model      string      `json:"model"`
	Exclusions []Exclusion `json:"exclusions,omitempty"`
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

// Result is a matched fingerprint applied to the evidence. Model carries the
// winning rule's explicit product model (empty when the rule doesn't pin one).
type Result struct {
	Vendor     string `json:"vendor"`
	DeviceType string `json:"device_type"`
	Confidence int    `json:"confidence"`
	Kind       string `json:"kind"`
	Pattern    string `json:"pattern"`
	Model      string `json:"model"`
}

// normOID strips a leading dot so ".1.3.6.1.4.1.9" and "1.3.6.1.4.1.9" compare
// equal, and ensures prefix matching is on dotted boundaries.
func normOID(s string) string {
	return strings.TrimPrefix(strings.TrimSpace(s), ".")
}

// matchKind reports whether a single (kind, pattern) condition matches the
// evidence. Shared by positive Print matching AND Exclusion evaluation so both
// use identical channel semantics.
func matchKind(kind, pattern string, ev Evidence) bool {
	switch kind {
	case KindOID:
		oid := normOID(ev.SysObjectID)
		pat := normOID(pattern)
		if oid == "" || pat == "" {
			return false
		}
		// Prefix match on a dotted boundary (so 1.3.6.1.4.1.9 matches
		// 1.3.6.1.4.1.9.1.516 but not 1.3.6.1.4.1.99).
		return oid == pat || strings.HasPrefix(oid, pat+".")
	case KindHTTP:
		return ev.HTTPServer != "" && containsFold(ev.HTTPServer, pattern)
	case KindSSH:
		return ev.SSHBanner != "" && containsFold(ev.SSHBanner, pattern)
	case KindService:
		return ev.SysDescr != "" && containsFold(ev.SysDescr, pattern)
	case KindSysName:
		return ev.SysName != "" && containsFold(ev.SysName, pattern)
	case KindPort:
		want := strings.TrimSpace(pattern)
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

// matches reports whether a single print's POSITIVE pattern matches the evidence.
func (p Print) matches(ev Evidence) bool { return matchKind(p.Kind, p.Pattern, ev) }

// excludedBy returns the first exclusion that matches the evidence (suppressing
// the rule), or nil if none do.
func (p Print) excludedBy(ev Evidence) *Exclusion {
	for i := range p.Exclusions {
		if matchKind(p.Exclusions[i].Kind, p.Exclusions[i].Pattern, ev) {
			return &p.Exclusions[i]
		}
	}
	return nil
}

// Rejected is a candidate that did NOT become the classification, with the
// reason — for the explanation/"why" layer. A rule is rejected either because an
// exclusion suppressed it, or because it lost the confidence ranking.
type Rejected struct {
	Vendor     string `json:"vendor"`
	DeviceType string `json:"device_type"`
	Confidence int    `json:"confidence"`
	Kind       string `json:"kind"`
	Pattern    string `json:"pattern"`
	Reason     string `json:"reason"`
}

// Match returns every matching print as a Result, ranked by confidence (highest
// first; ties keep OID > service > http > ssh > port ordering). Rules suppressed
// by an exclusion are omitted. Backward-compatible: callers that only want the
// winners keep using Match.
func Match(ev Evidence, lib []Print) []Result {
	out, _ := MatchWithRejected(ev, lib)
	return out
}

// MatchWithRejected is Match plus the rejected candidates and why: rules whose
// positive pattern matched but were either suppressed by an exclusion or out-
// ranked on confidence. Powers the classification-evidence / rejected-candidates
// explanation.
func MatchWithRejected(ev Evidence, lib []Print) (winners []Result, rejected []Rejected) {
	for _, p := range lib {
		if !p.matches(ev) {
			continue
		}
		if ex := p.excludedBy(ev); ex != nil {
			rejected = append(rejected, Rejected{
				Vendor: p.Vendor, DeviceType: p.DeviceType, Confidence: p.Confidence,
				Kind: p.Kind, Pattern: p.Pattern,
				Reason: "excluded by " + ex.Kind + " marker \"" + ex.Pattern + "\"",
			})
			continue
		}
		winners = append(winners, Result{Vendor: p.Vendor, DeviceType: p.DeviceType, Confidence: p.Confidence, Kind: p.Kind, Pattern: p.Pattern, Model: p.Model})
	}
	sort.SliceStable(winners, func(i, j int) bool {
		if winners[i].Confidence != winners[j].Confidence {
			return winners[i].Confidence > winners[j].Confidence
		}
		return kindRank(winners[i].Kind) < kindRank(winners[j].Kind)
	})
	// Runners-up (matched, not excluded, but out-ranked) are rejected "lower
	// confidence than the chosen classification" — only when there's a winner and
	// the runner-up resolves to a DIFFERENT device type (a competing classification).
	if len(winners) > 1 {
		top := winners[0]
		for _, w := range winners[1:] {
			if w.DeviceType != top.DeviceType {
				rejected = append(rejected, Rejected{
					Vendor: w.Vendor, DeviceType: w.DeviceType, Confidence: w.Confidence,
					Kind: w.Kind, Pattern: w.Pattern,
					Reason: "lower confidence (" + itoa(w.Confidence) + ") than chosen " + top.DeviceType + " (" + itoa(top.Confidence) + ")",
				})
			}
		}
	}
	return winners, rejected
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
	// p builds a Print with no explicit model — the built-in catalog derives the
	// model from sysDescr at classification time. (Operator rules can pin an
	// explicit Model via the DB; see dbToPrints.) Using a constructor keeps the
	// catalog readable now that Print carries a trailing Model field.
	p := func(kind, pattern, vendor, dtype string, conf int) Print {
		return Print{Kind: kind, Pattern: pattern, Vendor: vendor, DeviceType: dtype, Confidence: conf}
	}
	// pm is p with an explicit product Model — used when the winning print should
	// pin vendor + model (confidence ≥85), not derive the model from sysDescr.
	pm := func(kind, pattern, vendor, dtype, model string, conf int) Print {
		return Print{Kind: kind, Pattern: pattern, Vendor: vendor, DeviceType: dtype, Confidence: conf, Model: model}
	}
	return []Print{
		// --- Product-specific sysObjectID / sysDescr (exact identity) ---
		// These outrank generic enterprise-PEN prefixes AND driver fingerprints: a
		// device whose enterprise OID would otherwise read as "switch" is correctly
		// identified by its product. Enterprise prefix → VENDOR; product OID /
		// sysDescr → CATEGORY + MODEL (req #8: generic vendor must not force switch).
		// VE6120 leaves Model empty on purpose so the model is derived from sysDescr
		// ("VE6120 Medium"), exercising the sysDescr fallback path.
		p(KindOID, "1.3.6.1.4.1.1916.2.284", "Extreme Networks", "wireless_controller", 95), // ExtremeCloud IQ Controller VE6120
		p(KindService, "ExtremeCloud IQ Controller", "Extreme Networks", "wireless_controller", 92),
		p(KindService, "ExtremeCloud", "Extreme Networks", "wireless_controller", 80),

		// Ruckus ZoneDirector — product OID + sysDescr pin VENDOR + CATEGORY + MODEL
		// (the generic Ruckus PEN 25053 below only yields vendor + the wireless
		// category). sysObjectID 25053.3.1.x is the ruckusProducts ZoneDirector tree;
		// .3.1.5.3 is the ZD3050 observed live. sysDescr reads "Ruckus Wireless zd3050".
		pm(KindOID, "1.3.6.1.4.1.25053.3.1.5.3", "Ruckus Wireless", "wireless_controller", "ZoneDirector 3050", 96),
		pm(KindService, "zd3050", "Ruckus Wireless", "wireless_controller", "ZoneDirector 3050", 92),

		// --- SNMP sysObjectID enterprise prefixes (PEN) ---
		p(KindOID, "1.3.6.1.4.1.9", "Cisco", "switch", 80),
		p(KindOID, "1.3.6.1.4.1.9.1", "Cisco", "switch", 82),
		p(KindOID, "1.3.6.1.4.1.9.6.1", "Cisco", "switch", 78), // Cisco SMB / Small Business
		// HP enterprise PEN .11 is shared by ProCurve/Aruba SWITCHES and HP JetDirect
		// PRINTERS (.11.2.3.9 subtree, "HP ETHERNET MULTI-ENVIRONMENT"/JetDirect/
		// LaserJet sysDescr). The broad switch rule excludes those printer markers in
		// DATA so it never offers a "switch" candidate for an HP printer — the printer
		// rule (.11.2.3.9 @80) classifies it. (Mirrors the aruba driver's bail.)
		{Kind: KindOID, Pattern: "1.3.6.1.4.1.11", Vendor: "Aruba/HPE", DeviceType: "switch", Confidence: 78, Exclusions: []Exclusion{
			{Kind: KindOID, Pattern: "1.3.6.1.4.1.11.2.3.9"},
			{Kind: KindService, Pattern: "jetdirect"},
			{Kind: KindService, Pattern: "laserjet"},
			{Kind: KindService, Pattern: "ethernet multi-environment"},
		}},
		p(KindOID, "1.3.6.1.4.1.14823", "Aruba", "wireless", 80),
		p(KindOID, "1.3.6.1.4.1.2011", "Huawei", "switch", 80),
		p(KindOID, "1.3.6.1.4.1.12356", "Fortinet", "firewall", 85),
		p(KindOID, "1.3.6.1.4.1.2636", "Juniper", "switch", 80),
		p(KindOID, "1.3.6.1.4.1.1916", "Extreme", "switch", 82),
		p(KindOID, "1.3.6.1.4.1.30065", "Arista", "switch", 82),
		p(KindOID, "1.3.6.1.4.1.25461", "Palo Alto", "firewall", 85),
		// Dell PEN .674 is shared by PowerEdge servers + OpenManage (.674.10892) AND
		// PowerConnect/Force10/OS9-OS10 SWITCHES (.674.10895). The broad server rule
		// excludes the networking subtree in DATA so a Dell switch isn't offered a
		// "server" candidate — the .674.10895 switch rule (below) classifies it.
		{Kind: KindOID, Pattern: "1.3.6.1.4.1.674", Vendor: "Dell", DeviceType: "server", Confidence: 72, Exclusions: []Exclusion{
			{Kind: KindOID, Pattern: "1.3.6.1.4.1.674.10895"},
		}},
		p(KindOID, "1.3.6.1.4.1.14988", "MikroTik", "router", 80),
		p(KindOID, "1.3.6.1.4.1.41112", "Ubiquiti", "wireless", 78),
		p(KindOID, "1.3.6.1.4.1.4526", "Netgear", "switch", 70),
		p(KindOID, "1.3.6.1.4.1.1588", "Brocade", "switch", 75),
		p(KindOID, "1.3.6.1.4.1.6876", "VMware", "virtual_host", 85),
		p(KindOID, "1.3.6.1.4.1.8072", "Net-SNMP (Linux)", "server", 65),
		p(KindOID, "1.3.6.1.4.1.311", "Microsoft", "server", 68),
		// APC PowerNet PEN .318 is shared by UPSes (.318.1.1.1) AND rack PDUs
		// (.318.1.1.4 / .12 / .26). The broad ups rule excludes the PDU subtrees in
		// DATA so an APC PDU isn't offered a "ups" candidate — the .318.1.1.4→pdu rule
		// (SC3 edge pack below) classifies it. (Mirrors the HP/Dell exclusion pattern.)
		{Kind: KindOID, Pattern: "1.3.6.1.4.1.318", Vendor: "APC", DeviceType: "ups", Confidence: 85, Exclusions: []Exclusion{
			{Kind: KindOID, Pattern: "1.3.6.1.4.1.318.1.1.4"},
			{Kind: KindOID, Pattern: "1.3.6.1.4.1.318.1.1.12"},
			{Kind: KindOID, Pattern: "1.3.6.1.4.1.318.1.1.26"},
		}},
		p(KindOID, "1.3.6.1.4.1.39165", "Hikvision", "camera", 82),
		p(KindOID, "1.3.6.1.4.1.368", "Axis", "camera", 82),
		p(KindOID, "1.3.6.1.4.1.6574", "Synology", "storage", 80), // Synology DiskStation NAS (Phase 4 SC2: storage, was server)
		p(KindOID, "1.3.6.1.4.1.367", "Ricoh", "printer", 80),
		p(KindOID, "1.3.6.1.4.1.11.2.3.9", "HP", "printer", 80),
		p(KindOID, "1.3.6.1.4.1.1602", "Canon", "printer", 80),
		p(KindOID, "1.3.6.1.4.1.13885", "Polycom", "ip_phone", 78), // Polycom/Poly desk phones (SC3: ip_phone, was voip→pbx)

		// --- Extended vendor catalog (FP-ext): real IANA PENs ---
		p(KindOID, "1.3.6.1.4.1.25053", "Ruckus Wireless", "wireless", 80), // Ruckus Wireless (generic PEN; ZD product prints above pin model)
		p(KindOID, "1.3.6.1.4.1.534", "Eaton", "ups", 82),                  // Eaton / Powerware UPS
		p(KindOID, "1.3.6.1.4.1.24681", "QNAP", "storage", 80),             // QNAP NAS (Phase 4 SC2: storage, was server)
		p(KindOID, "1.3.6.1.4.1.10642", "Zebra", "printer", 80),            // Zebra label printers
		p(KindOID, "1.3.6.1.4.1.253", "Xerox", "printer", 80),              // Xerox
		p(KindOID, "1.3.6.1.4.1.1248", "Epson", "printer", 78),             // Seiko Epson
		// TP-Link/Omada PEN .11863 covers Omada SWITCHES and Omada EAP access points.
		// Exclude the EAP marker so an Omada AP isn't offered a "switch" candidate —
		// the "EAP"→access_point rule (SC3) classifies it. (rule #7/#10)
		{Kind: KindOID, Pattern: "1.3.6.1.4.1.11863", Vendor: "TP-Link", DeviceType: "switch", Confidence: 70, Exclusions: []Exclusion{
			{Kind: KindService, Pattern: "EAP"},
		}}, // TP-Link / Omada
		p(KindOID, "1.3.6.1.4.1.21342", "Grandstream", "ip_phone", 80),            // Grandstream IP phones (SC3: ip_phone, was voip→pbx)
		p(KindOID, "1.3.6.1.4.1.6486", "Alcatel-Lucent Enterprise", "switch", 78), // ALE OmniSwitch

		// --- SNMP sysDescr / service keywords ---
		p(KindService, "Cisco IOS", "Cisco", "switch", 75),
		p(KindService, "Adaptive Security Appliance", "Cisco", "firewall", 80),
		p(KindService, "ProCurve", "HPE", "switch", 75),
		p(KindService, "Aruba", "Aruba", "switch", 72),
		p(KindService, "FortiGate", "Fortinet", "firewall", 85),
		p(KindService, "FortiOS", "Fortinet", "firewall", 82),
		p(KindService, "Huawei Versatile Routing Platform", "Huawei", "switch", 78),
		p(KindService, "ExtremeXOS", "Extreme", "switch", 82),
		p(KindService, "JUNOS", "Juniper", "switch", 80),
		p(KindService, "Arista Networks", "Arista", "switch", 82),
		p(KindService, "VMware ESXi", "VMware", "virtual_host", 85),
		p(KindService, "Windows", "Microsoft", "server", 62),
		p(KindService, "Linux", "Linux", "server", 55),
		p(KindService, "RouterOS", "MikroTik", "router", 80),
		// Bare vendor-name fallbacks (low confidence) — these catch the
		// truncated sysDescr / vendor string HIMS persists today, so a
		// per-device suggestion still resolves when only the vendor is known.
		// Specific product patterns above always outrank these.
		p(KindService, "Fortinet", "Fortinet", "firewall", 68),
		p(KindService, "Cisco", "Cisco", "switch", 50),
		p(KindService, "Huawei", "Huawei", "switch", 58),
		p(KindService, "Extreme", "Extreme", "switch", 58),
		p(KindService, "VMware", "VMware", "virtual_host", 70),
		p(KindService, "Hikvision", "Hikvision", "camera", 70),
		p(KindService, "Axis", "Axis", "camera", 65),
		p(KindService, "APC", "APC", "ups", 70),
		p(KindService, "Ubiquiti", "Ubiquiti", "wireless", 62),
		p(KindService, "MikroTik", "MikroTik", "router", 70),
		// Extended vendor catalog (FP-ext) — sysDescr keywords. These resolve a
		// vendor when only a truncated sysDescr is known, and cover vendors whose
		// PEN we don't pin above (Dahua, Yealink).
		p(KindService, "OmniSwitch", "Alcatel-Lucent Enterprise", "switch", 80),
		p(KindService, "Ruckus", "Ruckus Wireless", "wireless", 70),
		pm(KindService, "ZoneDirector", "Ruckus Wireless", "wireless_controller", "ZoneDirector", 88),
		p(KindService, "SmartZone", "Ruckus Wireless", "wireless_controller", 82),
		// Eaton makes UPSes AND ePDUs; exclude the ePDU marker so an Eaton ePDU isn't
		// offered a "ups" candidate — the "ePDU"→pdu rule (SC3) classifies it. (rule #5/#10)
		{Kind: KindService, Pattern: "Eaton", Vendor: "Eaton", DeviceType: "ups", Confidence: 70, Exclusions: []Exclusion{
			{Kind: KindService, Pattern: "ePDU"},
		}},
		p(KindService, "QNAP", "QNAP", "storage", 70),
		p(KindService, "Zebra", "Zebra", "printer", 70),
		p(KindService, "Xerox", "Xerox", "printer", 70),
		p(KindService, "EPSON", "Epson", "printer", 70),
		p(KindService, "TP-LINK", "TP-Link", "switch", 60),
		p(KindService, "Grandstream", "Grandstream", "ip_phone", 72), // SC3: ip_phone (was voip→pbx)
		p(KindService, "Yealink", "Yealink", "ip_phone", 72),         // SC3: ip_phone (was voip→pbx)
		p(KindService, "Dahua", "Dahua", "camera", 72),

		// --- HTTP Server header / title ---
		p(KindHTTP, "Microsoft-IIS", "Microsoft", "server", 60),
		p(KindHTTP, "Apache", "Apache", "server", 45),
		p(KindHTTP, "nginx", "nginx", "server", 45),
		p(KindHTTP, "FortiGate", "Fortinet", "firewall", 80),
		p(KindHTTP, "App-webs", "Hikvision", "camera", 70), // Hikvision embedded web
		p(KindHTTP, "DNVRS-Webs", "Hikvision", "nvr", 86),  // Hikvision NVR web — must beat the .39165/App-webs camera @82 (SC3)
		p(KindHTTP, "GoAhead-Webs", "Embedded", "camera", 55),
		p(KindHTTP, "Boa", "Embedded", "camera", 50),
		p(KindHTTP, "RomPager", "Embedded", "router", 50),
		p(KindHTTP, "HP HTTP Server", "HP", "printer", 65),

		// --- SSH identification banner ---
		p(KindSSH, "Cisco", "Cisco", "switch", 70),
		p(KindSSH, "ROSSSH", "MikroTik", "router", 75),
		p(KindSSH, "dropbear", "Embedded", "server", 40),
		p(KindSSH, "OpenSSH", "Generic", "server", 30),

		// --- Open ports (weak, last-resort signals) ---
		p(KindPort, "9100", "Generic", "printer", 55),
		p(KindPort, "554", "Generic", "camera", 50),
		p(KindPort, "5060", "Generic", "voip", 50),

		// ============================================================
		// Phase 4 — Network & firewall pack (SC1)
		// Vendor/device packs extend coverage of the compatibility matrix's
		// catalog-only (🟡) and uncovered (❌) network gear. Product OIDs +
		// sysDescr keywords; HTTP banners where a vendor's appliance is web-first.
		// ============================================================

		// --- Switches / routers (enterprise PENs) ---
		p(KindOID, "1.3.6.1.4.1.171", "D-Link", "switch", 75),         // D-Link
		p(KindOID, "1.3.6.1.4.1.25506", "H3C", "switch", 80),          // H3C / New H3C
		p(KindOID, "1.3.6.1.4.1.4881", "Ruijie", "switch", 78),        // Ruijie Networks
		p(KindOID, "1.3.6.1.4.1.207", "Allied Telesis", "switch", 78), // Allied Telesis
		p(KindOID, "1.3.6.1.4.1.674.10895", "Dell", "switch", 82),     // Dell PowerConnect / Force10 / OS9-OS10 (more specific than .674→server)
		p(KindOID, "1.3.6.1.4.1.29671", "Cisco Meraki", "switch", 72), // Meraki (cloud-managed; MS switch — MX/MR refined by sysDescr below)
		p(KindOID, "1.3.6.1.4.1.3955", "Linksys", "switch", 65),       // Linksys / Belkin SMB

		// --- Switch / router sysDescr keywords ---
		p(KindService, "NX-OS", "Cisco", "switch", 80), // Cisco Nexus
		p(KindService, "Nexus", "Cisco", "switch", 78),
		p(KindService, "IOS-XE", "Cisco", "switch", 76),
		p(KindService, "IOS XR", "Cisco", "router", 78),
		p(KindService, "Comware", "H3C", "switch", 78), // H3C/HPE Comware
		p(KindService, "VyOS", "VyOS", "router", 80),
		p(KindService, "EdgeOS", "Ubiquiti", "router", 78), // Ubiquiti EdgeRouter
		p(KindService, "EdgeSwitch", "Ubiquiti", "switch", 78),

		// --- Firewalls (enterprise PENs) ---
		p(KindOID, "1.3.6.1.4.1.8741", "SonicWall", "firewall", 85),   // SonicWall
		p(KindOID, "1.3.6.1.4.1.3097", "WatchGuard", "firewall", 82),  // WatchGuard Firebox
		p(KindOID, "1.3.6.1.4.1.2620", "Check Point", "firewall", 85), // Check Point
		p(KindOID, "1.3.6.1.4.1.21067", "Sophos", "firewall", 82),     // Sophos (XG/SG)
		p(KindOID, "1.3.6.1.4.1.20632", "Barracuda", "firewall", 80),  // Barracuda

		// --- Firewall sysDescr keywords ---
		p(KindService, "PAN-OS", "Palo Alto", "firewall", 85),
		p(KindService, "SonicWALL", "SonicWall", "firewall", 82),
		p(KindService, "Firepower", "Cisco", "firewall", 84), // Cisco Firepower / FTD — beats generic Cisco .9.1 switch @82
		p(KindService, "Check Point", "Check Point", "firewall", 80),
		p(KindService, "Sophos", "Sophos", "firewall", 72),
		p(KindService, "pfSense", "Netgate", "firewall", 85),
		p(KindService, "OPNsense", "OPNsense", "firewall", 85),
		p(KindService, "SRX", "Juniper", "firewall", 85),  // Juniper SRX product marker — beats generic Juniper .2636 switch @80
		p(KindService, "USG", "Ubiquiti", "firewall", 72), // UniFi Security Gateway

		// --- Load balancers / ADCs (new category load_balancer) ---
		p(KindOID, "1.3.6.1.4.1.3375", "F5 Networks", "load_balancer", 88),   // F5 BIG-IP
		p(KindOID, "1.3.6.1.4.1.5951", "Citrix", "load_balancer", 85),        // Citrix NetScaler / ADC
		p(KindOID, "1.3.6.1.4.1.22610", "A10 Networks", "load_balancer", 84), // A10 Thunder / AX
		p(KindService, "BIG-IP", "F5 Networks", "load_balancer", 84),
		p(KindService, "NetScaler", "Citrix", "load_balancer", 84),
		p(KindService, "LoadMaster", "Kemp", "load_balancer", 82), // Kemp LoadMaster
		p(KindHTTP, "BIG-IP", "F5 Networks", "load_balancer", 78),

		// ============================================================
		// Phase 4 — Compute pack (SC2): server / BMC / virtualization / storage
		// NOTE ON BMCs: there is no bmc/management_controller device category in this
		// system; the existing redfish_bmc driver classifies BMCs as "server", so the
		// management-controller identity is carried by VENDOR + MODEL within the server
		// category (not a separate category). These prints beat generic HTTP/Linux so a
		// BMC is never left as a bare web server (Redfish/iLO/iDRAC > generic HTTP).
		// ============================================================

		// --- Server / BMC enterprise PENs ---
		p(KindOID, "1.3.6.1.4.1.232", "HPE", "server", 74),                    // HP/HPE ProLiant + iLO (Compaq PEN; NOT the .11 ProCurve switch tree)
		pm(KindOID, "1.3.6.1.4.1.674.10892.2", "Dell", "server", "iDRAC", 88), // Dell iDRAC (more specific than .674→server / .10892 OpenManage)
		p(KindOID, "1.3.6.1.4.1.674.10892.1", "Dell", "server", 80),           // Dell OpenManage / PowerEdge server agent
		p(KindOID, "1.3.6.1.4.1.19046", "Lenovo", "server", 74),               // Lenovo (ThinkSystem / XCC)
		p(KindOID, "1.3.6.1.4.1.10876", "Supermicro", "server", 74),           // Supermicro

		// --- Server / BMC sysDescr + HTTP markers (management controllers) ---
		pm(KindService, "iDRAC", "Dell", "server", "iDRAC", 86), // Dell iDRAC
		p(KindService, "Integrated Dell Remote Access", "Dell", "server", 86),
		p(KindService, "PowerEdge", "Dell", "server", 80),                          // Dell PowerEdge (server, not iDRAC)
		pm(KindService, "Integrated Lights-Out", "HPE", "server", "iLO", 86),       // HPE iLO (bare "iLO" omitted from sysDescr — substring-matches kilo/silo)
		p(KindService, "ProLiant", "HPE", "server", 80),                            // HPE ProLiant (server, not switch)
		pm(KindService, "XClarity", "Lenovo", "server", "XClarity Controller", 86), // Lenovo XCC
		p(KindService, "iBMC", "Huawei", "server", 80),                             // Huawei iBMC
		p(KindService, "Supermicro", "Supermicro", "server", 76),
		pm(KindHTTP, "iLO", "HPE", "server", "iLO", 84),     // iLO web — beats generic HTTP
		p(KindHTTP, "iDRAC", "Dell", "server", 84),          // iDRAC web
		p(KindHTTP, "Redfish", "Generic BMC", "server", 70), // generic Redfish service banner
		p(KindHTTP, "AMI MegaRAC", "AMI", "server", 72),     // AMI MegaRAC BMC (Supermicro/others)

		// --- Virtualization hosts ---
		p(KindService, "Proxmox", "Proxmox", "virtual_host", 84), // Proxmox VE — beats generic Linux/net-snmp @55-65
		p(KindHTTP, "pve-", "Proxmox", "virtual_host", 72),       // Proxmox web (pve-manager)
		p(KindService, "vCenter", "VMware", "virtual_host", 84),  // vCenter Server appliance
		p(KindService, "Nutanix", "Nutanix", "virtual_host", 82), // Nutanix AHV/CVM
		// (Hyper-V is plain Windows over SNMP — no distinct SNMP/banner fingerprint; it
		//  classifies as server/endpoint then the WinRM Get-VM collector finds the role.)

		// --- Storage / NAS (storage category; beats generic Linux/net-snmp server) ---
		p(KindOID, "1.3.6.1.4.1.789", "NetApp", "storage", 84),    // NetApp ONTAP
		p(KindOID, "1.3.6.1.4.1.1139", "Dell EMC", "storage", 82), // EMC (Unity/VNX/Isilon/PowerStore family)
		p(KindService, "DiskStation", "Synology", "storage", 82),
		p(KindService, "TrueNAS", "iXsystems", "storage", 84),
		p(KindService, "FreeNAS", "iXsystems", "storage", 80),
		p(KindService, "ONTAP", "NetApp", "storage", 84),
		p(KindService, "PowerStore", "Dell EMC", "storage", 82),
		p(KindService, "Isilon", "Dell EMC", "storage", 82),

		// ============================================================
		// Phase 4 — Edge pack (SC3): printers / UPS / PDU / CCTV / wireless-AP / VoIP
		// ============================================================

		// --- Printers / MFP (additional vendors; printer-MIB driver enriches) ---
		p(KindOID, "1.3.6.1.4.1.1347", "Kyocera", "printer", 80),         // Kyocera (also UTAX/TA OEM)
		p(KindOID, "1.3.6.1.4.1.2435", "Brother", "printer", 80),         // Brother
		p(KindOID, "1.3.6.1.4.1.641", "Lexmark", "printer", 80),          // Lexmark
		p(KindOID, "1.3.6.1.4.1.2385", "Sharp", "printer", 78),           // Sharp
		p(KindOID, "1.3.6.1.4.1.18334", "Konica Minolta", "printer", 80), // Konica Minolta
		p(KindOID, "1.3.6.1.4.1.1129", "Toshiba TEC", "printer", 76),     // Toshiba TEC MFP
		p(KindService, "iR-ADV", "Canon", "printer", 82),                 // Canon imageRUNNER ADVANCE
		p(KindService, "imageRUNNER", "Canon", "printer", 80),
		p(KindService, "LBP", "Canon", "printer", 72),      // Canon laser (LBP series)
		p(KindService, "ECOSYS", "Kyocera", "printer", 82), // Kyocera ECOSYS
		p(KindService, "TASKalfa", "Kyocera", "printer", 82),
		p(KindService, "UTAX", "UTAX", "printer", 80),
		p(KindService, "Lexmark", "Lexmark", "printer", 76),
		p(KindService, "Brother", "Brother", "printer", 74),
		p(KindService, "KONICA MINOLTA", "Konica Minolta", "printer", 78),
		p(KindService, "bizhub", "Konica Minolta", "printer", 80), // KM bizhub MFP
		p(KindService, "RICOH", "Ricoh", "printer", 74),
		p(KindService, "Lanier", "Ricoh", "printer", 74), // Ricoh OEM brand

		// --- UPS (additional vendors; UPS-MIB driver enriches) ---
		p(KindOID, "1.3.6.1.4.1.476", "Vertiv/Liebert", "ups", 82), // Liebert/Emerson Network Power UPS
		p(KindOID, "1.3.6.1.4.1.3808", "CyberPower", "ups", 82),    // CyberPower
		p(KindOID, "1.3.6.1.4.1.5491", "Tripp Lite", "ups", 80),    // Tripp Lite
		p(KindService, "Liebert", "Vertiv/Liebert", "ups", 76),
		p(KindService, "CyberPower", "CyberPower", "ups", 74),
		p(KindService, "Riello", "Riello", "ups", 74),
		p(KindService, "Smart-UPS", "APC", "ups", 80), // APC Smart-UPS line

		// --- PDU (rack power distribution; pdu category, distinct from UPS) ---
		pm(KindOID, "1.3.6.1.4.1.318.1.1.4", "APC", "pdu", "Rack PDU", 88),  // APC rPDU (excluded from .318→ups above)
		pm(KindOID, "1.3.6.1.4.1.318.1.1.12", "APC", "pdu", "Rack PDU", 88), // APC rPDU2
		pm(KindOID, "1.3.6.1.4.1.318.1.1.26", "APC", "pdu", "Rack PDU", 88), // APC rPDU (newer)
		p(KindOID, "1.3.6.1.4.1.1718", "Server Technology", "pdu", 86),      // ServerTech Sentry PDU
		p(KindOID, "1.3.6.1.4.1.13742.6", "Raritan", "pdu", 86),             // Raritan PX2/PX3 rPDU
		p(KindOID, "1.3.6.1.4.1.21239", "Geist", "pdu", 84),                 // Geist (Vertiv) PDU
		p(KindService, "ePDU", "Eaton", "pdu", 84),                          // Eaton ePDU (Eaton UPS stays .534→ups)
		p(KindService, "Switched PDU", "Generic", "pdu", 76),
		p(KindService, "Metered PDU", "Generic", "pdu", 76),
		p(KindService, "Rack PDU", "Generic", "pdu", 74),

		// --- CCTV: NVR / DVR markers must beat the generic vendor "camera" PEN ---
		p(KindService, "Network Video Recorder", "Generic", "nvr", 84), // beats Hikvision .39165 camera @82
		p(KindService, "Digital Video Recorder", "Generic", "dvr", 84),
		p(KindService, "Uniview", "Uniview", "camera", 72), // Uniview (UNV); OID PEN left out (unverified)
		p(KindHTTP, "Hipcam", "Generic", "camera", 55),

		// --- Wireless access points (access_point; product markers beat the generic
		//     vendor "wireless"→controller PEN and the TP-Link .11863 switch) ---
		p(KindService, "Instant AP", "Aruba", "access_point", 84), // Aruba Instant AP
		p(KindService, "Aruba AP", "Aruba", "access_point", 82),
		p(KindService, "UniFi AP", "Ubiquiti", "access_point", 84),
		p(KindService, "UAP", "Ubiquiti", "access_point", 80), // UniFi AP product code (UAP-AC-Pro …)
		p(KindSysName, "UAP-", "Ubiquiti", "access_point", 80),
		p(KindService, "ZoneFlex", "Ruckus Wireless", "access_point", 84), // Ruckus standalone AP line
		p(KindService, "Ruckus AP", "Ruckus Wireless", "access_point", 82),
		p(KindService, "EAP", "TP-Link/Omada", "access_point", 80),         // Omada EAP — beats TP-Link .11863 switch @70
		p(KindService, "Aerohive", "Extreme/Aerohive", "access_point", 82), // Extreme (Aerohive) APs
		p(KindService, "ExtremeWireless AP", "Extreme Networks", "access_point", 84),

		// --- VoIP / IP phones (ip_phone; phones, not pbx) ---
		p(KindService, "Cisco IP Phone", "Cisco", "ip_phone", 86),
		p(KindSysName, "SEP", "Cisco", "ip_phone", 72), // Cisco phone sysName prefix SEP<mac>
		p(KindService, "Polycom", "Polycom", "ip_phone", 76),
		p(KindService, "Fanvil", "Fanvil", "ip_phone", 80),
		p(KindService, "Snom", "Snom", "ip_phone", 78),
		p(KindService, "Avaya", "Avaya", "ip_phone", 70), // Avaya deskphones (also PBX; phones dominate by count)
	}
}
