// Package topology builds network topology links from multi-source evidence
// (LLDP/CDP neighbors + MAC FDB + ARP) and answers path/search queries.
//
// Building links:
//
//	BuildLinks reads neighbors, MAC, ARP tables and upserts topology_links.
//
// Searching:
//
//	ResolveIP: IP → ARP entry → MAC → switch+port+VLAN → uplink path.
//	ResolveMAC: MAC → switch+port+VLAN.
//	ResolveHostname: hostname/name → device row → same path.
package topology

import (
	"context"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// Querier is the subset of db.Queries the topology engine uses.
type Querier interface {
	FindMACByIP(ctx context.Context, ip netip.Addr) ([]db.FindMACByIPRow, error)
	FindMACOnSwitches(ctx context.Context, mac string) ([]db.FindMACOnSwitchesRow, error)
	SearchByIP(ctx context.Context, ip *netip.Addr) (db.SearchByIPRow, error)
	SearchByHostname(ctx context.Context, search *string) ([]db.SearchByHostnameRow, error)
	SearchByMAC(ctx context.Context, mac string) ([]db.SearchByMACRow, error)
	ListTopologyLinks(ctx context.Context, localDeviceID uuid.UUID) ([]db.TopologyLink, error)
	ListAllTopologyLinks(ctx context.Context) ([]db.ListAllTopologyLinksRow, error)
	UpsertTopologyLink(ctx context.Context, arg db.UpsertTopologyLinkParams) error
	ListNeighbors(ctx context.Context, deviceID uuid.UUID) ([]db.Neighbor, error)
	// Path Finder enrichment (source + last-seen attribution, upstream walk).
	GetDevice(ctx context.Context, id uuid.UUID) (db.Device, error)
	ListMACForDevice(ctx context.Context, deviceID uuid.UUID) ([]db.ListMACForDeviceRow, error)
	ListARPForDevice(ctx context.Context, deviceID uuid.UUID) ([]db.ListARPForDeviceRow, error)
}

// Link is a directed network link for API/UI consumption.
type Link struct {
	LocalDeviceID    uuid.UUID  `json:"local_device_id"`
	LocalDeviceName  string     `json:"local_device_name"`
	LocalIP          *string    `json:"local_ip,omitempty"`
	LocalIfIndex     *int32     `json:"local_if_index,omitempty"`
	LocalIfName      *string    `json:"local_if_name,omitempty"`
	RemoteDeviceID   *uuid.UUID `json:"remote_device_id,omitempty"`
	RemoteDeviceName *string    `json:"remote_device_name,omitempty"`
	RemoteIP         *string    `json:"remote_ip,omitempty"`
	RemoteSysName    *string    `json:"remote_sys_name,omitempty"`
	Source           string     `json:"link_source"`
}

// PathStep is one hop in an endpoint → core path. Role labels the hop so the UI
// can render the chain (endpoint → access → uplink → core → firewall/gateway).
type PathStep struct {
	Hop        int        `json:"hop"`
	Role       string     `json:"role"` // endpoint|access|uplink|distribution|core|gateway|firewall
	DeviceID   *uuid.UUID `json:"device_id,omitempty"`
	DeviceName *string    `json:"device_name,omitempty"`
	IP         *string    `json:"ip,omitempty"`
	IfIndex    *int32     `json:"if_index,omitempty"`
	IfName     *string    `json:"if_name,omitempty"`
	VLANID     *int32     `json:"vlan_id,omitempty"`
	PortRole   *string    `json:"port_role,omitempty"`
	Source     *string    `json:"source,omitempty"` // how this hop was derived: lldp|cdp|mac|arp|inventory
}

// SearchResult bundles the answer to any search query — the full L2 path with
// source attribution + a confidence assessment.
type SearchResult struct {
	Query      string     `json:"query"`
	QueryType  string     `json:"query_type"` // ip | mac | hostname
	MAC        *string    `json:"mac,omitempty"`
	DeviceID   *uuid.UUID `json:"device_id,omitempty"`
	DeviceName *string    `json:"device_name,omitempty"`
	// The switch + port the device is connected to (FDB matches).
	SwitchPort []SwitchPortEntry `json:"switch_port"`
	// Best-effort uplink path toward the core/gateway (LLDP/CDP/MAC evidence).
	Path []PathStep `json:"path"`
	// ARP attribution: which device's ARP table resolved IP→MAC, and how fresh.
	ARPDeviceID   *uuid.UUID `json:"arp_device_id,omitempty"`
	ARPDeviceName *string    `json:"arp_device_name,omitempty"`
	ARPSource     *string    `json:"arp_source,omitempty"`
	ARPLastSeen   *string    `json:"arp_last_seen,omitempty"`
	// Confidence in the resolved path.
	Confidence        string   `json:"confidence"` // high|medium|low|none
	ConfidenceReasons []string `json:"confidence_reasons"`
}

// SwitchPortEntry is one FDB match: a switch + port + VLAN that carries the MAC,
// with the collection source and freshness that produced the match.
type SwitchPortEntry struct {
	SwitchID   uuid.UUID `json:"switch_id"`
	SwitchName string    `json:"switch_name"`
	SwitchIP   *string   `json:"switch_ip,omitempty"`
	IfIndex    *int32    `json:"if_index,omitempty"`
	IfName     *string   `json:"if_name,omitempty"`
	VLANID     int32     `json:"vlan_id"`
	PortRole   *string   `json:"port_role,omitempty"`
	Source     *string   `json:"source,omitempty"`       // mac-table collection_source: snmp|cli|api
	LastSeenAt *string   `json:"last_seen_at,omitempty"` // mac-table last_seen_at (RFC3339)
}

// Engine wraps the Querier and provides topology operations.
type Engine struct {
	q Querier
}

// New creates an Engine over any Querier implementation.
func New(q Querier) *Engine { return &Engine{q: q} }

// AllLinks returns all topology links for the graph UI.
func (e *Engine) AllLinks(ctx context.Context) ([]Link, error) {
	rows, err := e.q.ListAllTopologyLinks(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Link, 0, len(rows))
	for _, r := range rows {
		lk := Link{
			LocalDeviceID:    r.LocalDeviceID,
			LocalDeviceName:  r.LocalName,
			Source:           r.LinkSource,
			LocalIfIndex:     r.LocalIfIndex,
			LocalIfName:      r.LocalIfName,
			RemoteDeviceID:   r.RemoteDeviceID,
			RemoteDeviceName: r.RemoteName,
			RemoteSysName:    r.RemoteSysName,
		}
		if r.LocalIp != nil && r.LocalIp.IsValid() {
			s := r.LocalIp.String()
			lk.LocalIP = &s
		}
		if r.RemoteIpCol != nil && r.RemoteIpCol.IsValid() {
			s := r.RemoteIpCol.String()
			lk.RemoteIP = &s
		}
		out = append(out, lk)
	}
	return out, nil
}

// SearchIP resolves an IP to the switch port it's connected to, with the full
// upstream path and source attribution.
func (e *Engine) SearchIP(ctx context.Context, ip netip.Addr) (SearchResult, error) {
	res := SearchResult{Query: ip.String(), QueryType: "ip"}

	// Step 1: Find the device row for this IP (might be a managed switch itself).
	dev, err := e.q.SearchByIP(ctx, &ip)
	if err == nil {
		res.DeviceID = &dev.ID
		res.DeviceName = &dev.Name
	}

	// Step 2: ARP → MAC, recording which device's ARP table resolved it.
	arpRows, err := e.q.FindMACByIP(ctx, ip)
	if err == nil && len(arpRows) > 0 {
		mac := arpRows[0].Mac
		res.MAC = &mac
		res.ARPDeviceID = &arpRows[0].DeviceID
		ls := arpRows[0].LastSeenAt.UTC().Format(time.RFC3339)
		res.ARPLastSeen = &ls
		e.attributeARP(ctx, &res, ip, mac, arpRows[0].DeviceID)
	}

	// Step 3: MAC → switch + port + VLAN, then the upstream path.
	if res.MAC != nil {
		res.SwitchPort = e.macToPort(ctx, *res.MAC)
	}
	res.Path = e.buildPath(ctx, &res)
	res.Confidence, res.ConfidenceReasons = assessConfidence(&res)
	res.normalizeSlices()
	return res, nil
}

// SearchMAC resolves a MAC address to the switch port(s) that carry it.
func (e *Engine) SearchMAC(ctx context.Context, mac string) (SearchResult, error) {
	res := SearchResult{Query: mac, QueryType: "mac", MAC: &mac}
	res.SwitchPort = e.macToPort(ctx, mac)
	res.Path = e.buildPath(ctx, &res)
	res.Confidence, res.ConfidenceReasons = assessConfidence(&res)
	res.normalizeSlices()
	return res, nil
}

// normalizeSlices guarantees the result's slices serialize as JSON arrays (never
// null), so the UI can safely call .length/.map without null checks.
func (r *SearchResult) normalizeSlices() {
	if r.SwitchPort == nil {
		r.SwitchPort = []SwitchPortEntry{}
	}
	if r.Path == nil {
		r.Path = []PathStep{}
	}
	if r.ConfidenceReasons == nil {
		r.ConfidenceReasons = []string{}
	}
}

// SearchHostname resolves a device name or hostname.
func (e *Engine) SearchHostname(ctx context.Context, name string) ([]SearchResult, error) {
	pattern := "%" + name + "%"
	rows, err := e.q.SearchByHostname(ctx, &pattern)
	if err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(rows))
	for _, r := range rows {
		sr := SearchResult{Query: name, QueryType: "hostname"}
		sr.DeviceID = &r.ID
		sr.DeviceName = &r.Name
		if r.PrimaryIp != nil && r.PrimaryIp.IsValid() {
			// Try to resolve the switch port for this device's IP.
			sub, _ := e.SearchIP(ctx, *r.PrimaryIp) //nolint:shadow
			sr.SwitchPort = sub.SwitchPort
			sr.MAC = sub.MAC
			sr.Path = sub.Path
			sr.ARPDeviceID, sr.ARPDeviceName = sub.ARPDeviceID, sub.ARPDeviceName
			sr.ARPSource, sr.ARPLastSeen = sub.ARPSource, sub.ARPLastSeen
			sr.Confidence, sr.ConfidenceReasons = sub.Confidence, sub.ConfidenceReasons
		}
		sr.normalizeSlices()
		out = append(out, sr)
	}
	return out, nil
}

