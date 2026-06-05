package classify

import (
	"testing"

	"github.com/coralsearesorts/hims/internal/domain"
)

func TestFromEvidence_Empty(t *testing.T) {
	r := FromEvidence(nil)
	if r.Category != string(domain.CatUnknown) || r.Confidence != 0 {
		t.Errorf("empty evidence → %+v, want unknown/0", r)
	}
}

func TestISAPI_NVRvsCamera(t *testing.T) {
	// The headline NVR-vs-camera disambiguation (172.21.210.x Hikvision range).
	nvr := FromEvidence(ISAPIDeviceInfo("NVR", "DS-7608NI-K2"))
	if nvr.Category != string(domain.CatNVR) {
		t.Errorf("deviceType=NVR → %q, want nvr", nvr.Category)
	}
	if nvr.OSFamily != domain.OSFamilyEmbedded || nvr.Subtype != "nvr" {
		t.Errorf("NVR os/subtype = %q/%q", nvr.OSFamily, nvr.Subtype)
	}
	if nvr.Confidence < 90 {
		t.Errorf("NVR confidence %d, want >=90 (deviceType + model corroborate)", nvr.Confidence)
	}

	cam := FromEvidence(ISAPIDeviceInfo("IPCamera", "DS-2CD2143G0-I"))
	if cam.Category != string(domain.CatCamera) {
		t.Errorf("deviceType=IPCamera → %q, want camera", cam.Category)
	}
	// A DVR is also a recorder, not a camera.
	dvr := FromEvidence(ISAPIDeviceInfo("DVR", ""))
	if dvr.Category != string(domain.CatNVR) {
		t.Errorf("deviceType=DVR → %q, want nvr", dvr.Category)
	}
}

// TestWindowsWorkstation_NotMislabeledServer is the regression for the bug where
// re-classifying a Windows 11 workstation (172.21.60.20) relabelled it a server.
// The open-port profile alone (RDP+WinRM+SMB) must NOT win "server": WinRM/SMB
// are category-neutral and RDP nudges toward endpoint, so a port-only probe of a
// managed Windows box lands on endpoint, never server.
func TestWindowsWorkstation_NotMislabeledServer(t *testing.T) {
	r := FromEvidence(OpenPorts([]int{3389, 5985, 445}))
	if r.Category == string(domain.CatServer) {
		t.Fatalf("RDP+WinRM+SMB classified as server (the regression); got %+v", r)
	}
	if r.Category != string(domain.CatEndpoint) {
		t.Errorf("RDP+WinRM+SMB → %q, want endpoint", r.Category)
	}
	if r.OSFamily != domain.OSFamilyWindows {
		t.Errorf("os_family = %q, want windows", r.OSFamily)
	}
}

// TestOSCaption_ClientVsServer verifies the authoritative deep-inventory caption
// distinguishes Windows client (workstation) from Windows Server, and that it
// dominates the weak open-port heuristic when both are present.
func TestOSCaption_ClientVsServer(t *testing.T) {
	ws := FromEvidence(OSCaption("Microsoft Windows 11 Pro for Workstations"))
	if ws.Category != string(domain.CatEndpoint) || ws.Subtype != "windows_workstation" {
		t.Errorf("Win11 caption → %q/%q, want endpoint/windows_workstation", ws.Category, ws.Subtype)
	}
	srv := FromEvidence(OSCaption("Microsoft Windows Server 2019 Standard"))
	if srv.Category != string(domain.CatServer) || srv.Subtype != "windows_server" {
		t.Errorf("Server caption → %q/%q, want server/windows_server", srv.Category, srv.Subtype)
	}

	// Caption must beat the open-port probe: a Win11 box exposing WinRM/RDP stays a workstation.
	ev := append(OpenPorts([]int{3389, 5985, 445}), OSCaption("Microsoft Windows 11 Pro for Workstations")...)
	mixed := FromEvidence(ev)
	if mixed.Category != string(domain.CatEndpoint) {
		t.Errorf("caption+ports → %q, want endpoint (caption is authoritative)", mixed.Category)
	}

	// Linux caption → server by default; macOS → workstation.
	if lin := FromEvidence(OSCaption("Ubuntu 22.04.4 LTS")); lin.Category != string(domain.CatServer) || lin.OSFamily != domain.OSFamilyLinux {
		t.Errorf("Ubuntu caption → %q/%q, want server/linux", lin.Category, lin.OSFamily)
	}
}

