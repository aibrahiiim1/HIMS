package api

import (
	"net/http"
	"strings"
)

// Wireless Controller Driver Catalog — the SINGLE, model-driven source of truth
// for every controller platform HIMS can onboard. It is NOT a flat vendor list:
// each entry is a driver definition that declares the platform family, the wire
// protocol, the credential type, the login/session method, the URL/API path
// rules, the connection parameters the form must collect, and — per capability —
// exactly what the backing collector can produce TODAY.
//
// Both the Add Controller form (GET /wireless/controller-vendors), the
// connection test (POST /wireless/controllers/test), and the persist+collect
// handler (POST /wireless/controllers) read THIS catalog, so:
//   - the form is generated from the catalog (no hardcoded React dropdown),
//   - validation, test, and collection routing share one definition,
//   - a capability is shown "supported" ONLY when its collector actually runs —
//     no fake "full support" labels.
//
// SCOPE OF A CATALOG ROW (important — read before adding a vendor):
// A catalog row enables onboarding/profile/gating only:
//   - showing the vendor in the Add form,
//   - defining its connection fields + validation,
//   - declaring its capabilities + honest status,
//   - the collector_pending gate.
// It does NOT, by itself, collect data. FULL COLLECTION FOR A NEW VENDOR STILL
// REQUIRES A REAL COLLECTOR ADAPTER (a client + a case in
// collectWirelessForDevice) UNLESS an existing compatible driver/client can
// already handle that controller's API. So: add a row to onboard + gate; wire a
// collector (or reuse a compatible one) to actually pull APs/SSIDs/clients —
// otherwise the row must declare collector_pending / not_implemented, never
// "supported".

type wlVendorStatus string

const (
	wlStatusWorking          wlVendorStatus = "working"           // at least one capability really collects
	wlStatusDetectionOnly    wlVendorStatus = "detection_only"    // fingerprint only, no profile collection
	wlStatusCollectorPending wlVendorStatus = "collector_pending" // profile/detection allowed; no collector yet
	wlStatusUnsupported      wlVendorStatus = "unsupported"       // cannot be added
)

// wlCapStatus is the honest per-capability state. The DECLARED set (catalog,
// design-time) is a subset; the RUNTIME set (collection-health, after a real
// collect/test) uses the rest. Keeping one enum lets the UI render every state
// with one legend.
type wlCapStatus string

const (
	// declared (design-time) ----------------------------------------------------
	capSupported        wlCapStatus = "supported"         // collector implements this and collects it
	capNotImplemented   wlCapStatus = "not_implemented"   // no collector code for this capability yet
	capCollectorPending wlCapStatus = "collector_pending" // whole driver is gated (no client at all)
	// runtime (after a real test/collection) ------------------------------------
	capNeedsConfiguration  wlCapStatus = "needs_configuration"   // a required field (e.g. controllerId) is missing
	capAuthFailed          wlCapStatus = "auth_failed"           // login rejected
	capUnsupportedByDevice wlCapStatus = "unsupported_by_device" // device/firmware lacks the feature
	capEndpointNotExposed  wlCapStatus = "endpoint_not_exposed"  // API path returned nothing on this firmware
	capCollected           wlCapStatus = "collected"             // real data was persisted this run
)

// wlCapability is one collectible feature with its declared status.
type wlCapability struct {
	Key    string      `json:"key"` // aps | ssids | clients | radios | health | firmware
	Label  string      `json:"label"`
	Status wlCapStatus `json:"status"`
}

// Canonical capability keys (also the wireless_collection_health.capability values).
const (
	wcapAPs      = "aps"
	wcapSSIDs    = "ssids"
	wcapClients  = "clients"
	wcapRadios   = "radios"
	wcapHealth   = "health" // controller events / health feed
	wcapFirmware = "firmware"
)

