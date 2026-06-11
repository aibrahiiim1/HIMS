package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// Web/HTTP access ports. Web-managed devices (NVRs/DVRs/cameras, appliances) do
// not always live on 80/443/8080/8443 — operators move the web/API surface to
// custom ports (Hikvision "8000 + host octet" → 8008/8010/8012, or 8081/8082…).
// This file: (1) a fleet-wide configurable candidate-port list, (2) a per-device
// override, (3) a builder that produces the ORDERED base URLs the HTTP/ISAPI/
// ONVIF collectors should try — discovered + configured + override BEFORE any
// hardcoded guess.

// --- port classification --------------------------------------------------

func isCCTVCategory(c string) bool {
	return c == "camera" || c == "nvr" || c == "dvr"
}

// webClass classifies an open TCP port for a device of the given category:
// the scheme to use (http/https/""), a human "looks like" kind, and whether it
// is a web/API candidate worth an HTTP/ISAPI probe.
func webClass(port int, category string) (scheme, kind string, web bool) {
	switch port {
	case 554:
		return "", "RTSP", false
	case 443, 8443:
		return "https", "HTTPS", true
	case 80:
		if isCCTVCategory(category) {
			return "http", "ONVIF / web UI", true
		}
		return "http", "HTTP", true
	case 8080, 8081, 8082:
		return "http", "HTTP (web UI)", true
	}
	if port >= 8000 && port <= 8099 {
		if isCCTVCategory(category) {
			return "http", "ISAPI-capable (Hikvision web)", true
		}
		return "http", "HTTP (web UI)", true
	}
	return "", knownService(port), false
}

// knownService names common non-web ports so the discovered-ports view can label
// them rather than showing a bare number ("unknown open TCP" for the rest).
func knownService(port int) string {
	switch port {
	case 22:
		return "SSH"
	case 23:
		return "Telnet"
	case 53:
		return "DNS"
	case 161:
		return "SNMP"
	case 389, 636:
		return "LDAP"
	case 445:
		return "SMB"
	case 3389:
		return "RDP"
	case 5985, 5986:
		return "WinRM"
	case 9100:
		return "JetDirect"
	case 1433, 1521, 5432:
		return "Database"
	}
	return "unknown open TCP"
}

// baseURL builds a canonical base URL, omitting the port for the scheme default.
func baseURL(ip, scheme string, port int) string {
	if scheme == "http" && port == 80 {
		return "http://" + ip
	}
	if scheme == "https" && port == 443 {
		return "https://" + ip
	}
	return fmt.Sprintf("%s://%s:%d", scheme, ip, port)
}

// parsePortsCSV parses "8010, 8000" into [8010,8000], dropping invalid entries.
func parsePortsCSV(s string) []int {
	var out []int
	for _, tok := range strings.Split(s, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if n, err := strconv.Atoi(tok); err == nil && n > 0 && n < 65536 {
			out = append(out, n)
		}
	}
	return out
}

// webCandidateBases returns the ORDERED base URLs an HTTP/ISAPI/ONVIF collector
// should try for a device, highest-confidence first:
//  1. the endpoint that last succeeded (web_last_ok),
//  2. the operator's per-device override (preferred port + alternates),
//  3. ports the scan discovered OPEN that look web-like ("use discovered before
//     guessing"),
//  4. the fleet-wide configured candidate ports.
//
// The ISAPI collector appends its own default scheme/port ladder as a final
// fallback and dedups, so this list only needs the device-specific + configured
// candidates. Returns nil when the device has no usable IP.
func (s *Server) webCandidateBases(ctx context.Context, d db.Device) []string {
	if d.PrimaryIp == nil || !d.PrimaryIp.IsValid() {
		return nil
	}
	ip := d.PrimaryIp.String()
	var out []string
	add := func(scheme string, port int) {
		switch scheme {
		case "both", "":
			out = append(out, baseURL(ip, "https", port), baseURL(ip, "http", port))
		default:
			out = append(out, baseURL(ip, scheme, port))
		}
	}

	// 1) last-OK endpoint (already a full base URL).
	if strings.TrimSpace(d.WebLastOk) != "" {
		out = append(out, d.WebLastOk)
	}
	// 2) per-device override — preferred port, then alternates. Unset scheme ⇒ try
	//    both (we don't know which the device speaks).
	ovScheme := d.WebSchemePref
	if ovScheme == "" {
		ovScheme = "both"
	}
	if d.WebPortPref != nil && *d.WebPortPref > 0 {
		add(ovScheme, int(*d.WebPortPref))
	}
	for _, p := range parsePortsCSV(d.WebAltPorts) {
		add(ovScheme, p)
	}
	// 3) discovered open web-like ports.
	for _, p := range s.deviceOpenPorts(ctx, d.ID) {
		if sch, _, web := webClass(p, d.Category); web {
			add(sch, p)
		}
	}
	// 4) fleet-wide configured candidate ports.
	if rows, err := s.queries.ListEnabledWebPortCandidates(ctx); err == nil {
		for _, c := range rows {
			add(c.Scheme, int(c.Port))
		}
	}
	// Dedupe, preserving first-seen order (same as the collector's internal dedupe),
	// so the displayed "endpoints tried" list is clean.
	seen := make(map[string]bool, len(out))
	deduped := out[:0]
	for _, b := range out {
		if seen[b] {
			continue
		}
		seen[b] = true
		deduped = append(deduped, b)
	}
	return deduped
}

