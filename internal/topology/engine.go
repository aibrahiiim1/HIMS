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
	DeleteStaleTopologyLinks(ctx context.Context, before time.Time) (int64, error)
	ListNeighbors(ctx context.Context, deviceID uuid.UUID) ([]db.Neighbor, error)
	// Path Finder enrichment (source + last-seen attribution, upstream walk).
	GetDevice(ctx context.Context, id uuid.UUID) (db.Device, error)
	ListMACForDevice(ctx context.Context, deviceID uuid.UUID) ([]db.ListMACForDeviceRow, error)
	ListARPForDevice(ctx context.Context, deviceID uuid.UUID) ([]db.ListARPForDeviceRow, error)
	// IP→MAC fallback via the wireless-client roster + AP inventory (used when the
	// ARP table has no entry for the searched IP).
	ResolveIPToMAC(ctx context.Context, ip string) ([]db.ResolveIPToMACRow, error)
	// FDB-based (vendor-neutral) link inference.
	ListAllDevices(ctx context.Context) ([]db.Device, error)
	ListFabricInterfaceMACs(ctx context.Context) ([]db.ListFabricInterfaceMACsRow, error)
	MACCountByPort(ctx context.Context, deviceID uuid.UUID) ([]db.MACCountByPortRow, error)
	// Wireless association (Path Finder: start the path at the AP a client is on).
	FindWirelessClient(ctx context.Context, term string) ([]db.FindWirelessClientRow, error)
	GetAccessPointByName(ctx context.Context, arg db.GetAccessPointByNameParams) (db.GetAccessPointByNameRow, error)
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
	Role       string     `json:"role"` // wireless_client|ap|wireless_controller|endpoint|access|uplink|distribution|core|gateway|firewall
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
	// Wireless association (set only when the searched endpoint is a Wi-Fi client):
	// the path then starts at the AP the client is on, then AP → controller → wired
	// uplink switch → core.
	Wireless *WirelessTrace `json:"wireless,omitempty"`
}

// WirelessTrace describes a searched endpoint's wireless association — the client,
// the AP it is associated to, and the controller — so the path can start at the AP
// (where the client gets the network) rather than at a wired switch port.
type WirelessTrace struct {
	ClientMAC          string     `json:"client_mac,omitempty"`
	ClientIP           *string    `json:"client_ip,omitempty"`
	Hostname           *string    `json:"hostname,omitempty"`
	SSID               *string    `json:"ssid,omitempty"`
	Band               *string    `json:"band,omitempty"`
	RSSI               *int32     `json:"rssi,omitempty"`
	APName             string     `json:"ap_name,omitempty"`
	APMAC              *string    `json:"ap_mac,omitempty"`
	APIP               *string    `json:"ap_ip,omitempty"`
	ControllerDeviceID *uuid.UUID `json:"controller_device_id,omitempty"`
	ControllerName     *string    `json:"controller_name,omitempty"`
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

// GraphNode is a device in the topology graph with its inferred layer + degree.
type GraphNode struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	IP       *string   `json:"ip,omitempty"`
	Category string    `json:"category"`
	Layer    string    `json:"layer"`  // core|distribution|access|edge|gateway|wireless|host|endpoint
	Degree   int       `json:"degree"` // distinct neighbors
}

// GraphEdge is a deduplicated, undirected topology link with a confidence rating.
type GraphEdge struct {
	SourceID   uuid.UUID `json:"source_id"`
	TargetID   uuid.UUID `json:"target_id"`
	Source     string    `json:"source"`     // lldp|cdp|mac|arp
	Confidence string    `json:"confidence"` // high|medium|low
	IfName     *string   `json:"if_name,omitempty"`
}

// Graph is the topology graph for the map UI: layer-classified nodes,
// deduplicated confidence-rated edges, and per-layer counts.
type Graph struct {
	Nodes  []GraphNode    `json:"nodes"`
	Edges  []GraphEdge    `json:"edges"`
	Layers map[string]int `json:"layers"`
}

