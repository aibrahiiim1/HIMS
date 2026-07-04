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

func TestParseSize(t *testing.T) {
	mb := 3839.8
	if got := parseSize("3839.8 MB"); got != int64(mb*1024*1024) {
		t.Fatalf("parseSize(3839.8 MB) = %d", got)
	}
	gb := 1014.41
	if got := parseSize("1014.41 GB"); got != int64(gb*1024*1024*1024) {
		t.Fatalf("parseSize(1014.41 GB) = %d", got)
	}
	if got := parseSize("2 GB"); got != 2*1024*1024*1024 {
		t.Fatalf("parseSize(2 GB) = %d", got)
	}
	if got := parseSize("27934135943168"); got != 27934135943168 {
		t.Fatalf("parseSize(bare bytes) = %d", got)
	}
	if got := parseSize(""); got != 0 {
		t.Fatalf("parseSize(empty) = %d", got)
	}
}

func TestParseInt64(t *testing.T) {
	if got := parseInt64("4000787030016"); got != 4000787030016 {
		t.Fatalf("parseInt64 = %d", got)
	}
	if got := parseInt64("N/A"); got != 0 {
		t.Fatalf("parseInt64(N/A) = %d", got)
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

func TestSplitVolumeDescr(t *testing.T) {
	name, pool := splitVolumeDescr("[Volume DataVol1, Pool 1]")
	if name != "DataVol1" || pool != "Pool 1" {
		t.Fatalf("splitVolumeDescr = %q,%q", name, pool)
	}
	name, pool = splitVolumeDescr("System")
	if name != "System" || pool != "" {
		t.Fatalf("splitVolumeDescr(no pool) = %q,%q", name, pool)
	}
}

func TestRaidLabel(t *testing.T) {
	cases := map[string]string{"5": "RAID 5", "1": "RAID 1", "": "", "-1": "", "RAID 6": "RAID 6"}
	for in, want := range cases {
		if got := raidLabel(in); got != want {
			t.Errorf("raidLabel(%q) = %q want %q", in, got, want)
		}
	}
}

func TestIscsiStatus(t *testing.T) {
	if iscsiStatus("1") != "Ready" || iscsiStatus("0") != "Offline" || iscsiStatus("") != "" {
		t.Fatal("iscsiStatus mapping wrong")
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
	if h := rollupHealth(nil, nil, nil); h != "Unknown" {
		t.Fatalf("empty = %q", h)
	}
	allGood := []Disk{{Health: "Good"}, {Health: "Good"}}
	if h := rollupHealth(allGood, []Pool{{Status: "Ready"}}, []Volume{{Status: "Ready"}}); h != "OK" {
		t.Fatalf("allGood = %q", h)
	}
	if h := rollupHealth(allGood, []Pool{{Status: "Degraded"}}, nil); h != "Warning" {
		t.Fatalf("degraded pool = %q", h)
	}
	if h := rollupHealth([]Disk{{Health: "Error"}}, nil, nil); h != "Critical" {
		t.Fatalf("bad disk = %q", h)
	}
}
