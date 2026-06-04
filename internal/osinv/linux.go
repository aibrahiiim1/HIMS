package osinv

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// linuxScript runs one round-trip over SSH and prints each section behind a
// marker line so ParseLinux can split + parse the pieces. Every command is
// guarded (2>/dev/null) so a missing tool yields an empty section ("Not
// collected") rather than failing the whole collection. No backticks.
const linuxScript = `
echo "@@@SEC osrelease"; cat /etc/os-release 2>/dev/null
echo "@@@SEC kernel"; uname -r 2>/dev/null
echo "@@@SEC arch"; uname -m 2>/dev/null
echo "@@@SEC hostname"; hostname 2>/dev/null; hostname -f 2>/dev/null
echo "@@@SEC uptime"; cat /proc/uptime 2>/dev/null
echo "@@@SEC timezone"; timedatectl show -p Timezone --value 2>/dev/null || cat /etc/timezone 2>/dev/null
echo "@@@SEC meminfo"; cat /proc/meminfo 2>/dev/null
echo "@@@SEC lscpu"; lscpu 2>/dev/null
echo "@@@SEC dmi_system"; cat /sys/class/dmi/id/sys_vendor /sys/class/dmi/id/product_name /sys/class/dmi/id/product_serial 2>/dev/null
echo "@@@SEC dmi_bios"; cat /sys/class/dmi/id/bios_version /sys/class/dmi/id/bios_date 2>/dev/null
echo "@@@SEC df"; df -B1 --output=source,fstype,size,avail,target 2>/dev/null
echo "@@@SEC iplink"; ip -o link show 2>/dev/null
echo "@@@SEC ipaddr"; ip -o -4 addr show 2>/dev/null
echo "@@@SEC iproute"; ip route 2>/dev/null
echo "@@@SEC resolv"; grep -h nameserver /etc/resolv.conf 2>/dev/null
echo "@@@SEC services"; systemctl list-units --type=service --all --no-pager --plain 2>/dev/null
echo "@@@SEC unitfiles"; systemctl list-unit-files --type=service --no-pager --plain 2>/dev/null
echo "@@@SEC ps"; ps -eo pid,comm,rss,pcpu --sort=-rss 2>/dev/null | head -51
echo "@@@SEC dpkg"; dpkg-query -W -f='${Package}\t${Version}\t${Architecture}\n' 2>/dev/null
echo "@@@SEC rpm"; rpm -qa --qf '%{NAME}\t%{VERSION}-%{RELEASE}\t%{ARCH}\n' 2>/dev/null
echo "@@@SEC end"
`

// CollectLinux runs the Linux inventory script over SSH and parses it.
func CollectLinux(ctx context.Context, r Runner) (Report, error) {
	out, err := r.Run(ctx, linuxScript)
	if err != nil {
		return Report{}, fmt.Errorf("ssh collect: %w", err)
	}
	return ParseLinux(out), nil
}

// splitSections splits the marked script output into section name → body lines.
func splitSections(out string) map[string][]string {
	secs := map[string][]string{}
	cur := ""
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "@@@SEC "); ok {
			cur = strings.TrimSpace(rest)
			secs[cur] = []string{}
			continue
		}
		if cur != "" {
			secs[cur] = append(secs[cur], line)
		}
	}
	return secs
}

