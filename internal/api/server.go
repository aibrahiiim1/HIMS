// Package api is the HIMS REST API server. It mounts all routes and wires
// the dependency set. The server is intentionally thin: all domain logic
// lives in the engine packages; handlers just translate HTTP ↔ domain.
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/alerting"
	"github.com/coralsearesorts/hims/internal/discovery"
	"github.com/coralsearesorts/hims/internal/driver"
	"github.com/coralsearesorts/hims/internal/monitoring"
	"github.com/coralsearesorts/hims/internal/secret"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/coralsearesorts/hims/internal/topology"
)

// Server holds the dependencies for the API.
type Server struct {
	router  chi.Router
	topo    *topology.Engine
	mon     *monitoring.Engine
	alerts  *alerting.Engine
	cipher  *secret.Cipher             // nil when no encryption key is configured
	reg     *driver.Registry           // nil disables operator-launched scans
	fetcher discovery.CandidateFetcher // credential scope resolver for scans
	queries *db.Queries
}

// NewServer wires dependencies and returns a ready-to-serve Server. cipher
// may be nil (no HIMS_ENCRYPTION_KEY set) — credential writes then return
// 503. reg + fetcher may be nil — operator-launched scans then return 503;
// everything else still serves.
func NewServer(queries *db.Queries, cipher *secret.Cipher, reg *driver.Registry, fetcher discovery.CandidateFetcher) *Server {
	s := &Server{
		queries: queries,
		topo:    topology.New(queries),
		// The API can seed defaults + run on-demand sweeps; the scheduled
		// loop lives in the collector. Both share this engine type.
		mon:     monitoring.NewEngine(queries, monitoring.NewPoller(nil, 0), nil),
		alerts:  alerting.NewEngine(queries, nil),
		cipher:  cipher,
		reg:     reg,
		fetcher: fetcher,
		router:  chi.NewRouter(),
	}
	s.routes()
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

// routes registers all API routes.
func (s *Server) routes() {
	r := s.router
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.StripSlashes)

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	r.Route("/api/v1", func(r chi.Router) {
		// --- Executive dashboard (cross-cutting rollups) -------------
		r.Get("/dashboard", s.dashboard)

		// --- Discovery (operator-launched subnet scans) --------------
		r.Get("/discovery/jobs", s.listDiscoveryJobs)
		r.Post("/discovery/scan", s.startScan)
		r.Post("/discovery/controller-import", s.startControllerImport)
		r.Post("/discovery/ad/browse", s.browseAD)
		r.Post("/discovery/ad-import", s.startADImport)
		r.Get("/discovery/jobs/{id}", s.getDiscoveryJob)
		r.Delete("/discovery/jobs/{id}", s.deleteDiscoveryJob)
		r.Post("/discovery/jobs/{id}/rerun", s.rerunDiscoveryJob)

		// --- Devices --------------------------------------------------
		r.Get("/devices", s.listDevices)
		r.Post("/devices", s.createManualDevice)
		r.Post("/devices/import-csv", s.importDevicesCSV)
		r.Post("/devices/import-file", s.importDevicesFile)
		r.Post("/devices/bulk-delete", s.bulkDeleteDevices)
		r.Post("/devices/bulk-assign", s.bulkAssignDevices)
		r.Patch("/devices/{id}", s.updateDevice)
		r.Delete("/devices/{id}", s.deleteDevice)
		r.Get("/devices/{id}/interfaces", s.deviceInterfaces)
		r.Get("/devices/{id}/vlans", s.deviceVLANs)
		r.Get("/devices/{id}/neighbors", s.deviceNeighbors)
		r.Get("/devices/{id}/topology", s.deviceTopology)
		r.Get("/devices/{id}/mac", s.deviceMACTable)
		r.Get("/devices/{id}/arp", s.deviceARPTable)
		r.Get("/devices/{id}/port-vlans", s.devicePortVlans)
		r.Get("/devices/{id}/mac-counts", s.deviceMACCounts)
		r.Get("/devices/{id}/storage", s.deviceStorage)
		r.Get("/devices/{id}/facts", s.deviceFacts)
		r.Get("/devices/{id}/roles", s.deviceRoles)
		r.Get("/devices/{id}/firewall-status", s.firewallStatus)
		r.Get("/devices/{id}/vpn-tunnels", s.vpnTunnels)
		r.Get("/devices/{id}/ha-members", s.haMembers)
		r.Get("/devices/{id}/licenses", s.licenses)
		r.Get("/devices/{id}/monitoring/checks", s.deviceMonitoringChecks)
		r.Get("/devices/{id}/monitoring/samples", s.deviceMonitoringSamples)
		r.Get("/devices/{id}/vms", s.deviceVMs)
		r.Get("/devices/{id}/camera", s.deviceCamera)
		r.Get("/devices/{id}/nvr-channels", s.deviceNVRChannels)
		r.Get("/devices/{id}/wlan", s.deviceWLAN)
		r.Get("/devices/{id}/access-points", s.deviceAccessPoints)
		r.Get("/devices/{id}/bmc", s.deviceBMC)
		r.Get("/devices/{id}/bmc-sensors", s.deviceBMCSensors)
		r.Get("/devices/{id}/printer-supplies", s.devicePrinterSupplies)
		r.Get("/devices/{id}/phones", s.devicePhones)
		r.Get("/devices/{id}/ups", s.deviceUPS)

		// --- Topology & search ----------------------------------------
		// IP/MAC/name → switch+port+path (the headline Phase 1 feature).
		r.Get("/search", s.search) // ?q=<IP|MAC|name>
		r.Get("/topology/links", s.allLinks)

		// --- Roles (CMDB role cut: databases, AD/DNS/DHCP, …) --------
		r.Get("/roles/summary", s.roleSummary)
		r.Get("/roles/{role}/devices", s.devicesByRole)

		// --- Locations -----------------------------------------------
		r.Get("/locations", s.listLocations)
		r.Get("/locations/all", s.listAllLocations)
		r.Post("/locations", s.createLocation)
		r.Patch("/locations/{id}", s.updateLocation)
		r.Delete("/locations/{id}", s.deleteLocation)
		r.Get("/locations/{id}/children", s.childLocations)
		r.Get("/locations/{id}/subnets", s.locationSubnets)
		r.Post("/locations/{id}/subnets", s.createSubnet)
		r.Get("/subnets", s.listAllSubnets)
		r.Delete("/subnets/{id}", s.deleteSubnet)

		// --- Operations: work orders + systems/licenses --------------
		r.Get("/work-orders", s.listWorkOrders)
		r.Post("/work-orders", s.createWorkOrder)
		r.Get("/work-orders/{id}", s.getWorkOrder)
		r.Patch("/work-orders/{id}", s.updateWorkOrder)
		r.Get("/work-orders/{id}/parts", s.listWorkOrderParts)
		r.Post("/work-orders/{id}/parts", s.addWorkOrderPart)
		r.Get("/systems", s.listSystems)
		r.Post("/systems", s.createSystem)

		// --- Operations B: spare parts, purchases, expenses ----------
		r.Get("/spare-parts", s.listSpareParts)
		r.Post("/spare-parts", s.createSparePart)
		r.Get("/spare-parts/low-stock", s.listLowStockParts)
		r.Patch("/spare-parts/{id}", s.updateSparePart)
		r.Patch("/spare-parts/{id}/stock", s.adjustSparePartStock)
		r.Delete("/spare-parts/{id}", s.deleteSparePart)
		r.Get("/purchases", s.listPurchases)
		r.Post("/purchases", s.createPurchase)
		r.Delete("/purchases/{id}", s.deletePurchase)
		r.Get("/expenses/by-category", s.expensesByCategory)
		r.Get("/expenses/by-location", s.expensesByLocation)

		// --- Monitoring engine ---------------------------------------
		r.Get("/monitoring/checks", s.listMonitoringChecks)
		r.Post("/monitoring/checks", s.registerMonitoringCheck)
		r.Patch("/monitoring/checks/{id}", s.setMonitoringCheckEnabled)
		r.Delete("/monitoring/checks/{id}", s.deleteMonitoringCheck)
		r.Get("/monitoring/overview", s.monitoringOverview)
		r.Post("/monitoring/seed", s.seedMonitoringDefaults)
		r.Post("/monitoring/run", s.runMonitoringNow)

		// --- Alerting engine + alert→work-order bridge ---------------
		r.Get("/alert-rules", s.listAlertRules)
		r.Post("/alert-rules", s.createAlertRule)
		r.Patch("/alert-rules/{id}", s.setAlertRuleEnabled)
		r.Delete("/alert-rules/{id}", s.deleteAlertRule)
		r.Get("/alerts", s.listAlerts)
		r.Post("/alerts/evaluate", s.evaluateAlerts)
		r.Post("/alerts/{id}/ack", s.acknowledgeAlert)
		r.Post("/alerts/{id}/resolve", s.resolveAlert)

		// --- Credentials (encrypted at rest; secrets never returned) -
		r.Get("/credentials", s.listCredentials)
		r.Post("/credentials", s.createCredential)
		r.Patch("/credentials/{id}", s.updateCredential)
		r.Delete("/credentials/{id}", s.deleteCredential)
		r.Get("/credential-groups", s.listCredentialGroups)

		// --- Settings (operator-tunable timeouts / concurrency) ------
		r.Get("/settings", s.getSettings)
		r.Put("/settings", s.updateSettings)
		r.Get("/lookups", s.listLookups)
		r.Post("/lookups", s.createLookup)
		r.Delete("/lookups/{id}", s.deleteLookup)
		r.Put("/devices/{id}/credential", s.bindDeviceCredential)

		// --- Administration: RBAC, templates, fingerprints, audit ----
		// RBAC is namespaced under /rbac so it never collides with the
		// CMDB device-role routes (/roles/summary, /roles/{role}/devices).
		r.Get("/rbac/users", s.listUsers)
		r.Post("/rbac/users", s.createUser)
		r.Patch("/rbac/users/{id}", s.updateUser)
		r.Delete("/rbac/users/{id}", s.deleteUser)
		r.Get("/rbac/users/{id}/roles", s.userRoles)
		r.Post("/rbac/users/{id}/roles", s.setUserRoles)
		r.Get("/rbac/roles", s.listRoles)
		r.Post("/rbac/roles", s.createRole)
		r.Delete("/rbac/roles/{id}", s.deleteRole)
		r.Get("/rbac/roles/{id}/permissions", s.rolePermissions)
		r.Post("/rbac/roles/{id}/permissions", s.setRolePermissions)
		r.Get("/rbac/permissions", s.listPermissions)
		r.Post("/rbac/permissions", s.createPermission)
		r.Delete("/rbac/permissions/{id}", s.deletePermission)

		r.Get("/device-templates", s.listDeviceTemplates)
		r.Post("/device-templates", s.createDeviceTemplate)
		r.Patch("/device-templates/{id}", s.updateDeviceTemplate)
		r.Delete("/device-templates/{id}", s.deleteDeviceTemplate)

		r.Get("/vendor-fingerprints", s.listVendorFingerprints)
		r.Post("/vendor-fingerprints", s.createVendorFingerprint)
		r.Patch("/vendor-fingerprints/{id}", s.updateVendorFingerprint)
		r.Delete("/vendor-fingerprints/{id}", s.deleteVendorFingerprint)

		r.Get("/audit-log", s.listAuditLog)

		// --- Security: encryption key lifecycle ----------------------
		r.Get("/security/encryption/status", s.encryptionStatus)
		r.Post("/security/encryption/generate", s.encryptionGenerate)
		r.Post("/security/encryption/validate", s.encryptionValidate)
		r.Post("/security/encryption/rotate", s.encryptionRotate)
		r.Post("/security/encryption/reset-credentials", s.encryptionResetCredentials)
		r.Get("/security/encryption/recovery-guide", s.encryptionRecoveryGuide)
		r.Get("/security/encryption/needs-reentry", s.credentialsNeedingReentry)
		r.Get("/security/startup-checklist", s.startupChecklist)

		// --- MIB upload engine ---------------------------------------
		r.Get("/mibs", s.listMibFiles)
		r.Post("/mibs", s.uploadMib)
		r.Get("/mibs/{id}/objects", s.listMibObjects)
		r.Get("/oid-mappings", s.listOIDMappings)
		r.Post("/oid-mappings", s.createOIDMapping)
		r.Delete("/oid-mappings/{id}", s.deleteOIDMapping)
	})
}

