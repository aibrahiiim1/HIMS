package fortigate

import (
	"context"
	"testing"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
	"github.com/coralsearesorts/hims/internal/mibs"
	"github.com/coralsearesorts/hims/internal/snmp"
)

type fakeClient struct {
	gets  map[string]snmp.PDU
	walks map[string][]snmp.PDU
}

func (f *fakeClient) Connect(context.Context) error { return nil }
func (f *fakeClient) Close() error                  { return nil }
func (f *fakeClient) Get(_ context.Context, oids ...string) ([]snmp.PDU, error) {
	out := make([]snmp.PDU, len(oids))
	for i, o := range oids {
		if p, ok := f.gets[o]; ok {
			out[i] = p
			out[i].OID = o
		} else {
			out[i] = snmp.PDU{OID: o, Type: snmp.TypeNoSuchInstance}
		}
	}
	return out, nil
}
func (f *fakeClient) Walk(ctx context.Context, root string, fn snmp.WalkFunc) error {
	return f.BulkWalk(ctx, root, fn)
}
func (f *fakeClient) BulkWalk(_ context.Context, root string, fn snmp.WalkFunc) error {
	for key, pdus := range f.walks {
		if !snmp.HasOIDPrefix(key, root) && key != root {
			continue
		}
		for _, p := range pdus {
			if err := fn(p); err != nil {
				return err
			}
		}
	}
	return nil
}

func gauge(oid string, v uint) snmp.PDU { return snmp.PDU{OID: oid, Type: snmp.TypeGauge32, Value: v} }
func i(oid string, v int) snmp.PDU      { return snmp.PDU{OID: oid, Type: snmp.TypeInt, Value: v} }
func c64(oid string, v uint64) snmp.PDU {
	return snmp.PDU{OID: oid, Type: snmp.TypeCounter64, Value: v}
}
func oct(oid, v string) snmp.PDU {
	return snmp.PDU{OID: oid, Type: snmp.TypeOctetString, Value: []byte(v)}
}

func TestFortiGate_FingerprintByOID(t *testing.T) {
	m := New().Fingerprint(driver.Probe{SNMPSysObjectID: ".1.3.6.1.4.1.12356.101.1.1"})
	if m.Confidence != 90 || m.Category != domain.CatFirewall {
		t.Fatalf("got %+v, want conf=90 firewall", m)
	}
}

func TestFortiGate_NoMatchForCisco(t *testing.T) {
	m := New().Fingerprint(driver.Probe{SNMPSysObjectID: ".1.3.6.1.4.1.9.1.1", OpenTCPPorts: []int{161}})
	if m.Confidence != 0 {
		t.Fatalf("Cisco must not match fortigate, got %+v", m)
	}
}

func baseGets() map[string]snmp.PDU {
	return map[string]snmp.PDU{
		mibs.FgSysVersion:   oct(mibs.FgSysVersion, "v7.4.11,build2701,240614 (GA.M)"),
		mibs.FgSysCpuUsage:  gauge(mibs.FgSysCpuUsage, 8),
		mibs.FgSysMemUsage:  gauge(mibs.FgSysMemUsage, 70),
		mibs.FgSysDiskUsage: gauge(mibs.FgSysDiskUsage, 51522), // MB
		mibs.FgSysDiskCap:   gauge(mibs.FgSysDiskCap, 472564),  // MB
		mibs.FgSysSesCount:  gauge(mibs.FgSysSesCount, 220799),
		mibs.FgHaSystemMode: i(mibs.FgHaSystemMode, mibs.FgHaModeStandalone),
	}
}

func collect(t *testing.T, fc *fakeClient) driver.Facts {
	t.Helper()
	f, err := New().Collect(&Session{Client: fc, Ctx: context.Background()}, driver.Probe{})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	return f
}

func TestFortiGate_DiskIsMegabytesNotPercent(t *testing.T) {
	f := collect(t, &fakeClient{gets: baseGets()})
	// Disk should be bytes (51522 MB * 1MiB) and a derived percent, NOT
	// the raw 51522 stored as a percent.
	if f.KV["disk.used_bytes"] != "54024732672" { // 51522 * 1048576
		t.Errorf("disk.used_bytes = %q, want 54024732672", f.KV["disk.used_bytes"])
	}
	if f.KV["disk.used_pct"] != "10" { // 51522*100/472564 = 10
		t.Errorf("disk.used_pct = %q, want 10", f.KV["disk.used_pct"])
	}
	if f.OSVersion != "7.4.11" {
		t.Errorf("OSVersion = %q, want 7.4.11", f.OSVersion)
	}
	if f.KV["firewall.session_count"] != "220799" {
		t.Errorf("sessions = %q", f.KV["firewall.session_count"])
	}
}

