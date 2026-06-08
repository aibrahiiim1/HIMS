// Aruba/HPE switch collection. The standard MIBs (interfaces, VLANs, FDB,
// LLDP) come from the shared swsnmp package; Aruba adds no proprietary
// protocol beyond LLDP, so this is a thin assembly of the shared collectors.
package aruba

import (
	"fmt"

	"github.com/coralsearesorts/hims/internal/driver"
	"github.com/coralsearesorts/hims/internal/driver/swsnmp"
)

// Collect implements driver.Collector. Called by the discovery pipeline
// after authentication succeeds on an identified Aruba/HPE switch.
func (d *Driver) Collect(sess driver.Session, _ driver.Probe) (driver.Facts, error) {
	as, ok := sess.(*Session)
	if !ok {
		return driver.Facts{}, fmt.Errorf("aruba_hpe: expected *Session, got %T", sess)
	}
	c, ctx := as.Client, as.Ctx

	f := driver.Facts{KV: map[string]string{}, Raw: map[string]any{}}

	si := swsnmp.CollectSysInfo(ctx, c)
	f.Hostname = si.Hostname
	f.Vendor = "Aruba/HPE"
	f.OSVersion = swsnmp.FirmwareFromDescr(si.SysDescr)
	if si.UptimeCS > 0 {
		f.KV["hardware.uptime_centisec"] = fmt.Sprintf("%d", si.UptimeCS)
	}
	f.Raw["sysDescr"] = si.SysDescr
	f.Raw["sysObjectID"] = si.SysObjectID

	f.Interfaces = swsnmp.CollectInterfaces(ctx, c)
	f.VLANs = swsnmp.CollectVLANs(ctx, c)
	f.PortVLANs = swsnmp.CollectPortVLANs(ctx, c)
	f.MACs = swsnmp.CollectFDB(ctx, c)
	f.Neighbors = swsnmp.CollectLLDP(ctx, c)
	f.Interfaces = swsnmp.DerivePortRoles(f.Interfaces, f.Neighbors, f.MACs)

	return f, nil
}
