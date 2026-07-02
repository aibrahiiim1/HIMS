// Package swsnmp holds the SNMP collection logic shared by every switch
// driver (Aruba, Cisco, Huawei, …). The standard MIBs — IF-MIB interfaces,
// Q-BRIDGE VLANs/FDB, LLDP neighbors — are vendor-neutral, so each driver
// calls these helpers and adds only its vendor-specific bits (e.g. Cisco
// CDP). This keeps the generic core in one place (ADR-0001).
package swsnmp

import (
	"context"
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
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

// CollectBasePortMap walks dot1dBasePortIfIndex → map[bridgePort]ifIndex.
// FDB tables and Q-BRIDGE port bitmaps are indexed by bridge PORT number, not
// ifIndex, so this map is required to attribute MACs / VLAN membership to the
// real interface. Empty when the device exposes no dot1dBasePortTable.
func CollectBasePortMap(ctx context.Context, c snmp.Client) map[int]int {
	m := map[int]int{}
	_ = c.BulkWalk(ctx, mibs.Dot1dBasePortEntry, func(p snmp.PDU) error {
		col, idx, ok := snmp.ColumnAndIndex(p.OID, mibs.Dot1dBasePortEntry)
		if !ok || len(idx) != 1 || int(col) != mibs.Dot1dBasePortColIfIndex {
			return nil
		}
		if v, ok := snmp.PDUInt64(p); ok && v > 0 {
			m[int(idx[0])] = int(v)
		}
		return nil
	})
	return m
}

// CollectPortVLANs decodes per-port VLAN membership from the Q-BRIDGE static
// egress + untagged port bitmaps, mapping bridge ports → ifIndex. A port in a
// VLAN's egress set is a member; if it's also in the untagged set it carries
// that VLAN untagged (its access / native VLAN), otherwise it's a tagged member
// (trunk). Vendor-neutral: works for any Q-BRIDGE switch (ProCurve/Aruba, Cisco,
// Huawei, …). Returns nothing when the device exposes no static VLAN table.
func CollectPortVLANs(ctx context.Context, c snmp.Client) []driver.PortVlanSnap {
	baseMap := CollectBasePortMap(ctx, c)
	egress := map[int][]byte{}
	untag := map[int][]byte{}
	_ = c.BulkWalk(ctx, mibs.Dot1qVlanStaticEntry, func(p snmp.PDU) error {
		col, idx, ok := snmp.ColumnAndIndex(p.OID, mibs.Dot1qVlanStaticEntry)
		if !ok || len(idx) != 1 {
			return nil
		}
		b, _ := p.Value.([]byte)
		switch int(col) {
		case mibs.Dot1qVlanStaticColEgress:
			egress[int(idx[0])] = b
		case mibs.Dot1qVlanStaticColUntagged:
			untag[int(idx[0])] = b
		}
		return nil
	})
	out := make([]driver.PortVlanSnap, 0)
	seen := map[[2]int]bool{}
	for vlan, eb := range egress {
		ub := untag[vlan]
		for _, bp := range decodePortBitmap(eb) {
			ifIdx := bp
			if real, ok := baseMap[bp]; ok {
				ifIdx = real
			}
			key := [2]int{ifIdx, vlan}
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, driver.PortVlanSnap{IfIndex: ifIdx, VLANID: vlan, Tagged: !bitSet(ub, bp)})
		}
	}
	return out
}

// decodePortBitmap returns the 1-based bridge-port numbers set in a Q-BRIDGE
// PortList octet string. Bit order: MSB of byte 0 = port 1.
func decodePortBitmap(b []byte) []int {
	ports := make([]int, 0)
	for i, by := range b {
		for bit := 0; bit < 8; bit++ {
			if by&(0x80>>uint(bit)) != 0 {
				ports = append(ports, i*8+bit+1)
			}
		}
	}
	return ports
}

// bitSet reports whether the given 1-based bridge port is set in a PortList.
func bitSet(b []byte, port int) bool {
	if port < 1 {
		return false
	}
	i := (port - 1) / 8
	if i >= len(b) {
		return false
	}
	return b[i]&(0x80>>uint((port-1)%8)) != 0
}

// CollectFDB walks the Q-BRIDGE FDB then the legacy bridge FDB. FDB port values
// are bridge PORT numbers; they are mapped to real ifIndex via dot1dBasePortTable.
func CollectFDB(ctx context.Context, c snmp.Client) []driver.MACSnap {
	baseMap := CollectBasePortMap(ctx, c)
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
		if m.IfIndex <= 0 {
			continue
		}
		// FDB port is a bridge port number — map it to the real ifIndex.
		if real, ok := baseMap[m.IfIndex]; ok {
			m.IfIndex = real
		}
		out = append(out, m)
	}
	return out
}

