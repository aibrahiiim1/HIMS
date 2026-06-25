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
	capSupported wlCapStatus = "supported" // collector implements this and is expected to collect it
	// implemented_live_validation_pending: the FULL technical path exists — collector
	// code, endpoint/command, parser, fixture test, and runtime route — but the exact
	// response shape has not been confirmed against real hardware/tenant yet. This is
	// NOT "developer work remaining"; it is "needs a live controller to validate".
	capLiveValidationPending wlCapStatus = "implemented_live_validation_pending"
	capExternalDependency    wlCapStatus = "external_dependency_required" // implemented; blocked on an external input (token/tenant)
	capNotImplemented        wlCapStatus = "not_implemented"              // genuinely no collector code (developer work remains)
	capCollectorPending      wlCapStatus = "collector_pending"            // whole driver is gated (no client at all)
	// runtime (after a real test/collection) ------------------------------------
	capNeedsConfiguration  wlCapStatus = "needs_configuration"   // a required field (e.g. controllerId) is missing
	capAuthFailed          wlCapStatus = "auth_failed"           // login rejected
	capUnsupportedByDevice wlCapStatus = "unsupported_by_device" // device/firmware lacks the feature
	capEndpointNotExposed  wlCapStatus = "endpoint_not_exposed"  // API path returned nothing on this firmware
	capCollected           wlCapStatus = "collected"             // real data was persisted this run
)

// wlCapability is one collectible feature with its declared status. Reason is the
// proof/justification shown when the status is a non-collectable one
// (unsupported_by_device / not_implemented) — e.g. the exact endpoint/command
// that does not exist on this controller family.
type wlCapability struct {
	Key    string      `json:"key"` // aps | ssids | clients | radios | health | events | firmware
	Label  string      `json:"label"`
	Status wlCapStatus `json:"status"`
	Reason string      `json:"reason,omitempty"`
}

// Canonical capability keys (also the wireless_collection_health.capability values).
const (
	wcapAPs      = "aps"
	wcapSSIDs    = "ssids"
	wcapClients  = "clients"
	wcapRadios   = "radios"
	wcapHealth   = "health" // controller / subsystem health
	wcapEvents   = "events" // controller events / alarms feed
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
	credToken           = "bearer_token" // the password field carries an API/OAuth2 token
)

