package redfish

import (
	"context"
	"encoding/json"
	"strings"
)

// ServiceRootInfo is the SAFE, UNAUTHENTICATED identity a Redfish service exposes
// at /redfish/v1/ — every DMTF-compliant BMC serves the ServiceRoot without a
// credential (deeper resources Systems/Chassis/Managers require Basic auth). It is
// the honest way to detect "this host is an out-of-band controller, and which
// vendor" without a credential and WITHOUT guessing from a generic web banner.
type ServiceRootInfo struct {
	Reachable      bool   // the ServiceRoot responded with valid Redfish JSON
	Vendor         string // Dell | HPE | Lenovo | (raw "Vendor" field)
	Product        string // e.g. "Integrated Dell Remote Access Controller"
	ControllerKind string // iDRAC | iLO | XClarity Controller | redfish
	ServiceTag     string // Dell Oem ServiceTag (the chassis serial) — real, unauth
	RedfishVersion string // protocol version (NOT the iLO/iDRAC firmware version)
}

// ProbeServiceRoot performs an UNAUTHENTICATED GET /redfish/v1/ and extracts the
// vendor/product identity. It never sends a credential and never touches
// Systems/Chassis (which need auth) — so it can classify a controller honestly
// while leaving full inventory gated behind a real Redfish credential. Returns a
// zero ServiceRootInfo (Reachable=false) when the host has no Redfish service.
func ProbeServiceRoot(ctx context.Context, baseURL string, doer Doer) ServiceRootInfo {
	c := NewClient(baseURL, "", "", doer) // empty user ⇒ GetJSON sends no auth header
	var raw struct {
		Vendor         string                     `json:"Vendor"`
		Product        string                     `json:"Product"`
		RedfishVersion string                     `json:"RedfishVersion"`
		Oem            map[string]json.RawMessage `json:"Oem"`
	}
	if err := c.GetJSON(ctx, "/redfish/v1/", &raw); err != nil {
		return ServiceRootInfo{}
	}
	info := ServiceRootInfo{
		Reachable:      true,
		Vendor:         strings.TrimSpace(raw.Vendor),
		Product:        strings.TrimSpace(raw.Product),
		RedfishVersion: strings.TrimSpace(raw.RedfishVersion),
	}
	// Oem keys are the DEFINITIVE vendor signal (a Dell iDRAC carries Oem.Dell, an
	// HPE iLO carries Oem.Hpe) and Dell exposes the ServiceTag (serial) there unauth.
	for k, v := range raw.Oem {
		switch strings.ToLower(k) {
		case "dell":
			info.Vendor, info.ControllerKind = "Dell", "iDRAC"
			var d struct {
				ServiceTag string `json:"ServiceTag"`
			}
			if json.Unmarshal(v, &d) == nil {
				info.ServiceTag = strings.TrimSpace(d.ServiceTag)
			}
		case "hpe", "hp":
			info.Vendor, info.ControllerKind = "HPE", "iLO"
		case "lenovo":
			info.Vendor, info.ControllerKind = "Lenovo", "XClarity Controller"
		}
	}
	// Fall back to the Product/Vendor strings when no recognized Oem key is present.
	if info.ControllerKind == "" {
		p := strings.ToLower(info.Product + " " + info.Vendor)
		switch {
		case strings.Contains(p, "idrac"), strings.Contains(p, "dell remote access"):
			info.ControllerKind = "iDRAC"
			if info.Vendor == "" {
				info.Vendor = "Dell"
			}
		case strings.Contains(p, "ilo"), strings.Contains(p, "integrated lights-out"):
			info.ControllerKind = "iLO"
			if info.Vendor == "" {
				info.Vendor = "HPE"
			}
		default:
			info.ControllerKind = "redfish" // a Redfish BMC of an unrecognized vendor — still honest
		}
	}
	return info
}