// CollectARP walks ipNetToMediaTable (the ARP cache) into IP↔MAC snapshots. This
// is the L3 binding the Path Finder needs to resolve a wired endpoint's IP to a
// MAC (and from there, via the FDB, to a switch port). Only L3 devices (routers /
// SVIs / gateways) answer with rows; pure L2 switches return an empty table.
//
// The PhysAddress column's row index is ifIndex.ipv4(4 octets), and the value is
// the 6-byte MAC — so a single column walk yields ifIndex + IP + MAC per entry.
func CollectARP(ctx context.Context, c snmp.Client) []driver.ARPSnap {
	out := []driver.ARPSnap{}
	_ = c.BulkWalk(ctx, mibs.IPNetToMediaPhysAddr, func(p snmp.PDU) error {
		_, idx, ok := snmp.ColumnAndIndex(p.OID, mibs.IPNetToMediaEntry)
		if !ok || len(idx) < 5 { // [ifIndex, o1, o2, o3, o4]
			return nil
		}
		b, ok := p.Value.([]byte)
		if !ok || len(b) != 6 {
			return nil
		}
		mac := macStr(b)
		// Incomplete / placeholder entries some agents return — not real bindings.
		if mac == "00:00:00:00:00:00" || mac == "ff:ff:ff:ff:ff:ff" {
			return nil
		}
		out = append(out, driver.ARPSnap{
			IfIndex: int(idx[0]),
			IP:      fmt.Sprintf("%d.%d.%d.%d", idx[1], idx[2], idx[3], idx[4]),
			MAC:     mac,
		})
		return nil
	})
	return out
}

// CollectIPAddresses walks ipAddrTable (RFC 1213) — the IPs configured ON this
// device, each with its owning ifIndex + netmask. For an L3 switch this yields
// the SVI / VLAN-interface gateway IPs (and loopbacks / mgmt IP); pure L2
// switches return an empty table. This is the binding that attributes a VLAN
// gateway IP (e.g. an SVI on the core switch) to the switch that owns it.
func CollectIPAddresses(ctx context.Context, c snmp.Client) []driver.IPInterfaceSnap {
	type row struct {
		ifIndex int
		mask    string
	}
	rows := map[string]*row{}
	get := func(ip string) *row {
		r := rows[ip]
		if r == nil {
			r = &row{}
			rows[ip] = r
		}
		return r
	}
	_ = c.BulkWalk(ctx, mibs.IPAddrEntry, func(p snmp.PDU) error {
		col, idx, ok := snmp.ColumnAndIndex(p.OID, mibs.IPAddrEntry)
		if !ok || len(idx) != 4 { // index = the IPv4 address itself
			return nil
		}
		ip := fmt.Sprintf("%d.%d.%d.%d", idx[0], idx[1], idx[2], idx[3])
		switch int(col) {
		case 2: // ipAdEntIfIndex
			if v, ok := snmp.PDUInt64(p); ok {
				get(ip).ifIndex = int(v)
			}
		case 3: // ipAdEntNetMask (SNMP IpAddress = 4 bytes)
			get(ip).mask = ipv4Bytes(p.Value)
		}
		return nil
	})
	out := make([]driver.IPInterfaceSnap, 0, len(rows))
	for ip, r := range rows {
		out = append(out, driver.IPInterfaceSnap{IP: ip, IfIndex: r.ifIndex, NetMask: r.mask})
	}
	return out
}

// ipv4Bytes renders an SNMP IpAddress value (4-byte octet string) as dotted quad.
// ipv4Bytes normalises an SNMP IpAddress value (used by ipAddrTable netmask) to a
// dotted-quad string. gosnmp decodes the ASN.1 IpAddress type into a Go string
// already in "a.b.c.d" form, but some agents/paths surface the 4 raw octets as
// []byte (or a 4-char string) — handle all three so the mask is never dropped.
func ipv4Bytes(v any) string {
	switch t := v.(type) {
	case string:
		if a, err := netip.ParseAddr(strings.TrimSpace(t)); err == nil && a.Is4() {
			return a.String()
		}
		if len(t) == 4 {
			return fmt.Sprintf("%d.%d.%d.%d", t[0], t[1], t[2], t[3])
		}
	case []byte:
		if len(t) == 4 {
			return fmt.Sprintf("%d.%d.%d.%d", t[0], t[1], t[2], t[3])
		}
	}
	return ""
}

// sviNameRe extracts a VLAN id from an SVI / VLAN-interface name across vendors:
// Cisco "Vlan210", Aruba/ProCurve "VLAN210", Comware "Vlan-interface210",
// Juniper "irb.210" / "vlan.210". Loopbacks/physical ports don't match.
var sviNameRe = regexp.MustCompile(`(?i)^(?:vlan\D*?|irb\.)(\d{1,4})$`)

