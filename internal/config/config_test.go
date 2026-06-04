package config

import "testing"

func TestCommandFor(t *testing.T) {
	cases := map[string]string{
		"cisco_ios":      "show running-config",
		"aruba_hpe":      "show running-config",
		"extreme_switch": "show running-config",
		"fortigate":      "show full-configuration",
		"huawei_vrp":     "display current-configuration",
		"unknown_driver": "",
		"":               "",
	}
	for driver, want := range cases {
		if got := CommandFor(driver); got != want {
			t.Errorf("CommandFor(%q) = %q, want %q", driver, got, want)
		}
	}
}

func TestNormalizeAndHashIgnoreCosmetic(t *testing.T) {
	a := "hostname sw1\ninterface Gi0/1  \n description uplink\n"
	b := "hostname sw1\r\ninterface Gi0/1\r\n description uplink\r\n\r\n"
	if Normalize(a) != Normalize(b) {
		t.Fatalf("normalize did not converge:\n%q\nvs\n%q", Normalize(a), Normalize(b))
	}
	if Hash(a) != Hash(b) {
		t.Errorf("hash differs for cosmetically-identical configs: %s vs %s", Hash(a), Hash(b))
	}
}

func TestHashChangesOnRealEdit(t *testing.T) {
	a := "hostname sw1\nvlan 10\n"
	b := "hostname sw1\nvlan 20\n"
	if Hash(a) == Hash(b) {
		t.Error("hash should differ when a config line actually changes")
	}
}

func TestDiff(t *testing.T) {
	a := "line1\nline2\nline3\n"
	b := "line1\nline2-changed\nline3\nline4\n"
	lines, stat := Diff(a, b)
	if stat.Added != 2 { // "line2-changed" + "line4"
		t.Errorf("Added = %d, want 2", stat.Added)
	}
	if stat.Removed != 1 { // "line2"
		t.Errorf("Removed = %d, want 1", stat.Removed)
	}
	// Context line1 must survive as unchanged.
	if lines[0].Op != ' ' || lines[0].Text != "line1" {
		t.Errorf("first diff line = %q %q, want context line1", string(lines[0].Op), lines[0].Text)
	}
}

func TestDiffIdentical(t *testing.T) {
	a := "a\nb\nc\n"
	_, stat := Diff(a, a)
	if stat.Added != 0 || stat.Removed != 0 {
		t.Errorf("identical configs should diff to 0/0, got %+v", stat)
	}
}
