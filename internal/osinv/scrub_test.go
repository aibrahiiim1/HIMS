package osinv

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// assertClean fails if s still contains a NUL byte or invalid UTF-8.
func assertClean(t *testing.T, label, s string) {
	t.Helper()
	if strings.IndexByte(s, 0) >= 0 {
		t.Errorf("%s still contains a NUL byte: %q", label, s)
	}
	if !utf8.ValidString(s) {
		t.Errorf("%s still contains invalid UTF-8: %q", label, s)
	}
}

// TestScrubReportStripsNULAndInvalidUTF8 proves the scrubber reaches every kind
// of string location in a Report — top-level fields, nested structs, slice
// elements, and the optional *EventSummary pointer — and that it removes NUL
// bytes / invalid UTF-8 while preserving the surrounding clean text. This is the
// guard for the SQLSTATE 22021 persist failure that previously lost successful
// WinRM collections whenever a value carried an embedded 0x00.
func TestScrubReportStripsNULAndInvalidUTF8(t *testing.T) {
	rep := Report{
		Method:       "winrm",
		SoftwareNote: "collected via\x00 remote_registry",
		Identity:     Identity{Hostname: "PC-01\x00", Domain: "corp\xfflocal"},
		OS:           OSInfo{Caption: "Windows 10 Pro\x00", Version: "10.0.19045"},
		Hardware:     Hardware{Serial: "SN\x00123", Manufacturer: "Dell Inc."},
		Disks:        []Disk{{Name: "C:\x00", Model: "Samsung SSD"}},
		Nics:         []Nic{{Name: "Ethernet0\x00", IPAddresses: "172.21.60.106"}},
		Services:     []Service{{Name: "Spooler", Description: "Print\x00 Spooler"}},
		Software:     []Software{{Name: "App\x00", Publisher: "Vendor\xfe"}},
		Events:       &EventSummary{LastCritical: "evt\x00source"},
	}

	scrubReport(&rep)

	assertClean(t, "SoftwareNote", rep.SoftwareNote)
	assertClean(t, "Identity.Hostname", rep.Identity.Hostname)
	assertClean(t, "Identity.Domain", rep.Identity.Domain)
	assertClean(t, "OS.Caption", rep.OS.Caption)
	assertClean(t, "Hardware.Serial", rep.Hardware.Serial)
	assertClean(t, "Disks[0].Name", rep.Disks[0].Name)
	assertClean(t, "Nics[0].Name", rep.Nics[0].Name)
	assertClean(t, "Services[0].Description", rep.Services[0].Description)
	assertClean(t, "Software[0].Name", rep.Software[0].Name)
	assertClean(t, "Software[0].Publisher", rep.Software[0].Publisher)
	assertClean(t, "Events.LastCritical", rep.Events.LastCritical)

	// Clean surrounding text must survive unchanged (only the bad bytes are dropped).
	if rep.SoftwareNote != "collected via remote_registry" {
		t.Errorf("SoftwareNote not preserved around NUL: %q", rep.SoftwareNote)
	}
	if rep.Identity.Hostname != "PC-01" {
		t.Errorf("Hostname not preserved: %q", rep.Identity.Hostname)
	}
	if rep.OS.Version != "10.0.19045" {
		t.Errorf("clean field OS.Version was altered: %q", rep.OS.Version)
	}
	if rep.Hardware.Manufacturer != "Dell Inc." {
		t.Errorf("clean field Hardware.Manufacturer was altered: %q", rep.Hardware.Manufacturer)
	}
	if rep.Nics[0].IPAddresses != "172.21.60.106" {
		t.Errorf("clean field Nics[0].IPAddresses was altered: %q", rep.Nics[0].IPAddresses)
	}
}

// TestScrubReportNilPointerSafe ensures a Report with no EventSummary pointer
// (the common case) is scrubbed without panicking on the nil pointer.
func TestScrubReportNilPointerSafe(t *testing.T) {
	rep := Report{Identity: Identity{Hostname: "ok\x00"}}
	scrubReport(&rep) // must not panic on rep.Events == nil
	assertClean(t, "Identity.Hostname", rep.Identity.Hostname)
}