// macToPort looks up the switch+port+VLAN for a MAC address and attributes each
// match with its FDB collection source + freshness (from the MAC table). The
// access-edge port (port_role access/edge) is sorted first so the path starts
// at the true point of attachment rather than an uplink/trunk that also learned
// the MAC.
func (e *Engine) macToPort(ctx context.Context, mac string) []SwitchPortEntry {
	rows, err := e.q.SearchByMAC(ctx, mac)
	if err != nil {
		return nil
	}
	// Cache per-device MAC tables so we attribute source/last-seen without
	// re-querying for MACs that appear on the same switch multiple times.
	macCache := map[uuid.UUID][]db.ListMACForDeviceRow{}
	out := make([]SwitchPortEntry, 0, len(rows))
	for _, r := range rows {
		entry := SwitchPortEntry{
			SwitchID:   r.DeviceID,
			SwitchName: r.SwitchName,
			VLANID:     r.VlanID,
		}
		if r.SwitchIp != nil && r.SwitchIp.IsValid() {
			s := r.SwitchIp.String()
			entry.SwitchIP = &s
		}
		entry.IfIndex = r.IfIndex
		entry.IfName = r.IfName
		entry.PortRole = r.PortRole

		mt, ok := macCache[r.DeviceID]
		if !ok {
			mt, _ = e.q.ListMACForDevice(ctx, r.DeviceID)
			macCache[r.DeviceID] = mt
		}
		for _, m := range mt {
			if m.VlanID == r.VlanID && strings.EqualFold(m.Mac, mac) {
				src := m.CollectionSource
				ls := m.LastSeenAt.UTC().Format(time.RFC3339)
				entry.Source = &src
				entry.LastSeenAt = &ls
				break
			}
		}
		out = append(out, entry)
	}
	// Access/edge ports first, then most-recently-seen.
	sortAccessFirst(out)
	return out
}