func TestFortiGate_VpnCompositeIndexAndCounter64(t *testing.T) {
	v := mibs.FgVpnTunEntry
	col := func(c int, idx string) string { return v + "." + itoa(c) + "." + idx }
	fc := &fakeClient{
		gets: baseGets(),
		walks: map[string][]snmp.PDU{
			// Composite index "1.1": tunnel up, Counter64 octets, gw.
			v: {
				oct(col(mibs.FgVpnTunColP2Name, "1.1"), "to-branch-a"),
				{OID: col(mibs.FgVpnTunColRemGwy, "1.1"), Type: snmp.TypeIPAddress, Value: "203.0.113.7"},
				c64(col(mibs.FgVpnTunColInOct, "1.1"), 67766475720),
				c64(col(mibs.FgVpnTunColOutOct, "1.1"), 43062298494),
				i(col(mibs.FgVpnTunColStatus, "1.1"), mibs.FgVpnStatusUp),
				oct(col(mibs.FgVpnTunColP2Name, "2.1"), "to-branch-b"),
				i(col(mibs.FgVpnTunColStatus, "2.1"), mibs.FgVpnStatusDown),
			},
		},
	}
	f := collect(t, fc)
	if len(f.VpnTunnels) != 2 {
		t.Fatalf("composite index must yield 2 tunnels, got %d", len(f.VpnTunnels))
	}
	var a *driver.VpnTunnelSnap
	for idx := range f.VpnTunnels {
		if f.VpnTunnels[idx].TunnelName == "to-branch-a" {
			a = &f.VpnTunnels[idx]
		}
	}
	if a == nil {
		t.Fatal("missing to-branch-a")
	}
	if a.Status != "up" {
		t.Errorf("status = %q, want up", a.Status)
	}
	if a.InOctets == nil || *a.InOctets != 67766475720 {
		t.Errorf("Counter64 in_octets not parsed: %v", a.InOctets)
	}
	if a.RemoteGW == nil || a.RemoteGW.String() != "203.0.113.7" {
		t.Errorf("remote_gw = %v", a.RemoteGW)
	}
}

func TestFortiGate_HAMemberCountFromRows(t *testing.T) {
	e := mibs.FgHaStatsEntry
	fc := &fakeClient{
		gets: baseGets(),
		walks: map[string][]snmp.PDU{
			// One HA-stats row with CPU/mem but NO serial → counts as 1
			// member but yields no detail row (standalone-unit behavior).
			e: {
				gauge(e+"."+itoa(mibs.FgHaStatsColCpu)+".1", 9),
				gauge(e+"."+itoa(mibs.FgHaStatsColMem)+".1", 50),
			},
		},
	}
	f := collect(t, fc)
	if f.FirewallStatus == nil || f.FirewallStatus.HAMemberCount != 1 {
		t.Fatalf("ha_member_count should be 1 (one row), got %+v", f.FirewallStatus)
	}
	if len(f.HAMembers) != 0 {
		t.Errorf("serial-less row yields no detail member, got %d", len(f.HAMembers))
	}
	if f.FirewallStatus.HAMode != "standalone" {
		t.Errorf("ha_mode = %q, want standalone", f.FirewallStatus.HAMode)
	}
}

func TestFortiGate_HAMemberWithSerial(t *testing.T) {
	e := mibs.FgHaStatsEntry
	fc := &fakeClient{
		gets: baseGets(),
		walks: map[string][]snmp.PDU{
			e: {
				oct(e+"."+itoa(mibs.FgHaStatsColSerial)+".1", "FGT-A-SERIAL"),
				oct(e+"."+itoa(mibs.FgHaStatsColHostname)+".1", "fw-primary"),
				i(e+"."+itoa(mibs.FgHaStatsColSyncStat)+".1", mibs.FgHaSyncSync),
			},
		},
	}
	f := collect(t, fc)
	if len(f.HAMembers) != 1 || f.HAMembers[0].Serial != "FGT-A-SERIAL" {
		t.Fatalf("expected 1 member with serial, got %+v", f.HAMembers)
	}
	if f.HAMembers[0].SyncStatus != "synchronized" {
		t.Errorf("sync = %q, want synchronized", f.HAMembers[0].SyncStatus)
	}
}

func TestFortiGate_Licenses(t *testing.T) {
	e := mibs.FgLicContractEntry
	fc := &fakeClient{
		gets: baseGets(),
		walks: map[string][]snmp.PDU{
			e: {
				oct(e+"."+itoa(mibs.FgLicColDesc)+".1", "AV"),
				oct(e+"."+itoa(mibs.FgLicColExpiry)+".1", "2027-01-15"),
			},
		},
	}
	f := collect(t, fc)
	if len(f.Licenses) != 1 || f.Licenses[0].Contract != "AV" || f.Licenses[0].Expiry != "2027-01-15" {
		t.Fatalf("license parse wrong: %+v", f.Licenses)
	}
}