// webBasesForPorts turns a set of just-discovered open ports into ordered web
// base URLs (scheme decided by webClass; the Hikvision 8000-octet ports are plain
// HTTP). Used to seed the CCTV collector's prefer list directly from the live scan
// result, since the discovery_result probe_data webCandidateBases reads is not yet
// persisted while the scan's collection runs. Non-web ports (e.g. 554/RTSP) drop.
func webBasesForPorts(ip, category string, ports []int) []string {
	// HTTP bases first, HTTPS after: the 8000-octet CCTV web/ISAPI ports are plain
	// HTTP and answer instantly, whereas a stray open HTTPS port (e.g. 8443) on these
	// recorders frequently hangs the full TLS timeout — trying it first burns the
	// collection budget before the working HTTP port is reached.
	var httpB, httpsB []string
	for _, p := range ports {
		sch, _, web := webClass(p, category)
		if !web {
			continue
		}
		if sch == "https" {
			httpsB = append(httpsB, baseURL(ip, "https", p))
		} else {
			httpB = append(httpB, baseURL(ip, "http", p))
		}
	}
	return append(httpB, httpsB...)
}

// enabledWebPorts returns the enabled configured candidate ports, for injection
// into the discovery scan's TCP port set (PipelineConfig.ExtraPorts) so custom
// web ports are discovered open and stored.
func (s *Server) enabledWebPorts(ctx context.Context) []int {
	rows, err := s.queries.ListEnabledWebPortCandidates(ctx)
	if err != nil {
		return nil
	}
	out := make([]int, 0, len(rows))
	for _, c := range rows {
		out = append(out, int(c.Port))
	}
	return out
}

// --- Settings: web port candidates CRUD -----------------------------------

type webPortDTO struct {
	ID      string `json:"id"`
	Port    int    `json:"port"`
	Scheme  string `json:"scheme"`
	Enabled bool   `json:"enabled"`
	Note    string `json:"note"`
}

func toWebPortDTO(c db.WebPortCandidate) webPortDTO {
	return webPortDTO{ID: c.ID.String(), Port: int(c.Port), Scheme: c.Scheme, Enabled: c.Enabled, Note: c.Note}
}

func validWebScheme(s string) bool { return s == "http" || s == "https" || s == "both" }

func (s *Server) listWebPorts(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListWebPortCandidates(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]webPortDTO, len(rows))
	for i, c := range rows {
		out[i] = toWebPortDTO(c)
	}
	writeJSON(w, http.StatusOK, out)
}

type webPortReq struct {
	Port    int    `json:"port"`
	Scheme  string `json:"scheme"`
	Enabled *bool  `json:"enabled"`
	Note    string `json:"note"`
}

