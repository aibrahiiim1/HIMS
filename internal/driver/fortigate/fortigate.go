// Package fortigate is the HIMS driver for Fortinet FortiGate firewalls.
// It is a vendor-specific driver (FORTINET-FORTIGATE-MIB), carrying the
// OID lessons validated against a real FortiGate MIB during NIMS:
//   - fgSysDiskUsage/Capacity are MEGABYTES, not percent → derive bytes+pct
//   - VPN tunnel table has a COMPOSITE {tunnel, phase2} index
//   - in/out octets are Counter64
//   - fgHaGroupName is fgHaInfo 7 (not 3 = fgHaPriority)
//   - HA member count = number of fgHaStatsTable rows (serial may be empty
//     on a standalone unit, which still counts as one member)
package fortigate

import (
	"context"
	"fmt"
	"net/netip"
	"regexp"
	"strings"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
	"github.com/coralsearesorts/hims/internal/driver/swsnmp"
	"github.com/coralsearesorts/hims/internal/mibs"
	"github.com/coralsearesorts/hims/internal/snmp"
)

// Driver identifies and collects FortiGate firewalls.
type Driver struct{}

// New returns the driver.
func New() *Driver { return &Driver{} }

// Name implements driver.Driver.
func (*Driver) Name() string { return "fortigate" }

// Template implements driver.Driver.
func (*Driver) Template() string { return "firewall" }

// Fingerprint implements driver.Driver: authoritative when sysObjectID is
// under the Fortinet PEN (12356), with a sysDescr "fortigate"/"fortios"
// fallback.
func (*Driver) Fingerprint(p driver.Probe) driver.Match {
	oid := strings.TrimPrefix(strings.TrimSpace(p.SNMPSysObjectID), ".")
	if oid == strings.TrimPrefix(mibs.FortinetEnterprise, ".") ||
		strings.HasPrefix(oid, strings.TrimPrefix(mibs.FortinetEnterprise, ".")+".") {
		return driver.Match{Confidence: 90, Category: domain.CatFirewall}
	}
	if p.HasTCPPort(161) || len(p.OpenUDPPorts) > 0 {
		d := strings.ToLower(p.SNMPSysDescr)
		if strings.Contains(d, "fortigate") || strings.Contains(d, "fortios") {
			return driver.Match{Confidence: 70, Category: domain.CatFirewall}
		}
	}
	return driver.NoMatch
}

// Session aliases the shared SNMP session (swsnmp.Session).
type Session = swsnmp.Session

const mib = 1024 * 1024 // FortiGate disk units are binary megabytes

var reVer = regexp.MustCompile(`v?(\d+\.\d+\.\d+)`)

// Collect implements driver.Collector.
func (d *Driver) Collect(sess driver.Session, _ driver.Probe) (driver.Facts, error) {
	fs, ok := sess.(*Session)
	if !ok {
		return driver.Facts{}, fmt.Errorf("fortigate: expected *Session, got %T", sess)
	}
	c, ctx := fs.Client, fs.Ctx

	f := driver.Facts{KV: map[string]string{}, Raw: map[string]any{}}
	f.Vendor = "Fortinet"

	// --- Scalars: firmware, CPU, memory, disk (MB!), sessions, HA mode/group.
	pdus, err := c.Get(ctx,
		mibs.FgSysVersion, mibs.FgSysCpuUsage, mibs.FgSysMemUsage,
		mibs.FgSysDiskUsage, mibs.FgSysDiskCap, mibs.FgSysSesCount,
		mibs.FgHaSystemMode, mibs.FgHaGroupName,
	)
	if err != nil {
		return f, err
	}
	byOID := map[string]snmp.PDU{}
	for _, p := range pdus {
		byOID[p.OID] = p
	}

	if v := reVer.FindStringSubmatch(snmp.PDUString(byOID[mibs.FgSysVersion])); v != nil {
		f.OSVersion = v[1]
	}
	if v, ok := snmp.PDUInt64(byOID[mibs.FgSysCpuUsage]); ok {
		f.KV["cpu.load_pct"] = fmt.Sprintf("%d", v)
	}
	if v, ok := snmp.PDUInt64(byOID[mibs.FgSysMemUsage]); ok {
		f.KV["memory.used_pct"] = fmt.Sprintf("%d", v)
	}
	// Disk: fgSysDiskUsage/Capacity are MEGABYTES. Emit bytes + derived pct.
	usedMB, usedOK := snmp.PDUInt64(byOID[mibs.FgSysDiskUsage])
	capMB, capOK := snmp.PDUInt64(byOID[mibs.FgSysDiskCap])
	if usedOK {
		f.KV["disk.used_bytes"] = fmt.Sprintf("%d", usedMB*mib)
	}
	if capOK {
		f.KV["disk.size_bytes"] = fmt.Sprintf("%d", capMB*mib)
	}
	if usedOK && capOK && capMB > 0 {
		f.KV["disk.used_pct"] = fmt.Sprintf("%d", usedMB*100/capMB)
	}
	var sessions *int64
	if v, ok := snmp.PDUInt64(byOID[mibs.FgSysSesCount]); ok {
		f.KV["firewall.session_count"] = fmt.Sprintf("%d", v)
		sv := v
		sessions = &sv
	}

	// --- HA members (fgHaStatsTable). Count = rows; detail rows need serial.
	members, memberRows := walkHAMembers(ctx, c)
	f.HAMembers = members

	status := &driver.FirewallStatusSnap{
		HAMode:        haMode(byOID[mibs.FgHaSystemMode]),
		HAGroupName:   strings.TrimSpace(snmp.PDUString(byOID[mibs.FgHaGroupName])),
		HAMemberCount: int32(memberRows),
		SessionCount:  sessions,
	}
	f.FirewallStatus = status

	// --- VPN tunnels (composite index) + licenses.
	f.VpnTunnels = walkVPN(ctx, c)
	f.Licenses = walkLicenses(ctx, c)

	// Interfaces via the shared collector (FortiGate exposes IF-MIB).
	f.Interfaces = swsnmp.CollectInterfaces(ctx, c)
	// ARP (ipNetToMedia): the firewall is the default gateway, so its ARP cache is
	// the richest IP↔MAC source in the fabric — the Path Finder's best resolver.
	f.ARP = swsnmp.CollectARP(ctx, c)
	return f, nil
}

