package classify

import (
	"strings"

	"github.com/coralsearesorts/hims/internal/domain"
)

// The rule functions below turn one raw observation into zero or more pieces of
// evidence. They are pure string/port logic — discovery probes (SC3) supply the
// observations. Confidence reflects how *definitive* a signal is: an explicit
// Hikvision ISAPI deviceType is near-certain (~90); a lone open port is a weak
// hint (~40). FromEvidence merges everything.

func ev(source, signal, category, osFamily, subtype string, conf int) domain.ClassificationEvidence {
	return domain.ClassificationEvidence{Source: source, Signal: signal, Category: category, OSFamily: osFamily, Subtype: subtype, Confidence: conf}
}

// ISAPIDeviceInfo classifies a Hikvision (and OEM-compatible) device from its
// /ISAPI/System/deviceInfo <deviceType> + <model>. This is the definitive
// recorder-vs-camera signal: deviceType "NVR" → nvr, "DVR" → dvr (distinct in
// CCTV Phase 2 so the fleet summary counts them separately), "IPCamera"/"IPDome"
// → camera. Model code is a corroborating signal (…NI…/…NVR… = NVR; …HGHI/HQHI…
// or an explicit DVR token = DVR). deviceType beats model when both are present.
func ISAPIDeviceInfo(deviceType, model string) []domain.ClassificationEvidence {
	dt := strings.ToLower(strings.TrimSpace(deviceType))
	m := strings.ToUpper(strings.TrimSpace(model))
	var out []domain.ClassificationEvidence
	switch {
	case strings.Contains(dt, "nvr"):
		out = append(out, ev(domain.EvidenceSourceISAPI, "deviceType="+deviceType, string(domain.CatNVR), domain.OSFamilyEmbedded, "nvr", 90))
	case strings.Contains(dt, "dvr") || strings.Contains(dt, "hybrid"):
		out = append(out, ev(domain.EvidenceSourceISAPI, "deviceType="+deviceType, string(domain.CatDVR), domain.OSFamilyEmbedded, "dvr", 90))
	case strings.Contains(dt, "ipcamera") || strings.Contains(dt, "ipdome") || strings.Contains(dt, "ipc") || strings.Contains(dt, "camera"):
		out = append(out, ev(domain.EvidenceSourceISAPI, "deviceType="+deviceType, string(domain.CatCamera), domain.OSFamilyEmbedded, "ip_camera", 88))
	case dt != "":
		// Some other Hikvision appliance — still embedded, category uncertain.
		out = append(out, ev(domain.EvidenceSourceISAPI, "deviceType="+deviceType, "", domain.OSFamilyEmbedded, "", 40))
	}
	// Model-code corroboration. DVR is the more specific token, so check it first
	// (a DS-7xxxHGHI DVR also matches the DS-7 NVR prefix otherwise).
	if m != "" {
		switch {
		case strings.Contains(m, "DVR") || strings.Contains(m, "HGHI") || strings.Contains(m, "HQHI") || strings.Contains(m, "HUHI"):
			out = append(out, ev(domain.EvidenceSourceISAPI, "model="+model, string(domain.CatDVR), domain.OSFamilyEmbedded, "dvr", 70))
		case strings.Contains(m, "NVR") || strings.HasPrefix(m, "DS-7") || strings.HasPrefix(m, "DS-8") || strings.HasPrefix(m, "DS-9"):
			out = append(out, ev(domain.EvidenceSourceISAPI, "model="+model, string(domain.CatNVR), domain.OSFamilyEmbedded, "nvr", 70))
		case strings.HasPrefix(m, "DS-2CD") || strings.HasPrefix(m, "DS-2DE"):
			out = append(out, ev(domain.EvidenceSourceISAPI, "model="+model, string(domain.CatCamera), domain.OSFamilyEmbedded, "ip_camera", 65))
		}
	}
	return out
}

