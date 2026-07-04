// Package nas collects deep inventory from NAS appliances over SNMP. The first
// supported platform is QNAP QTS (proven live against a TS-853A / QTS 5.2): the
// per-disk bay table comes from the QTS-5 enterprise MIB, live system health from
// the legacy QNAP NAS-MIB, and logical volumes + CPU/uptime from HOST-RESOURCES-MIB.
// Everything here is real device data — no synthesized values; unread fields stay nil.
package nas

import (
	"context"
	"strconv"
	"strings"

	"github.com/coralsearesorts/hims/internal/driver/swsnmp"
	"github.com/coralsearesorts/hims/internal/snmp"
)

// QNAP OID roots. Two MIBs are in play because QTS-5 moved the disk table under a
// new enterprise PEN (55062) while keeping the human-readable system-health scalars
// under the legacy NAS-MIB (24681).
const (
	// QTS-5 enterprise disk table: <root>.2.1.<col>.<slot>
	qnapDiskCount     = "1.3.6.1.4.1.55062.1.10.1.0"
	qnapDiskEntry     = "1.3.6.1.4.1.55062.1.10.2.1"
	qnapDiskVendor    = 3
	qnapDiskModel     = 4
	qnapDiskSerial    = 5
	qnapDiskInterface = 6
	qnapDiskHealth    = 7
	qnapDiskTemp      = 8
	qnapDiskCapacity  = 9

	// Legacy NAS-MIB system-health scalars (respond to GET; a full walk can time out).
	qnapSysCPUUsage = "1.3.6.1.4.1.24681.1.2.1.0"  // "22.4 %"
	qnapSysMemTotal = "1.3.6.1.4.1.24681.1.2.2.0"  // "3839.8 MB"
	qnapSysMemFree  = "1.3.6.1.4.1.24681.1.2.3.0"  // "1990.8 MB"
	qnapSysCPUTemp  = "1.3.6.1.4.1.24681.1.2.5.0"  // "43 C/109 F"
	qnapSysTemp     = "1.3.6.1.4.1.24681.1.2.6.0"  // "37 C/99 F"
	qnapSysModel    = "1.3.6.1.4.1.24681.1.2.12.0" // "TS-853A"
	qnapSysHostname = "1.3.6.1.4.1.24681.1.2.13.0" // "CHV-QNAP."

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

// Volume is one logical data volume / pool mount.
type Volume struct {
	Index      int    `json:"index"`
	Name       string `json:"name"`
	FSType     string `json:"fs_type"`
	TotalBytes int64  `json:"total_bytes"`
	UsedBytes  int64  `json:"used_bytes"`
}

// Report is the full NAS inventory snapshot for one device.
type Report struct {
	Vendor        string   `json:"vendor"`
	Model         string   `json:"model"`
	Firmware      string   `json:"firmware"`
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
}

// CollectQNAP reads a QNAP NAS's inventory over an already-connected SNMP client.
// The client must have authenticated already (bound community); this never sprays.
func CollectQNAP(ctx context.Context, c snmp.Client) Report {
	var r Report
	r.Vendor = "QNAP"

	// --- identity (legacy scalars + sysDescr fallback) --------------------------
	sys := getMap(ctx, c, oidSysDescr, oidSysName, qnapSysModel, qnapSysHostname,
		qnapSysCPUUsage, qnapSysMemTotal, qnapSysMemFree, qnapSysCPUTemp, qnapSysTemp)
	sysDescr := sys[oidSysDescr]
	r.Model = firstNonEmpty(sys[qnapSysModel], modelFromDescr(sysDescr))
	r.Firmware = firmwareFromDescr(sysDescr)
	r.Hostname = strings.TrimRight(firstNonEmpty(sys[qnapSysHostname], sys[oidSysName]), ".")

	if v, ok := parsePct(sys[qnapSysCPUUsage]); ok {
		r.CPUPct = &v
	}
	r.MemTotalBytes = parseMB(sys[qnapSysMemTotal])
	if free := parseMB(sys[qnapSysMemFree]); r.MemTotalBytes > 0 && free > 0 && free <= r.MemTotalBytes {
		r.MemUsedBytes = r.MemTotalBytes - free
	}
	if v, ok := parseTempC(sys[qnapSysCPUTemp]); ok {
		r.CPUTempC = &v
	}
	if v, ok := parseTempC(sys[qnapSysTemp]); ok {
		r.SysTempC = &v
	}

	// --- CPU load / uptime / logical volumes (HOST-RESOURCES) -------------------
	hr := swsnmp.CollectHostResources(ctx, c)
	r.UptimeSeconds = hr.UptimeCS / 100
	if r.CPUPct == nil && hr.CPULoadPct > 0 {
		v := float64(hr.CPULoadPct)
		r.CPUPct = &v
	}
	for _, st := range hr.Storage {
		if st.Type != "disk" || !isDataVolume(st.Descr) {
			continue
		}
		r.Volumes = append(r.Volumes, Volume{
			Index: int(st.Index), Name: st.Descr, FSType: st.Type,
			TotalBytes: st.TotalBytes, UsedBytes: st.UsedBytes,
		})
	}

	// --- physical disks (QTS-5 enterprise MIB) ----------------------------------
	r.Disks = collectDisks(ctx, c)
	r.Health = rollupHealth(r.Disks)
	return r
}

// collectDisks walks the QTS-5 enterprise disk table into per-slot rows.
func collectDisks(ctx context.Context, c snmp.Client) []Disk {
	byslot := map[int]*Disk{}
	get := func(slot int) *Disk {
		d := byslot[slot]
		if d == nil {
			d = &Disk{Slot: slot}
			byslot[slot] = d
		}
		return d
	}
	_ = c.BulkWalk(ctx, qnapDiskEntry, func(p snmp.PDU) error {
		col, idx, ok := snmp.ColumnAndIndex(p.OID, qnapDiskEntry)
		if !ok || len(idx) != 1 {
			return nil
		}
		d := get(int(idx[0]))
		switch int(col) {
		case qnapDiskVendor:
			d.Vendor = strings.TrimSpace(snmp.PDUString(p))
		case qnapDiskModel:
			d.Model = strings.TrimSpace(snmp.PDUString(p))
		case qnapDiskSerial:
			d.Serial = strings.TrimSpace(snmp.PDUString(p))
		case qnapDiskInterface:
			d.InterfaceType = strings.TrimSpace(snmp.PDUString(p))
		case qnapDiskHealth:
			d.Health = strings.TrimSpace(snmp.PDUString(p))
		case qnapDiskTemp:
			if v, ok := parseTempC(snmp.PDUString(p)); ok {
				d.TempC = &v
			}
		case qnapDiskCapacity:
			// QNAP reports capacity as a DisplayString (e.g. "4000787030016"), so
			// prefer the integer decode but fall back to parsing the string form.
			if v, ok := snmp.PDUInt64(p); ok && v > 0 {
				d.CapacityBytes = v
			} else if n, err := strconv.ParseInt(strings.TrimSpace(snmp.PDUString(p)), 10, 64); err == nil {
				d.CapacityBytes = n
			}
		}
		return nil
	})
	out := make([]Disk, 0, len(byslot))
	for _, d := range byslot {
		// A populated bay reports at least a model or vendor; skip empty index-only rows.
		if d.Model == "" && d.Vendor == "" && d.Serial == "" {
			continue
		}
		out = append(out, *d)
	}
	sortDisks(out)
	return out
}

// rollupHealth summarizes per-disk health into a single NAS health state.
func rollupHealth(disks []Disk) string {
	if len(disks) == 0 {
		return "Unknown"
	}
	worst := "OK"
	for _, d := range disks {
		switch strings.ToLower(d.Health) {
		case "good", "ok", "normal", "":
			// healthy / unreported-but-present
		case "warning", "warn":
			if worst == "OK" {
				worst = "Warning"
			}
		default:
			worst = "Critical"
		}
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

// parseMB parses "3839.8 MB" -> bytes.
func parseMB(s string) int64 {
	f := strings.Fields(s)
	if len(f) == 0 {
		return 0
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return 0
	}
	unit := "MB"
	if len(f) > 1 {
		unit = strings.ToUpper(f[1])
	}
	mult := 1024.0 * 1024.0
	switch unit {
	case "KB":
		mult = 1024
	case "GB":
		mult = 1024 * 1024 * 1024
	case "TB":
		mult = 1024 * 1024 * 1024 * 1024
	}
	return int64(v * mult)
}

// parseTempC parses "43 C/109 F" or "43" -> 43.
func parseTempC(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	// Take the leading integer (handles "43", "43 C", "43 C/109 F").
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
// OS/system mounts (/proc, /sys, /tmp, /mnt/HDA_ROOT, sockets, ...).
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

func sortDisks(d []Disk) {
	for i := 1; i < len(d); i++ {
		for j := i; j > 0 && d[j-1].Slot > d[j].Slot; j-- {
			d[j-1], d[j] = d[j], d[j-1]
		}
	}
}