func haMode(p snmp.PDU) string {
	v, ok := snmp.PDUInt64(p)
	if !ok {
		return "unknown"
	}
	switch int(v) {
	case mibs.FgHaModeStandalone:
		return "standalone"
	case mibs.FgHaModeActiveActive:
		return "active-active"
	case mibs.FgHaModeActivePassive:
		return "active-passive"
	}
	return "unknown"
}

// walkHAMembers returns member detail rows (those with a serial) and the
// total row count (the headline member count, independent of serial).
func walkHAMembers(ctx context.Context, c snmp.Client) ([]driver.HAMemberSnap, int) {
	type acc struct {
		serial, hostname, sync string
		cpu, mem               *int32
		sess                   *int64
	}
	rows := map[uint32]*acc{}
	get := func(i uint32) *acc {
		a := rows[i]
		if a == nil {
			a = &acc{sync: "unknown"}
			rows[i] = a
		}
		return a
	}
	_ = c.BulkWalk(ctx, mibs.FgHaStatsEntry, func(p snmp.PDU) error {
		col, idx, ok := snmp.ColumnAndIndex(p.OID, mibs.FgHaStatsEntry)
		if !ok || len(idx) != 1 {
			return nil
		}
		a := get(idx[0])
		switch int(col) {
		case mibs.FgHaStatsColSerial:
			a.serial = strings.TrimSpace(snmp.PDUString(p))
		case mibs.FgHaStatsColHostname:
			a.hostname = strings.TrimSpace(snmp.PDUString(p))
		case mibs.FgHaStatsColCpu:
			if v, ok := snmp.PDUInt64(p); ok {
				vi := int32(v)
				a.cpu = &vi
			}
		case mibs.FgHaStatsColMem:
			if v, ok := snmp.PDUInt64(p); ok {
				vi := int32(v)
				a.mem = &vi
			}
		case mibs.FgHaStatsColSesCount:
			if v, ok := anyInt64(p); ok {
				a.sess = &v
			}
		case mibs.FgHaStatsColSyncStat:
			if v, ok := snmp.PDUInt64(p); ok {
				a.sync = syncStatus(int(v))
			}
		}
		return nil
	})
	out := make([]driver.HAMemberSnap, 0, len(rows))
	for _, a := range rows {
		if a.serial == "" {
			continue // no DB key; still counted via memberRows
		}
		out = append(out, driver.HAMemberSnap{
			Serial: a.serial, Hostname: a.hostname,
			CPUPct: a.cpu, MemPct: a.mem, SessionCount: a.sess, SyncStatus: a.sync,
		})
	}
	return out, len(rows)
}

func syncStatus(v int) string {
	switch v {
	case mibs.FgHaSyncSync:
		return "synchronized"
	case mibs.FgHaSyncUnsync:
		return "unsynchronized"
	}
	return "unknown"
}

