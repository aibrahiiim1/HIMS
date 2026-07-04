// Package nas collects deep inventory from NAS appliances over SNMP. The first
// supported platform is QNAP QTS (proven live against a TS-853A / QTS 5.2): physical
// disks + fans + system info from the QTS-5 enterprise MIB (55062), and RAID volumes,
// storage pools, iSCSI LUNs/targets, network stats, and the appliance serial from the
// QNAP NAS-MIB (24681). Everything here is real device data — no synthesized values;
// unread fields stay nil/zero.
package nas

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/coralsearesorts/hims/internal/driver/swsnmp"
	"github.com/coralsearesorts/hims/internal/snmp"
)

// QNAP OID roots. Two MIBs are in play because QTS-5 moved the disk table under a
// new enterprise PEN (55062) while keeping most storage tables under the legacy
// NAS-MIB (24681).
const (
	// QTS-5 enterprise disk table: <root>.2.1.<col>.<slot>
	qnapDiskEntry     = "1.3.6.1.4.1.55062.1.10.2.1"
	qnapDiskVendor    = 3
	qnapDiskModel     = 4
	qnapDiskSerial    = 5
	qnapDiskInterface = 6
	qnapDiskHealth    = 7
	qnapDiskTemp      = 8
	qnapDiskCapacity  = 9

	// QTS-5 fan table: <root>.<col>.<idx>  (col 2 = name, col 3 = rpm)
	qnapFanEntry = "1.3.6.1.4.1.55062.1.12.9.1"
	qnapFanName  = 2
	qnapFanRPM   = 3

	// Legacy NAS-MIB system-health scalars (respond to GET; a full walk can time out).
	qnapSysCPUUsage = "1.3.6.1.4.1.24681.1.2.1.0"               // "22.4 %"
	qnapSysMemTotal = "1.3.6.1.4.1.24681.1.2.2.0"               // "3839.8 MB"
	qnapSysMemFree  = "1.3.6.1.4.1.24681.1.2.3.0"               // "1990.8 MB"
	qnapSysCPUTemp  = "1.3.6.1.4.1.24681.1.2.5.0"               // "43 C/109 F"
	qnapSysTemp     = "1.3.6.1.4.1.24681.1.2.6.0"               // "37 C/99 F"
	qnapSysModel    = "1.3.6.1.4.1.24681.1.2.12.0"              // "TS-853A"
	qnapSysHostname = "1.3.6.1.4.1.24681.1.2.13.0"              // "CHV-QNAP."
	qnapSerial      = "1.3.6.1.4.1.24681.1.4.1.1.1.1.1.2.1.4.1" // enclosure serial "Q16BI08544"

	// Legacy NAS-MIB RAID volume table: <root>.<col>.<idx>
	qnapVolEntry  = "1.3.6.1.4.1.24681.1.2.17.1"
	qnapVolDescr  = 2 // "[Volume DataVol1, Pool 1]"
	qnapVolFS     = 3 // "EXT4"
	qnapVolTotal  = 4 // "1014.41 GB"
	qnapVolFree   = 5 // "566.00 GB"
	qnapVolStatus = 6 // "Ready"

	// NAS-MIB storage-pool table: <root>.<col>.<idx>
	qnapPoolEntry  = "1.3.6.1.4.1.24681.1.4.1.1.1.2.1.2.1"
	qnapPoolRaw    = 3 // bytes
	qnapPoolStatus = 5 // "Ready"
	qnapPoolRAID   = 7 // "5" -> RAID 5

	// NAS-MIB iSCSI LUN table: <root>.<col>.<idx>
	qnapLunEntry    = "1.3.6.1.4.1.24681.1.4.1.1.2.1.10.2.1"
	qnapLunCapacity = 3 // bytes
	qnapLunStatus   = 5 // "Enabled"
	qnapLunName     = 6 // "VeeamRepo"

	// NAS-MIB iSCSI target table: <root>.<col>.<idx>
	qnapTgtEntry  = "1.3.6.1.4.1.24681.1.4.1.1.2.1.11.2.1"
	qnapTgtName   = 3 // "veeamrepo"
	qnapTgtIQN    = 4 // "iqn.2004-04.com.qnap:..."
	qnapTgtStatus = 5

	oidSysDescr = "1.3.6.1.2.1.1.1.0"
	oidSysName  = "1.3.6.1.2.1.1.5.0"
)

