package osinv

import "testing"

func TestNormalizeMediaType(t *testing.T) {
	cases := []struct{ hint, model, want string }{
		{"NVMe", "Samsung SSD 980", "NVMe"},        // bus wins over model "SSD"
		{"", "Samsung MZVLB512 NVMe", "NVMe"},        // model-only nvme
		{"4", "", "SSD"},                             // MSFT MediaType 4
		{"SSD", "", "SSD"},
		{"", "Crucial CT500 SSD", "SSD"},            // model-only ssd
		{"nvme rotational=0", "", "NVMe"},            // lsblk nvme
		{"sata rotational=0", "", "SSD"},            // lsblk non-rotational sata
		{"sata rotational=1", "", "HDD"},            // lsblk rotational
		{"3", "", "HDD"},                             // MSFT MediaType 3
		{"HDD", "", "HDD"},
		{"", "WDC WD10EZEX", ""},                     // unknown model -> honest empty
		{"", "", ""},
		{"Unspecified", "", ""},                      // never guessed
	}
	for _, c := range cases {
		if got := NormalizeMediaType(c.hint, c.model); got != c.want {
			t.Errorf("NormalizeMediaType(%q,%q)=%q want %q", c.hint, c.model, got, c.want)
		}
	}
}

func TestBaseDisk(t *testing.T) {
	cases := map[string]string{
		"/dev/sda2": "sda", "/dev/sda": "sda", "/dev/vda1": "vda",
		"/dev/nvme0n1p2": "nvme0n1", "/dev/nvme0n1": "nvme0n1",
		"/dev/mapper/ubuntu--vg-ubuntu--lv": "", "tmpfs": "", "/dev/dm-0": "", "": "",
	}
	for in, want := range cases {
		if got := baseDisk(in); got != want {
			t.Errorf("baseDisk(%q)=%q want %q", in, got, want)
		}
	}
}

func TestParseLsblkMapsMedia(t *testing.T) {
	m := parseLsblk([]string{"sda 1 sata", "sdb 0 sata", "nvme0n1 0 nvme"})
	if m["sda"] != "HDD" || m["sdb"] != "SSD" || m["nvme0n1"] != "NVMe" {
		t.Fatalf("parseLsblk map wrong: %+v", m)
	}
}
