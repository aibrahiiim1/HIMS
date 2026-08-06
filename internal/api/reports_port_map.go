package api

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/coralsearesorts/hims/internal/reports"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/coralsearesorts/hims/internal/topology"
	"github.com/google/uuid"
)

// Port Map report — "where is each device plugged in": device → switch, port,
// VLAN.
//
// It resolves attachment through the SAME topology engine the per-device
// Connectivity panel uses, rather than joining the FDB directly. That matters:
// a switch's forwarding table lists every downstream MAC on its UPLINK port
// too, so a naive join reports the core uplink for every device in the estate.
// The engine ranks candidate ports by fan-out (fewest MACs = the true edge
// port), so SwitchPort[0] is the real attachment — and the export then agrees
// with what the operator sees on the device page.
//
// Rows are emitted for EVERY device, including ones with no attachment
// evidence. A device missing from a port map reads as "not on the network";
// a device present with an explicit "not resolved" reason reads as what it is —
// unknown, and why.

// portMapRow is one device's attachment, flattened for a spreadsheet.
type portMapRow struct {
	DeviceName  string
	Hostname    string
	DeviceIP    string
	DeviceMAC   string
	Category    string
	Site        string
	SwitchName  string
	SwitchIP    string
	Port        string
	PortIndex   string
	PortDescr   string
	VLAN        string
	VLANName    string
	TaggedVLANs string
	MACsOnPort  string
	Confidence  string
	Evidence    string
	Unresolved  string
}

func (r portMapRow) cells() []string {
	return []string{
		r.DeviceName, r.Hostname, r.DeviceIP, r.DeviceMAC, r.Category, r.Site,
		r.SwitchName, r.SwitchIP, r.Port, r.PortIndex, r.PortDescr,
		r.VLAN, r.VLANName, r.TaggedVLANs, r.MACsOnPort,
		r.Confidence, r.Evidence, r.Unresolved,
	}
}

var portMapHeaders = []string{
	"Device", "Hostname", "Device IP", "Device MAC", "Category", "Site",
	"Switch", "Switch IP", "Port", "Port ifIndex", "Port description",
	"VLAN", "VLAN name", "Tagged VLANs", "MACs on port",
	"Confidence", "Evidence", "Not resolved — reason",
}

// resolveDeviceAttachment mirrors deviceConnectivity: try the device's primary
// IP, then any MAC known from its interfaces or OS NICs.
func (s *Server) resolveDeviceAttachment(ctx context.Context, d db.Device) (topology.SearchResult, bool) {
	if d.PrimaryIp != nil {
		if sr, err := s.topo.SearchIP(ctx, *d.PrimaryIp); err == nil && len(sr.SwitchPort) > 0 {
			return sr, true
		}
	}
	macs := map[string]bool{}
	if ifs, err := s.queries.ListInterfaces(ctx, d.ID); err == nil {
		for _, i := range ifs {
			if i.Mac != nil && *i.Mac != "" {
				macs[normMAC(*i.Mac)] = true
			}
		}
	}
	if nics, err := s.queries.ListOSNics(ctx, d.ID); err == nil {
		for _, n := range nics {
			if n.Mac != nil && *n.Mac != "" {
				macs[normMAC(*n.Mac)] = true
			}
		}
	}
	for m := range macs {
		if sr, err := s.topo.SearchMAC(ctx, m); err == nil && len(sr.SwitchPort) > 0 {
			return sr, true
		}
	}
	return topology.SearchResult{}, false
}