// ParseLinux turns the marked script output into a Report (pure).
func ParseLinux(out string) Report {
	s := splitSections(out)
	rep := Report{Method: "ssh"}

	// OS
	osr := parseOSRelease(s["osrelease"])
	rep.OS.Caption = firstNonEmpty(osr["PRETTY_NAME"], osr["NAME"])
	rep.OS.Version = firstNonEmpty(osr["VERSION_ID"], osr["VERSION"])
	rep.OS.Edition = osr["VERSION_CODENAME"]
	rep.OS.Kernel = firstLine(s["kernel"])
	rep.OS.Arch = firstLine(s["arch"])
	rep.OS.Timezone = firstLine(s["timezone"])
	if up := firstLine(s["uptime"]); up != "" {
		if f := strings.Fields(up); len(f) > 0 {
			if secs, err := strconv.ParseFloat(f[0], 64); err == nil {
				rep.OS.UptimeSeconds = int64(secs)
			}
		}
	}

	// Identity
	host := nonEmptyLines(s["hostname"])
	if len(host) > 0 {
		rep.Identity.Hostname = host[0]
	}
	if len(host) > 1 && strings.Contains(host[1], ".") {
		rep.Identity.FQDN = host[1]
		if i := strings.IndexByte(host[1], '.'); i >= 0 {
			rep.Identity.Domain = host[1][i+1:]
		}
	}

	// Hardware: meminfo + lscpu + dmi
	mem := parseMeminfo(s["meminfo"])
	rep.Hardware.RAMTotalBytes = mem["MemTotal"]
	rep.Hardware.SwapTotalBytes = mem["SwapTotal"]
	parseLscpu(s["lscpu"], &rep.Hardware)
	dmiSys := nonEmptyLines(s["dmi_system"]) // vendor, product, serial (in order)
	if len(dmiSys) >= 1 {
		rep.Hardware.Manufacturer = dmiSys[0]
	}
	if len(dmiSys) >= 2 {
		rep.Hardware.Model = dmiSys[1]
	}
	if len(dmiSys) >= 3 {
		rep.Hardware.Serial = dmiSys[2]
	}
	dmiBios := nonEmptyLines(s["dmi_bios"])
	if len(dmiBios) >= 1 {
		rep.Hardware.BIOSVersion = dmiBios[0]
	}
	if len(dmiBios) >= 2 {
		rep.Hardware.BIOSDate = dmiBios[1]
	}

	rep.Disks = parseDF(s["df"])
	rep.Nics = parseNics(s["iplink"], s["ipaddr"], s["iproute"], s["resolv"])
	rep.Services = parseSystemd(s["services"], s["unitfiles"])
	rep.Processes = parsePS(s["ps"])
	rep.Software = parsePackages(s["dpkg"], s["rpm"])
	return rep
}

// --- section parsers (pure) ---

func parseOSRelease(lines []string) map[string]string {
	out := map[string]string{}
	for _, l := range lines {
		k, v, ok := strings.Cut(strings.TrimSpace(l), "=")
		if !ok {
			continue
		}
		out[k] = strings.Trim(v, `"'`)
	}
	return out
}

func parseMeminfo(lines []string) map[string]int64 {
	out := map[string]int64{}
	for _, l := range lines {
		f := strings.Fields(l)
		if len(f) >= 2 {
			key := strings.TrimSuffix(f[0], ":")
			if kb, err := strconv.ParseInt(f[1], 10, 64); err == nil {
				out[key] = kb * 1024 // meminfo is in kB
			}
		}
	}
	return out
}

func parseLscpu(lines []string, hw *Hardware) {
	var coresPerSocket, sockets int
	for _, l := range lines {
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "Model name":
			hw.CPUModel = v
		case "Socket(s)":
			sockets, _ = strconv.Atoi(v)
		case "Core(s) per socket":
			coresPerSocket, _ = strconv.Atoi(v)
		}
	}
	hw.CPUSockets = sockets
	if sockets > 0 && coresPerSocket > 0 {
		hw.CPUCores = sockets * coresPerSocket
	}
}

func parseDF(lines []string) []Disk {
	var out []Disk
	for i, l := range lines {
		f := strings.Fields(l)
		if len(f) < 5 {
			continue
		}
		if i == 0 && f[0] == "Filesystem" { // header
			continue
		}
		size, _ := strconv.ParseInt(f[2], 10, 64)
		avail, _ := strconv.ParseInt(f[3], 10, 64)
		out = append(out, Disk{
			Name: f[4], Model: f[0], Filesystem: f[1],
			TotalBytes: size, FreeBytes: avail, SizeBytes: size,
		})
	}
	return out
}

func parseNics(linkLines, addrLines, routeLines, resolvLines []string) []Nic {
	macByIf := map[string]string{}
	for _, l := range linkLines {
		// "2: eth0: <...> ... link/ether aa:bb:cc:dd:ee:ff ..."
		name := ifaceName(l)
		if name == "" || name == "lo" {
			continue
		}
		if i := strings.Index(l, "link/ether "); i >= 0 {
			rest := strings.Fields(l[i+len("link/ether "):])
			if len(rest) > 0 {
				macByIf[name] = strings.ToUpper(rest[0])
			}
		}
	}
	ipsByIf := map[string][]string{}
	order := []string{}
	for _, l := range addrLines {
		// "2: eth0    inet 10.0.0.5/24 brd ... scope global eth0"
		f := strings.Fields(l)
		if len(f) < 4 || f[2] != "inet" {
			continue
		}
		name := strings.TrimSuffix(f[1], ":")
		ip := f[3]
		if i := strings.IndexByte(ip, '/'); i >= 0 {
			ip = ip[:i]
		}
		if _, seen := ipsByIf[name]; !seen {
			order = append(order, name)
		}
		ipsByIf[name] = append(ipsByIf[name], ip)
	}
	gateway := ""
	for _, l := range routeLines {
		if strings.HasPrefix(l, "default via ") {
			f := strings.Fields(l)
			if len(f) >= 3 {
				gateway = f[2]
			}
		}
	}
	var dns []string
	for _, l := range resolvLines {
		f := strings.Fields(l)
		if len(f) >= 2 && f[0] == "nameserver" {
			dns = append(dns, f[1])
		}
	}
	var out []Nic
	for _, name := range order {
		out = append(out, Nic{
			Name: name, MAC: macByIf[name],
			IPAddresses: strings.Join(ipsByIf[name], ","),
			Gateway:     gateway, DNSServers: strings.Join(dns, ","),
		})
	}
	return out
}