// ---- Device handlers --------------------------------------------------------

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cat := r.URL.Query().Get("category")
	if cat == "all" {
		rows, err := s.queries.ListAllDevices(ctx)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, rows)
		return
	}
	if cat == "" {
		cat = "switch"
	}
	rows, err := s.queries.ListDevicesByCategory(ctx, cat)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) deviceInterfaces(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	rows, err := s.queries.ListInterfaces(ctx, id) //nolint
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) deviceVLANs(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	rows, err := s.queries.ListVlans(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) deviceNeighbors(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	rows, err := s.queries.ListNeighbors(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) deviceTopology(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	links, err := s.queries.ListTopologyLinks(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, links)
}

func (s *Server) deviceStorage(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	rows, err := s.queries.ListServerStorage(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) deviceFacts(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	rows, err := s.queries.ListDeviceFacts(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) deviceVMs(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	rows, err := s.queries.ListVMsByHost(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) deviceCamera(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	row, err := s.queries.GetCameraInfo(ctx, id)
	if err != nil {
		// No camera_info row yet is not an error — return an empty object.
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (s *Server) deviceNVRChannels(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	rows, err := s.queries.ListNVRChannels(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) deviceWLAN(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	row, err := s.queries.GetWLANControllerInfo(ctx, id)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (s *Server) deviceAccessPoints(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	rows, err := s.queries.ListAccessPoints(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) deviceBMC(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	row, err := s.queries.GetBMCInfo(ctx, id)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{}) // no BMC collected yet
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (s *Server) deviceUPS(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	row, err := s.queries.GetUPSStatus(ctx, id)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{}) // no UPS status yet
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (s *Server) devicePrinterSupplies(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	rows, err := s.queries.ListPrinterSupplies(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) devicePhones(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	rows, err := s.queries.ListPbxPhones(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) deviceBMCSensors(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	rows, err := s.queries.ListBMCSensors(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) deviceRoles(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	rows, err := s.queries.ListDeviceRoles(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) firewallStatus(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	row, err := s.queries.GetFirewallStatus(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (s *Server) vpnTunnels(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	rows, err := s.queries.ListVpnTunnels(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) haMembers(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	rows, err := s.queries.ListHAMembers(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) licenses(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	rows, err := s.queries.ListLicenses(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// ---- Role handlers -----------------------------------------------------------

func (s *Server) roleSummary(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.RoleSummary(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) devicesByRole(w http.ResponseWriter, r *http.Request) {
	role := chi.URLParam(r, "role")
	if role == "" {
		writeErr(w, errBadRequest("role is required"))
		return
	}
	rows, err := s.queries.ListDevicesByRole(r.Context(), role)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// ---- Topology & search handlers ----------------------------------------------

// search accepts ?q= with IP, MAC, or hostname and returns a SearchResult.
func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query().Get("q")
	if q == "" {
		writeErr(w, errBadRequest("q parameter is required"))
		return
	}

	// Try IP first, then MAC, then hostname.
	if ip, err := netip.ParseAddr(q); err == nil {
		res, err := s.topo.SearchIP(ctx, ip)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
		return
	}
	if isMACLike(q) {
		res, err := s.topo.SearchMAC(ctx, normMAC(q))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
		return
	}
	results, err := s.topo.SearchHostname(ctx, q)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, results)
}

func (s *Server) allLinks(w http.ResponseWriter, r *http.Request) {
	links, err := s.topo.AllLinks(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, links)
}

// ---- Location handlers -------------------------------------------------------

func (s *Server) listLocations(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListRootLocations(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) childLocations(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathLocation(w, r)
	if !ok {
		return
	}
	rows, err := s.queries.ListChildLocations(ctx, &id) //nolint
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// locationSubnets lists the subnets bound to a site (for site-scoped scans).
func (s *Server) locationSubnets(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	rows, err := s.queries.ListSubnetsByLocation(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// listCredentialGroups lists credential groups with member/binding counts for
// the scan-time group multi-select. No secrets or credential identities are
// returned — only group names + counts.
func (s *Server) listCredentialGroups(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListCredentialGroups(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// ---- Helpers ----------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type apiError struct {
	Error string `json:"error"`
}

func writeErr(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

type badRequest struct{ msg string }

func errBadRequest(msg string) error { return &badRequest{msg} }
func (e *badRequest) Error() string  { return e.msg }

func pathDevice(w http.ResponseWriter, r *http.Request) (context.Context, uuid.UUID, bool) {
	return pathUUID(w, r, "id")
}

func pathLocation(w http.ResponseWriter, r *http.Request) (context.Context, uuid.UUID, bool) {
	return pathUUID(w, r, "id")
}

func pathUUID(w http.ResponseWriter, r *http.Request, param string) (context.Context, uuid.UUID, bool) {
	raw := chi.URLParam(r, param)
	id, err := uuid.Parse(raw)
	if err != nil {
		http.Error(w, "invalid UUID: "+raw, http.StatusBadRequest)
		return r.Context(), uuid.Nil, false
	}
	return r.Context(), id, true
}

func isMACLike(s string) bool {
	return len(s) >= 12 && (len(s) == 17 || len(s) == 12 || len(s) == 14)
}

func normMAC(s string) string {
	// Normalize aa:bb:cc:dd:ee:ff or aabbccddeff → lowercase colon-sep.
	return s // simplified; Phase 2 adds proper normalization
}