// SSHBanner reads OS hints from an SSH server identification string
// ("SSH-2.0-OpenSSH_8.0p1 Ubuntu-..."). OpenSSH alone is a weak Linux hint;
// an embedded distro token (Ubuntu/Debian/CentOS) makes it strong. Cisco/Huawei
// SSH banners point at network gear.
func SSHBanner(banner string) []domain.ClassificationEvidence {
	b := strings.ToLower(banner)
	switch {
	case strings.Contains(b, "ubuntu") || strings.Contains(b, "debian") || strings.Contains(b, "centos") || strings.Contains(b, "el7") || strings.Contains(b, "el8") || strings.Contains(b, "el9"):
		return []domain.ClassificationEvidence{ev(domain.EvidenceSourceSSHBanner, banner, string(domain.CatServer), domain.OSFamilyLinux, "", 70)}
	case strings.Contains(b, "cisco"):
		return []domain.ClassificationEvidence{ev(domain.EvidenceSourceSSHBanner, banner, string(domain.CatSwitch), domain.OSFamilyNetwork, "", 60)}
	case strings.Contains(b, "openssh"):
		// Unix-like, role unknown — OS family hint only.
		return []domain.ClassificationEvidence{ev(domain.EvidenceSourceSSHBanner, banner, "", domain.OSFamilyLinux, "", 45)}
	}
	return nil
}

// SNMPSysDescr classifies from SNMP sysDescr.0 — the richest cheap signal.
func SNMPSysDescr(sysDescr string) []domain.ClassificationEvidence {
	d := strings.ToLower(sysDescr)
	switch {
	case strings.Contains(d, "windows"):
		return []domain.ClassificationEvidence{ev(domain.EvidenceSourceSNMPSysDescr, sysDescr, string(domain.CatServer), domain.OSFamilyWindows, "", 80)}
	case strings.Contains(d, "cisco ios") || strings.Contains(d, "cisco internetwork"):
		return []domain.ClassificationEvidence{ev(domain.EvidenceSourceSNMPSysDescr, sysDescr, string(domain.CatSwitch), domain.OSFamilyNetwork, "", 85)}
	case strings.Contains(d, "fortigate") || strings.Contains(d, "fortios"):
		return []domain.ClassificationEvidence{ev(domain.EvidenceSourceSNMPSysDescr, sysDescr, string(domain.CatFirewall), domain.OSFamilyNetwork, "", 88)}
	case strings.Contains(d, "huawei") || strings.Contains(d, "vrp"):
		return []domain.ClassificationEvidence{ev(domain.EvidenceSourceSNMPSysDescr, sysDescr, string(domain.CatSwitch), domain.OSFamilyNetwork, "", 80)}
	case strings.Contains(d, "aruba") || strings.Contains(d, "procurve") || strings.Contains(d, "hp j") || strings.Contains(d, "hewlett"):
		return []domain.ClassificationEvidence{ev(domain.EvidenceSourceSNMPSysDescr, sysDescr, string(domain.CatSwitch), domain.OSFamilyNetwork, "", 78)}
	case strings.Contains(d, "linux"):
		return []domain.ClassificationEvidence{ev(domain.EvidenceSourceSNMPSysDescr, sysDescr, string(domain.CatServer), domain.OSFamilyLinux, "", 70)}
	// Generic device-class keywords — catch vendors with no specific rule above (e.g.
	// "3Com Baseline Switch 2928", "MikroTik RouterOS"). Lower confidence than the named
	// vendors, but enough to classify a switch/router/firewall/AP instead of "unknown".
	case strings.Contains(d, "firewall"):
		return []domain.ClassificationEvidence{ev(domain.EvidenceSourceSNMPSysDescr, sysDescr, string(domain.CatFirewall), domain.OSFamilyNetwork, "", 68)}
	case strings.Contains(d, "router"):
		return []domain.ClassificationEvidence{ev(domain.EvidenceSourceSNMPSysDescr, sysDescr, string(domain.CatRouter), domain.OSFamilyNetwork, "", 66)}
	case strings.Contains(d, "switch"):
		return []domain.ClassificationEvidence{ev(domain.EvidenceSourceSNMPSysDescr, sysDescr, string(domain.CatSwitch), domain.OSFamilyNetwork, "", 66)}
	case strings.Contains(d, "access point") || strings.Contains(d, "wireless ap"):
		return []domain.ClassificationEvidence{ev(domain.EvidenceSourceSNMPSysDescr, sysDescr, string(domain.CatAccessPoint), domain.OSFamilyNetwork, "", 64)}
	}
	// SNMP ANSWERED with a sysDescr but matched no rule: it is a real SNMP-managed network
	// device, so label it network_device_unclassified (honest) rather than vague "unknown".
	// Low confidence so any later fingerprint/keyword match wins. (req: 150.0.0.0/24 .100.)
	if strings.TrimSpace(sysDescr) != "" {
		return []domain.ClassificationEvidence{ev(domain.EvidenceSourceSNMPSysDescr, sysDescr, string(domain.CatNetworkUnclassified), domain.OSFamilyNetwork, "", 30)}
	}
	return nil
}

