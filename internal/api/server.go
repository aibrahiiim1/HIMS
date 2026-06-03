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
	cipher  *secret.Cipher // nil when no encryption key is configured
	queries *db.Queries
}

// NewServer wires dependencies and returns a ready-to-serve Server. cipher
// may be nil (no HIMS_ENCRYPTION_KEY set) — credential writes then return
// 503; everything else still serves.
func NewServer(queries *db.Queries, cipher *secret.Cipher) *Server {
	s := &Server{
		queries: queries,
		topo:    topology.New(queries),
		// The API can seed defaults + run on-demand sweeps; the scheduled
		// loop lives in the collector. Both share this engine type.
		mon:    monitoring.NewEngine(queries, monitoring.NewPoller(nil, 0), nil),
		alerts: alerting.NewEngine(queries, nil),
		cipher: cipher,
		router: chi.NewRouter(),
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
		// --- Devices --------------------------------------------------
		r.Get("/devices", s.listDevices)
		r.Get("/devices/{id}/interfaces", s.deviceInterfaces)
		r.Get("/devices/{id}/vlans", s.deviceVLANs)
		r.Get("/devices/{id}/neighbors", s.deviceNeighbors)
		r.Get("/devices/{id}/topology", s.deviceTopology)
		r.Get("/devices/{id}/storage", s.deviceStorage)
		r.Get("/devices/{id}/facts", s.deviceFacts)
		r.Get("/devices/{id}/roles", s.deviceRoles)
		r.Get("/devices/{id}/firewall-status", s.firewallStatus)
		r.Get("/devices/{id}/vpn-tunnels", s.vpnTunnels)
		r.Get("/devices/{id}/ha-members", s.haMembers)
		r.Get("/devices/{id}/licenses", s.licenses)
		r.Get("/devices/{id}/monitoring/checks", s.deviceMonitoringChecks)
		r.Get("/devices/{id}/monitoring/samples", s.deviceMonitoringSamples)

		// --- Topology & search ----------------------------------------
		// IP/MAC/name → switch+port+path (the headline Phase 1 feature).
		r.Get("/search", s.search) // ?q=<IP|MAC|name>
		r.Get("/topology/links", s.allLinks)

		// --- Locations -----------------------------------------------
		r.Get("/locations", s.listLocations)
		r.Get("/locations/{id}/children", s.childLocations)

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
		r.Put("/devices/{id}/credential", s.bindDeviceCredential)
	})
}

// ---- Device handlers --------------------------------------------------------

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cat := r.URL.Query().Get("category")
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