func TestOSFamily_FromSignals(t *testing.T) {
	cases := []struct {
		name string
		ev   []domain.ClassificationEvidence
		cat  string
		os   string
	}{
		{"windows snmp", SNMPSysDescr("Hardware: Intel64 ... Windows Server 2019"), string(domain.CatServer), domain.OSFamilyWindows},
		{"linux ssh", SSHBanner("SSH-2.0-OpenSSH_8.0p1 Ubuntu-6ubuntu0.1"), string(domain.CatServer), domain.OSFamilyLinux},
		{"cisco ios", SNMPSysDescr("Cisco IOS Software, C2960 Software"), string(domain.CatSwitch), domain.OSFamilyNetwork},
		{"fortigate", SNMPSysDescr("FortiGate-60F v7.2.5"), string(domain.CatFirewall), domain.OSFamilyNetwork},
		{"iis windows", HTTPServer("Microsoft-IIS/10.0", ""), string(domain.CatServer), domain.OSFamilyWindows},
	}
	for _, c := range cases {
		r := FromEvidence(c.ev)
		if r.Category != c.cat || r.OSFamily != c.os {
			t.Errorf("%s → cat=%q os=%q, want %q/%q", c.name, r.Category, r.OSFamily, c.cat, c.os)
		}
	}
}

func TestCorroboration_BoostsAgreement(t *testing.T) {
	// Two independent sources agreeing on Windows server should out-score the
	// single strongest signal alone.
	single := FromEvidence([]domain.ClassificationEvidence{
		{Source: domain.EvidenceSourceSNMPSysDescr, Category: string(domain.CatServer), OSFamily: domain.OSFamilyWindows, Confidence: 80},
	})
	multi := FromEvidence([]domain.ClassificationEvidence{
		{Source: domain.EvidenceSourceSNMPSysDescr, Category: string(domain.CatServer), OSFamily: domain.OSFamilyWindows, Confidence: 80},
		{Source: domain.EvidenceSourceWinRM, Category: string(domain.CatServer), OSFamily: domain.OSFamilyWindows, Confidence: 50},
	})
	if multi.Confidence <= single.Confidence {
		t.Errorf("corroborated confidence %d should exceed single %d", multi.Confidence, single.Confidence)
	}
	if multi.Confidence > 100 {
		t.Errorf("confidence must cap at 100, got %d", multi.Confidence)
	}
}

func TestDeriveSubtype_ServerOSFamily(t *testing.T) {
	r := FromEvidence([]domain.ClassificationEvidence{
		{Source: domain.EvidenceSourceSNMPSysDescr, Category: string(domain.CatServer), OSFamily: domain.OSFamilyLinux, Confidence: 70},
	})
	if r.Subtype != "linux_server" {
		t.Errorf("server+linux subtype = %q, want linux_server", r.Subtype)
	}
}

func TestDomainController_FromPorts(t *testing.T) {
	r := FromEvidence(OpenPorts([]int{53, 88, 135, 389, 445, 3389}))
	if r.OSFamily != domain.OSFamilyWindows {
		t.Errorf("DC ports → os %q, want windows", r.OSFamily)
	}
	if r.Subtype != "domain_controller" {
		t.Errorf("DC ports → subtype %q, want domain_controller", r.Subtype)
	}
}

func TestEvidenceSortedStrongestFirst(t *testing.T) {
	r := FromEvidence([]domain.ClassificationEvidence{
		{Source: "z", Category: string(domain.CatServer), Confidence: 30},
		{Source: "a", Category: string(domain.CatServer), Confidence: 90},
	})
	if len(r.Evidence) != 2 || r.Evidence[0].Confidence != 90 {
		t.Errorf("evidence not sorted strongest-first: %+v", r.Evidence)
	}
}