// Disk is one physical disk bay.
type Disk struct {
	Slot          int    `json:"slot"`
	Vendor        string `json:"vendor"`
	Model         string `json:"model"`
	Serial        string `json:"serial"`
	InterfaceType string `json:"interface_type"`
	CapacityBytes int64  `json:"capacity_bytes"`
	TempC         *int   `json:"temp_c"`
	Health        string `json:"health"`
}

// Volume is one logical RAID volume (or, as a fallback, a filesystem mount).
type Volume struct {
	Index      int    `json:"index"`
	Name       string `json:"name"`
	Pool       string `json:"pool"`
	FSType     string `json:"fs_type"`
	TotalBytes int64  `json:"total_bytes"`
	UsedBytes  int64  `json:"used_bytes"`
	Status     string `json:"status"`
}

// Pool is one RAID storage pool.
type Pool struct {
	Index    int    `json:"index"`
	Name     string `json:"name"`
	RaidType string `json:"raid_type"`
	RawBytes int64  `json:"raw_bytes"`
	Status   string `json:"status"`
}

// LUN is one iSCSI LUN.
type LUN struct {
	Index         int    `json:"index"`
	Name          string `json:"name"`
	CapacityBytes int64  `json:"capacity_bytes"`
	Status        string `json:"status"`
}

// Target is one iSCSI target.
type Target struct {
	Index  int    `json:"index"`
	Name   string `json:"name"`
	IQN    string `json:"iqn"`
	Status string `json:"status"`
}

// Fan is one cooling fan reading.
type Fan struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
	RPM   int    `json:"rpm"`
}

// Report is the full NAS inventory snapshot for one device.
type Report struct {
	Vendor        string   `json:"vendor"`
	Model         string   `json:"model"`
	Firmware      string   `json:"firmware"`
	Serial        string   `json:"serial"`
	Hostname      string   `json:"hostname"`
	CPUPct        *float64 `json:"cpu_pct"`
	MemTotalBytes int64    `json:"mem_total_bytes"`
	MemUsedBytes  int64    `json:"mem_used_bytes"`
	CPUTempC      *int     `json:"cpu_temp_c"`
	SysTempC      *int     `json:"sys_temp_c"`
	UptimeSeconds int64    `json:"uptime_seconds"`
	Health        string   `json:"health"`
	Disks         []Disk   `json:"disks"`
	Volumes       []Volume `json:"volumes"`
	Pools         []Pool   `json:"pools"`
	LUNs          []LUN    `json:"luns"`
	Targets       []Target `json:"targets"`
	Fans          []Fan    `json:"fans"`
}