// deviceDisplayName returns the most useful name for a device.
//
// Discovery names a device after its IP when nothing better is known at enrol
// time, but a later OS/SNMP collection often learns the real hostname and
// stores it in devices.hostname WITHOUT rewriting the name. A report that shows
// only `name` then prints "172.21.60.101" for a machine everyone calls
// "CHV-DOF" — which is useless for the one job this report has: telling an
// engineer which physical box is on which port.
//
// So: a name that is not just the IP wins (an operator may have set it
// deliberately); otherwise fall back to the learned hostname; otherwise the IP,
// which at least identifies the row.
func deviceDisplayName(d db.Device) string {
	ip := ""
	if d.PrimaryIp != nil && d.PrimaryIp.IsValid() {
		ip = d.PrimaryIp.String()
	}
	name := strings.TrimSpace(d.Name)
	if name != "" && name != ip {
		return name
	}
	if d.Hostname != nil {
		if h := strings.TrimSpace(*d.Hostname); h != "" {
			return h
		}
	}
	return d.Name
}

// portMapVLAN decides what belongs in the VLAN / VLAN-name columns for one
// attachment port, in priority order:
//
//  1. the switch's authoritative per-port untagged/native VLAN;
//  2. otherwise the FDB-derived VLAN — but only if it is a real 802.1Q id the
//     switch actually has configured.
//
// Anything else yields a BLANK VLAN plus a reason. A VLAN column is read as
// fact, so an id the switch never configured (VLANSuspect) or a bridge/FdbId
// index such as 0 — which non-VLAN-aware forwarding tables report — must not be
// printed as though it were the port's VLAN.
func portMapVLAN(untagged *int32, untaggedName *string, fdbVLAN int32, suspect bool, fdbName *string) (vlan, name string) {
	switch {
	case untagged != nil:
		vlan = strconv.Itoa(int(*untagged))
		if untaggedName != nil {
			name = *untaggedName
		}
	case suspect:
		name = "(FDB reported " + strconv.Itoa(int(fdbVLAN)) + " — not a configured VLAN on this switch)"
	case fdbVLAN <= 0 || fdbVLAN > 4094:
		name = "(switch reported no usable VLAN for this port; the FDB is not VLAN-aware here)"
	default:
		vlan = strconv.Itoa(int(fdbVLAN))
		if fdbName != nil {
			name = *fdbName
		}
	}
	return vlan, name
}