// DetectSVIGateways binds each L3 IP to a VLAN when its owning interface is an
// SVI (name like "Vlan210"), setting that VLAN's GatewayIP. VLANs with no SVI
// are returned unchanged; an SVI whose VLAN wasn't in the static table is added
// so a gateway IP is never dropped. The first IP per VLAN wins (stable).
func DetectSVIGateways(ifaces []driver.InterfaceSnap, ips []driver.IPInterfaceSnap, vlans []driver.VLANSnap) []driver.VLANSnap {
	sviVlan := map[int]int{} // ifIndex → vlan id
	for _, f := range ifaces {
		name := strings.TrimSpace(f.IfName)
		if name == "" {
			name = strings.TrimSpace(f.IfDescr)
		}
		if m := sviNameRe.FindStringSubmatch(name); m != nil {
			if vid, err := strconv.Atoi(m[1]); err == nil && vid > 0 && vid < 4096 {
				sviVlan[int(f.IfIndex)] = vid
			}
		}
	}
	gw := map[int]string{} // vlan id → gateway ip
	for _, ip := range ips {
		if vid, ok := sviVlan[ip.IfIndex]; ok {
			if _, seen := gw[vid]; !seen {
				gw[vid] = ip.IP
			}
		}
	}
	out := make([]driver.VLANSnap, len(vlans))
	copy(out, vlans)
	byID := map[int]int{} // vlan id → index in out
	for i := range out {
		byID[out[i].VLANID] = i
	}
	for vid, ip := range gw {
		if i, ok := byID[vid]; ok {
			out[i].GatewayIP = ip
		} else {
			out = append(out, driver.VLANSnap{VLANID: vid, GatewayIP: ip})
		}
	}
	return out
}

// CollectLLDP walks lldpRemTable into neighbor snapshots.
func CollectLLDP(ctx context.Context, c snmp.Client) []driver.NeighborSnap {
	type acc struct {
		chassisSub, portSub        int
		chassisRaw, portRaw        []byte
		chassisStr, portStr        string
		portDesc, sysName, sysDesc string
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
		case mibs.LldpRemColChassisIDSubtype:
			if v, ok := snmp.PDUInt64(p); ok {
				a.chassisSub = int(v)
			}
		case mibs.LldpRemColChassisID:
			if b, ok := p.Value.([]byte); ok {
				a.chassisRaw = b
			}
			a.chassisStr = snmp.PDUString(p)
		case mibs.LldpRemColPortIDSubtype:
			if v, ok := snmp.PDUInt64(p); ok {
				a.portSub = int(v)
			}
		case mibs.LldpRemColPortID:
			if b, ok := p.Value.([]byte); ok {
				a.portRaw = b
			}
			a.portStr = snmp.PDUString(p)
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
			RemChassisID: decodeLLDPID(a.chassisSub, a.chassisRaw, a.chassisStr),
			RemPortID:    decodeLLDPID(a.portSub, a.portRaw, a.portStr),
			RemPortDesc:  a.portDesc,
			RemSysName:   a.sysName,
			RemSysDesc:   a.sysDesc,
			Protocol:     "lldp",
		})
	}
	return out
}

// decodeLLDPID renders an LLDP chassis-id / port-id per its IEEE-802.1AB subtype:
// macAddress(3) → MAC hex, networkAddress(4) → IP, and the string subtypes
// (interfaceAlias/portComponent/interfaceName/agentCircuitId/local) → text. It
// falls back to MAC/hex for non-printable bytes so the UI never shows control
// characters (the cause of the garbled "Remote port" values).
func decodeLLDPID(subtype int, raw []byte, str string) string {
	if len(raw) == 0 {
		return strings.TrimRight(str, "\x00 ")
	}
	switch subtype {
	case 3: // macAddress
		if len(raw) == 6 {
			return macStr(raw)
		}
	case 4: // networkAddress: [addrFamily][address…]; family 1 = IPv4
		if len(raw) >= 5 && raw[0] == 1 {
			return fmt.Sprintf("%d.%d.%d.%d", raw[1], raw[2], raw[3], raw[4])
		}
	}
	if isPrintableBytes(raw) {
		return strings.TrimRight(string(raw), "\x00 ")
	}
	if len(raw) == 6 { // unlabelled MAC
		return macStr(raw)
	}
	return fmt.Sprintf("%x", raw)
}

func macStr(b []byte) string {
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", b[0], b[1], b[2], b[3], b[4], b[5])
}

func isPrintableBytes(b []byte) bool {
	for _, c := range b {
		if c == 0 {
			continue
		}
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
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