// sortAccessFirst orders FDB matches so access/edge ports lead, then freshest.
func sortAccessFirst(p []SwitchPortEntry) {
	rank := func(e SwitchPortEntry) int {
		if e.PortRole == nil {
			return 2
		}
		switch *e.PortRole {
		case "access", "edge":
			return 0
		case "uplink", "trunk":
			return 3
		default:
			return 1
		}
	}
	for i := 1; i < len(p); i++ {
		for j := i; j > 0; j-- {
			a, b := p[j-1], p[j]
			swap := rank(b) < rank(a)
			if rank(a) == rank(b) && b.LastSeenAt != nil && a.LastSeenAt != nil {
				swap = *b.LastSeenAt > *a.LastSeenAt
			}
			if !swap {
				break
			}
			p[j-1], p[j] = p[j], p[j-1]
		}
	}
}

// attributeARP records which device's ARP table resolved the IP→MAC mapping and
// the collection source behind it.
func (e *Engine) attributeARP(ctx context.Context, res *SearchResult, ip netip.Addr, mac string, arpDevID uuid.UUID) {
	if dev, err := e.q.GetDevice(ctx, arpDevID); err == nil {
		res.ARPDeviceName = &dev.Name
	}
	rows, err := e.q.ListARPForDevice(ctx, arpDevID)
	if err != nil {
		return
	}
	for _, a := range rows {
		if a.IpAddress == ip && strings.EqualFold(a.Mac, mac) {
			src := a.CollectionSource
			res.ARPSource = &src
			ls := a.LastSeenAt.UTC().Format(time.RFC3339)
			res.ARPLastSeen = &ls
			return
		}
	}
}