// Graph builds the layer-aware topology graph from resolved links.
func (e *Engine) Graph(ctx context.Context) (Graph, error) {
	rows, err := e.q.ListAllTopologyLinks(ctx)
	if err != nil {
		return Graph{}, err
	}
	type nodeAcc struct {
		node      GraphNode
		neighbors map[uuid.UUID]bool
	}
	nodes := map[uuid.UUID]*nodeAcc{}
	ensure := func(id uuid.UUID, name, cat string, ip *netip.Addr) *nodeAcc {
		n, ok := nodes[id]
		if !ok {
			n = &nodeAcc{node: GraphNode{ID: id, Name: name, Category: cat}, neighbors: map[uuid.UUID]bool{}}
			if ip != nil && ip.IsValid() {
				s := ip.String()
				n.node.IP = &s
			}
			nodes[id] = n
		}
		return n
	}

	edgeKey := map[string]*GraphEdge{}
	for _, r := range rows {
		if r.RemoteDeviceID == nil {
			continue // only managed-to-managed edges form the graph
		}
		ln := ensure(r.LocalDeviceID, r.LocalName, r.LocalCategory, r.LocalIp)
		rname := ""
		if r.RemoteName != nil {
			rname = *r.RemoteName
		}
		rcat := ""
		if r.RemoteCategory != nil {
			rcat = *r.RemoteCategory
		}
		rn := ensure(*r.RemoteDeviceID, rname, rcat, r.RemoteIpCol)
		ln.neighbors[*r.RemoteDeviceID] = true
		rn.neighbors[r.LocalDeviceID] = true

		// Dedup the undirected edge (A→B and B→A collapse); keep best confidence.
		a, b := r.LocalDeviceID, *r.RemoteDeviceID
		if a.String() > b.String() {
			a, b = b, a
		}
		key := a.String() + "|" + b.String()
		conf := linkConfidence(r.LinkSource, r.LastSeenAt)
		if ex, ok := edgeKey[key]; !ok {
			edgeKey[key] = &GraphEdge{SourceID: a, TargetID: b, Source: r.LinkSource, Confidence: conf, IfName: r.LocalIfName}
		} else if confRank(conf) > confRank(ex.Confidence) {
			ex.Confidence, ex.Source = conf, r.LinkSource
		}
	}

	// Max degree among switch-like nodes drives core/distribution detection.
	maxSwitchDeg := 0
	for _, n := range nodes {
		n.node.Degree = len(n.neighbors)
		if isSwitchLike(n.node.Category) && n.node.Degree > maxSwitchDeg {
			maxSwitchDeg = n.node.Degree
		}
	}

	g := Graph{Layers: map[string]int{}}
	for _, n := range nodes {
		n.node.Layer = classifyLayer(n.node.Category, n.node.Degree, maxSwitchDeg)
		g.Layers[n.node.Layer]++
		g.Nodes = append(g.Nodes, n.node)
	}
	for _, e := range edgeKey {
		g.Edges = append(g.Edges, *e)
	}
	return g, nil
}

// CleanupStaleLinks removes topology links not re-seen since the cutoff.
func (e *Engine) CleanupStaleLinks(ctx context.Context, olderThan time.Duration) (int64, error) {
	return e.q.DeleteStaleTopologyLinks(ctx, time.Now().UTC().Add(-olderThan))
}

func isSwitchLike(cat string) bool {
	switch cat {
	case "switch", "router", "unknown", "":
		return true
	}
	return false
}

// classifyLayer infers a device's topology layer from category + link degree.
func classifyLayer(cat string, degree, maxSwitchDeg int) string {
	switch cat {
	case "firewall":
		return "edge"
	case "router", "gateway":
		return "gateway"
	case "wireless_controller", "access_point":
		return "wireless"
	case "server", "virtual_host", "storage", "database", "printer", "ups", "camera", "nvr", "pbx":
		return "host"
	}
	// switch / unknown: layer by connectivity degree.
	switch {
	case maxSwitchDeg >= 2 && degree >= maxSwitchDeg:
		return "core"
	case degree >= 2:
		return "distribution"
	default:
		return "access"
	}
}

