package discovery

import (
	"strings"

	"github.com/coralsearesorts/hims/internal/domain"
)

// ProtocolPlan is the per-target "expected protocol" decision the pipeline makes
// BEFORE testing any credentials. It answers: what kind of device does the cheap,
// unauthenticated evidence (open ports + HTTP/SSH banners) suggest, and therefore
// which credential protocols are worth testing? This stops the scan from blindly
// trying every credential type against every host (SNMP/ONVIF/SSH against a
// Windows workstation, etc.), which produced confusing "auth_failed" noise in
// Credential Health. Irrelevant protocols are simply not tested — and recorded as
// deliberately skipped, not as failures.
type ProtocolPlan struct {
	Candidate string   // windows | linux | network | camera | vmware | printer | unknown
	Expected  []string // primary expected protocol tokens, most relevant first
	relevant  map[domain.CredentialKind]bool
}

// Relevant reports whether a credential kind is worth testing for this candidate.
func (p ProtocolPlan) Relevant(k domain.CredentialKind) bool { return p.relevant[k] }

// SNMPRelevant reports whether the SNMP classification probe should run at all.
func (p ProtocolPlan) SNMPRelevant() bool {
	return p.relevant[domain.CredSNMPv2c] || p.relevant[domain.CredSNMPv3]
}