// CollectQNAP reads a QNAP NAS's inventory over an already-connected SNMP client.
// The client must have authenticated already (bound community); this never sprays.
func CollectQNAP(ctx context.Context, c snmp.Client) Report {
	var r Report
	r.Vendor = "QNAP"

	// --- identity (legacy scalars + sysDescr fallback + enclosure serial) --------
	sys := getMap(ctx, c, oidSysDescr, oidSysName, qnapSysModel, qnapSysHostname,
		qnapSysCPUUsage, qnapSysMemTotal, qnapSysMemFree, qnapSysCPUTemp, qnapSysTemp, qnapSerial)
	sysDescr := sys[oidSysDescr]
	r.Model = firstNonEmpty(sys[qnapSysModel], modelFromDescr(sysDescr))
	r.Firmware = firmwareFromDescr(sysDescr)
	r.Hostname = strings.TrimRight(firstNonEmpty(sys[qnapSysHostname], sys[oidSysName]), ".")
	if s := strings.TrimSpace(sys[qnapSerial]); s != "" && s != "--" {
		r.Serial = s
	}

	if v, ok := parsePct(sys[qnapSysCPUUsage]); ok {
		r.CPUPct = &v
	}
	r.MemTotalBytes = parseSize(sys[qnapSysMemTotal])
	if free := parseSize(sys[qnapSysMemFree]); r.MemTotalBytes > 0 && free > 0 && free <= r.MemTotalBytes {
		r.MemUsedBytes = r.MemTotalBytes - free
	}
	if v, ok := parseTempC(sys[qnapSysCPUTemp]); ok {
		r.CPUTempC = &v
	}
	if v, ok := parseTempC(sys[qnapSysTemp]); ok {
		r.SysTempC = &v
	}

	// --- CPU load / uptime (HOST-RESOURCES) -------------------------------------
	hr := swsnmp.CollectHostResources(ctx, c)
	r.UptimeSeconds = hr.UptimeCS / 100
	if r.CPUPct == nil && hr.CPULoadPct > 0 {
		v := float64(hr.CPULoadPct)
		r.CPUPct = &v
	}

	// --- logical volumes: prefer the QNAP RAID volume table (name/pool/FS/status),
	//     fall back to HOST-RESOURCES data mounts when the table is absent. --------
	r.Volumes = collectVolumes(ctx, c)
	if len(r.Volumes) == 0 {
		for _, st := range hr.Storage {
			if st.Type != "disk" || !isDataVolume(st.Descr) {
				continue
			}
			r.Volumes = append(r.Volumes, Volume{
				Index: int(st.Index), Name: st.Descr, FSType: st.Type,
				TotalBytes: st.TotalBytes, UsedBytes: st.UsedBytes,
			})
		}
	}

	r.Disks = collectDisks(ctx, c)
	r.Pools = collectPools(ctx, c)
	r.LUNs, r.Targets = collectISCSI(ctx, c)
	r.Fans = collectFans(ctx, c)
	r.Health = rollupHealth(r.Disks, r.Pools, r.Volumes)
	return r
}

// walkTable walks a QNAP columnar table into rows[index][column] = value. It handles
// the deeply-nested NAS-MIB entry roots where each leaf OID is "<entry>.<col>.<idx>".
func walkTable(ctx context.Context, c snmp.Client, entry string) map[string]map[int]string {
	rows := map[string]map[int]string{}
	_ = c.BulkWalk(ctx, entry, func(p snmp.PDU) error {
		rest := strings.TrimPrefix(strings.TrimPrefix(p.OID, "."), strings.TrimPrefix(entry, ".")+".")
		parts := strings.Split(rest, ".")
		if len(parts) < 2 {
			return nil
		}
		col, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil
		}
		idx := strings.Join(parts[1:], ".")
		if rows[idx] == nil {
			rows[idx] = map[int]string{}
		}
		rows[idx][col] = strings.TrimSpace(snmp.PDUString(p))
		return nil
	})
	return rows
}

// collectDisks walks the QTS-5 enterprise disk table into per-slot rows.
func collectDisks(ctx context.Context, c snmp.Client) []Disk {
	out := []Disk{}
	for idxStr, cols := range walkTable(ctx, c, qnapDiskEntry) {
		if cols[qnapDiskModel] == "" && cols[qnapDiskVendor] == "" && cols[qnapDiskSerial] == "" {
			continue
		}
		d := Disk{
			Slot: atoiOr(idxStr, 0), Vendor: cols[qnapDiskVendor], Model: cols[qnapDiskModel],
			Serial: cols[qnapDiskSerial], InterfaceType: cols[qnapDiskInterface], Health: cols[qnapDiskHealth],
		}
		d.CapacityBytes = parseInt64(cols[qnapDiskCapacity])
		if v, ok := parseTempC(cols[qnapDiskTemp]); ok {
			d.TempC = &v
		}
		out = append(out, d)
	}
	sortBySlot(out)
	return out
}