func confRank(c string) int {
	switch c {
	case "high":
		return 2
	case "medium":
		return 1
	default:
		return 0
	}
}

// linkConfidence rates a link from its evidence source and freshness:
// LLDP/CDP are authoritative (high); MAC-derived medium; ARP low; any link not
// seen in over 7 days is downgraded one step.
func linkConfidence(source string, lastSeen time.Time) string {
	rank := 0
	switch source {
	case "lldp", "cdp":
		rank = 2
	case "mac":
		rank = 1
	default: // arp / unknown
		rank = 0
	}
	if time.Since(lastSeen) > 7*24*time.Hour {
		rank--
	}
	switch {
	case rank >= 2:
		return "high"
	case rank == 1:
		return "medium"
	default:
		return "low"
	}
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

	// Step 2b: ARP fallback — if no ARP entry mapped this IP to a MAC (e.g. ARP
	// collection is sparse or absent), resolve it from the wireless-client roster
	// or the AP inventory. The resolved MAC then flows through the same FDB lookup
	// below, so a wireless endpoint or AP still traces to its switch port.
	var resolvedVia string
	if res.MAC == nil {
		if rows, rerr := e.q.ResolveIPToMAC(ctx, ip.String()); rerr == nil && len(rows) > 0 {
			// FDB stores MACs lowercase-colon; AP/client rosters may store them
			// uppercase. Fold to lowercase so the exact-match FDB lookup below hits.
			mac := strings.ToLower(rows[0].Mac)
			res.MAC = &mac
			resolvedVia = rows[0].Source
			if res.DeviceName == nil && rows[0].DeviceName != "" {
				dn := rows[0].DeviceName
				res.DeviceName = &dn
			}
		}
	}

	// Step 3: MAC → switch + port + VLAN, then the upstream path.
	if res.MAC != nil {
		res.SwitchPort = e.macToPort(ctx, *res.MAC)
	}
	// If this endpoint is a Wi-Fi client, start the path at the AP it's on.
	res.Wireless = e.wirelessAssociation(ctx, ip.String())
	if res.Wireless != nil {
		res.Path = e.buildWirelessPath(ctx, &res)
	} else {
		res.Path = e.buildPath(ctx, &res)
	}
	res.Confidence, res.ConfidenceReasons = assessConfidence(&res)
	applyWirelessConfidence(&res)
	if resolvedVia != "" {
		via := map[string]string{
			"wireless_client": "the wireless-client roster",
			"access_point":    "the access-point inventory",
		}[resolvedVia]
		if via == "" {
			via = resolvedVia
		}
		res.ConfidenceReasons = append([]string{
			"IP resolved to its MAC via " + via + " (no ARP entry for this IP)",
		}, res.ConfidenceReasons...)
	}
	res.normalizeSlices()
	return res, nil
}