// buildPath assembles the endpoint → access → upstream → gateway chain. The
// endpoint is the searched host; the access switch is the access-edge FDB match;
// upstream hops are followed via topology links (LLDP/CDP, then MAC/ARP) until a
// firewall/router/gateway is reached or no further managed neighbor exists.
func (e *Engine) buildPath(ctx context.Context, res *SearchResult) []PathStep {
	if len(res.SwitchPort) == 0 {
		return nil
	}
	steps := []PathStep{}
	hop := 0

	// Endpoint hop.
	endpoint := PathStep{Hop: hop, Role: "endpoint", DeviceID: res.DeviceID, DeviceName: res.DeviceName}
	if res.QueryType == "ip" {
		ipc := res.Query
		endpoint.IP = &ipc
	}
	if res.ARPSource != nil {
		src := "arp"
		endpoint.Source = &src
	}
	steps = append(steps, endpoint)
	hop++

	// Access switch hop (first, access-sorted entry).
	acc := res.SwitchPort[0]
	v := acc.VLANID
	src := "mac"
	if acc.Source != nil {
		src = *acc.Source
	}
	accStep := PathStep{
		Hop: hop, Role: "access",
		DeviceID: &acc.SwitchID, DeviceName: &acc.SwitchName, IP: acc.SwitchIP,
		IfIndex: acc.IfIndex, IfName: acc.IfName, VLANID: &v, PortRole: acc.PortRole, Source: &src,
	}
	steps = append(steps, accStep)
	hop++

	// Walk upstream from the access switch.
	visited := map[uuid.UUID]bool{acc.SwitchID: true}
	cur := acc.SwitchID
	for i := 0; i < 6; i++ { // hop cap guards against loops
		next, role, ok := e.nextUpstream(ctx, cur, visited)
		if !ok {
			break
		}
		visited[next.DeviceID] = true
		steps = append(steps, PathStep{
			Hop: hop, Role: role,
			DeviceID: &next.DeviceID, DeviceName: &next.DeviceName, IP: next.IP,
			IfName: next.LocalIfName, Source: &next.Source,
		})
		hop++
		cur = next.DeviceID
		if role == "firewall" || role == "gateway" {
			break // reached the edge
		}
	}
	return steps
}

