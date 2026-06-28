package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// Manual Device Onboarding registry — the SINGLE data-driven catalog the onboarding wizard
// renders from (mirrors the wireless driver catalog pattern). Each device type declares its
// category/subtype, the connection methods it accepts (each mapped to a real credtest kind +
// default port), the base + method fields, and an HONEST capability/collector status. The
// frontend builds the whole wizard (type → address → method → credential → test → save) from
// this; the test + save endpoints choreograph the existing credtest/create/override/bind/
// audit/collect infrastructure. Nothing here fabricates capability: every method carries an
// EXPLICIT status (implemented_collected / implemented_tested / external_dependency_required /
// manual_inventory_only) — never a vague "collector_pending". A failed/unsupported test can
// only be saved as manual_inventory_only until a real protocol test/collection proves it.

type onbField struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Type        string   `json:"type"` // text|number|password|select|textarea
	Required    bool     `json:"required"`
	Placeholder string   `json:"placeholder,omitempty"`
	Help        string   `json:"help,omitempty"`
	Default     string   `json:"default,omitempty"`
	Options     []string `json:"options,omitempty"`
}

type onbMethod struct {
	Key            string `json:"key"`             // connection method id
	Label          string `json:"label"`           // "Redfish (HTTPS)"
	CredentialKind string `json:"credential_kind"` // domain cred kind; "" = no credential (manual/RTSP-anon)
	DefaultPort    int    `json:"default_port"`
	TestKind       string `json:"test_kind"`       // credtest kind OR special: vsphere|redfish|onvif|isapi|zkteco|manual
	CollectorReady bool   `json:"collector_ready"` // a real deep collector exists for this method+type
	Status         string `json:"status"`          // explicit: implemented_collected|implemented_tested|implemented_live_validation_pending|external_dependency_required|manual_inventory_only
	Note           string `json:"note,omitempty"`  // honest note / external dependency reason
}

type onbCapability struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Status string `json:"status"` // supported | collector_pending | not_implemented
	Reason string `json:"reason,omitempty"`
}

type onbType struct {
	Type         string          `json:"type"`         // registry key (used by the wizard + save)
	Category     string          `json:"category"`     // devices.category written on save
	Subtype      string          `json:"subtype"`      // devices.subtype written on save
	DisplayName  string          `json:"display_name"` // "Biometric Device (ZKTeco)"
	AddLabel     string          `json:"add_label"`    // "Add Biometric Device"
	Group        string          `json:"group"`        // inventory group key (compute/endpoints/network/...)
	Vendors      []string        `json:"vendors,omitempty"`
	Methods      []onbMethod     `json:"methods"`
	BaseFields   []onbField      `json:"base_fields"`
	Capabilities []onbCapability `json:"capabilities,omitempty"`
	LockOnSave   bool            `json:"lock_on_save"` // manual classification locks the type (always true here)
	Notes        string          `json:"notes,omitempty"`
}

// baseFields are the fields every manual device shares. Method-specific fields (port
// override, vendor controller params) are merged from the chosen method client-side.
func baseFields(vendors []string) []onbField {
	f := []onbField{
		{Key: "primary_ip", Label: "IP address", Type: "text", Required: true, Placeholder: "10.0.0.10", Help: "If this IP already exists from a scan, the device is UPDATED (manual override), never duplicated."},
		{Key: "name", Label: "Display name / hostname", Type: "text", Required: false, Placeholder: "defaults to the IP"},
		{Key: "location", Label: "Site / location", Type: "text", Required: false},
	}
	if len(vendors) > 0 {
		f = append(f, onbField{Key: "vendor", Label: "Vendor", Type: "select", Required: false, Options: vendors})
	} else {
		f = append(f, onbField{Key: "vendor", Label: "Vendor", Type: "text", Required: false})
	}
	f = append(f,
		onbField{Key: "model", Label: "Model (if known)", Type: "text", Required: false},
		onbField{Key: "criticality", Label: "Management priority", Type: "select", Required: false, Default: "normal", Options: []string{"low", "normal", "high", "critical"}},
		onbField{Key: "manual_classification_reason", Label: "Manual classification reason", Type: "text", Required: false, Placeholder: "why the operator is asserting this type"},
		onbField{Key: "notes", Label: "Notes", Type: "textarea", Required: false},
		onbField{Key: "port", Label: "Port override (optional)", Type: "number", Required: false, Help: "leave blank to use the method's default port"},
	)
	return f
}

