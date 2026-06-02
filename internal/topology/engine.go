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

// PathStep is one hop in an IP → path result.
type PathStep struct {
	DeviceID   *uuid.UUID `json:"device_id,omitempty"`
	DeviceName *string    `json:"device_name,omitempty"`
	IP         *string    `json:"ip,omitempty"`
	IfIndex    *int32     `json:"if_index,omitempty"`
	IfName     *string    `json:"if_name,omitempty"`
	VLANID     *int32     `json:"vlan_id,omitempty"`
	PortRole   *string    `json:"port_role,omitempty"`
}

// SearchResult bundles the answer to any search query.
type SearchResult struct {
	Query      string     `json:"query"`
	QueryType  string     `json:"query_type"` // ip | mac | hostname
	MAC        *string    `json:"mac,omitempty"`
	DeviceID   *uuid.UUID `json:"device_id,omitempty"`
	DeviceName *string    `json:"device_name,omitempty"`
	// The switch + port the device is connected to.
	SwitchPort []SwitchPortEntry `json:"switch_port"`
	// Best-effort uplink path toward the core.
	Path []PathStep `json:"path"`
}

// SwitchPortEntry is one FDB match: a switch + port + VLAN that carries the MAC.
type SwitchPortEntry struct {
	SwitchID   uuid.UUID `json:"switch_id"`
	SwitchName string    `json:"switch_name"`
	SwitchIP   *string   `json:"switch_ip,omitempty"`
	IfIndex    *int32    `json:"if_index,omitempty"`
	IfName     *string   `json:"if_name,omitempty"`
	VLANID     int32     `json:"vlan_id"`
	PortRole   *string   `json:"port_role,omitempty"`
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

// SearchIP resolves an IP to the switch port it's connected to.
func (e *Engine) SearchIP(ctx context.Context, ip netip.Addr) (SearchResult, error) {
	res := SearchResult{Query: ip.String(), QueryType: "ip"}

	// Step 1: Find the device row for this IP (might be a managed switch itself).
	dev, err := e.q.SearchByIP(ctx, &ip)
	if err == nil {
		res.DeviceID = &dev.ID
		res.DeviceName = &dev.Name
	}

	// Step 2: ARP → MAC.
	arpRows, err := e.q.FindMACByIP(ctx, ip)
	if err != nil || len(arpRows) == 0 {
		return res, nil
	}
	mac := arpRows[0].Mac
	res.MAC = &mac

	// Step 3: MAC → switch + port + VLAN.
	res.SwitchPort = e.macToPort(ctx, mac)
	return res, nil
}

// SearchMAC resolves a MAC address to the switch port(s) that carry it.
func (e *Engine) SearchMAC(ctx context.Context, mac string) (SearchResult, error) {
	res := SearchResult{Query: mac, QueryType: "mac", MAC: &mac}
	res.SwitchPort = e.macToPort(ctx, mac)
	return res, nil
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
		}
		out = append(out, sr)
	}
	return out, nil
}

// macToPort looks up the switch+port+VLAN for a MAC address.
func (e *Engine) macToPort(ctx context.Context, mac string) []SwitchPortEntry {
	rows, err := e.q.SearchByMAC(ctx, mac)
	if err != nil {
		return nil
	}
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
		if r.IfIndex != nil {
			entry.IfIndex = r.IfIndex
		}
		if r.IfName != nil {
			entry.IfName = r.IfName
		}
		if r.PortRole != nil {
			entry.PortRole = r.PortRole
		}
		out = append(out, entry)
	}
	return out
}

// BuildLinks derives topology_links from the latest neighbor + MAC + ARP
// data. Called by the collector engine after each collect cycle.
func (e *Engine) BuildLinks(ctx context.Context, deviceID uuid.UUID) error {
	neighbors, err := e.q.ListNeighbors(ctx, deviceID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, n := range neighbors {
		arg := db.UpsertTopologyLinkParams{
			LocalDeviceID: deviceID,
			LocalIfIndex:  n.LocalIfIndex,
			LocalIfName:   n.LocalIfName,
			RemoteSysName: n.RemSysName,
			LinkSource:    n.Protocol,
			LastSeenAt:    now,
		}
		if n.RemMgmtIp != nil && n.RemMgmtIp.IsValid() {
			arg.RemoteIp = n.RemMgmtIp
		}
		_ = e.q.UpsertTopologyLink(ctx, arg) // best-effort; link building is non-critical
	}
	return nil
}
