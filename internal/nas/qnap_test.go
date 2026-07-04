package nas

import "testing"

func TestParsePct(t *testing.T) {
	v, ok := parsePct("22.4 %")
	if !ok || v != 22.4 {
		t.Fatalf("parsePct(22.4 %%) = %v,%v", v, ok)
	}
	if _, ok := parsePct(""); ok {
		t.Fatal("empty should not parse")
	}
	if v, ok := parsePct("100%"); !ok || v != 100 {
		t.Fatalf("parsePct(100%%) = %v,%v", v, ok)
	}
}

func TestParseMB(t *testing.T) {
	mb := 3839.8
	if got := parseMB("3839.8 MB"); got != int64(mb*1024*1024) {
		t.Fatalf("parseMB(3839.8 MB) = %d", got)
	}
	if got := parseMB("2 GB"); got != 2*1024*1024*1024 {
		t.Fatalf("parseMB(2 GB) = %d", got)
	}
	if got := parseMB(""); got != 0 {
		t.Fatalf("parseMB(empty) = %d", got)
	}
}

func TestParseTempC(t *testing.T) {
	cases := map[string]int{"43 C/109 F": 43, "37 C/99 F": 37, "29": 29, "0 C/32 F": 0}
	for in, want := range cases {
		v, ok := parseTempC(in)
		if !ok || v != want {
			t.Fatalf("parseTempC(%q) = %v,%v want %d", in, v, ok, want)
		}
	}
	if _, ok := parseTempC(""); ok {
		t.Fatal("empty should not parse")
	}
	if _, ok := parseTempC("N/A"); ok {
		t.Fatal("N/A should not parse")
	}
}

func TestModelFirmwareFromDescr(t *testing.T) {
	const descr = "Linux TS-X53II 5.2.4.3079"
	if m := modelFromDescr(descr); m != "TS-X53II" {
		t.Fatalf("modelFromDescr = %q", m)
	}
	if fw := firmwareFromDescr(descr); fw != "5.2.4.3079" {
		t.Fatalf("firmwareFromDescr = %q", fw)
	}
	if fw := firmwareFromDescr("Linux localhost"); fw != "" {
		t.Fatalf("firmwareFromDescr(no version) = %q", fw)
	}
}

func TestIsDataVolume(t *testing.T) {
	keep := []string{"/share/CACHEDEV1_DATA", "/mnt/pool1", "/share/ZFS530_DATA"}
	drop := []string{"/proc", "/sys", "/tmp", "/mnt/HDA_ROOT", "/dev/shm",
		"/share/CACHEDEV1_DATA/.samba/lock/msg.lock", "/mnt/ext/opt/samba/private/msg.sock"}
	for _, d := range keep {
		if !isDataVolume(d) {
			t.Errorf("isDataVolume(%q) = false, want true", d)
		}
	}
	for _, d := range drop {
		if isDataVolume(d) {
			t.Errorf("isDataVolume(%q) = true, want false", d)
		}
	}
}

func TestRollupHealth(t *testing.T) {
	if h := rollupHealth(nil); h != "Unknown" {
		t.Fatalf("empty = %q", h)
	}
	allGood := []Disk{{Health: "Good"}, {Health: "Good"}}
	if h := rollupHealth(allGood); h != "OK" {
		t.Fatalf("allGood = %q", h)
	}
	warn := []Disk{{Health: "Good"}, {Health: "Warning"}}
	if h := rollupHealth(warn); h != "Warning" {
		t.Fatalf("warn = %q", h)
	}
	bad := []Disk{{Health: "Good"}, {Health: "Error"}}
	if h := rollupHealth(bad); h != "Critical" {
		t.Fatalf("bad = %q", h)
	}
}