// HTTPServer classifies from an HTTP Server header / page-title hint.
func HTTPServer(server, title string) []domain.ClassificationEvidence {
	s := strings.ToLower(server)
	t := strings.ToLower(title)
	switch {
	case strings.Contains(s, "cisco-ios") || strings.Contains(s, "cisco ios"):
		return []domain.ClassificationEvidence{ev(domain.EvidenceSourceHTTP, "Server: "+server, string(domain.CatSwitch), domain.OSFamilyNetwork, "", 80)}
	case strings.Contains(s, "microsoft-iis") || strings.Contains(s, "iis/"):
		return []domain.ClassificationEvidence{ev(domain.EvidenceSourceHTTP, "Server: "+server, string(domain.CatServer), domain.OSFamilyWindows, "", 70)}
	case strings.Contains(s, "hikvision") || strings.Contains(s, "dnvrs-webs") || strings.Contains(s, "app-webs") || strings.Contains(t, "hikvision") || strings.Contains(t, "webcomponents"):
		// Hikvision web stack — camera or NVR; ISAPI deviceType disambiguates.
		return []domain.ClassificationEvidence{ev(domain.EvidenceSourceHTTP, "Server: "+server, string(domain.CatCamera), domain.OSFamilyEmbedded, "", 45)}
	}
	return nil
}