// portMapSheets builds the Port Map report.
func (s *Server) portMapSheets(ctx context.Context) ([]reports.Sheet, error) {
	devs, err := s.queries.ListAllDevices(ctx)
	if err != nil {
		return nil, err
	}
	siteName := map[uuid.UUID]string{}
	for _, l := range s.locationsByID(ctx) {
		siteName[l.ID] = l.Name
	}
	// Switches are excluded from the "endpoint" sheet only when they are the
	// SUBJECT of a row; a switch plugged into another switch is legitimate data.
	rows := make([]portMapRow, 0, len(devs))
	for _, d := range devs {
		row := portMapRow{
			DeviceName: deviceDisplayName(d),
			Category:   d.Category,
		}
		if d.Hostname != nil {
			row.Hostname = strings.TrimSpace(*d.Hostname)
		}
		if d.PrimaryIp != nil && d.PrimaryIp.IsValid() {
			row.DeviceIP = d.PrimaryIp.String()
		}
		if d.LocationID != nil {
			row.Site = siteName[*d.LocationID]
		}
		sr, ok := s.resolveDeviceAttachment(ctx, d)
		if !ok {
			// Say WHY, so a blank row is never mistaken for "no switch".
			row.Unresolved = unresolvedReason(d)
			rows = append(rows, row)
			continue
		}
		if sr.MAC != nil {
			row.DeviceMAC = *sr.MAC
		}
		p := sr.SwitchPort[0] // engine-ranked: lowest fan-out = the true edge port
		row.SwitchName = p.SwitchName
		if p.SwitchIP != nil {
			row.SwitchIP = *p.SwitchIP
		}
		if p.IfName != nil {
			row.Port = *p.IfName
		}
		if p.IfIndex != nil {
			row.PortIndex = strconv.Itoa(int(*p.IfIndex))
		}
		if p.IfAlias != nil {
			row.PortDescr = *p.IfAlias
		}
		row.VLAN, row.VLANName = portMapVLAN(p.UntaggedVLAN, p.UntaggedVLANName, p.VLANID, p.VLANSuspect, p.VLANName)
		if len(p.TaggedVLANs) > 0 {
			tv := make([]string, 0, len(p.TaggedVLANs))
			for _, v := range p.TaggedVLANs {
				tv = append(tv, strconv.Itoa(int(v)))
			}
			row.TaggedVLANs = strings.Join(tv, ",")
		}
		if p.MACCount != nil {
			row.MACsOnPort = strconv.FormatInt(*p.MACCount, 10)
		}
		row.Confidence = sr.Confidence
		row.Evidence = strings.Join(sr.ConfidenceReasons, "; ")
		rows = append(rows, row)
	}

	// Stable, human order: by switch, then by port index numerically, then device.
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].SwitchName != rows[j].SwitchName {
			// Unresolved rows (no switch) sort last rather than first.
			if rows[i].SwitchName == "" {
				return false
			}
			if rows[j].SwitchName == "" {
				return true
			}
			return rows[i].SwitchName < rows[j].SwitchName
		}
		a, _ := strconv.Atoi(rows[i].PortIndex)
		b, _ := strconv.Atoi(rows[j].PortIndex)
		if a != b {
			return a < b
		}
		return rows[i].DeviceName < rows[j].DeviceName
	})

	all := make([][]string, 0, len(rows))
	resolved := make([][]string, 0, len(rows))
	unresolved := make([][]string, 0)
	for _, r := range rows {
		all = append(all, r.cells())
		if r.SwitchName != "" {
			resolved = append(resolved, r.cells())
		} else {
			unresolved = append(unresolved, r.cells())
		}
	}
	// Per-switch port utilisation, so the report also answers "how full is this
	// switch" without a second query.
	type swAgg struct {
		name, ip string
		ports    map[string]bool
		devices  int
	}
	aggs := map[string]*swAgg{}
	for _, r := range rows {
		if r.SwitchName == "" {
			continue
		}
		a := aggs[r.SwitchName]
		if a == nil {
			a = &swAgg{name: r.SwitchName, ip: r.SwitchIP, ports: map[string]bool{}}
			aggs[r.SwitchName] = a
		}
		a.devices++
		if r.Port != "" {
			a.ports[r.Port] = true
		}
	}
	names := make([]string, 0, len(aggs))
	for n := range aggs {
		names = append(names, n)
	}
	sort.Strings(names)
	swRows := make([][]string, 0, len(names))
	for _, n := range names {
		a := aggs[n]
		swRows = append(swRows, []string{a.name, a.ip, strconv.Itoa(a.devices), strconv.Itoa(len(a.ports))})
	}

	return []reports.Sheet{
		{Name: "Port Map", Headers: portMapHeaders, Rows: all},
		{Name: "Resolved", Headers: portMapHeaders, Rows: resolved},
		{Name: "Not Resolved", Headers: portMapHeaders, Rows: unresolved},
		{Name: "By Switch", Headers: []string{"Switch", "Switch IP", "Devices attached", "Ports in use"}, Rows: swRows},
	}, nil
}

// unresolvedReason explains, in the operator's terms, why a device has no
// switch/port — never a bare blank.
func unresolvedReason(d db.Device) string {
	if d.PrimaryIp == nil || !d.PrimaryIp.IsValid() {
		return "device has no IP, so it cannot be matched to a MAC or a switch port"
	}
	switch d.Category {
	case "virtual_machine", "vm":
		return "virtual machine — attaches through its hypervisor's uplink, not a physical access port"
	case "access_point", "wireless_client":
		return "wireless — see the wireless controller association rather than a switch port"
	}
	return "no switch learned this device's MAC: the access switch has no SNMP credential, its FDB/ARP has not been collected, or the device was not on the network at collection time"
}
