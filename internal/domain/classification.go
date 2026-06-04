package domain

import "encoding/json"

// OS family values for devices.os_family. Kept deliberately coarse — the fine
// distinction (e.g. "Windows Server 2019") lives in os_version, the role
// (windows_server / domain_controller) in device_class. "" means unknown.
const (
	OSFamilyWindows  = "windows"
	OSFamilyLinux    = "linux"
	OSFamilyNetwork  = "network_os" // switch/router/firewall firmware (IOS, VRP, FortiOS…)
	OSFamilyEmbedded = "embedded"   // cameras/NVRs/printers/UPS appliances
	OSFamilyMacOS    = "macos"
)

// Classification-evidence source tags. A device's classification_evidence is an
// array of these; each names HOW a signal was observed so an operator can audit
// the decision and so auto-classification can be re-derived or overridden.
const (
	EvidenceSourceISAPI         = "isapi"             // Hikvision /ISAPI/System/deviceInfo
	EvidenceSourceONVIF         = "onvif"             // ONVIF device-info
	EvidenceSourceSNMPSysDescr  = "snmp_sysdescr"     // SNMP sysDescr.0
	EvidenceSourceSNMPSysObject = "snmp_sysobjectid"  // SNMP sysObjectID.0
	EvidenceSourceSMB           = "smb"               // SMB / port 445 negotiation
	EvidenceSourceRDP           = "rdp"               // RDP / port 3389
	EvidenceSourceWinRM         = "winrm"             // WinRM / port 5985-5986
	EvidenceSourceSSHBanner     = "ssh_banner"        // SSH server banner
	EvidenceSourceHTTP          = "http"              // HTTP Server header / title
	EvidenceSourcePort          = "port"              // open-port heuristic
	EvidenceSourceAD            = "ad"                // Active Directory computer object
)

// ClassificationEvidence is one observed signal supporting a device's
// category / OS-family classification. Persisted as the device's
// classification_evidence JSONB array. Plain data so it is trivially
// (de)serialised and unit-tested without a transport.
type ClassificationEvidence struct {
	Source     string `json:"source"`               // one of EvidenceSource* — how it was observed
	Signal     string `json:"signal"`               // the observed value, e.g. "deviceType=DVR", "Server: cisco-IOS"
	Category   string `json:"category,omitempty"`   // category this signal points to (DeviceCategory)
	OSFamily   string `json:"os_family,omitempty"`  // OS family this signal points to
	Subtype    string `json:"subtype,omitempty"`    // device_class subtype this signal points to
	Confidence int    `json:"confidence"`           // 0..100 weight of THIS signal alone
	ObservedAt string `json:"observed_at,omitempty"` // RFC3339, set by the caller
}

// MarshalEvidence serialises an evidence slice for the classification_evidence
// JSONB column. A nil/empty slice becomes "[]" (NOT "null") so it satisfies the
// devices_classification_evidence_is_array CHECK constraint — json.Marshal of a
// nil slice would otherwise emit "null" and the write would be rejected.
func MarshalEvidence(ev []ClassificationEvidence) ([]byte, error) {
	if len(ev) == 0 {
		return []byte("[]"), nil
	}
	return json.Marshal(ev)
}

// UnmarshalEvidence parses a classification_evidence JSONB value back into a
// slice. Empty/absent input yields a nil slice (no error).
func UnmarshalEvidence(b []byte) ([]ClassificationEvidence, error) {
	if len(b) == 0 || string(b) == "null" {
		return nil, nil
	}
	var ev []ClassificationEvidence
	if err := json.Unmarshal(b, &ev); err != nil {
		return nil, err
	}
	return ev, nil
}
