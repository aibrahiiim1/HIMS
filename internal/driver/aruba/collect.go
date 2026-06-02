// Aruba/HPE switch collection: interfaces (IF-MIB ifTable + ifXTable),
// VLANs (Q-BRIDGE-MIB), MAC FDB, and LLDP neighbors. Port-role
// derivation runs after the walks complete.
package aruba

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/coralsearesorts/hims/internal/driver"
	"github.com/coralsearesorts/hims/internal/mibs"
	"github.com/coralsearesorts/hims/internal/snmp"
)

// Collect implements driver.Collector. It is called by the discovery
// pipeline after authentication succeeds on an identified Aruba/HPE switch.
// The Session carries the live SNMP client.
func (d *Driver) Collect(sess driver.Session, probe driver.Probe) (driver.Facts, error) {
	as, ok := sess.(*Session)
	if !ok {
		return driver.Facts{}, fmt.Errorf("aruba_hpe: expected *Session, got %T", sess)
	}
	c := as.Client
	ctx := as.Ctx

	f := driver.Facts{KV: map[string]string{}, Raw: map[string]any{}}

	// --- Sysinfo ----------------------------------------------------------
	pdus, err := c.Get(ctx, mibs.SysDescr, mibs.SysObjectID, mibs.SysUpTime, mibs.SysName)
	if err == nil {
		byOID := pduMap(pdus)
		f.Hostname = snmp.PDUString(byOID[mibs.SysName])
		f.OSVersion = extractFirmware(snmp.PDUString(byOID[mibs.SysDescr]))
		if v, ok := snmp.PDUInt64(byOID[mibs.SysUpTime]); ok {
			f.KV["hardware.uptime_centisec"] = fmt.Sprintf("%d", v)
		}
		f.Raw["sysDescr"] = snmp.PDUString(byOID[mibs.SysDescr])
		f.Raw["sysObjectID"] = snmp.PDUString(byOID[mibs.SysObjectID])
	}

	// --- Interfaces (ifTable + ifXTable) ----------------------------------
	type ifAccum struct {
		name        string
		descr       string
		alias       string
		ifType      int
		mac         string
		speedMbps   int
		adminStatus int
		operStatus  int
	}
	accum := map[int32]*ifAccum{}
	getIf := func(idx int32) *ifAccum {
		a := accum[idx]
		if a == nil {
			a = &ifAccum{}
			accum[idx] = a
		}
		return a
	}

	// ifXTable walk — name, alias, high-speed
	_ = c.BulkWalk(ctx, mibs.IfXEntry1, func(p snmp.PDU) error {
		col, idx, ok := snmp.ColumnAndIndex(p.OID, mibs.IfXEntry1)
		if !ok || len(idx) != 1 {
			return nil
		}
		a := getIf(int32(idx[0]))
		switch int(col) {
		case mibs.IfXColName:
			a.name = snmp.PDUString(p)
		case mibs.IfXColAlias:
			a.alias = snmp.PDUString(p)
		case mibs.IfXColHighSpeed:
			if v, ok := snmp.PDUInt64(p); ok {
				a.speedMbps = int(v)
			}
		}
		return nil
	})

	// ifTable walk — descr, type, MAC, status
	_ = c.BulkWalk(ctx, mibs.IfEntry, func(p snmp.PDU) error {
		col, idx, ok := snmp.ColumnAndIndex(p.OID, mibs.IfEntry)
		if !ok || len(idx) != 1 {
			return nil
		}
		a := getIf(int32(idx[0]))
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
				a.adminStatus = int(v)
			}
		case mibs.IfOperStatus:
			if v, ok := snmp.PDUInt64(p); ok {
				a.operStatus = int(v)
			}
		}
		return nil
	})

	// Assemble interface snapshots into Facts.KV (phase-1 storage is via
	// the collector engine in SC3; here we just pack them for the engine).
	ifaces := make([]driver.InterfaceSnap, 0, len(accum))
	for ifIdx, a := range accum {
		ifaces = append(ifaces, driver.InterfaceSnap{
			IfIndex:     ifIdx,
			IfName:      a.name,
			IfDescr:     a.descr,
			IfAlias:     a.alias,
			IfType:      a.ifType,
			MAC:         a.mac,
			SpeedMbps:   a.speedMbps,
			AdminStatus: int16(a.adminStatus),
			OperStatus:  int16(a.operStatus),
		})
	}
	f.Interfaces = ifaces

	// --- VLANs (dot1qVlanStaticTable) -------------------------------------
	vlanNames := map[int]string{}
	_ = c.BulkWalk(ctx, mibs.Dot1qVlanStaticEntry, func(p snmp.PDU) error {
		col, idx, ok := snmp.ColumnAndIndex(p.OID, mibs.Dot1qVlanStaticEntry)
		if !ok || len(idx) != 1 {
			return nil
		}
		if int(col) == mibs.Dot1qVlanStaticColName {
			vlanNames[int(idx[0])] = snmp.PDUString(p)
		}
		return nil
	})
	vlans := make([]driver.VLANSnap, 0, len(vlanNames))
	for vid, name := range vlanNames {
		vlans = append(vlans, driver.VLANSnap{VLANID: vid, Name: name})
	}
	f.VLANs = vlans

	// --- MAC FDB (dot1qTpFdbTable then legacy dot1dTpFdbTable) ------------
	macs := map[string]driver.MACSnap{}
	// Q-BRIDGE: index = (vlan_id, MAC 6 bytes)
	_ = c.BulkWalk(ctx, mibs.Dot1qTpFdbEntry, func(p snmp.PDU) error {
		col, idx, ok := snmp.ColumnAndIndex(p.OID, mibs.Dot1qTpFdbEntry)
		if !ok || len(idx) < 7 {
			return nil
		}
		vid := int(idx[0])
		mac := fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x",
			idx[1], idx[2], idx[3], idx[4], idx[5], idx[6])
		m := macs[mac]
		m.VLANID = vid
		m.MAC = mac
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
	// Legacy bridge table (for non-Q-BRIDGE switches)
	_ = c.BulkWalk(ctx, mibs.Dot1dTpFdbEntry, func(p snmp.PDU) error {
		col, idx, ok := snmp.ColumnAndIndex(p.OID, mibs.Dot1dTpFdbEntry)
		if !ok || len(idx) < 6 {
			return nil
		}
		mac := fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x",
			idx[0], idx[1], idx[2], idx[3], idx[4], idx[5])
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
	macList := make([]driver.MACSnap, 0, len(macs))
	for _, m := range macs {
		if m.IfIndex > 0 {
			macList = append(macList, m)
		}
	}
	f.MACs = macList

	// --- LLDP neighbors ---------------------------------------------------
	type lldpAccum struct {
		chassisIDSubtype int
		chassisID        string
		portIDSubtype    int
		portID           string
		portDesc         string
		sysName          string
		sysDesc          string
		mgmtIP           *netip.Addr
	}
	// Index: (timeMark, localPortNum, remIndex) → 3 elements
	lldpRows := map[string]*lldpAccum{}
	lldpKey := func(idx []uint32) string {
		// ignore timeMark (idx[0]); key on (localPortNum, remIndex)
		if len(idx) < 3 {
			return ""
		}
		return fmt.Sprintf("%d.%d", idx[1], idx[2])
	}
	_ = c.BulkWalk(ctx, mibs.LldpRemEntry, func(p snmp.PDU) error {
		col, idx, ok := snmp.ColumnAndIndex(p.OID, mibs.LldpRemEntry)
		if !ok || len(idx) < 3 {
			return nil
		}
		key := lldpKey(idx)
		a := lldpRows[key]
		if a == nil {
			a = &lldpAccum{}
			lldpRows[key] = a
		}
		switch int(col) {
		case mibs.LldpRemColChassisIDSubtype:
			if v, ok := snmp.PDUInt64(p); ok {
				a.chassisIDSubtype = int(v)
			}
		case mibs.LldpRemColChassisID:
			// Often binary MAC; render as hex.
			if b, ok := p.Value.([]byte); ok && len(b) == 6 {
				a.chassisID = fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x",
					b[0], b[1], b[2], b[3], b[4], b[5])
			} else {
				a.chassisID = snmp.PDUString(p)
			}
		case mibs.LldpRemColPortIDSubtype:
			if v, ok := snmp.PDUInt64(p); ok {
				a.portIDSubtype = int(v)
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

	lldpList := make([]driver.NeighborSnap, 0, len(lldpRows))
	for key, a := range lldpRows {
		// parse localPortNum from key "portNum.remIndex"
		var localPort int
		fmt.Sscanf(key, "%d.", &localPort)
		ns := driver.NeighborSnap{
			LocalIfIndex: localPort,
			RemChassisID: a.chassisID,
			RemPortID:    a.portID,
			RemPortDesc:  a.portDesc,
			RemSysName:   a.sysName,
			RemSysDesc:   a.sysDesc,
			RemMgmtIP:    a.mgmtIP,
			Protocol:     "lldp",
		}
		// Map localPortNum back to ifIndex via the accum (if we have one).
		// Aruba typically makes localPortNum == ifIndex.
		ns.LocalIfIndex = localPort
		lldpList = append(lldpList, ns)
	}
	f.Neighbors = lldpList

	// --- Port-role derivation ---------------------------------------------
	f.Interfaces = derivePortRoles(f.Interfaces, f.Neighbors, f.MACs)

	return f, nil
}

// pduMap builds an OID → PDU index from a Get result.
func pduMap(pdus []snmp.PDU) map[string]snmp.PDU {
	m := make(map[string]snmp.PDU, len(pdus))
	for _, p := range pdus {
		m[p.OID] = p
	}
	return m
}

// extractFirmware tries to pull a version token from a sysDescr string.
func extractFirmware(descr string) string {
	// Aruba/ProCurve sysDescr example:
	// "HP J9773A 2530-24G-PoEP Switch, revision YA.16.04.0015, ROM Y.10.02"
	// Extract "YA.16.04.0015" style.
	for _, part := range strings.Fields(descr) {
		if strings.Contains(part, ".") && len(part) > 4 && !strings.ContainsAny(part, "/()[]") {
			return strings.Trim(part, ",;")
		}
	}
	return ""
}

// derivePortRoles classifies each interface using a simple heuristic:
// - Ports that are LLDP neighbors of another switch → "uplink"
// - Ports with many distinct MACs → "trunk" (access switch uplink)
// - Ports with 1 MAC → "edge" (end-device connected)
// - Ports admin-down → "disabled"
// - Everything else → "unknown"
func derivePortRoles(ifaces []driver.InterfaceSnap, neighbors []driver.NeighborSnap, macs []driver.MACSnap) []driver.InterfaceSnap {
	uplinkPorts := map[int]struct{}{}
	for _, n := range neighbors {
		if n.LocalIfIndex > 0 {
			uplinkPorts[n.LocalIfIndex] = struct{}{}
		}
	}
	// MAC count per port
	macCount := map[int]int{}
	for _, m := range macs {
		if m.IfIndex > 0 {
			macCount[m.IfIndex]++
		}
	}
	for i, iface := range ifaces {
		switch {
		case iface.AdminStatus == 2:
			ifaces[i].PortRole = "disabled"
		case uplinkPorts[int(iface.IfIndex)] != (struct{}{}):
			ifaces[i].PortRole = "uplink"
		case macCount[int(iface.IfIndex)] > 3:
			ifaces[i].PortRole = "trunk"
		case macCount[int(iface.IfIndex)] == 1:
			ifaces[i].PortRole = "edge"
		default:
			if iface.PortRole == "" {
				ifaces[i].PortRole = "unknown"
			}
		}
	}
	return ifaces
}