// wlVendorField describes a vendor-specific connection parameter the form must
// render beyond the universal IP / name / username / password / port fields.
// Key is the JSON key sent back to the test + persist handlers, so it must match
// a field on the request structs (site | controller_id | api_base).
type wlVendorField struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder,omitempty"`
	Default     string `json:"default,omitempty"`
	Help        string `json:"help,omitempty"`
	Required    bool   `json:"required"`
}

type wlVendor struct {
	Key                string          `json:"key"`             // public driver key (form + API + Driver column)
	DisplayName        string          `json:"display_name"`    // dropdown label
	ModelFamily        string          `json:"model_family"`    // platform/model family this driver covers
	ControllerType     string          `json:"controller_type"` // short human description of the mgmt interface
	Protocol           string          `json:"protocol"`        // wire protocol (REST/HTTPS, Web-XML, …)
	DefaultPort        int             `json:"default_port"`
	CredentialType     string          `json:"credential_type"`  // username_password | username_password_or_token
	LoginMethod        string          `json:"login_method"`     // how the session is established
	PathRules          string          `json:"path_rules"`       // URL/API path expectations
	HealthEndpoints    string          `json:"health_endpoints"` // what Test Connection probes
	Fields             []wlVendorField `json:"fields"`
	Capabilities       []wlCapability  `json:"capabilities"`
	Status             wlVendorStatus  `json:"status"`
	CollectorAvailable bool            `json:"collector_available"`
	Message            string          `json:"message,omitempty"`     // honest UI note (shown when status != working)
	NextAction         string          `json:"next_action,omitempty"` // operator next step for gated drivers

	// internal, NOT serialised --------------------------------------------------
	deviceVendor      string // vendor string stamped on the device row
	profileVendorType string // vendor_type persisted on the profile + routed by collectWirelessForDevice
}

const (
	credUserPass        = "username_password"
	credUserPassOrToken = "username_password_or_token"
)

// cap is a tiny helper to keep the catalog rows readable.
func wcap(key, label string, st wlCapStatus) wlCapability {
	return wlCapability{Key: key, Label: label, Status: st}
}