// WebVendorMarkers classifies from vendor fingerprints in the HTTP Server
// header, page <title>, or a small body snippet — the safe, unauthenticated way
// to spot VMware ESXi, wireless controllers, and voice/PBX systems that don't
// answer SNMP. Confidence is moderate (a banner is suggestive, not definitive);
// authenticated collection later confirms + enriches.
func WebVendorMarkers(server, title, body string) []domain.ClassificationEvidence {
	s := strings.ToLower(server + " " + title + " " + body)
	switch {
	case strings.Contains(s, "vmware") || strings.Contains(s, "esxi") || strings.Contains(s, "vsphere") || strings.Contains(s, "id_eesx"):
		return []domain.ClassificationEvidence{ev(domain.EvidenceSourceHTTP, "VMware/ESXi web marker", string(domain.CatVirtualHost), "", "esxi", 55)}
	// Voice/PBX FIRST — must beat the wireless check below, because "Cisco Unified"
	// contains the substring "unifi" and would otherwise false-match UniFi.
	case strings.Contains(s, "cisco unified") || strings.Contains(s, "cucm") || strings.Contains(s, "callmanager") || strings.Contains(s, "unified cm"):
		return []domain.ClassificationEvidence{ev(domain.EvidenceSourceHTTP, "Cisco CUCM web marker", string(domain.CatPBX), "", "cucm", 60)}
	// Alcatel-Lucent Enterprise OmniSwitch is a LAN SWITCH (not a PBX). Match it
	// specifically BEFORE the OmniPCX/OmniVista voice markers.
	case strings.Contains(s, "omniswitch"):
		return []domain.ClassificationEvidence{ev(domain.EvidenceSourceHTTP, "Alcatel OmniSwitch web marker", string(domain.CatSwitch), domain.OSFamilyNetwork, "alcatel_omniswitch", 60)}
	case strings.Contains(s, "omnipcx") || strings.Contains(s, "omnivista"):
		return []domain.ClassificationEvidence{ev(domain.EvidenceSourceHTTP, "Alcatel OmniPCX/OmniVista voice web marker", string(domain.CatPBX), "", "alcatel_voice", 50)}
	// Web-managed L2 switches that answer ONLY on HTTP (no SNMP/SSH): the page
	// title carries the vendor + "switch". Ruijie Easy-Smart and similar SOHO/
	// unmanaged-plus switches expose a web UI only — classify them as switch so
	// they leave the "unknown" bucket. Deep SNMP/SSH management may still not be
	// available (the scan reports that honestly), but the device type is known.
	case strings.Contains(s, "ruijie") || (strings.Contains(s, "easy-smart") && strings.Contains(s, "switch")):
		return []domain.ClassificationEvidence{ev(domain.EvidenceSourceHTTP, "Ruijie/Easy-Smart web-managed switch marker", string(domain.CatSwitch), domain.OSFamilyNetwork, "", 55)}
	// NAS storage appliances — QNAP QTS/QuTS (also exposes the QDocRoot XML on
	// /cgi-bin/authLogin.cgi), Synology DSM, TrueNAS/FreeNAS. Strong vendor markers.
	case strings.Contains(s, "qnap") || strings.Contains(s, "qdocroot") || strings.Contains(s, "quts") || strings.Contains(s, "/qts/"):
		return []domain.ClassificationEvidence{ev(domain.EvidenceSourceHTTP, "QNAP QTS web marker", string(domain.CatStorage), domain.OSFamilyEmbedded, "qnap", 85)}
	case strings.Contains(s, "synology") || strings.Contains(s, "diskstation") || strings.Contains(s, "dsm ") || strings.Contains(s, "synoscgi"):
		return []domain.ClassificationEvidence{ev(domain.EvidenceSourceHTTP, "Synology DSM web marker", string(domain.CatStorage), domain.OSFamilyEmbedded, "synology", 85)}
	case strings.Contains(s, "truenas") || strings.Contains(s, "freenas"):
		return []domain.ClassificationEvidence{ev(domain.EvidenceSourceHTTP, "TrueNAS web marker", string(domain.CatStorage), domain.OSFamilyEmbedded, "truenas", 85)}
	// Wireless controllers. "unifi" is guarded against "unified" (Cisco Unified) —
	// already handled above, but the guard keeps any other "unified…" string out.
	case (strings.Contains(s, "unifi") && !strings.Contains(s, "unified")) || strings.Contains(s, "aruba") || strings.Contains(s, "ruckus") ||
		strings.Contains(s, "extremecloud") || strings.Contains(s, "wireless controller") || strings.Contains(s, "omada"):
		return []domain.ClassificationEvidence{ev(domain.EvidenceSourceHTTP, "wireless-controller web marker", string(domain.CatWirelessController), domain.OSFamilyNetwork, "", 55)}
	}
	return nil
}

