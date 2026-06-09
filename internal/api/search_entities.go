package api

import (
	"fmt"
	"net/http"
	"strings"
)

// entityHit is one row in the unified global-search result. Every hit carries
// the owning/controller device (id + category) so the frontend can deep-link a
// MAC/IP/name found ANYWHERE — an access-point MAC, a wireless-client IP, a
// learned FDB MAC, an ARP entry — back to the device that knows about it.
type entityHit struct {
	Kind           string `json:"kind"`    // access_point | wireless_client | fdb | arp
	Title          string `json:"title"`   // primary label (name / hostname / MAC)
	Subtitle       string `json:"subtitle"` // contextual detail (model, vlan/port, ssid, …)
	IP             string `json:"ip,omitempty"`
	Mac            string `json:"mac,omitempty"`
	DeviceID       string `json:"device_id,omitempty"`       // owning/controller device (link target)
	DeviceName     string `json:"device_name,omitempty"`     // its display name
	DeviceCategory string `json:"device_category,omitempty"` // its category (route selection)
}

// searchEntitiesResponse groups hits by kind with per-group counts so the UI can
// render a panel per entity type and show "showing N of …" honestly.
type searchEntitiesResponse struct {
	Query          string      `json:"query"`
	Total          int         `json:"total"`
	AccessPoints   []entityHit `json:"access_points"`
	WirelessClient []entityHit `json:"wireless_clients"`
	Fdb            []entityHit `json:"fdb"`
	Arp            []entityHit `json:"arp"`
}

// escapeLike escapes the LIKE/ILIKE metacharacters so an operator pasting a MAC,
// an IP, or a name with '%' or '_' searches for those literal characters rather
// than triggering wildcard matches. '\' is the default ILIKE escape character.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// searchEntities is the unified "find anything" endpoint: GET /search/entities?q=
// It runs substring (ILIKE) matches across access points, associated wireless
// clients, the bridge FDB (learned MACs), and ARP tables, so a single query box
// catches a MAC/IP/name no matter which subsystem observed it. Each result links
// to the device that owns the observation. This complements /search (which
// returns a single deep topology path trace for an exact IP/MAC/hostname).
func (s *Server) searchEntities(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 2 {
		writeErr(w, errBadRequest("q parameter must be at least 2 characters"))
		return
	}
	// Substring term for ILIKE; escape wildcard metacharacters.
	term := escapeLike(q)

	resp := searchEntitiesResponse{
		Query:          q,
		AccessPoints:   []entityHit{},
		WirelessClient: []entityHit{},
		Fdb:            []entityHit{},
		Arp:            []entityHit{},
	}

	// --- Access points (name / MAC / IP / serial / model) ---------------------
	if aps, err := s.queries.SearchAccessPoints(ctx, &term); err != nil {
		writeErr(w, err)
		return
	} else {
		for _, ap := range aps {
			h := entityHit{
				Kind:           "access_point",
				Title:          ap.Name,
				Subtitle:       apSubtitle(derefStr(ap.Model), ap.Site, ap.Status),
				IP:             ap.Ip,
				Mac:            derefStr(ap.Mac),
				DeviceID:       ap.ControllerDeviceID.String(),
				DeviceName:     derefStr(ap.ControllerName),
				DeviceCategory: derefStr(ap.ControllerCategory),
			}
			resp.AccessPoints = append(resp.AccessPoints, h)
		}
	}

	// --- Wireless clients (MAC / IP / hostname / SSID / AP name) --------------
	if cls, err := s.queries.SearchWirelessClients(ctx, &term); err != nil {
		writeErr(w, err)
		return
	} else {
		for _, c := range cls {
			title := c.Hostname
			if strings.TrimSpace(title) == "" {
				title = c.Mac
			}
			resp.WirelessClient = append(resp.WirelessClient, entityHit{
				Kind:           "wireless_client",
				Title:          title,
				Subtitle:       clientSubtitle(c.Ssid, c.ApName, c.Band),
				IP:             c.Ip,
				Mac:            c.Mac,
				DeviceID:       c.ControllerDeviceID.String(),
				DeviceName:     derefStr(c.ControllerName),
				DeviceCategory: derefStr(c.ControllerCategory),
			})
		}
	}

	// --- Learned MACs / bridge FDB (which switch + port saw a MAC) ------------
	if macs, err := s.queries.SearchFdbMacs(ctx, &term); err != nil {
		writeErr(w, err)
		return
	} else {
		for _, m := range macs {
			port := derefStr(m.IfName)
			if port == "" && m.IfIndex != nil {
				port = fmt.Sprintf("ifIndex %d", *m.IfIndex)
			}
			resp.Fdb = append(resp.Fdb, entityHit{
				Kind:           "fdb",
				Title:          m.Mac,
				Subtitle:       fdbSubtitle(int(m.VlanID), port, derefStr(m.IfAlias)),
				Mac:            m.Mac,
				DeviceID:       m.DeviceID.String(),
				DeviceName:     m.DeviceName,
				DeviceCategory: m.DeviceCategory,
			})
		}
	}

	// --- ARP table (which L3 device resolved an IP↔MAC) ----------------------
	if arps, err := s.queries.SearchArpEntries(ctx, &term); err != nil {
		writeErr(w, err)
		return
	} else {
		for _, a := range arps {
			resp.Arp = append(resp.Arp, entityHit{
				Kind:           "arp",
				Title:          a.Ip,
				Subtitle:       "MAC " + a.Mac,
				IP:             a.Ip,
				Mac:            a.Mac,
				DeviceID:       a.DeviceID.String(),
				DeviceName:     a.DeviceName,
				DeviceCategory: a.DeviceCategory,
			})
		}
	}

	resp.Total = len(resp.AccessPoints) + len(resp.WirelessClient) + len(resp.Fdb) + len(resp.Arp)
	writeJSON(w, http.StatusOK, resp)
}

func apSubtitle(model, site, status string) string {
	parts := []string{}
	if model != "" {
		parts = append(parts, model)
	}
	if site != "" {
		parts = append(parts, site)
	}
	if status != "" {
		parts = append(parts, status)
	}
	return strings.Join(parts, " · ")
}

func clientSubtitle(ssid, apName, band string) string {
	parts := []string{}
	if ssid != "" {
		parts = append(parts, "SSID "+ssid)
	}
	if apName != "" {
		parts = append(parts, "AP "+apName)
	}
	if band != "" {
		parts = append(parts, band)
	}
	return strings.Join(parts, " · ")
}

func fdbSubtitle(vlan int, port, alias string) string {
	parts := []string{}
	if vlan > 0 {
		parts = append(parts, fmt.Sprintf("VLAN %d", vlan))
	}
	if port != "" {
		parts = append(parts, "port "+port)
	}
	if alias != "" {
		parts = append(parts, alias)
	}
	return strings.Join(parts, " · ")
}