// wirelessVendors is the driver catalog, in dropdown order. Add a row to onboard
// a platform; nothing else in the form, test, or persist handler needs to change.
var wirelessVendors = []wlVendor{
	{
		Key: "ruckus_zd", DisplayName: "Ruckus ZoneDirector",
		ModelFamily: "Ruckus ZoneDirector (ZD1100/1200/3000/5000)", ControllerType: "Web-XML admin interface",
		Protocol: "Web-XML (AJAX over HTTPS)", DefaultPort: 443,
		CredentialType: credUserPass, LoginMethod: "Form login + CSRF (admin-path auto-discovery)",
		PathRules: "/admin or /admin10 (auto-discovered at login)", HealthEndpoints: "admin login + stamgr AP roster",
		Fields: nil,
		Capabilities: []wlCapability{
			wcap(wcapAPs, "APs", capSupported), wcap(wcapSSIDs, "SSIDs/WLANs", capSupported),
			wcap(wcapClients, "Clients", capSupported), wcap(wcapFirmware, "Firmware/OS", capSupported),
			wcap(wcapRadios, "Radios", capNotImplemented), wcap(wcapHealth, "Events/Health", capNotImplemented),
		},
		Status: wlStatusWorking, CollectorAvailable: true,
		deviceVendor: "Ruckus Wireless", profileVendorType: "ruckus_zd",
	},
	{
		Key: "extreme_xcc", DisplayName: "Extreme — ExtremeCloud IQ Controller / XCC",
		ModelFamily: "ExtremeCloud IQ Controller (XCC / VE6120)", ControllerType: "On-prem controller REST API",
		Protocol: "REST/HTTPS", DefaultPort: 5825,
		CredentialType: credUserPassOrToken, LoginMethod: "REST login or API token (leave username blank for a bare token)",
		PathRules:       "API base e.g. /management/v1 — auto-discovered on Test Connection",
		HealthEndpoints: "auth + read-only API probe (APs/SSIDs/clients/events)",
		Fields: []wlVendorField{
			{Key: "api_base", Label: "API base", Placeholder: "/management/v1", Default: "/management/v1", Help: "Auto-discovered on Test Connection if left default.", Required: false},
		},
		Capabilities: []wlCapability{
			wcap(wcapAPs, "APs", capSupported), wcap(wcapSSIDs, "SSIDs/WLANs", capSupported),
			wcap(wcapClients, "Clients", capSupported), wcap(wcapHealth, "Events/Health", capSupported),
			wcap(wcapFirmware, "Firmware/OS", capSupported), wcap(wcapRadios, "Radios", capNotImplemented),
		},
		Status: wlStatusWorking, CollectorAvailable: true,
		deviceVendor: "Extreme Networks", profileVendorType: "extreme_xcc",
	},
	{
		Key: "ruckus_sz", DisplayName: "Ruckus SmartZone (vSZ)",
		ModelFamily: "Ruckus SmartZone (vSZ / SZ100 / SZ300)", ControllerType: "Public REST API",
		Protocol: "REST/HTTPS", DefaultPort: 8443,
		CredentialType: credUserPass, LoginMethod: "REST session (serviceTicket)",
		PathRules:       "API base /wsg/api/public/v<ver> (version varies by firmware)",
		HealthEndpoints: "serviceTicket login + AP query",
		Fields: []wlVendorField{
			{Key: "api_base", Label: "API base", Placeholder: "/wsg/api/public/v9_1", Default: "/wsg/api/public/v9_1", Help: "SmartZone public-API path; version varies by firmware.", Required: false},
		},
		Capabilities: []wlCapability{
			wcap(wcapAPs, "APs", capSupported), wcap(wcapSSIDs, "SSIDs/WLANs", capNotImplemented),
			wcap(wcapClients, "Clients", capNotImplemented), wcap(wcapRadios, "Radios", capNotImplemented),
			wcap(wcapHealth, "Events/Health", capNotImplemented), wcap(wcapFirmware, "Firmware/OS", capNotImplemented),
		},
		Status: wlStatusWorking, CollectorAvailable: true,
		deviceVendor: "Ruckus Wireless", profileVendorType: "wireless_ruckus",
	},
	{
		Key: "unifi", DisplayName: "Ubiquiti UniFi",
		ModelFamily: "Ubiquiti UniFi (UDM / Cloud Key / self-hosted)", ControllerType: "UniFi Network REST API",
		Protocol: "REST/HTTPS", DefaultPort: 8443,
		CredentialType: credUserPass, LoginMethod: "REST login (cookie session)",
		PathRules:       "/api/login + /api/s/<site>/stat/device",
		HealthEndpoints: "login + site device list",
		Fields: []wlVendorField{
			{Key: "site", Label: "Controller site", Placeholder: "default", Default: "default", Help: "UniFi site name (not the HIMS location). Defaults to 'default'.", Required: false},
		},
		Capabilities: []wlCapability{
			wcap(wcapAPs, "APs", capSupported), wcap(wcapSSIDs, "SSIDs/WLANs", capNotImplemented),
			wcap(wcapClients, "Clients", capNotImplemented), wcap(wcapRadios, "Radios", capNotImplemented),
			wcap(wcapHealth, "Events/Health", capNotImplemented), wcap(wcapFirmware, "Firmware/OS", capNotImplemented),
		},
		Status: wlStatusWorking, CollectorAvailable: true,
		deviceVendor: "Ubiquiti UniFi", profileVendorType: "wireless_unifi",
	},
	{
		Key: "omada", DisplayName: "TP-Link Omada",
		ModelFamily: "TP-Link Omada (OC200 / OC300 / software)", ControllerType: "Omada Controller REST API",
		Protocol: "REST/HTTPS", DefaultPort: 8043,
		CredentialType: credUserPass, LoginMethod: "REST login (token + controller-id + CSRF)",
		PathRules:       "/<controller-id>/api/v2/... — requires the controllerId URL segment",
		HealthEndpoints: "login + site device list",
		Fields: []wlVendorField{
			{Key: "controller_id", Label: "Controller ID", Placeholder: "omadac-id from the controller URL", Help: "Required: the controllerId segment in the Omada web URL.", Required: true},
			{Key: "site", Label: "Controller site", Placeholder: "Default", Default: "Default", Help: "Omada site name (not the HIMS location). Defaults to 'Default'.", Required: false},
		},
		Capabilities: []wlCapability{
			wcap(wcapAPs, "APs", capSupported), wcap(wcapSSIDs, "SSIDs/WLANs", capNotImplemented),
			wcap(wcapClients, "Clients", capNotImplemented), wcap(wcapRadios, "Radios", capNotImplemented),
			wcap(wcapHealth, "Events/Health", capNotImplemented), wcap(wcapFirmware, "Firmware/OS", capNotImplemented),
		},
		Status: wlStatusWorking, CollectorAvailable: true,
		deviceVendor: "TP-Link Omada", profileVendorType: "wireless_omada",
	},
	{
		Key: "aruba", DisplayName: "Aruba (Mobility / Instant)",
		ModelFamily: "Aruba Mobility Controller / Instant", ControllerType: "Controller REST API",
		Protocol: "REST/HTTPS", DefaultPort: 443,
		CredentialType: credUserPass, LoginMethod: "—",
		PathRules: "—", HealthEndpoints: "detection/classification only (no collector yet)",
		Fields: []wlVendorField{
			{Key: "site", Label: "Controller site", Placeholder: "(optional)", Help: "Stored for the future collector; not used for collection yet.", Required: false},
		},
		Capabilities: []wlCapability{
			wcap(wcapAPs, "APs", capCollectorPending), wcap(wcapSSIDs, "SSIDs/WLANs", capCollectorPending),
			wcap(wcapClients, "Clients", capCollectorPending), wcap(wcapRadios, "Radios", capCollectorPending),
			wcap(wcapHealth, "Events/Health", capCollectorPending), wcap(wcapFirmware, "Firmware/OS", capCollectorPending),
		},
		Status: wlStatusCollectorPending, CollectorAvailable: false,
		Message:      "Aruba collection client is not implemented yet. This driver stores the profile and classifies the device for detection only — it does NOT fetch APs/SSIDs/clients until a real Aruba collector is built.",
		NextAction:   "Aruba collector is not implemented yet — the device is tracked + classified; deep collection is pending an Aruba REST client.",
		deviceVendor: "Aruba", profileVendorType: "wireless_aruba",
	},
}