// containsAny reports whether haystack contains any of the (lowercase) needles.
func containsAny(haystack string, needles ...string) bool {
	for _, n := range needles {
		if n != "" && strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}

// planProtocols derives the protocol plan from unauthenticated evidence. Strong
// non-SNMP signals (Windows mgmt ports, an SSH/Linux banner, camera/ONVIF/RTSP
// markers, an ESXi web banner) take priority so the scan does NOT SNMP-probe an
// obvious Windows/Linux/camera/VMware host. Only when no such signal exists does
// the host fall to the network/printer/unknown bucket where SNMP+SSH apply.
func planProtocols(ports []int, sshBanner, httpServer, httpTitle, httpBody string) ProtocolPlan {
	sb := strings.ToLower(sshBanner)
	web := strings.ToLower(httpServer + " " + httpTitle + " " + httpBody)
	httpOpen := hasPort(ports, 80) || hasPort(ports, 443) || hasPort(ports, 8000) || hasPort(ports, 8080) || hasPort(ports, 8443)

	windows := hasPort(ports, 445) || hasPort(ports, 135) || hasPort(ports, 3389) || hasPort(ports, 5985) || hasPort(ports, 5986)
	linux := hasPort(ports, 22) && containsAny(sb, "openssh", "ubuntu", "debian", "linux", "centos", "rocky", "raspbian")
	camera := hasPort(ports, 554) || containsAny(web, "hikvision", "dahua", "onvif", "isapi", "ip camera", "webcam", "nvr", "dvr", "uniview", "axis")
	vmware := containsAny(web, "vmware", "esxi", "vsphere", "/sdk", "vmware esx")
	netVendor := containsAny(web, "cisco", "huawei", "hp ", "hpe", "aruba", "extreme", "fortinet", "fortigate", "mikrotik", "ruckus", "juniper", "ubiquiti", "edgeos")

	// applianceWeb: vendor names OR role/product markers in the web banner that
	// indicate a network appliance management UI (controller/firewall/switch/etc).
	applianceWeb := netVendor || containsAny(web,
		"controller", "wireless", "firewall", "switch", "gateway", "appliance",
		"fortios", "panos", "palo alto", "routeros", "big-ip", "f5", "vyos", "wlc")
	// Non-standard management web ports (8443/8080/8000) strongly indicate an
	// appliance UI rather than a plain web server (which uses 80/443).
	mgmtWebPort := hasPort(ports, 8443) || hasPort(ports, 8080) || hasPort(ports, 8000)
	// appliance: an SSH-reachable host that is a likely NETWORK APPLIANCE — it has
	// SSH plus either a non-standard mgmt web port, OR a standard web port (80/443)
	// carrying a vendor/role marker. These are commonly managed over SNMP/161 (UDP,
	// invisible to a TCP port scan), so they must be SNMP-probed — unlike a plain
	// Linux server. The marker gate keeps SNMP attempts off ordinary Linux servers.
	appliance := hasPort(ports, 22) && (mgmtWebPort || (httpOpen && applianceWeb))

	mk := func(cand string, expected []string, kinds ...domain.CredentialKind) ProtocolPlan {
		rel := make(map[domain.CredentialKind]bool, len(kinds))
		for _, k := range kinds {
			rel[k] = true
		}
		return ProtocolPlan{Candidate: cand, Expected: expected, relevant: rel}
	}

	switch {
	case camera:
		// Cameras/NVR/DVR: ONVIF + HTTP/ISAPI. SNMP/SSH/WinRM are not the point.
		return mk("camera", []string{"onvif", "http"}, domain.CredONVIF, domain.CredHTTPBasic, domain.CredVendorAPI)
	case vmware:
		// ESXi/vCenter: onboarded via a VMware Vendor Connection Profile; the
		// bound credential is vendor_api/http_basic. No SNMP/SSH/WinRM/ONVIF.
		return mk("vmware", []string{"vmware"}, domain.CredVendorAPI, domain.CredHTTPBasic)
	case windows:
		// Windows: WinRM is the management protocol. HTTP only if a web port is
		// open (rare mgmt UI). Never SNMP/SSH/ONVIF.
		if httpOpen {
			return mk("windows", []string{"winrm"}, domain.CredWinRM, domain.CredHTTPBasic)
		}
		return mk("windows", []string{"winrm"}, domain.CredWinRM)
	case appliance:
		// SSH-managed network appliance (wireless controller, firewall, load
		// balancer, switch with a mgmt UI). Try SNMP + SSH (+ HTTP) — NOT SSH-only —
		// because these are routinely managed over SNMP even when the box is Linux
		// underneath and presents an OpenSSH banner. Gated on appliance evidence
		// (non-standard mgmt port or vendor/role web marker) so plain Linux servers
		// stay SSH-only and don't generate noisy SNMP failures.
		return mk("appliance", []string{"snmp", "ssh"}, domain.CredSNMPv2c, domain.CredSNMPv3, domain.CredSSH, domain.CredHTTPBasic)
	case linux:
		// Linux: SSH. (Some Linux servers run SNMP, but to keep health clean we
		// expect SSH; SNMP can still be tested for network/unknown hosts.)
		return mk("linux", []string{"ssh"}, domain.CredSSH)
	case netVendor:
		// Network gear identified by a vendor web banner: SNMP + SSH.
		return mk("network", []string{"snmp", "ssh"}, domain.CredSNMPv2c, domain.CredSNMPv3, domain.CredSSH)
	case hasPort(ports, 9100) && !httpOpen && !hasPort(ports, 22):
		// Bare JetDirect printer: SNMP (Printer-MIB).
		return mk("printer", []string{"snmp"}, domain.CredSNMPv2c, domain.CredSNMPv3)
	default:
		// Genuinely ambiguous: be permissive but bounded — SNMP + SSH + HTTP, plus
		// WinRM only if a WinRM port happens to be open. This still excludes ONVIF
		// unless a camera signal fired above.
		kinds := []domain.CredentialKind{domain.CredSNMPv2c, domain.CredSNMPv3, domain.CredSSH, domain.CredHTTPBasic, domain.CredVendorAPI}
		exp := []string{"snmp"}
		if hasPort(ports, 22) {
			exp = append(exp, "ssh")
		}
		if hasPort(ports, 5985) || hasPort(ports, 5986) {
			kinds = append(kinds, domain.CredWinRM)
			exp = append(exp, "winrm")
		}
		return mk("unknown", exp, kinds...)
	}
}
