package swsnmp

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	"github.com/coralsearesorts/hims/internal/driver"
	"github.com/coralsearesorts/hims/internal/mibs"
	"github.com/coralsearesorts/hims/internal/snmp"
)

// CollectCDP walks the CISCO-CDP-MIB cdpCacheTable into neighbor snapshots.
// CDP is the Cisco-to-Cisco complement to LLDP; a mixed-vendor segment may
// expose one, the other, or both — the topology engine merges them.
func CollectCDP(ctx context.Context, c snmp.Client) []driver.NeighborSnap {
	type acc struct {
		deviceID, devicePort, platform string
		mgmtIP                         *netip.Addr
		localIf                        int
	}
	rows := map[string]*acc{}
	_ = c.BulkWalk(ctx, mibs.CdpCacheEntry, func(p snmp.PDU) error {
		col, idx, ok := snmp.ColumnAndIndex(p.OID, mibs.CdpCacheEntry)
		if !ok || len(idx) < 2 {
			return nil
		}
		key := fmt.Sprintf("%d.%d", idx[0], idx[1])
		a := rows[key]
		if a == nil {
			a = &acc{localIf: int(idx[0])}
			rows[key] = a
		}
		switch int(col) {
		case mibs.CdpCacheColDeviceID:
			a.deviceID = strings.TrimSpace(snmp.PDUString(p))
		case mibs.CdpCacheColDevicePort:
			a.devicePort = strings.TrimSpace(snmp.PDUString(p))
		case mibs.CdpCacheColPlatform:
			a.platform = strings.TrimSpace(snmp.PDUString(p))
		case mibs.CdpCacheColAddress:
			if ip := parseCDPAddr(p); ip != nil {
				a.mgmtIP = ip
			}
		}
		return nil
	})
	out := make([]driver.NeighborSnap, 0, len(rows))
	for _, a := range rows {
		out = append(out, driver.NeighborSnap{
			LocalIfIndex: a.localIf,
			RemSysName:   a.deviceID,
			RemPortID:    a.devicePort,
			RemSysDesc:   a.platform,
			RemMgmtIP:    a.mgmtIP,
			Protocol:     "cdp",
		})
	}
	return out
}

// parseCDPAddr decodes cdpCacheAddress, usually a 4-byte IPv4 OctetString.
func parseCDPAddr(p snmp.PDU) *netip.Addr {
	if b, ok := p.Value.([]byte); ok && len(b) == 4 {
		a := netip.AddrFrom4([4]byte{b[0], b[1], b[2], b[3]})
		return &a
	}
	if s := snmp.PDUString(p); s != "" {
		if a, err := netip.ParseAddr(s); err == nil {
			return &a
		}
	}
	return nil
}