// wlVendorByKey resolves a public driver key to its catalog entry. ok=false for
// an unknown key so callers reject it instead of silently mishandling it.
func wlVendorByKey(key string) (wlVendor, bool) {
	key = strings.TrimSpace(key)
	for _, v := range wirelessVendors {
		if v.Key == key {
			return v, true
		}
	}
	return wlVendor{}, false
}

// wirelessProfileVendorTypes returns every vendor_type the catalog can persist on
// a connection profile — used to disable any OTHER wireless profile bound to a
// device when a controller is (re)added under a different driver, so the
// collection authority can never pick a stale collector.
func wirelessProfileVendorTypes() []string {
	out := make([]string, 0, len(wirelessVendors))
	for _, v := range wirelessVendors {
		out = append(out, v.profileVendorType)
	}
	return out
}

// wlVendorKeyForProfileType maps a stored profile vendor_type back to its public
// driver key — used to stamp the device "Driver" column with the active
// collector. Falls back to the vendor_type itself for any unregistered value.
func wlVendorKeyForProfileType(vendorType string) string {
	for _, v := range wirelessVendors {
		if v.profileVendorType == vendorType {
			return v.Key
		}
	}
	return vendorType
}

// listWirelessVendors handles GET /wireless/controller-vendors — the form reads
// this to generate the dropdown, the per-driver fields, and the honest
// capability/status badges, so the UI is always in lock-step with what the
// backend can actually collect.
func (s *Server) listWirelessVendors(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, wirelessVendors)
}