// wcap / wcapWhy keep the catalog rows readable. wcapWhy attaches the proof for a
// non-collectable capability (the endpoint/command that does not exist).
func wcap(key, label string, st wlCapStatus) wlCapability {
	return wlCapability{Key: key, Label: label, Status: st}
}
func wcapWhy(key, label string, st wlCapStatus, reason string) wlCapability {
	return wlCapability{Key: key, Label: label, Status: st, Reason: reason}
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
			wcap(wcapRadios, "Radios", capSupported),
			wcapWhy(wcapHealth, "Health", capUnsupportedByDevice, "ZoneDirector's AJAX admin interface exposes no controller/subsystem health endpoint; reachability + firmware (backfilled from the AP fleet) are the available signals."),
			wcapWhy(wcapEvents, "Events", capUnsupportedByDevice, "ZoneDirector's AJAX interface returns zero event/alarm rows on this firmware; events are delivered via SNMP traps, not a pollable endpoint."),
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
			wcap(wcapClients, "Clients", capSupported), wcap(wcapHealth, "Health", capSupported),
			wcap(wcapEvents, "Events", capSupported), wcap(wcapFirmware, "Firmware/OS", capSupported),
			wcapWhy(wcapRadios, "Radios", capLiveValidationPending, "Implemented: parser reads the AP payload's 'radios' array (channel/power/width/clients), persisted + fixture-tested + routed. Live validation pending — no XCC wireless profile is bound in this environment to confirm the field shape against a VE6120."),
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
		HealthEndpoints: "serviceTicket login + AP / WLAN / client / radio / event query + controller",
		Fields: []wlVendorField{
			{Key: "api_base", Label: "API base", Placeholder: "/wsg/api/public/v9_1", Default: "/wsg/api/public/v9_1", Help: "SmartZone public-API path; version varies by firmware (auto-detected via apiInfo).", Required: false},
		},
		Capabilities: []wlCapability{
			wcap(wcapAPs, "APs", capSupported), wcap(wcapSSIDs, "SSIDs/WLANs", capSupported),
			wcap(wcapClients, "Clients", capSupported), wcap(wcapRadios, "Radios", capSupported),
			wcap(wcapHealth, "Health", capSupported), wcap(wcapEvents, "Events", capSupported), wcap(wcapFirmware, "Firmware/OS", capSupported),
		},
		Status: wlStatusWorking, CollectorAvailable: true,
		deviceVendor: "Ruckus Wireless", profileVendorType: "wireless_ruckus",
	},
	{
		Key: "unifi", DisplayName: "Ubiquiti UniFi",
		ModelFamily: "Ubiquiti UniFi (UDM / Cloud Key / self-hosted)", ControllerType: "UniFi Network REST API",
		Protocol: "REST/HTTPS", DefaultPort: 8443,
		CredentialType: credUserPass, LoginMethod: "REST login (cookie session)",
		PathRules:       "/api/login + /api/s/<site>/{stat/device,stat/sta,rest/wlanconf,stat/sysinfo,stat/health,stat/event}",
		HealthEndpoints: "login + device / station / wlanconf / sysinfo / health / event",
		Fields: []wlVendorField{
			{Key: "site", Label: "Controller site", Placeholder: "default", Default: "default", Help: "UniFi site name (not the HIMS location). Defaults to 'default'.", Required: false},
		},
		Capabilities: []wlCapability{
			wcap(wcapAPs, "APs", capSupported), wcap(wcapSSIDs, "SSIDs/WLANs", capSupported),
			wcap(wcapClients, "Clients", capSupported), wcap(wcapRadios, "Radios", capSupported),
			wcap(wcapHealth, "Health", capSupported), wcap(wcapEvents, "Events", capSupported), wcap(wcapFirmware, "Firmware/OS", capSupported),
		},
		Status: wlStatusWorking, CollectorAvailable: true,
		deviceVendor: "Ubiquiti UniFi", profileVendorType: "wireless_unifi",
	},
	{
		Key: "omada", DisplayName: "TP-Link Omada",
		ModelFamily: "TP-Link Omada (OC200 / OC300 / software)", ControllerType: "Omada Controller REST API",
		Protocol: "REST/HTTPS", DefaultPort: 8043,
		CredentialType: credUserPass, LoginMethod: "REST login (token + controller-id + CSRF)",
		PathRules:       "/api/info + /<controller-id>/api/v2/sites/<site>/{devices,setting/wlans/ssids,clients,alerts}",
		HealthEndpoints: "login + info / devices / ssids / clients / alerts",
		Fields: []wlVendorField{
			{Key: "controller_id", Label: "Controller ID", Placeholder: "omadac-id from the controller URL", Help: "Required: the controllerId segment in the Omada web URL.", Required: true},
			{Key: "site", Label: "Controller site", Placeholder: "Default", Default: "Default", Help: "Omada site name (not the HIMS location). Defaults to 'Default'.", Required: false},
		},
		Capabilities: []wlCapability{
			wcap(wcapAPs, "APs", capSupported), wcap(wcapSSIDs, "SSIDs/WLANs", capSupported),
			wcap(wcapClients, "Clients", capSupported),
			wcap(wcapHealth, "Health", capSupported), wcap(wcapEvents, "Events", capSupported), wcap(wcapFirmware, "Firmware/OS", capSupported),
			wcapWhy(wcapRadios, "Radios", capLiveValidationPending, "Implemented: per-AP detail call (/eaps/{mac}) parses radioList (band/channel/power/width/clients), persisted + fixture-tested + routed (bounded to 200 APs). Live validation pending — the radioList shape varies across controller v4 vs v5/OC200 and needs a live controller to confirm."),
		},
		Status: wlStatusWorking, CollectorAvailable: true,
		deviceVendor: "TP-Link Omada", profileVendorType: "wireless_omada",
	},
	{
		Key: "aruba_os8", DisplayName: "Aruba — Mobility Controller (ArubaOS 8)",
		ModelFamily: "Aruba Mobility Controller / Conductor (ArubaOS 8)", ControllerType: "On-prem REST showcommand API",
		Protocol: "REST/HTTPS", DefaultPort: 4343,
		CredentialType: credUserPass, LoginMethod: "REST login (UIDARUBA session token)",
		PathRules:       "/v1/api/login + /v1/configuration/showcommand?command=…",
		HealthEndpoints: "login + show ap database long / show user-table",
		Fields: []wlVendorField{
			{Key: "site", Label: "Controller site", Placeholder: "(optional)", Help: "Optional grouping label; not required by the ArubaOS API.", Required: false},
		},
		Capabilities: []wlCapability{
			wcap(wcapAPs, "APs", capSupported), wcap(wcapSSIDs, "SSIDs/WLANs", capSupported),
			wcap(wcapClients, "Clients", capSupported), wcap(wcapHealth, "Health", capSupported), wcap(wcapFirmware, "Firmware/OS", capSupported),
			wcapWhy(wcapRadios, "Radios", capLiveValidationPending, "Implemented: 'show ap bss-table' parsed into per-AP per-band radios (channel from 'ch', band from 'phy'), fixture-tested + routed. Live validation pending — BSS-table column naming varies by AOS train and needs a real controller to confirm."),
			wcapWhy(wcapEvents, "Events", capUnsupportedByDevice, "ArubaOS controllers stream events to syslog/SNMP; no structured showcommand returns a pollable event/alarm table."),
		},
		Status: wlStatusWorking, CollectorAvailable: true,
		Message:      "ArubaOS 8 on-prem Mobility Controller via the REST showcommand API (APs from 'show ap database long', clients from 'show user-table', firmware from 'show version', SSIDs derived from the live client set). Parser-tested against documented payloads; live validation against a real ArubaOS 8 controller is pending (external dependency: live controller/credential).",
		NextAction:   "Run Test Connection (port 4343) to validate the login + showcommand path, then Run Collection.",
		deviceVendor: "Aruba", profileVendorType: "wireless_aruba",
	},
	{
		Key: "aruba_os10", DisplayName: "Aruba — Mobility Conductor (ArubaOS 10, on-prem)",
		ModelFamily: "Aruba Mobility Conductor / Gateway (ArubaOS 10, on-prem managed)", ControllerType: "On-prem REST showcommand API",
		Protocol: "REST/HTTPS", DefaultPort: 4343,
		CredentialType: credUserPass, LoginMethod: "REST login (UIDARUBA session token)",
		PathRules:       "/v1/api/login + /v1/configuration/showcommand?command=… (AOS10 retains the AOS8-compatible config REST API)",
		HealthEndpoints: "login + show ap database long / show user-table",
		Fields: []wlVendorField{
			{Key: "site", Label: "Controller site", Placeholder: "(optional)", Help: "Optional grouping label; not required by the ArubaOS API.", Required: false},
		},
		Capabilities: []wlCapability{
			wcap(wcapAPs, "APs", capSupported), wcap(wcapSSIDs, "SSIDs/WLANs", capSupported),
			wcap(wcapClients, "Clients", capSupported), wcap(wcapHealth, "Health", capSupported), wcap(wcapFirmware, "Firmware/OS", capSupported),
			wcapWhy(wcapRadios, "Radios", capLiveValidationPending, "Implemented: 'show ap bss-table' parsed into per-AP per-band radios (channel from 'ch', band from 'phy'), fixture-tested + routed. Live validation pending — BSS-table column naming varies by AOS train and needs a real AOS10 controller to confirm."),
			wcapWhy(wcapEvents, "Events", capUnsupportedByDevice, "ArubaOS controllers stream events to syslog/SNMP; no structured showcommand returns a pollable event/alarm table."),
		},
		Status: wlStatusWorking, CollectorAvailable: true,
		Message:      "ArubaOS 10 ON-PREM (Conductor-led, not cloud) reuses the AOS8-compatible REST showcommand API, so the same collector applies (APs, clients, firmware via 'show version', SSIDs derived). Parser-tested; the AOS10 REST surface needs live confirmation (external dependency: live AOS10 controller). Cloud-managed AOS10 tenants must use the 'Aruba Central' driver instead.",
		NextAction:   "Run Test Connection (port 4343). If this AOS10 deployment is cloud-managed by Aruba Central, use the Aruba Central driver instead.",
		deviceVendor: "Aruba", profileVendorType: "wireless_aruba_os10",
	},
	{
		Key: "aruba_central", DisplayName: "Aruba Central (cloud / ArubaOS 10 cloud-managed)",
		ModelFamily: "Aruba Central (cloud-managed ArubaOS 10)", ControllerType: "Cloud Monitoring REST API (OAuth2)",
		Protocol: "REST/HTTPS (OAuth2 bearer)", DefaultPort: 443,
		CredentialType: credToken, LoginMethod: "OAuth2 bearer access token (no username/password at this layer)",
		PathRules:       "Regional API gateway + /monitoring/v2/aps, /monitoring/v1/clients/wireless, /monitoring/v2/networks, /central/v1/alerts",
		HealthEndpoints: "bearer GET /monitoring/v2/aps + /central/v1/alerts (token + gateway reachability)",
		Fields: []wlVendorField{
			{Key: "api_base", Label: "API gateway base URL", Placeholder: "apigw-prod2.central.arubanetworks.com", Help: "Region-specific Central API gateway host (the Target URL); the access token is supplied as the password.", Required: false},
		},
		Capabilities: []wlCapability{
			wcap(wcapAPs, "APs", capSupported), wcap(wcapSSIDs, "SSIDs/WLANs", capSupported),
			wcap(wcapClients, "Clients", capSupported),
			wcap(wcapHealth, "Health", capSupported), wcap(wcapEvents, "Events", capSupported), wcap(wcapFirmware, "Firmware/OS", capSupported),
			wcapWhy(wcapRadios, "Radios", capLiveValidationPending, "Implemented: parses the per-AP 'radios' array from /monitoring/v2/aps (band/channel/power/width/clients) in one call, fixture-tested + routed. Live validation pending — needs a live Central tenant to confirm the radios field shape."),
		},
		Status: wlStatusWorking, CollectorAvailable: true,
		Message:      "Aruba Central cloud monitoring API (the ArubaOS 10 cloud-managed path) — APs/clients/networks/firmware/alerts via OAuth2 bearer token. The token is the credential (enter it as the password; username is ignored). Parser-tested; live validation against a real Central tenant is pending (external dependency: tenant + API token + regional gateway).",
		NextAction:   "Set the regional API gateway as the Target/host, paste the Central access token as the password, then Test Connection.",
		deviceVendor: "Aruba", profileVendorType: "wireless_aruba_central",
	},
}

// missingCredential returns a non-empty operator message when the request lacks
// the credential the driver needs. A bearer-token driver (Aruba Central) needs
// only the token (carried in the password); everyone else needs username+password.
func missingCredential(v wlVendor, req addWirelessControllerReq) string {
	if v.CredentialType == credToken {
		if req.Password == "" {
			return v.DisplayName + " requires an API/OAuth2 token (enter it as the password)."
		}
		return ""
	}
	if strings.TrimSpace(req.Username) == "" || req.Password == "" {
		return "admin username and password are required"
	}
	return ""
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
