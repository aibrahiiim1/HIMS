// Package swsnmp holds the SNMP collection logic shared by every switch
// driver (Aruba, Cisco, Huawei, …). The standard MIBs — IF-MIB interfaces,
// Q-BRIDGE VLANs/FDB, LLDP neighbors — are vendor-neutral, so each driver
// calls these helpers and adds only its vendor-specific bits (e.g. Cisco
// CDP). This keeps the generic core in one place (ADR-0001).
package swsnmp

import (
	"context"
	"fmt"
	"strings"

	"github.com/coralsearesorts/hims/internal/driver"
	"github.com/coralsearesorts/hims/internal/mibs"
	"github.com/coralsearesorts/hims/internal/snmp"
)

// Session is the shared SNMP collection session every SNMP driver accepts.
// The discovery pipeline builds one of these on successful auth and hands it
// to whichever driver matched, so a single concrete type works for all of
// them (each driver aliases its own Session to this — see e.g. aruba.Session).
type Session struct {
	driver.SessionBase
	Client snmp.Client
	Ctx    context.Context //nolint:containedctx // deliberate: driver.Session is transport-agnostic
}

// SysInfo is the basic system identity from SNMPv2-MIB.
type SysInfo struct {
	Hostname    string
	SysDescr    string
	SysObjectID string
	UptimeCS    int64
}

// CollectSysInfo reads sysName / sysDescr / sysObjectID / sysUpTime.
func CollectSysInfo(ctx context.Context, c snmp.Client) SysInfo {
	var si SysInfo
	pdus, err := c.Get(ctx, mibs.SysName, mibs.SysDescr, mibs.SysObjectID, mibs.SysUpTime)
	if err != nil {
		return si
	}
	byOID := pduMap(pdus)
	si.Hostname = strings.TrimSpace(snmp.PDUString(byOID[mibs.SysName]))
	si.SysDescr = snmp.PDUString(byOID[mibs.SysDescr])
	si.SysObjectID = snmp.PDUString(byOID[mibs.SysObjectID])
	if v, ok := snmp.PDUInt64(byOID[mibs.SysUpTime]); ok {
		si.UptimeCS = v
	}
	return si
}

// CollectInterfaces walks ifTable + ifXTable into interface snapshots.
func CollectInterfaces(ctx context.Context, c snmp.Client) []driver.InterfaceSnap {
	type acc struct {
		name, descr, alias, mac    string
		ifType, speed, admin, oper int
	}
	rows := map[int32]*acc{}
	get := func(idx int32) *acc {
		a := rows[idx]
		if a == nil {
			a = &acc{}
			rows[idx] = a
		}
		return a
	}

	_ = c.BulkWalk(ctx, mibs.IfXEntry1, func(p snmp.PDU) error {
		col, idx, ok := snmp.ColumnAndIndex(p.OID, mibs.IfXEntry1)
		if !ok || len(idx) != 1 {
			return nil
		}
		a := get(int32(idx[0]))
		switch int(col) {
		case mibs.IfXColName:
			a.name = snmp.PDUString(p)
		case mibs.IfXColAlias:
			a.alias = snmp.PDUString(p)
		case mibs.IfXColHighSpeed:
			if v, ok := snmp.PDUInt64(p); ok {
				a.speed = int(v)
			}
		}
		return nil
	})

	_ = c.BulkWalk(ctx, mibs.IfEntry, func(p snmp.PDU) error {
		col, idx, ok := snmp.ColumnAndIndex(p.OID, mibs.IfEntry)
		if !ok || len(idx) != 1 {
			return nil
		}
		a := get(int32(idx[0]))
		switch int(col) {
		case mibs.IfDescr:
			a.descr = snmp.PDUString(p)
		case mibs.IfType:
			if v, ok := snmp.PDUInt64(p); ok {
				a.ifType = int(v)
			}
		case mibs.IfPhysAddress:
			if mac := snmp.PDUMACAddress(p); mac != "" {
				a.mac = mac
			}
		case mibs.IfAdminStatus:
			if v, ok := snmp.PDUInt64(p); ok {
				a.admin = int(v)
			}
		case mibs.IfOperStatus:
			if v, ok := snmp.PDUInt64(p); ok {
				a.oper = int(v)
			}
		}
		return nil
	})

	out := make([]driver.InterfaceSnap, 0, len(rows))
	for idx, a := range rows {
		out = append(out, driver.InterfaceSnap{
			IfIndex: idx, IfName: a.name, IfDescr: a.descr, IfAlias: a.alias,
			IfType: a.ifType, MAC: a.mac, SpeedMbps: a.speed,
			AdminStatus: int16(a.admin), OperStatus: int16(a.oper),
		})
	}
	return out
}

// CollectVLANs walks dot1qVlanStaticTable.
func CollectVLANs(ctx context.Context, c snmp.Client) []driver.VLANSnap {
	names := map[int]string{}
	_ = c.BulkWalk(ctx, mibs.Dot1qVlanStaticEntry, func(p snmp.PDU) error {
		col, idx, ok := snmp.ColumnAndIndex(p.OID, mibs.Dot1qVlanStaticEntry)
		if !ok || len(idx) != 1 {
			return nil
		}
		if int(col) == mibs.Dot1qVlanStaticColName {
			names[int(idx[0])] = snmp.PDUString(p)
		}
		return nil
	})
	out := make([]driver.VLANSnap, 0, len(names))
	for vid, name := range names {
		out = append(out, driver.VLANSnap{VLANID: vid, Name: name})
	}
	return out
}