// method helpers
func m(key, label, credKind, testKind string, port int, ready bool, note string) onbMethod {
	return onbMethod{Key: key, Label: label, CredentialKind: credKind, TestKind: testKind, DefaultPort: port, CollectorReady: ready, Note: note}
}
func cap(key, label, status, reason string) onbCapability {
	return onbCapability{Key: key, Label: label, Status: status, Reason: reason}
}

// onboardingCatalog returns the full data-driven device-type catalog.
func onboardingCatalog() []onbType {
	snmp := func() []onbMethod {
		return []onbMethod{
			m("snmp_v2c", "SNMP v2c (community)", "snmp_v2c", "snmp_v2c", 161, true, ""),
			m("snmp_v3", "SNMP v3 (user/auth/priv)", "snmp_v3", "snmp_v3", 161, true, ""),
			m("ssh", "SSH CLI", "ssh", "ssh", 22, true, ""),
		}
	}
	cat := []onbType{
		// ---- Network ----
		{Type: "switch", Category: "switch", Subtype: "", DisplayName: "Switch", AddLabel: "Add Switch", Group: "network",
			Vendors: []string{"Cisco", "Aruba/HPE", "Huawei", "Juniper", "Extreme", "Arista", "MikroTik", "Netgear", "3Com/HPE", "Other"},
			Methods: snmp(), BaseFields: baseFields(nil),
			Capabilities: []onbCapability{cap("interfaces", "Interfaces/MAC (IF-MIB)", "supported", ""), cap("lldp", "LLDP neighbours", "supported", ""), cap("identity", "sysObjectID/sysDescr", "supported", "")}, LockOnSave: true},
		{Type: "router", Category: "router", Subtype: "", DisplayName: "Router", AddLabel: "Add Router", Group: "network",
			Vendors: []string{"Cisco", "MikroTik", "Juniper", "Huawei", "Other"}, Methods: snmp(), BaseFields: baseFields(nil),
			Capabilities: []onbCapability{cap("identity", "sysObjectID/sysDescr", "supported", "")}, LockOnSave: true},
		{Type: "firewall", Category: "firewall", Subtype: "", DisplayName: "Firewall", AddLabel: "Add Firewall", Group: "network",
			Vendors:    []string{"Fortinet", "Palo Alto", "Cisco", "Sophos", "Other"},
			Methods:    []onbMethod{m("snmp_v2c", "SNMP v2c", "snmp_v2c", "snmp_v2c", 161, true, ""), m("ssh", "SSH CLI", "ssh", "ssh", 22, true, ""), m("vendor_api", "Vendor API (HTTPS)", "vendor_api", "http_basic", 443, false, "FortiGate REST collector runs via existing driver; other vendors are identity-only until a collector exists")},
			BaseFields: baseFields(nil), LockOnSave: true},
		{Type: "wireless_controller", Category: "wireless_controller", Subtype: "", DisplayName: "Wireless Controller", AddLabel: "Add Wireless Controller", Group: "network",
			Vendors:    []string{"Ruckus", "Aruba", "Ubiquiti UniFi", "Omada", "Extreme", "Other"},
			Methods:    []onbMethod{m("vendor_api", "Controller API", "http_basic", "http_basic", 443, true, "uses the wireless driver catalog — pick the vendor on the wireless page for full params"), m("snmp_v2c", "SNMP v2c", "snmp_v2c", "snmp_v2c", 161, true, "")},
			BaseFields: baseFields(nil), Notes: "Full controller collection is driven by the wireless driver catalog (Discovery → Controllers). Manual add here registers identity + binds a credential.", LockOnSave: true},
		{Type: "access_point", Category: "access_point", Subtype: "", DisplayName: "Access Point", AddLabel: "Add Access Point", Group: "network",
			Vendors: []string{"Ruckus", "Aruba", "Ubiquiti", "Extreme", "Other"}, Methods: snmp(), BaseFields: baseFields(nil), LockOnSave: true},
		// ---- Compute ----
		{Type: "server", Category: "server", Subtype: "", DisplayName: "Server", AddLabel: "Add Server", Group: "compute",
			Vendors:    []string{"HPE", "Dell", "Lenovo", "Supermicro", "Other"},
			Methods:    []onbMethod{m("windows", "Windows (WinRM → WMI/DCOM)", "windows", "winrm", 5985, true, ""), m("ssh", "SSH (Linux)", "ssh", "ssh", 22, true, ""), m("snmp_v2c", "SNMP v2c", "snmp_v2c", "snmp_v2c", 161, true, "")},
			BaseFields: baseFields(nil), LockOnSave: true},
		{Type: "virtual_host_esxi", Category: "virtual_host", Subtype: "esxi", DisplayName: "Virtual Host — VMware ESXi", AddLabel: "Add Virtual Host (ESXi)", Group: "compute",
			Vendors:    []string{"VMware"},
			Methods:    []onbMethod{m("vsphere", "vSphere API (HTTPS)", "vendor_api", "vsphere", 443, true, "bound credential tried ALONE — no spray (ESXi root lockout-safe)"), m("ssh", "SSH (root)", "ssh", "ssh", 22, true, "")},
			BaseFields: baseFields(nil), Capabilities: []onbCapability{cap("host", "Host identity", "supported", ""), cap("vms", "Virtual machines", "supported", ""), cap("datastores", "Datastores", "supported", "")}, LockOnSave: true},
		{Type: "virtual_host_hyperv", Category: "virtual_host", Subtype: "hyperv", DisplayName: "Virtual Host — Hyper-V", AddLabel: "Add Virtual Host (Hyper-V)", Group: "compute",
			Vendors:    []string{"Microsoft"},
			Methods:    []onbMethod{m("windows", "Windows (WinRM → WMI/DCOM)", "windows", "winrm", 5985, true, "legacy PS 2.0 hosts collected via Get-WmiObject")},
			BaseFields: baseFields(nil), Capabilities: []onbCapability{cap("host", "Host identity", "supported", ""), cap("vms", "VMs (Get-VM/Msvm)", "supported", "")}, LockOnSave: true},
		{Type: "virtual_host_kvm", Category: "virtual_host", Subtype: "kvm", DisplayName: "Virtual Host — Linux / KVM", AddLabel: "Add Virtual Host (KVM)", Group: "compute",
			Vendors:    []string{"Linux KVM", "Proxmox", "Other"},
			Methods:    []onbMethod{m("ssh", "SSH", "ssh", "ssh", 22, true, "host OS identity over SSH"), m("snmp_v2c", "SNMP v2c", "snmp_v2c", "snmp_v2c", 161, true, "")},
			BaseFields: baseFields(nil), LockOnSave: true,
			Capabilities: []onbCapability{cap("host", "Host OS identity", "implemented_collected", "SSH"), cap("vms", "Guest VMs (libvirt/virsh)", "external_dependency_required", "libvirt/virsh VM enumeration collector not implemented — host identity collected now")}},
		{Type: "bmc", Category: "bmc", Subtype: "", DisplayName: "iLO / BMC / iDRAC", AddLabel: "Add iLO / BMC", Group: "compute",
			Vendors:    []string{"HPE iLO", "Dell iDRAC", "Lenovo XClarity/IMM", "Huawei iBMC", "Supermicro IPMI", "Generic Redfish"},
			Methods:    []onbMethod{m("redfish", "Redfish (HTTPS)", "http_basic", "redfish", 443, true, ""), m("snmp_v2c", "SNMP v2c", "snmp_v2c", "snmp_v2c", 161, true, ""), m("ipmi", "IPMI", "", "manual", 623, false, "no IPMI collector yet — identity-only / manual_inventory_only")},
			BaseFields: baseFields(nil), Capabilities: []onbCapability{cap("identity", "Serial/UUID/firmware", "supported", "via Redfish"), cap("health", "Health/power", "supported", "via Redfish"), cap("link", "Link to server", "supported", "serial/UUID/hostname evidence")}, LockOnSave: true},
		// ---- Endpoints ----
		{Type: "endpoint", Category: "endpoint", Subtype: "", DisplayName: "Workstation / Endpoint", AddLabel: "Add Workstation", Group: "endpoints",
			Methods: []onbMethod{m("windows", "Windows (WinRM → WMI/DCOM)", "windows", "winrm", 5985, true, ""), m("ssh", "SSH (Linux/macOS)", "ssh", "ssh", 22, true, "")}, BaseFields: baseFields(nil), LockOnSave: true},
		{Type: "printer", Category: "printer", Subtype: "", DisplayName: "Printer", AddLabel: "Add Printer", Group: "endpoints",
			Vendors: []string{"HP", "Canon", "Epson", "Brother", "Xerox", "Other"}, Methods: []onbMethod{m("snmp_v2c", "SNMP v2c (Printer-MIB)", "snmp_v2c", "snmp_v2c", 161, true, ""), m("http_basic", "HTTP/HTTPS", "http_basic", "http_basic", 443, false, "identity-only until a printer web collector exists")}, BaseFields: baseFields(nil), LockOnSave: true},
		{Type: "ups", Category: "ups", Subtype: "", DisplayName: "UPS", AddLabel: "Add UPS", Group: "endpoints",
			Vendors: []string{"APC", "Eaton", "Vertiv", "CyberPower", "Other"}, Methods: []onbMethod{m("snmp_v2c", "SNMP v2c (UPS-MIB)", "snmp_v2c", "snmp_v2c", 161, true, "")}, BaseFields: baseFields(nil), LockOnSave: true},
		{Type: "biometric_zkteco", Category: "biometric", Subtype: "zkteco", DisplayName: "Biometric Device — ZKTeco", AddLabel: "Add Biometric Device", Group: "endpoints",
			Vendors: []string{"ZKTeco"},
			Methods: []onbMethod{
				m("zkteco", "ZKTeco protocol (TCP 4370)", "", "zkteco", 4370, true, "read-only identity: serial/firmware/device name (attendance/users intentionally out of scope)"),
				m("http_basic", "HTTP/HTTPS web UI", "http_basic", "http_basic", 80, false, "identity-only fallback for web-enabled models"),
				m("manual", "Manual inventory only", "", "manual", 0, false, "operator fallback"),
			},
			BaseFields: baseFields(nil), LockOnSave: true,
			Capabilities: []onbCapability{cap("identity", "Serial/firmware/device name", "implemented_collected", "native ZK protocol"), cap("attendance", "Attendance/user logs", "unsupported_by_device", "intentionally out of scope (read-only identity only)")},
			Notes:        "ZKTeco is a REAL connector: native TCP/4370 protocol handshake (+ optional communication key) and read-only identity. Attendance/user data is intentionally NOT collected."},
		{Type: "biometric_hikvision", Category: "biometric", Subtype: "hikvision_access", DisplayName: "Biometric / Access — Hikvision", AddLabel: "Add Hikvision Biometric", Group: "endpoints",
			Vendors:      []string{"Hikvision"},
			Methods:      []onbMethod{m("isapi", "ISAPI (HTTP Digest)", "http_basic", "isapi", 80, true, ""), m("http_basic", "HTTP/HTTPS", "http_basic", "http_basic", 80, false, "identity-only"), m("manual", "Manual inventory only", "", "manual", 0, false, "")},
			BaseFields:   baseFields(nil),
			Capabilities: []onbCapability{cap("identity", "Model/serial/firmware", "implemented_tested", "ISAPI deviceInfo")}, LockOnSave: true},
		{Type: "biometric_dahua", Category: "biometric", Subtype: "dahua_access", DisplayName: "Biometric / Access — Dahua", AddLabel: "Add Dahua Biometric", Group: "endpoints",
			Vendors:    []string{"Dahua"},
			Methods:    []onbMethod{m("http_basic", "HTTP/HTTPS (Digest)", "http_basic", "http_basic", 80, false, "identity via web auth"), m("onvif", "ONVIF", "onvif", "onvif", 80, true, ""), m("manual", "Manual inventory only", "", "manual", 0, false, "")},
			BaseFields: baseFields(nil), LockOnSave: true},
		{Type: "biometric_suprema", Category: "biometric", Subtype: "suprema", DisplayName: "Biometric — Suprema (BioStar)", AddLabel: "Add Suprema Biometric", Group: "endpoints",
			Vendors:      []string{"Suprema"},
			Methods:      []onbMethod{m("http_basic", "BioStar local API (HTTPS)", "http_basic", "http_basic", 443, false, "BioStar2 device API auth test"), m("manual", "Manual inventory only", "", "manual", 0, false, "")},
			BaseFields:   baseFields(nil),
			Capabilities: []onbCapability{cap("identity", "Device identity", "external_dependency_required", "deep Suprema/BioStar inventory needs the BioStar2 server API/SDK; HTTPS auth test is available now")}, LockOnSave: true},
		{Type: "biometric_anviz", Category: "biometric", Subtype: "anviz", DisplayName: "Biometric — Anviz", AddLabel: "Add Anviz Biometric", Group: "endpoints",
			Vendors:      []string{"Anviz"},
			Methods:      []onbMethod{m("http_basic", "HTTP/HTTPS web UI", "http_basic", "http_basic", 80, false, "identity via web auth"), m("manual", "Manual inventory only", "", "manual", 0, false, "")},
			BaseFields:   baseFields(nil),
			Capabilities: []onbCapability{cap("identity", "Device identity", "external_dependency_required", "deep Anviz inventory needs the Anviz SDK/cloud API; web auth test is available now")}, LockOnSave: true},
		{Type: "biometric_generic", Category: "biometric_device_unclassified", Subtype: "", DisplayName: "Biometric / Access — Generic", AddLabel: "Add Biometric Device", Group: "endpoints",
			Vendors:    []string{"Generic", "Other"},
			Methods:    []onbMethod{m("http_basic", "HTTP/HTTPS", "http_basic", "http_basic", 80, false, "identity via web auth"), m("snmp_v2c", "SNMP v2c (if enabled)", "snmp_v2c", "snmp_v2c", 161, true, ""), m("manual", "Manual inventory only", "", "manual", 0, false, "")},
			BaseFields: baseFields(nil), LockOnSave: true,
			Notes: "Unknown vendor → classified biometric_device_unclassified (honest), never a fabricated vendor."},
		{Type: "pos", Category: "pos", Subtype: "", DisplayName: "Point of Sale", AddLabel: "Add POS Device", Group: "endpoints",
			Vendors:    []string{"Verifone", "Ingenico", "PAX", "Generic Windows POS", "Other"},
			Methods:    []onbMethod{m("windows", "Windows (WinRM → WMI/DCOM)", "windows", "winrm", 5985, true, ""), m("ssh", "SSH", "ssh", "ssh", 22, true, ""), m("snmp_v2c", "SNMP v2c", "snmp_v2c", "snmp_v2c", 161, true, ""), m("http_basic", "HTTP/HTTPS", "http_basic", "http_basic", 443, false, "identity-only"), m("manual", "Manual inventory only", "", "manual", 0, false, "for POS terminals with no remote management")},
			BaseFields: baseFields(nil), LockOnSave: true,
			Notes: "A POS terminal with no remote-management protocol saves as manual_inventory_only — operator-asserted type, never faked as managed."},
		// ---- Security & Surveillance ----
		{Type: "camera", Category: "camera", Subtype: "", DisplayName: "Camera", AddLabel: "Add Camera", Group: "security",
			Vendors:    []string{"Hikvision", "Dahua", "Axis", "Hanwha", "Uniview/Uniarch", "Generic ONVIF"},
			Methods:    []onbMethod{m("onvif", "ONVIF", "onvif", "onvif", 80, true, ""), m("isapi", "ISAPI (Hikvision)", "http_basic", "isapi", 80, true, ""), m("http_basic", "HTTP Digest/Basic", "http_basic", "http_basic", 80, false, "identity-only"), m("rtsp", "RTSP reachability", "", "manual", 554, false, "stream reachability only; not management")},
			BaseFields: baseFields(nil), LockOnSave: true, Notes: "A bare HTTP 200 with no auth = web_reachable, never managed."},
		{Type: "nvr", Category: "nvr", Subtype: "", DisplayName: "NVR / DVR", AddLabel: "Add NVR / DVR", Group: "security",
			Vendors:    []string{"Hikvision", "Dahua", "Uniview", "Generic ONVIF"},
			Methods:    []onbMethod{m("onvif", "ONVIF", "onvif", "onvif", 80, true, ""), m("isapi", "ISAPI", "http_basic", "isapi", 80, true, ""), m("http_basic", "HTTP", "http_basic", "http_basic", 80, false, "identity-only")},
			BaseFields: baseFields(nil), LockOnSave: true},
		// ---- Voice ----
		{Type: "ip_phone", Category: "ip_phone", Subtype: "", DisplayName: "IP Phone", AddLabel: "Add IP Phone", Group: "voice",
			Vendors: []string{"Cisco", "Yealink", "Grandstream", "Polycom", "Other"}, Methods: []onbMethod{m("snmp_v2c", "SNMP v2c", "snmp_v2c", "snmp_v2c", 161, false, "identity-only"), m("http_basic", "HTTP/HTTPS", "http_basic", "http_basic", 80, false, "identity-only"), m("manual", "Manual inventory only", "", "manual", 0, false, "")}, BaseFields: baseFields(nil), LockOnSave: true},
		{Type: "pbx", Category: "pbx", Subtype: "", DisplayName: "PBX / Voice Gateway", AddLabel: "Add PBX / Voice Gateway", Group: "voice",
			Vendors:    []string{"Cisco CUCM", "Alcatel OmniPCX", "Avaya", "Other"},
			Methods:    []onbMethod{m("vendor_api", "CUCM AXL / vendor API", "vendor_api", "http_basic", 443, true, "CUCM via existing AXL collector"), m("ssh", "SSH/CLI", "ssh", "ssh", 22, true, ""), m("snmp_v2c", "SNMP v2c", "snmp_v2c", "snmp_v2c", 161, false, "identity-only")},
			BaseFields: baseFields(nil), LockOnSave: true},
	}
	for ti := range cat {
		for mi := range cat[ti].Methods {
			cat[ti].Methods[mi].Status = methodStatus(cat[ti].Methods[mi])
		}
	}
	return cat
}

// methodStatus derives the explicit, honest status of a connection method from whether a
// real collector exists, its test kind, and any external-dependency note — never the vague
// "collector_pending"/"not implemented".
func methodStatus(m onbMethod) string {
	switch {
	case m.TestKind == "manual":
		return "manual_inventory_only"
	case strings.Contains(strings.ToLower(m.Note), "external") || m.TestKind == "ipmi":
		return "external_dependency_required"
	case m.CollectorReady:
		return "implemented_collected" // real deep collector exists for this method+type
	default:
		return "implemented_tested" // protocol Test Connection works; deep identity collection is partial
	}
}

func onbTypeByKey(key string) (onbType, bool) {
	for _, t := range onboardingCatalog() {
		if t.Type == key {
			return t, true
		}
	}
	return onbType{}, false
}

// listManualDeviceTypes is GET /manual-onboarding/device-types — the whole catalog.
func (s *Server) listManualDeviceTypes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, onboardingCatalog())
}

// getManualDeviceType is GET /manual-onboarding/device-types/{type} — one type's schema.
func (s *Server) getManualDeviceType(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(chi.URLParam(r, "type"))
	t, ok := onbTypeByKey(key)
	if !ok {
		http.Error(w, "unknown device type "+key, http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, t)
}