func ifaceName(linkLine string) string {
	// "2: eth0: <BROADCAST...>" → eth0
	f := strings.SplitN(linkLine, ":", 3)
	if len(f) < 2 {
		return ""
	}
	name := strings.TrimSpace(f[1])
	if i := strings.IndexByte(name, '@'); i >= 0 { // eth0@if2
		name = name[:i]
	}
	return name
}

func parseSystemd(unitLines, unitFileLines []string) []Service {
	enabled := map[string]string{} // unit → enabled|disabled|static|...
	for _, l := range unitFileLines {
		f := strings.Fields(l)
		if len(f) >= 2 && strings.HasSuffix(f[0], ".service") {
			enabled[strings.TrimSuffix(f[0], ".service")] = f[1]
		}
	}
	var out []Service
	for _, l := range unitLines {
		f := strings.Fields(l)
		if len(f) < 4 || !strings.HasSuffix(f[0], ".service") {
			continue
		}
		name := strings.TrimSuffix(f[0], ".service")
		// UNIT LOAD ACTIVE SUB DESCRIPTION...
		out = append(out, Service{
			Name: name, Status: f[3], StartType: enabled[name],
			Description: strings.Join(f[4:], " "),
		})
	}
	return out
}

func parsePS(lines []string) []Process {
	var out []Process
	for i, l := range lines {
		f := strings.Fields(l)
		if len(f) < 4 {
			continue
		}
		if i == 0 && f[0] == "PID" {
			continue
		}
		pid, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		rssKB, _ := strconv.ParseInt(f[2], 10, 64)
		cpu, _ := strconv.ParseFloat(f[3], 64)
		out = append(out, Process{Name: f[1], PID: pid, MemBytes: rssKB * 1024, CPUPct: cpu})
	}
	return out
}

func parsePackages(dpkgLines, rpmLines []string) []Software {
	var out []Software
	add := func(lines []string) {
		for _, l := range lines {
			f := strings.Split(strings.TrimRight(l, "\r"), "\t")
			if len(f) < 2 || strings.TrimSpace(f[0]) == "" {
				continue
			}
			sw := Software{Name: f[0], Version: f[1]}
			if len(f) >= 3 {
				sw.Arch = f[2]
			}
			out = append(out, sw)
		}
	}
	add(dpkgLines)
	add(rpmLines)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// linuxRoleSignals maps a service-name substring / package to a role.
var linuxServiceRoles = map[string]string{
	"nginx": "Web Server", "apache2": "Web Server", "httpd": "Web Server",
	"mysql": "Database Server", "mariadb": "Database Server", "postgresql": "Database Server",
	"named": "DNS Server", "bind9": "DNS Server",
	"isc-dhcp-server": "DHCP Server", "dhcpd": "DHCP Server", "kea-dhcp4": "DHCP Server",
	"smbd": "File Server", "nfs-server": "File Server",
	"docker": "Docker Host", "containerd": "Docker Host",
	"kubelet": "Kubernetes Node",
	"libvirtd": "Hypervisor", "qemu-kvm": "Hypervisor",
	"prometheus": "Monitoring Server", "zabbix-server": "Monitoring Server", "grafana-server": "Monitoring Server",
}

// DetectLinuxRoles infers roles from active services (real evidence, not ports).
func DetectLinuxRoles(rep Report) []string {
	seen := map[string]bool{}
	var roles []string
	for _, s := range rep.Services {
		// only count services that are actually running/active
		if s.Status != "" && s.Status != "running" && s.Status != "active" && s.Status != "exited" {
			continue
		}
		for sig, role := range linuxServiceRoles {
			if strings.Contains(strings.ToLower(s.Name), sig) && !seen[role] {
				seen[role] = true
				roles = append(roles, role)
			}
		}
	}
	sort.Strings(roles)
	return roles
}

// --- small helpers ---

func firstLine(lines []string) string {
	for _, l := range lines {
		if t := strings.TrimSpace(l); t != "" {
			return t
		}
	}
	return ""
}

func nonEmptyLines(lines []string) []string {
	var out []string
	for _, l := range lines {
		if t := strings.TrimSpace(l); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