// SearchMAC resolves a MAC address to the switch port(s) that carry it.
func (e *Engine) SearchMAC(ctx context.Context, mac string) (SearchResult, error) {
	res := SearchResult{Query: mac, QueryType: "mac", MAC: &mac}
	res.SwitchPort = e.macToPort(ctx, mac)
	// If this MAC belongs to a Wi-Fi client, start the path at its AP.
	res.Wireless = e.wirelessAssociation(ctx, mac)
	if res.Wireless != nil {
		res.Path = e.buildWirelessPath(ctx, &res)
	} else {
		res.Path = e.buildPath(ctx, &res)
	}
	res.Confidence, res.ConfidenceReasons = assessConfidence(&res)
	applyWirelessConfidence(&res)
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
	// A searched name might be a wireless client's hostname rather than a managed
	// device — trace it from its AP.
	if len(out) == 0 {
		if w := e.wirelessAssociation(ctx, name); w != nil {
			sr := SearchResult{Query: name, QueryType: "hostname", Wireless: w}
			if w.ClientMAC != "" {
				m := w.ClientMAC
				sr.MAC = &m
			}
			sr.Path = e.buildWirelessPath(ctx, &sr)
			sr.Confidence, sr.ConfidenceReasons = assessConfidence(&sr)
			applyWirelessConfidence(&sr)
			sr.normalizeSlices()
			out = append(out, sr)
		}
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

// wirelessAssociation resolves a search term (IP / MAC / hostname) to an associated
// Wi-Fi client and the AP + controller it is on. Returns nil when the term is not a
// wireless client with a known AP — in which case the normal wired path applies.
func (e *Engine) wirelessAssociation(ctx context.Context, term string) *WirelessTrace {
	if strings.TrimSpace(term) == "" {
		return nil
	}
	rows, err := e.q.FindWirelessClient(ctx, term)
	if err != nil || len(rows) == 0 {
		return nil
	}
	c := rows[0]
	wt := &WirelessTrace{ClientMAC: c.Mac, APName: c.ApName, RSSI: c.Rssi}
	if c.Ip != "" {
		ip := c.Ip
		wt.ClientIP = &ip
	}
	if c.Hostname != "" {
		h := c.Hostname
		wt.Hostname = &h
	}
	if c.Ssid != "" {
		s := c.Ssid
		wt.SSID = &s
	}
	if c.Band != "" {
		b := c.Band
		wt.Band = &b
	}
	cid := c.ControllerDeviceID
	wt.ControllerDeviceID = &cid
	wt.ControllerName = c.ControllerName
	// Resolve the AP row for its MAC/IP, used to find its wired uplink via the FDB.
	if ap, aerr := e.q.GetAccessPointByName(ctx, db.GetAccessPointByNameParams{
		ControllerDeviceID: c.ControllerDeviceID, Name: c.ApName,
	}); aerr == nil {
		wt.APMAC = ap.Mac
		if ap.Ip != "" {
			apip := ap.Ip
			wt.APIP = &apip
		}
	}
	return wt
}

// buildWirelessPath assembles a Wi-Fi endpoint's path: client → AP → controller →
// (the AP's wired uplink switch, resolved from the AP's MAC via the FDB) → upstream
// walk to the edge. The AP→switch hop is FDB-derived (the only signal available); if
// the AP's MAC is not learned on any switch, it falls back to the client's own FDB
// match, and if neither resolves the path stops at the controller (flagged in the
// confidence reasons).
func (e *Engine) buildWirelessPath(ctx context.Context, res *SearchResult) []PathStep {
	w := res.Wireless
	if w == nil {
		return e.buildPath(ctx, res)
	}
	steps := []PathStep{}
	hop := 0
	wsrc := "wireless"

	// Endpoint: the wireless client.
	client := PathStep{Hop: hop, Role: "wireless_client", IP: w.ClientIP, Source: &wsrc}
	if w.Hostname != nil {
		client.DeviceName = w.Hostname
	}
	steps = append(steps, client)
	hop++

	// AP hop (where the client gets the network).
	apName := w.APName
	ap := PathStep{Hop: hop, Role: "ap", DeviceName: &apName, IP: w.APIP, Source: &wsrc}
	steps = append(steps, ap)
	hop++

	// Controller hop (the managed wireless controller device).
	if w.ControllerDeviceID != nil {
		ctrl := PathStep{Hop: hop, Role: "wireless_controller", DeviceID: w.ControllerDeviceID, DeviceName: w.ControllerName, Source: &wsrc}
		steps = append(steps, ctrl)
		hop++
	}

	// Wired uplink: the switch port that learned the AP's MAC (FDB). Fall back to the
	// client's own FDB match (already resolved by the caller) when the AP MAC isn't
	// in any switch table.
	var ports []SwitchPortEntry
	if w.APMAC != nil && *w.APMAC != "" {
		ports = e.macToPort(ctx, strings.ToLower(*w.APMAC))
	}
	if len(ports) == 0 {
		ports = res.SwitchPort
	}
	if len(ports) > 0 {
		res.SwitchPort = ports
		acc := ports[0]
		v := acc.VLANID
		msrc := "mac"
		if acc.Source != nil {
			msrc = *acc.Source
		}
		steps = append(steps, PathStep{
			Hop: hop, Role: "access",
			DeviceID: &acc.SwitchID, DeviceName: &acc.SwitchName, IP: acc.SwitchIP,
			IfIndex: acc.IfIndex, IfName: acc.IfName, VLANID: &v, PortRole: acc.PortRole, Source: &msrc,
		})
		hop++

		// Walk upstream from the access switch toward the edge.
		visited := map[uuid.UUID]bool{acc.SwitchID: true}
		cur := acc.SwitchID
		for i := 0; i < 6; i++ {
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
				break
			}
		}
	}
	return steps
}

// applyWirelessConfidence augments the confidence reasons for a wireless trace and
// ensures a located client+AP is never reported as "none".
func applyWirelessConfidence(res *SearchResult) {
	if res.Wireless == nil {
		return
	}
	reasons := []string{"Endpoint is a Wi-Fi client associated to AP " + res.Wireless.APName + " — the path starts at the access point."}
	if len(res.SwitchPort) == 0 {
		reasons = append(reasons, "AP wired uplink not found in any switch MAC table — showing client → AP → controller only.")
		if res.Confidence == "" || res.Confidence == "none" {
			res.Confidence = "low"
		}
	}
	res.ConfidenceReasons = append(reasons, res.ConfidenceReasons...)
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

// fdbFabricCats are the device classes that form the switched fabric. FDB-based
// link inference only connects these to each other — endpoints, servers, APs
// etc. that merely appear in a forwarding table must not become topology nodes.
var fdbFabricCats = map[string]bool{"switch": true, "router": true, "isp_router": true}

// fdbEdgePortMaxMACs caps how many MAC addresses a port may carry to be treated
// as a DIRECT edge / point-to-point attachment in FDB inference. Above this the
// port is an aggregation trunk where the device is merely *downstream*, not
// directly attached — those are skipped so the map isn't polluted with guessed
// transit links. (Real edge/access ports carry a handful to a few dozen MACs;
// uplinks/trunks carry hundreds.)
const fdbEdgePortMaxMACs = 64

// normMAC lowercases a MAC and strips any separators so values from different
// sources/vendors (aa:bb:.., AA-BB-.., aabb..) compare equal.
func normMAC(s string) string {
	var b strings.Builder
	b.Grow(12)
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
			b.WriteRune(r)
		case r >= 'A' && r <= 'F':
			b.WriteRune(r + 32)
		}
	}
	return b.String()
}