func (s *Server) createWebPort(w http.ResponseWriter, r *http.Request) {
	var req webPortReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Port <= 0 || req.Port >= 65536 {
		http.Error(w, "port out of range (1..65535)", http.StatusBadRequest)
		return
	}
	if req.Scheme == "" {
		req.Scheme = "http"
	}
	if !validWebScheme(req.Scheme) {
		http.Error(w, "scheme must be http, https or both", http.StatusBadRequest)
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	c, err := s.queries.CreateWebPortCandidate(r.Context(), db.CreateWebPortCandidateParams{
		Port: int32(req.Port), Scheme: req.Scheme, Enabled: enabled, Note: strings.TrimSpace(req.Note),
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "config", "web_port.create", "web_port", c.ID.String(), "Added web candidate port "+strconv.Itoa(req.Port), map[string]any{"port": req.Port, "scheme": req.Scheme})
	writeJSON(w, http.StatusCreated, toWebPortDTO(c))
}

func (s *Server) updateWebPort(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	var req webPortReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Port <= 0 || req.Port >= 65536 {
		http.Error(w, "port out of range (1..65535)", http.StatusBadRequest)
		return
	}
	if !validWebScheme(req.Scheme) {
		http.Error(w, "scheme must be http, https or both", http.StatusBadRequest)
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	c, err := s.queries.UpdateWebPortCandidate(r.Context(), db.UpdateWebPortCandidateParams{
		ID: id, Port: int32(req.Port), Scheme: req.Scheme, Enabled: enabled, Note: strings.TrimSpace(req.Note),
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "config", "web_port.update", "web_port", id.String(), "Updated web candidate port "+strconv.Itoa(req.Port), map[string]any{"port": req.Port, "scheme": req.Scheme, "enabled": enabled})
	writeJSON(w, http.StatusOK, toWebPortDTO(c))
}

func (s *Server) deleteWebPort(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := s.queries.DeleteWebPortCandidate(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "config", "web_port.delete", "web_port", id.String(), "Deleted web candidate port", nil)
	w.WriteHeader(http.StatusNoContent)
}

// --- Device web-access view + override -------------------------------------

type discoveredPortDTO struct {
	Port   int    `json:"port"`
	Scheme string `json:"scheme"` // "" for non-web
	Kind   string `json:"kind"`   // RTSP / HTTPS / ISAPI-capable / ONVIF / web UI / unknown open TCP
	Web    bool   `json:"web"`    // is an HTTP/ISAPI candidate
}

type deviceWebAccessDTO struct {
	Discovered []discoveredPortDTO `json:"discovered"` // every open port, classified
	Candidates []string            `json:"candidates"` // ordered base URLs the collector will try first
	// Override
	Scheme    string `json:"scheme"`     // '', http, https
	Port      *int   `json:"port"`       // preferred port
	AltPorts  string `json:"alt_ports"`  // alternates (CSV)
	Notes     string `json:"notes"`      // notes
	PrefProto string `json:"pref_proto"` // '', isapi, onvif, http
	// Last successful collection — exactly what worked.
	LastProto      string `json:"last_proto"`      // isapi | onvif | http | …
	LastScheme     string `json:"last_scheme"`     // http | https
	LastPort       *int   `json:"last_port"`       // port that answered
	LastOK         string `json:"last_ok"`         // last successful base URL
	LastOKAt       string `json:"last_ok_at"`      // RFC3339, "" if never
	LastCredential string `json:"last_credential"` // credential name that authenticated
}

func (s *Server) deviceWebAccess(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	d, err := s.queries.GetDevice(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	disc := []discoveredPortDTO{}
	seenPort := map[int]bool{}
	for _, p := range s.deviceOpenPorts(ctx, d.ID) {
		if seenPort[p] {
			continue
		}
		seenPort[p] = true
		sch, kind, web := webClass(p, d.Category)
		disc = append(disc, discoveredPortDTO{Port: p, Scheme: sch, Kind: kind, Web: web})
	}
	out := deviceWebAccessDTO{
		Discovered: disc,
		Candidates: s.webCandidateBases(ctx, d),
		Scheme:     d.WebSchemePref,
		AltPorts:   d.WebAltPorts,
		Notes:      d.WebNotes,
		PrefProto:  d.WebPrefProto,
		LastProto:  d.WebLastProto,
		LastScheme: d.WebLastScheme,
		LastOK:     d.WebLastOk,
	}
	if d.WebPortPref != nil {
		p := int(*d.WebPortPref)
		out.Port = &p
	}
	if d.WebLastPort != nil {
		p := int(*d.WebLastPort)
		out.LastPort = &p
	}
	if d.WebLastOkAt != nil {
		out.LastOKAt = d.WebLastOkAt.Format("2006-01-02T15:04:05Z07:00")
	}
	if d.WebLastCredentialID != nil {
		if c, err := s.queries.GetCredential(ctx, *d.WebLastCredentialID); err == nil {
			out.LastCredential = c.Name
		}
	}
	writeJSON(w, http.StatusOK, out)
}

type setWebAccessReq struct {
	Scheme    string `json:"scheme"`     // '', http, https
	Port      *int   `json:"port"`       // null clears
	AltPorts  string `json:"alt_ports"`  // CSV
	Notes     string `json:"notes"`
	PrefProto string `json:"pref_proto"` // '', isapi, onvif, http
}

func (s *Server) setDeviceWebAccess(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	var req setWebAccessReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Scheme != "" && req.Scheme != "http" && req.Scheme != "https" {
		http.Error(w, "scheme must be empty, http or https", http.StatusBadRequest)
		return
	}
	switch req.PrefProto {
	case "", "isapi", "onvif", "http":
	default:
		http.Error(w, "pref_proto must be empty, isapi, onvif or http", http.StatusBadRequest)
		return
	}
	var port *int32
	if req.Port != nil {
		if *req.Port <= 0 || *req.Port >= 65536 {
			http.Error(w, "port out of range (1..65535)", http.StatusBadRequest)
			return
		}
		p := int32(*req.Port)
		port = &p
	}
	// Normalise the alternates CSV to valid ports only.
	alt := make([]string, 0)
	for _, p := range parsePortsCSV(req.AltPorts) {
		alt = append(alt, strconv.Itoa(p))
	}
	if err := s.queries.SetDeviceWebOverride(ctx, db.SetDeviceWebOverrideParams{
		ID: id, WebSchemePref: req.Scheme, WebPortPref: port,
		WebAltPorts: strings.Join(alt, ","), WebNotes: strings.TrimSpace(req.Notes),
		WebPrefProto: req.PrefProto,
	}); err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "inventory", "device.web_override", "device", id.String(), "Set web-access override", map[string]any{"scheme": req.Scheme, "alt_ports": strings.Join(alt, ",")})
	// Return the refreshed view.
	s.deviceWebAccess(w, r)
}