// collectVolumes walks the QNAP RAID volume table.
func collectVolumes(ctx context.Context, c snmp.Client) []Volume {
	out := []Volume{}
	for idxStr, cols := range walkTable(ctx, c, qnapVolEntry) {
		descr := cols[qnapVolDescr]
		if descr == "" {
			continue
		}
		name, pool := splitVolumeDescr(descr)
		total := parseSize(cols[qnapVolTotal])
		free := parseSize(cols[qnapVolFree])
		used := int64(0)
		if total > 0 && free >= 0 && free <= total {
			used = total - free
		}
		out = append(out, Volume{
			Index: atoiOr(idxStr, 0), Name: name, Pool: pool, FSType: cols[qnapVolFS],
			TotalBytes: total, UsedBytes: used, Status: cols[qnapVolStatus],
		})
	}
	sortByIndex2(out)
	return out
}

// collectPools walks the QNAP RAID storage-pool table.
func collectPools(ctx context.Context, c snmp.Client) []Pool {
	out := []Pool{}
	for idxStr, cols := range walkTable(ctx, c, qnapPoolEntry) {
		raw := parseInt64(cols[qnapPoolRaw])
		status := cols[qnapPoolStatus]
		raid := raidLabel(cols[qnapPoolRAID])
		if raw == 0 && status == "" && raid == "" {
			continue
		}
		out = append(out, Pool{
			Index: atoiOr(idxStr, 0), Name: "Pool " + idxStr, RaidType: raid, RawBytes: raw, Status: status,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out
}

// collectISCSI walks the QNAP iSCSI LUN and target tables.
func collectISCSI(ctx context.Context, c snmp.Client) ([]LUN, []Target) {
	luns := []LUN{}
	for idxStr, cols := range walkTable(ctx, c, qnapLunEntry) {
		name := cols[qnapLunName]
		if name == "" {
			continue
		}
		luns = append(luns, LUN{
			Index: atoiOr(idxStr, 0), Name: name,
			CapacityBytes: parseInt64(cols[qnapLunCapacity]), Status: cols[qnapLunStatus],
		})
	}
	targets := []Target{}
	for idxStr, cols := range walkTable(ctx, c, qnapTgtEntry) {
		iqn := cols[qnapTgtIQN]
		name := cols[qnapTgtName]
		if iqn == "" && name == "" {
			continue
		}
		targets = append(targets, Target{
			Index: atoiOr(idxStr, 0), Name: name, IQN: iqn, Status: iscsiStatus(cols[qnapTgtStatus]),
		})
	}
	sort.Slice(luns, func(i, j int) bool { return luns[i].Index < luns[j].Index })
	sort.Slice(targets, func(i, j int) bool { return targets[i].Index < targets[j].Index })
	return luns, targets
}

// collectFans walks the QTS-5 fan table.
func collectFans(ctx context.Context, c snmp.Client) []Fan {
	out := []Fan{}
	for idxStr, cols := range walkTable(ctx, c, qnapFanEntry) {
		name := cols[qnapFanName]
		if name == "" {
			continue
		}
		out = append(out, Fan{Index: atoiOr(idxStr, 0), Name: name, RPM: int(parseInt64(cols[qnapFanRPM]))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out
}

// rollupHealth summarizes disk + pool + volume health into a single NAS health state.
func rollupHealth(disks []Disk, pools []Pool, vols []Volume) string {
	worst := "OK"
	seen := false
	bump := func(s string) {
		seen = true
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "good", "ok", "normal", "ready", "enabled", "":
		case "warning", "warn", "rebuilding", "resyncing", "degraded":
			if worst == "OK" {
				worst = "Warning"
			}
		default:
			worst = "Critical"
		}
	}
	for _, d := range disks {
		bump(d.Health)
	}
	for _, p := range pools {
		bump(p.Status)
	}
	for _, v := range vols {
		bump(v.Status)
	}
	if !seen {
		return "Unknown"
	}
	return worst
}

// --- parsing helpers --------------------------------------------------------

// getMap does one SNMP GET for all oids and returns oid->string (missing => absent).
func getMap(ctx context.Context, c snmp.Client, oids ...string) map[string]string {
	out := map[string]string{}
	pdus, err := c.Get(ctx, oids...)
	if err != nil {
		return out
	}
	for _, p := range pdus {
		out[strings.TrimPrefix(p.OID, ".")] = strings.TrimSpace(snmp.PDUString(p))
	}
	return out
}

// parsePct parses "22.4 %" -> 22.4.
func parsePct(s string) (float64, bool) {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "%"))
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// parseSize parses "3839.8 MB" / "1014.41 GB" / "27934135943168" -> bytes.
func parseSize(s string) int64 {
	f := strings.Fields(s)
	if len(f) == 0 {
		return 0
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return 0
	}
	mult := 1.0
	if len(f) > 1 {
		switch strings.ToUpper(f[1]) {
		case "KB":
			mult = 1024
		case "MB":
			mult = 1024 * 1024
		case "GB":
			mult = 1024 * 1024 * 1024
		case "TB":
			mult = 1024 * 1024 * 1024 * 1024
		case "PB":
			mult = 1024 * 1024 * 1024 * 1024 * 1024
		}
	}
	return int64(v * mult)
}

// parseInt64 parses a plain integer string (QNAP reports capacities as DisplayStrings).
func parseInt64(s string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func atoiOr(s string, d int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}
	return d
}

// parseTempC parses "43 C/109 F" or "43" -> 43.
func parseTempC(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	i := 0
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		i = 1
	}
	j := i
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	if j == i {
		return 0, false
	}
	v, err := strconv.Atoi(s[i:j])
	if err != nil {
		return 0, false
	}
	if neg {
		v = -v
	}
	return v, true
}

// raidLabel turns a numeric RAID level ("5") into "RAID 5"; passes through text.
func raidLabel(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || s == "-1" {
		return ""
	}
	if _, err := strconv.Atoi(s); err == nil {
		return "RAID " + s
	}
	if strings.Contains(strings.ToLower(s), "raid") {
		return s
	}
	return "RAID " + s
}

// iscsiStatus maps the numeric target status to a label (1 => Ready).
func iscsiStatus(s string) string {
	switch strings.TrimSpace(s) {
	case "1":
		return "Ready"
	case "0":
		return "Offline"
	case "":
		return ""
	}
	return s
}

// splitVolumeDescr parses "[Volume DataVol1, Pool 1]" -> ("DataVol1", "Pool 1").
func splitVolumeDescr(d string) (name, pool string) {
	d = strings.TrimSpace(d)
	d = strings.TrimPrefix(d, "[")
	d = strings.TrimSuffix(d, "]")
	d = strings.TrimSpace(strings.TrimPrefix(d, "Volume "))
	if i := strings.Index(d, ","); i >= 0 {
		name = strings.TrimSpace(d[:i])
		pool = strings.TrimSpace(d[i+1:])
		return name, pool
	}
	return d, ""
}

// modelFromDescr pulls the model token from a QNAP sysDescr "Linux TS-X53II 5.2.4.3079".
func modelFromDescr(descr string) string {
	f := strings.Fields(descr)
	if len(f) >= 2 && strings.EqualFold(f[0], "Linux") {
		return f[1]
	}
	return ""
}

// firmwareFromDescr pulls the QTS firmware/version token (last field if it looks like a version).
func firmwareFromDescr(descr string) string {
	f := strings.Fields(descr)
	if len(f) == 0 {
		return ""
	}
	last := f[len(f)-1]
	if strings.ContainsAny(last, "0123456789") && strings.Contains(last, ".") {
		return last
	}
	return ""
}

// isDataVolume keeps operator-facing data volumes (QNAP data shares / pools) and drops
// OS/system mounts (used only for the HOST-RESOURCES fallback path).
func isDataVolume(descr string) bool {
	d := strings.ToLower(descr)
	if strings.Contains(d, ".sock") || strings.Contains(d, ".lock") {
		return false
	}
	return strings.HasPrefix(d, "/share/cachedev") ||
		strings.HasPrefix(d, "/share/zfs") ||
		strings.HasPrefix(d, "/mnt/pool")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func sortBySlot(d []Disk) {
	for i := 1; i < len(d); i++ {
		for j := i; j > 0 && d[j-1].Slot > d[j].Slot; j-- {
			d[j-1], d[j] = d[j], d[j-1]
		}
	}
}

func sortByIndex2(v []Volume) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j-1].Index > v[j].Index; j-- {
			v[j-1], v[j] = v[j], v[j-1]
		}
	}
}