// fdbPairKey returns an order-independent key for an unordered device pair.
func fdbPairKey(a, b uuid.UUID) [2]uuid.UUID {
	if a.String() > b.String() {
		a, b = b, a
	}
	return [2]uuid.UUID{a, b}
}

// fdbCand is one observation "switch S learned device D's MAC on port P, and P
// carries macCount MACs in total".
type fdbCand struct {
	switchID uuid.UUID
	ifIndex  *int32
	ifName   *string
	macCount int64
}

// InferLinksFromFDB derives vendor-neutral switch-to-switch topology links from
// the bridge MAC forwarding tables (FDB/CAM), filling the gaps LLDP/CDP can't —
// notably cross-vendor uplinks (e.g. a Cisco access switch on an Aruba/HPE port)
// where the two ends speak different neighbor-discovery protocols and never see
// each other.
//
// For every fabric device D, each switch S whose FDB learned one of D's
// interface MACs is a candidate; D is taken to attach directly to the S whose
// port-toward-D carries the FEWEST MACs (the closest, edge-like port). Trunk
// ports (high fan-out — D is merely downstream) and pairs already connected by
// authoritative LLDP/CDP are skipped, so this only adds genuine, edge-like links
// and never overrides protocol evidence. Stored with link_source='mac'; the
// graph rates these medium-confidence. Returns the number of links upserted.
func (e *Engine) InferLinksFromFDB(ctx context.Context) (int, error) {
	devs, err := e.q.ListAllDevices(ctx)
	if err != nil {
		return 0, err
	}
	nameByID := make(map[uuid.UUID]string, len(devs))
	isFabric := make(map[uuid.UUID]bool, len(devs))
	for _, d := range devs {
		nameByID[d.ID] = d.Name
		if fdbFabricCats[d.Category] {
			isFabric[d.ID] = true
		}
	}

	// MAC → owning fabric device (only fabric MACs, so endpoints never link).
	ownerRows, err := e.q.ListFabricInterfaceMACs(ctx)
	if err != nil {
		return 0, err
	}
	macOwner := make(map[string]uuid.UUID, len(ownerRows))
	for _, r := range ownerRows {
		if r.Mac == nil {
			continue
		}
		if nm := normMAC(*r.Mac); len(nm) == 12 {
			macOwner[nm] = r.DeviceID
		}
	}

	// Pairs already connected by authoritative LLDP/CDP — never override these.
	authPair := map[[2]uuid.UUID]bool{}
	allLinks, err := e.q.ListAllTopologyLinks(ctx)
	if err != nil {
		return 0, err
	}
	for _, l := range allLinks {
		if l.RemoteDeviceID != nil && (l.LinkSource == "lldp" || l.LinkSource == "cdp") {
			authPair[fdbPairKey(l.LocalDeviceID, *l.RemoteDeviceID)] = true
		}
	}

	// For each fabric switch S, find — per target device — the smallest-fan-out
	// port of S that learned that target's MAC.
	candidates := map[uuid.UUID][]fdbCand{} // target device → candidate attach points
	for _, d := range devs {
		if !isFabric[d.ID] {
			continue
		}
		counts, cerr := e.q.MACCountByPort(ctx, d.ID)
		if cerr != nil {
			continue
		}
		portCount := map[int32]int64{}
		for _, c := range counts {
			if c.IfIndex != nil {
				portCount[*c.IfIndex] = c.MacCount
			}
		}
		fdb, ferr := e.q.ListMACForDevice(ctx, d.ID)
		if ferr != nil {
			continue
		}
		best := map[uuid.UUID]fdbCand{} // target → smallest port on THIS switch
		for _, m := range fdb {
			if m.IfIndex == nil {
				continue
			}
			owner, ok := macOwner[normMAC(m.Mac)]
			if !ok || owner == d.ID || !isFabric[owner] {
				continue
			}
			cnt := portCount[*m.IfIndex]
			if cur, seen := best[owner]; !seen || cnt < cur.macCount {
				best[owner] = fdbCand{switchID: d.ID, ifIndex: m.IfIndex, ifName: m.IfName, macCount: cnt}
			}
		}
		for owner, c := range best {
			candidates[owner] = append(candidates[owner], c)
		}
	}

	// Pick, per target, the closest switch (fewest MACs on the port toward it),
	// then dedupe reciprocal observations of the same physical link — keeping the
	// end whose port has the smaller fan-out, which best attributes the port.
	chosen := map[[2]uuid.UUID]db.UpsertTopologyLinkParams{}
	chosenCnt := map[[2]uuid.UUID]int64{}
	for target, cands := range candidates {
		best := cands[0]
		for _, c := range cands[1:] {
			if c.macCount < best.macCount {
				best = c
			}
		}
		if best.macCount > fdbEdgePortMaxMACs {
			continue // only edge-like (point-to-point/access) attachments
		}
		pk := fdbPairKey(best.switchID, target)
		if authPair[pk] {
			continue // already proven by LLDP/CDP
		}
		if prev, ok := chosenCnt[pk]; ok && prev <= best.macCount {
			continue // a stronger (lower fan-out) observation already won this pair
		}
		name := nameByID[target]
		tgt := target
		chosen[pk] = db.UpsertTopologyLinkParams{
			LocalDeviceID:  best.switchID,
			LocalIfIndex:   best.ifIndex,
			LocalIfName:    best.ifName,
			RemoteDeviceID: &tgt,
			RemoteSysName:  &name,
			LinkSource:     "mac",
			LastSeenAt:     time.Now().UTC(),
		}
		chosenCnt[pk] = best.macCount
	}

	built := 0
	for _, arg := range chosen {
		if err := e.q.UpsertTopologyLink(ctx, arg); err == nil {
			built++
		}
	}
	return built, nil
}