// walkVPN walks fgVpnTunTable. The index is COMPOSITE {tunnel, phase2}; key
// the accumulator by the whole index so rows aren't discarded.
func walkVPN(ctx context.Context, c snmp.Client) []driver.VpnTunnelSnap {
	type acc struct {
		p1, p2, status string
		gw             *netip.Addr
		in, out        *int64
	}
	rows := map[string]*acc{}
	get := func(idx []uint32) *acc {
		key := joinIdx(idx)
		a := rows[key]
		if a == nil {
			a = &acc{status: "down"}
			rows[key] = a
		}
		return a
	}
	_ = c.BulkWalk(ctx, mibs.FgVpnTunEntry, func(p snmp.PDU) error {
		col, idx, ok := snmp.ColumnAndIndex(p.OID, mibs.FgVpnTunEntry)
		if !ok || len(idx) < 1 {
			return nil
		}
		a := get(idx)
		switch int(col) {
		case mibs.FgVpnTunColP1Name:
			a.p1 = strings.TrimSpace(snmp.PDUString(p))
		case mibs.FgVpnTunColP2Name:
			a.p2 = strings.TrimSpace(snmp.PDUString(p))
		case mibs.FgVpnTunColRemGwy:
			if ip := pduIP(p); ip != nil && ip.IsValid() && !ip.IsUnspecified() {
				a.gw = ip
			}
		case mibs.FgVpnTunColInOct:
			if v, ok := anyInt64(p); ok {
				a.in = &v
			}
		case mibs.FgVpnTunColOutOct:
			if v, ok := anyInt64(p); ok {
				a.out = &v
			}
		case mibs.FgVpnTunColStatus:
			if v, ok := snmp.PDUInt64(p); ok && int(v) == mibs.FgVpnStatusUp {
				a.status = "up"
			}
		}
		return nil
	})
	out := make([]driver.VpnTunnelSnap, 0, len(rows))
	for _, a := range rows {
		if a.p2 == "" {
			continue // phase2 name is the upsert key
		}
		out = append(out, driver.VpnTunnelSnap{
			TunnelName: a.p2, P1Name: a.p1, RemoteGW: a.gw,
			Status: a.status, InOctets: a.in, OutOctets: a.out,
		})
	}
	return out
}

// walkLicenses walks fgLicContractTable (indexed per VDOM).
func walkLicenses(ctx context.Context, c snmp.Client) []driver.LicenseSnap {
	type acc struct{ desc, expiry string }
	rows := map[uint32]*acc{}
	get := func(i uint32) *acc {
		a := rows[i]
		if a == nil {
			a = &acc{}
			rows[i] = a
		}
		return a
	}
	_ = c.BulkWalk(ctx, mibs.FgLicContractEntry, func(p snmp.PDU) error {
		col, idx, ok := snmp.ColumnAndIndex(p.OID, mibs.FgLicContractEntry)
		if !ok || len(idx) != 1 {
			return nil
		}
		a := get(idx[0])
		switch int(col) {
		case mibs.FgLicColDesc:
			a.desc = strings.TrimSpace(snmp.PDUString(p))
		case mibs.FgLicColExpiry:
			a.expiry = strings.TrimSpace(snmp.PDUString(p))
		}
		return nil
	})
	out := make([]driver.LicenseSnap, 0, len(rows))
	for _, a := range rows {
		if a.desc == "" {
			continue
		}
		out = append(out, driver.LicenseSnap{Contract: a.desc, Expiry: a.expiry})
	}
	return out
}

// anyInt64 reads an integer including Counter64 (VPN/HA octets are Counter64).
func anyInt64(p snmp.PDU) (int64, bool) {
	if v, ok := snmp.PDUInt64(p); ok {
		return v, true
	}
	return snmp.PDUCounter64(p)
}

func pduIP(p snmp.PDU) *netip.Addr {
	switch p.Type {
	case snmp.TypeIPAddress:
		switch v := p.Value.(type) {
		case string:
			if a, err := netip.ParseAddr(v); err == nil {
				return &a
			}
		case []byte:
			if a, ok := netip.AddrFromSlice(v); ok {
				return &a
			}
		}
	case snmp.TypeOctetString:
		if b, ok := p.Value.([]byte); ok {
			if a, ok := netip.AddrFromSlice(b); ok {
				return &a
			}
		}
	}
	return nil
}

func joinIdx(idx []uint32) string {
	parts := make([]string, len(idx))
	for i, v := range idx {
		parts[i] = itoa(int(v))
	}
	return strings.Join(parts, ".")
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }
