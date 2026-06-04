package osinv

import "testing"

// linuxFixture mirrors the linuxScript output for an Ubuntu web+db host.
const linuxFixture = `@@@SEC osrelease
PRETTY_NAME="Ubuntu 22.04.3 LTS"
NAME="Ubuntu"
VERSION_ID="22.04"
VERSION_CODENAME=jammy
@@@SEC kernel
5.15.0-91-generic
@@@SEC arch
x86_64
@@@SEC hostname
web01
web01.corp.local
@@@SEC uptime
864000.50 1700000.00
@@@SEC timezone
Europe/Istanbul
@@@SEC meminfo
MemTotal:        8174288 kB
SwapTotal:       2097148 kB
@@@SEC lscpu
Model name:            Intel(R) Xeon(R) CPU E5-2670
Socket(s):             2
Core(s) per socket:    4
@@@SEC dmi_system
QEMU
Standard PC (i440FX + PIIX, 1996)
@@@SEC dmi_bios
1.15.0-1
04/01/2014
@@@SEC df
Filesystem     Type      1B-blocks        Avail Mounted on
/dev/sda1      ext4    52521566208  31000000000 /
/dev/sda2      xfs    104857600000  90000000000 /data
@@@SEC iplink
1: lo: <LOOPBACK,UP> mtu 65536 ... link/loopback 00:00:00:00:00:00
2: eth0: <BROADCAST,MULTICAST,UP> mtu 1500 ... link/ether 52:54:00:ab:cd:ef brd ff:ff:ff:ff:ff:ff
@@@SEC ipaddr
2: eth0    inet 10.0.0.20/24 brd 10.0.0.255 scope global eth0
@@@SEC iproute
default via 10.0.0.1 dev eth0 proto static
10.0.0.0/24 dev eth0 proto kernel scope link
@@@SEC resolv
nameserver 10.0.0.10
nameserver 1.1.1.1
@@@SEC services
nginx.service            loaded active running A high performance web server
postgresql.service       loaded active running PostgreSQL RDBMS
ssh.service              loaded active running OpenBSD Secure Shell server
@@@SEC unitfiles
nginx.service            enabled
postgresql.service       enabled
ssh.service              enabled
@@@SEC ps
    PID COMMAND          RSS %CPU
   1234 postgres      524288  2.5
    900 nginx          20480  0.1
@@@SEC dpkg
nginx	1.18.0-6ubuntu14	amd64
postgresql-14	14.10-0ubuntu0.22.04.1	amd64
@@@SEC rpm
@@@SEC end
`

func TestParseLinux(t *testing.T) {
	rep := ParseLinux(linuxFixture)
	if rep.Method != "ssh" {
		t.Errorf("method = %q", rep.Method)
	}
	if rep.OS.Caption != "Ubuntu 22.04.3 LTS" || rep.OS.Version != "22.04" || rep.OS.Kernel != "5.15.0-91-generic" {
		t.Errorf("os wrong: %+v", rep.OS)
	}
	if rep.OS.Arch != "x86_64" || rep.OS.Timezone != "Europe/Istanbul" || rep.OS.UptimeSeconds != 864000 {
		t.Errorf("os2 wrong: %+v", rep.OS)
	}
	if rep.Identity.Hostname != "web01" || rep.Identity.FQDN != "web01.corp.local" || rep.Identity.Domain != "corp.local" {
		t.Errorf("identity wrong: %+v", rep.Identity)
	}
	if rep.Hardware.RAMTotalBytes != 8174288*1024 || rep.Hardware.SwapTotalBytes != 2097148*1024 {
		t.Errorf("mem wrong: %+v", rep.Hardware)
	}
	if rep.Hardware.CPUSockets != 2 || rep.Hardware.CPUCores != 8 || rep.Hardware.CPUModel == "" {
		t.Errorf("cpu wrong: %+v", rep.Hardware)
	}
	if rep.Hardware.Manufacturer != "QEMU" || rep.Hardware.BIOSVersion != "1.15.0-1" {
		t.Errorf("dmi wrong: %+v", rep.Hardware)
	}
	if len(rep.Disks) != 2 || rep.Disks[0].Name != "/" || rep.Disks[0].Filesystem != "ext4" || rep.Disks[1].Name != "/data" {
		t.Errorf("disks wrong: %+v", rep.Disks)
	}
	if len(rep.Nics) != 1 || rep.Nics[0].Name != "eth0" || rep.Nics[0].MAC != "52:54:00:AB:CD:EF" {
		t.Errorf("nics wrong: %+v", rep.Nics)
	}
	if rep.Nics[0].IPAddresses != "10.0.0.20" || rep.Nics[0].Gateway != "10.0.0.1" || rep.Nics[0].DNSServers != "10.0.0.10,1.1.1.1" {
		t.Errorf("nic detail wrong: %+v", rep.Nics[0])
	}
	if len(rep.Services) != 3 || rep.Services[0].Name != "nginx" || rep.Services[0].Status != "running" || rep.Services[0].StartType != "enabled" {
		t.Errorf("services wrong: %+v", rep.Services)
	}
	if len(rep.Processes) != 2 || rep.Processes[0].Name != "postgres" || rep.Processes[0].MemBytes != 524288*1024 {
		t.Errorf("processes wrong: %+v", rep.Processes)
	}
	if len(rep.Software) != 2 {
		t.Errorf("software count = %d, want 2", len(rep.Software))
	}
}

func TestDetectLinuxRoles(t *testing.T) {
	rep := ParseLinux(linuxFixture)
	roles := DetectLinuxRoles(rep)
	got := map[string]bool{}
	for _, r := range roles {
		got[r] = true
	}
	if !got["Web Server"] || !got["Database Server"] {
		t.Errorf("expected Web + Database server roles, got %v", roles)
	}
}

func TestParseLinux_MissingSectionsAreEmpty(t *testing.T) {
	// A minimal host that only answered hostname — everything else "Not collected".
	rep := ParseLinux("@@@SEC hostname\nminimal\n@@@SEC end\n")
	if rep.Identity.Hostname != "minimal" {
		t.Errorf("hostname = %q", rep.Identity.Hostname)
	}
	if len(rep.Disks) != 0 || len(rep.Services) != 0 || rep.Hardware.RAMTotalBytes != 0 {
		t.Error("absent sections must stay empty, not fabricated")
	}
}