// upstream is a resolved next hop in the path walk.
type upstream struct {
	DeviceID    uuid.UUID
	DeviceName  string
	IP          *string
	LocalIfName *string
	Source      string // lldp|cdp|mac|arp
}

// nextUpstream picks the best upstream neighbor of dev that hasn't been visited,
// preferring a firewall/router (the edge) then another switch.
func (e *Engine) nextUpstream(ctx context.Context, dev uuid.UUID, visited map[uuid.UUID]bool) (upstream, string, bool) {
	links, err := e.q.ListTopologyLinks(ctx, dev)
	if err != nil {
		return upstream{}, "", false
	}
	var best *upstream
	var bestRole string
	bestRank := 99
	for i := range links {
		l := links[i]
		if l.RemoteDeviceID == nil || visited[*l.RemoteDeviceID] {
			continue
		}
		rdev, err := e.q.GetDevice(ctx, *l.RemoteDeviceID)
		if err != nil {
			continue
		}
		role, rank := roleForCategory(rdev.Category)
		if rank < bestRank {
			u := upstream{DeviceID: rdev.ID, DeviceName: rdev.Name, LocalIfName: l.LocalIfName, Source: l.LinkSource}
			if rdev.PrimaryIp != nil && rdev.PrimaryIp.IsValid() {
				s := rdev.PrimaryIp.String()
				u.IP = &s
			}
			best = &u
			bestRole = role
			bestRank = rank
		}
	}
	if best == nil {
		return upstream{}, "", false
	}
	return *best, bestRole, true
}

// roleForCategory maps a device category to a path role + a preference rank
// (lower = closer to the edge, picked first).
func roleForCategory(cat string) (string, int) {
	switch cat {
	case "firewall":
		return "firewall", 0
	case "router", "gateway":
		return "gateway", 1
	case "switch":
		return "uplink", 2
	default:
		return "uplink", 3
	}
}

// assessConfidence rates how trustworthy the resolved path is, with reasons.
func assessConfidence(res *SearchResult) (string, []string) {
	reasons := []string{}
	if len(res.SwitchPort) == 0 {
		if res.DeviceID != nil {
			return "low", []string{"Device is known in inventory but no switch-port (FDB) match was found."}
		}
		return "none", []string{"No MAC/ARP/FDB evidence located this endpoint."}
	}
	acc := res.SwitchPort[0]
	score := 0
	if acc.PortRole != nil && (*acc.PortRole == "access" || *acc.PortRole == "edge") {
		score += 2
		reasons = append(reasons, "MAC learned on an access/edge port (true point of attachment).")
	} else {
		reasons = append(reasons, "MAC matched on a non-access port; attachment is best-effort.")
	}
	if acc.LastSeenAt != nil {
		if t, err := time.Parse(time.RFC3339, *acc.LastSeenAt); err == nil {
			age := time.Since(t)
			switch {
			case age < 24*time.Hour:
				score += 2
				reasons = append(reasons, "FDB entry seen within the last 24h.")
			case age < 7*24*time.Hour:
				score++
				reasons = append(reasons, "FDB entry seen within the last 7 days.")
			default:
				reasons = append(reasons, "FDB entry is stale (older than 7 days).")
			}
		}
	}
	hasLLDP := false
	for _, p := range res.Path {
		if p.Source != nil && (*p.Source == "lldp" || *p.Source == "cdp") {
			hasLLDP = true
			break
		}
	}
	if hasLLDP {
		score += 2
		reasons = append(reasons, "Upstream path corroborated by LLDP/CDP neighbors.")
	} else if len(res.Path) > 2 {
		score++
		reasons = append(reasons, "Upstream path derived from MAC/ARP evidence only.")
	}
	if res.ARPSource != nil {
		score++
		reasons = append(reasons, "IP→MAC confirmed by an ARP table entry.")
	}
	switch {
	case score >= 5:
		return "high", reasons
	case score >= 3:
		return "medium", reasons
	default:
		return "low", reasons
	}
}