// CollectFDB walks the Q-BRIDGE FDB then the legacy bridge FDB.
func CollectFDB(ctx context.Context, c snmp.Client) []driver.MACSnap {
	macs := map[string]driver.MACSnap{}
	_ = c.BulkWalk(ctx, mibs.Dot1qTpFdbEntry, func(p snmp.PDU) error {
		col, idx, ok := snmp.ColumnAndIndex(p.OID, mibs.Dot1qTpFdbEntry)
		if !ok || len(idx) < 7 {
			return nil
		}
		vid := int(idx[0])
		mac := fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", idx[1], idx[2], idx[3], idx[4], idx[5], idx[6])
		m := macs[mac]
		m.VLANID, m.MAC = vid, mac
		switch int(col) {
		case mibs.Dot1qTpFdbColPort:
			if v, ok := snmp.PDUInt64(p); ok {
				m.IfIndex = int(v)
			}
		case mibs.Dot1qTpFdbColStatus:
			if v, ok := snmp.PDUInt64(p); ok {
				m.Status = int(v)
			}
		}
		macs[mac] = m
		return nil
	})
	_ = c.BulkWalk(ctx, mibs.Dot1dTpFdbEntry, func(p snmp.PDU) error {
		col, idx, ok := snmp.ColumnAndIndex(p.OID, mibs.Dot1dTpFdbEntry)
		if !ok || len(idx) < 6 {
			return nil
		}
		mac := fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", idx[0], idx[1], idx[2], idx[3], idx[4], idx[5])
		m := macs[mac]
		m.MAC = mac
		switch int(col) {
		case mibs.Dot1dTpFdbColPort:
			if v, ok := snmp.PDUInt64(p); ok {
				m.IfIndex = int(v)
			}
		case mibs.Dot1dTpFdbColStatus:
			if v, ok := snmp.PDUInt64(p); ok {
				m.Status = int(v)
			}
		}
		macs[mac] = m
		return nil
	})
	out := make([]driver.MACSnap, 0, len(macs))
	for _, m := range macs {
		if m.IfIndex > 0 {
			out = append(out, m)
		}
	}
	return out
}

// CollectLLDP walks lldpRemTable into neighbor snapshots.
func CollectLLDP(ctx context.Context, c snmp.Client) []driver.NeighborSnap {
	type acc struct {
		chassisID, portID, portDesc, sysName, sysDesc string
	}
	rows := map[string]*acc{}
	localPort := map[string]int{}
	_ = c.BulkWalk(ctx, mibs.LldpRemEntry, func(p snmp.PDU) error {
		col, idx, ok := snmp.ColumnAndIndex(p.OID, mibs.LldpRemEntry)
		if !ok || len(idx) < 3 {
			return nil
		}
		key := fmt.Sprintf("%d.%d", idx[1], idx[2])
		localPort[key] = int(idx[1])
		a := rows[key]
		if a == nil {
			a = &acc{}
			rows[key] = a
		}
		switch int(col) {
		case mibs.LldpRemColChassisID:
			if b, ok := p.Value.([]byte); ok && len(b) == 6 {
				a.chassisID = fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", b[0], b[1], b[2], b[3], b[4], b[5])
			} else {
				a.chassisID = snmp.PDUString(p)
			}
		case mibs.LldpRemColPortID:
			a.portID = snmp.PDUString(p)
		case mibs.LldpRemColPortDesc:
			a.portDesc = snmp.PDUString(p)
		case mibs.LldpRemColSysName:
			a.sysName = strings.TrimSpace(snmp.PDUString(p))
		case mibs.LldpRemColSysDesc:
			a.sysDesc = snmp.PDUString(p)
		}
		return nil
	})
	out := make([]driver.NeighborSnap, 0, len(rows))
	for key, a := range rows {
		out = append(out, driver.NeighborSnap{
			LocalIfIndex: localPort[key],
			RemChassisID: a.chassisID,
			RemPortID:    a.portID,
			RemPortDesc:  a.portDesc,
			RemSysName:   a.sysName,
			RemSysDesc:   a.sysDesc,
			Protocol:     "lldp",
		})
	}
	return out
}

// DerivePortRoles classifies interfaces from neighbor + MAC evidence.
func DerivePortRoles(ifaces []driver.InterfaceSnap, neighbors []driver.NeighborSnap, macs []driver.MACSnap) []driver.InterfaceSnap {
	uplink := map[int]struct{}{}
	for _, n := range neighbors {
		if n.LocalIfIndex > 0 {
			uplink[n.LocalIfIndex] = struct{}{}
		}
	}
	macCount := map[int]int{}
	for _, m := range macs {
		if m.IfIndex > 0 {
			macCount[m.IfIndex]++
		}
	}
	for i, iface := range ifaces {
		idx := int(iface.IfIndex)
		switch {
		case iface.AdminStatus == 2:
			ifaces[i].PortRole = "disabled"
		case has(uplink, idx):
			ifaces[i].PortRole = "uplink"
		case macCount[idx] > 3:
			ifaces[i].PortRole = "trunk"
		case macCount[idx] == 1:
			ifaces[i].PortRole = "edge"
		default:
			ifaces[i].PortRole = "unknown"
		}
	}
	return ifaces
}

func has(m map[int]struct{}, k int) bool { _, ok := m[k]; return ok }

func pduMap(pdus []snmp.PDU) map[string]snmp.PDU {
	m := make(map[string]snmp.PDU, len(pdus))
	for _, p := range pdus {
		m[p.OID] = p
	}
	return m
}

// FirmwareFromDescr extracts a version-looking token from a sysDescr.
func FirmwareFromDescr(descr string) string {
	for _, part := range strings.Fields(descr) {
		if strings.Contains(part, ".") && len(part) > 4 && !strings.ContainsAny(part, "/()[]") {
			return strings.Trim(part, ",;")
		}
	}
	return ""
}