// OpenPorts emits weak OS/category hints from the open-TCP-port profile. These
// are corroborating signals only — never decisive on their own.
func OpenPorts(tcp []int) []domain.ClassificationEvidence {
	has := map[int]bool{}
	for _, p := range tcp {
		has[p] = true
	}
	var out []domain.ClassificationEvidence
	// Windows management surface. RDP/WinRM/SMB are all enabled on *managed
	// workstations* as well as servers, so on their own they are only an
	// os_family=windows hint — never a server-vs-workstation signal. (A device's
	// server-vs-workstation role comes from the authenticated OS caption
	// (OSCaption) or AD operatingSystem (ADComputer), not from which mgmt port is
	// open.) RDP nudges weakly toward endpoint since it's the most workstation-y
	// of the three; WinRM/SMB stay category-neutral.
	if has[3389] {
		out = append(out, ev(domain.EvidenceSourceRDP, "tcp/3389 (RDP)", string(domain.CatEndpoint), domain.OSFamilyWindows, "", 45))
	}
	if has[5985] || has[5986] {
		out = append(out, ev(domain.EvidenceSourceWinRM, "tcp/5985-5986 (WinRM)", "", domain.OSFamilyWindows, "", 45))
	}
	if has[445] {
		out = append(out, ev(domain.EvidenceSourceSMB, "tcp/445 (SMB)", "", domain.OSFamilyWindows, "", 40))
	}
	// A domain controller advertises Kerberos + LDAP together.
	if has[88] && has[389] {
		out = append(out, ev(domain.EvidenceSourcePort, "tcp/88+389 (Kerberos+LDAP)", string(domain.CatServer), domain.OSFamilyWindows, "domain_controller", 60))
	}
	// RTSP is a video signal (camera or NVR).
	if has[554] {
		out = append(out, ev(domain.EvidenceSourcePort, "tcp/554 (RTSP)", string(domain.CatCamera), domain.OSFamilyEmbedded, "", 40))
	}
	// JetDirect printing.
	if has[9100] {
		out = append(out, ev(domain.EvidenceSourcePort, "tcp/9100 (JetDirect)", string(domain.CatPrinter), domain.OSFamilyEmbedded, "", 55))
	}
	// SIP signalling — an IP phone / voice endpoint (Alcatel, Cisco, Yealink, Grandstream…).
	// Many SIP phones expose ONLY 5060 (no web/SSH), so without this rule they answer no
	// scanned port and are never enrolled. Guarded against a Windows/RPC surface: a PC running
	// a SOFTPHONE also opens 5060 but is a workstation, not a phone — let the Windows evidence win.
	winMgmt := has[135] || has[445] || has[3389] || has[5985] || has[5986]
	if (has[5060] || has[5061]) && !winMgmt {
		out = append(out, ev(domain.EvidenceSourcePort, "tcp/5060 (SIP)", string(domain.CatIPPhone), domain.OSFamilyEmbedded, "", 55))
	}
	// NFS export (2049, usually with the 111 portmapper) is a file-serving / NAS signal.
	// A device exposing NFS — especially alongside SMB + a web admin UI — is storage
	// (QNAP, Synology, TrueNAS, a Linux NAS), not a generic app server. Modest confidence so a
	// vendor web marker (below) or an authenticated OS caption can still refine it; the operator
	// can also reclassify. Windows file servers use SMB (445), handled separately above.
	if has[2049] {
		out = append(out, ev(domain.EvidenceSourcePort, "tcp/2049 (NFS export)", string(domain.CatStorage), domain.OSFamilyEmbedded, "", 50))
	}
	// VMware ESXi host agent. Port 902 (vpxa/authd) points at ESXi — BUT only on a host that is
	// not also clearly Windows. A bare ESXi host exposes 902 (+ the 443 vSphere SDK) and does NOT
	// run WinRM/SMB/RPC/RDP; a Windows box that happens to expose 902 (VMware Workstation, a
	// Windows vCenter, or a stray 902) also has 5985/135/445/3389 — classifying THAT as virtual_host
	// strands it on vSphere collection and it is never tried with Windows credentials. So: suppress
	// the ESXi signal when Windows ports are present (let the Windows rules win), require the 443 SDK
	// port for the high-confidence ESXi case, and treat 902-alone as only a weak candidate.
	if has[902] {
		windowsPorts := has[5985] || has[5986] || has[135] || has[445] || has[3389]
		switch {
		case windowsPorts:
			// Not a bare ESXi host — defer to the Windows classification rules.
		case has[443]:
			out = append(out, ev(domain.EvidenceSourcePort, "tcp/902+443 (VMware ESXi host agent + vSphere SDK)", string(domain.CatVirtualHost), "", "esxi", 88))
		default:
			out = append(out, ev(domain.EvidenceSourcePort, "tcp/902 (VMware ESXi host agent — unconfirmed, no 443 SDK)", string(domain.CatVirtualHost), "", "esxi", 60))
		}
	}
	return out
}