// BuildLinks derives topology_links for one device from its LLDP/CDP neighbors,
// resolving each neighbor's management IP to a managed device so the link has a
// real remote_device_id (which the topology map + path walk require). Only
// resolved managed-to-managed links are persisted — unresolved neighbors stay
// visible in the per-device Neighbors view but don't create stub rows (which
// would accumulate as duplicates across rebuilds since NULL remote_device_id is
// distinct under the unique index). Returns the number of links upserted.
func (e *Engine) BuildLinks(ctx context.Context, deviceID uuid.UUID) (int, error) {
	neighbors, err := e.q.ListNeighbors(ctx, deviceID)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	built := 0
	for _, n := range neighbors {
		var rid *uuid.UUID
		var remIP *netip.Addr

		// 1) Resolve by management IP (most reliable).
		if n.RemMgmtIp != nil && n.RemMgmtIp.IsValid() {
			ip := *n.RemMgmtIp
			remIP = &ip
			if dev, derr := e.q.SearchByIP(ctx, &ip); derr == nil && dev.ID != uuid.Nil && dev.ID != deviceID {
				id := dev.ID
				rid = &id
			}
		}
		// 2) Fallback: resolve by remote system name (exact, case-insensitive).
		//    LLDP/CDP usually carries a sysName even when no mgmt IP is present.
		if rid == nil && n.RemSysName != nil && *n.RemSysName != "" {
			exact := *n.RemSysName
			if rows, herr := e.q.SearchByHostname(ctx, &exact); herr == nil {
				for _, d := range rows {
					if d.ID == deviceID {
						continue
					}
					if strings.EqualFold(d.Name, exact) || (d.Hostname != nil && strings.EqualFold(*d.Hostname, exact)) {
						id := d.ID
						rid = &id
						break
					}
				}
			}
		}
		if rid == nil {
			continue // unresolved neighbor — no managed device to link to
		}

		arg := db.UpsertTopologyLinkParams{
			LocalDeviceID:  deviceID,
			LocalIfIndex:   n.LocalIfIndex,
			LocalIfName:    n.LocalIfName,
			RemoteDeviceID: rid,
			RemoteIp:       remIP,
			RemoteSysName:  n.RemSysName,
			LinkSource:     n.Protocol,
			LastSeenAt:     now,
		}
		if err := e.q.UpsertTopologyLink(ctx, arg); err == nil {
			built++
		}
	}
	return built, nil
}

// RebuildAll rebuilds topology links for every supplied device. Returns the
// number of devices processed and the total links upserted.
func (e *Engine) RebuildAll(ctx context.Context, deviceIDs []uuid.UUID) (devicesProcessed, linksBuilt int) {
	for _, id := range deviceIDs {
		n, err := e.BuildLinks(ctx, id)
		if err != nil {
			continue
		}
		devicesProcessed++
		linksBuilt += n
	}
	return devicesProcessed, linksBuilt
}
