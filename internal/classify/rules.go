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
// /ISAPI/System/deviceInfo <deviceType> + <model>. This is the definitive NVR
// vs camera signal: deviceType "DVR"/"NVR" is a recorder, "IPCamera"/"IPDome" a
// camera. Model code is a corroborating signal (…NI…/…NVR… = recorder).
func ISAPIDeviceInfo(deviceType, model string) []domain.ClassificationEvidence {
	dt := strings.ToLower(strings.TrimSpace(deviceType))
	m := strings.ToUpper(strings.TrimSpace(model))
	var out []domain.ClassificationEvidence
	switch {
	case strings.Contains(dt, "nvr") || strings.Contains(dt, "dvr"):
		out = append(out, ev(domain.EvidenceSourceISAPI, "deviceType="+deviceType, string(domain.CatNVR), domain.OSFamilyEmbedded, "nvr", 90))
	case strings.Contains(dt, "ipcamera") || strings.Contains(dt, "ipdome") || strings.Contains(dt, "ipc") || strings.Contains(dt, "camera"):
		out = append(out, ev(domain.EvidenceSourceISAPI, "deviceType="+deviceType, string(domain.CatCamera), domain.OSFamilyEmbedded, "ip_camera", 88))
	case dt != "":
		// Some other Hikvision appliance — still embedded, category uncertain.
		out = append(out, ev(domain.EvidenceSourceISAPI, "deviceType="+deviceType, "", domain.OSFamilyEmbedded, "", 40))
	}
	// Model-code corroboration (Hikvision DS-7xxxN[I]/...NVR = recorder; DS-2CD = camera).
	if m != "" {
		switch {
		case strings.Contains(m, "NVR") || strings.Contains(m, "DVR") || strings.HasPrefix(m, "DS-7") || strings.HasPrefix(m, "DS-8") || strings.HasPrefix(m, "DS-9"):
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