// ADComputer turns an Active Directory computer object's OS string into
// evidence — authoritative for Windows fleet membership.
func ADComputer(operatingSystem string) []domain.ClassificationEvidence {
	os := strings.ToLower(operatingSystem)
	if os == "" {
		return nil
	}
	if strings.Contains(os, "server") {
		return []domain.ClassificationEvidence{ev(domain.EvidenceSourceAD, operatingSystem, string(domain.CatServer), domain.OSFamilyWindows, "windows_server", 85)}
	}
	if strings.Contains(os, "windows") {
		return []domain.ClassificationEvidence{ev(domain.EvidenceSourceAD, operatingSystem, string(domain.CatEndpoint), domain.OSFamilyWindows, "windows_workstation", 80)}
	}
	return nil
}

// OSCaption classifies from the authenticated deep-inventory OS caption
// (WinRM Win32_OperatingSystem.Caption / SSH /etc/os-release PRETTY_NAME), e.g.
// "Microsoft Windows 11 Pro for Workstations" or "Microsoft Windows Server 2019
// Standard". Because this comes from a *successful authenticated collection* it
// is the most authoritative OS signal HIMS has — far stronger than a network
// port guess — so it carries high confidence and, crucially, distinguishes a
// Windows **client** edition (→ endpoint/workstation) from a Windows **Server**
// edition (→ server). This is what prevents a Win11 workstation that happens to
// expose WinRM/RDP from being relabelled a server on re-classify.
func OSCaption(caption string) []domain.ClassificationEvidence {
	c := strings.ToLower(strings.TrimSpace(caption))
	if c == "" {
		return nil
	}
	switch {
	case strings.Contains(c, "windows server") || (strings.Contains(c, "windows") && strings.Contains(c, "server")):
		return []domain.ClassificationEvidence{ev(domain.EvidenceSourceOSInventory, caption, string(domain.CatServer), domain.OSFamilyWindows, "windows_server", 92)}
	case strings.Contains(c, "windows"):
		// Any non-Server Windows edition (11/10/8/7/Vista/XP, Pro/Home/Enterprise) is a workstation.
		return []domain.ClassificationEvidence{ev(domain.EvidenceSourceOSInventory, caption, string(domain.CatEndpoint), domain.OSFamilyWindows, "windows_workstation", 90)}
	case strings.Contains(c, "mac os") || strings.Contains(c, "macos") || strings.Contains(c, "darwin") || strings.Contains(c, "os x"):
		return []domain.ClassificationEvidence{ev(domain.EvidenceSourceOSInventory, caption, string(domain.CatEndpoint), domain.OSFamilyMacOS, "mac_workstation", 90)}
	case strings.Contains(c, "linux") || strings.Contains(c, "ubuntu") || strings.Contains(c, "debian") ||
		strings.Contains(c, "centos") || strings.Contains(c, "red hat") || strings.Contains(c, "rhel") ||
		strings.Contains(c, "rocky") || strings.Contains(c, "almalinux") || strings.Contains(c, "suse") ||
		strings.Contains(c, "fedora") || strings.Contains(c, "oracle linux"):
		// Linux desktop vs server is rarely distinguishable from the caption and
		// in this fleet Linux hosts are servers — default to server. A "desktop"
		// token downgrades to workstation.
		if strings.Contains(c, "desktop") {
			return []domain.ClassificationEvidence{ev(domain.EvidenceSourceOSInventory, caption, string(domain.CatEndpoint), domain.OSFamilyLinux, "linux_workstation", 88)}
		}
		return []domain.ClassificationEvidence{ev(domain.EvidenceSourceOSInventory, caption, string(domain.CatServer), domain.OSFamilyLinux, "linux_server", 88)}
	}
	return nil
}
