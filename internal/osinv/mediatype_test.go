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
		{"Virtual", "", "Virtual"},                   // hypervisor-presented disk
		{"", "VMware Virtual disk", "Virtual"},        // VMware guest
		{"", "Msft Virtual Disk", "Virtual"},          // Hyper-V guest
		{"sata rotational=1 QEMU HARDDISK", "QEMU HARDDISK", "Virtual"}, // KVM guest (virtual beats HDD)
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

func TestIsVirtualHost(t *testing.T) {
	virtual := [][2]string{
		{"Microsoft Corporation", "Virtual Machine"}, // Hyper-V (the 150.0.0.111 case)
		{"VMware, Inc.", "VMware Virtual Platform"},
		{"innotek GmbH", "VirtualBox"},
		{"QEMU", "Standard PC"},
	}
	for _, c := range virtual {
		if !IsVirtualHost(c[0], c[1]) {
			t.Errorf("IsVirtualHost(%q,%q)=false, want true", c[0], c[1])
		}
	}
	physical := [][2]string{{"HP", "ProLiant DL380 Gen10"}, {"Dell Inc.", "PowerEdge R740"}, {"", ""}}
	for _, c := range physical {
		if IsVirtualHost(c[0], c[1]) {
			t.Errorf("IsVirtualHost(%q,%q)=true, want false", c[0], c[1])
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
	m := parseLsblk([]string{
		`NAME="sda" ROTA="1" TRAN="sata" MODEL="ST1000DM010"`,
		`NAME="sdb" ROTA="0" TRAN="sata" MODEL="Samsung SSD 860"`,
		`NAME="nvme0n1" ROTA="0" TRAN="nvme" MODEL="WD_BLACK SN770"`,
		`NAME="sdc" ROTA="1" TRAN="" MODEL="VMware Virtual disk"`,
	})
	if m["sda"] != "HDD" || m["sdb"] != "SSD" || m["nvme0n1"] != "NVMe" || m["sdc"] != "Virtual" {
		t.Fatalf("parseLsblk map wrong: %+v", m)
	}
}
