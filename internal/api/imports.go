package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"time"

	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/collect"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// collectDeps assembles the shared collection dependencies from the server,
// applying the operator-configured HTTP + WinRM timeouts.
func (s *Server) collectDeps(ctx context.Context) collect.Deps {
	m := s.resolveSettings(ctx)
	return collect.Deps{
		Queries: s.queries, Reg: s.reg, Fetcher: s.fetcher, Decrypt: s.scanDecrypt,
		HTTPTimeout:  time.Duration(m["http_timeout_ms"]) * time.Millisecond,
		WinRMTimeout: time.Duration(m["winrm_timeout_ms"]) * time.Millisecond,
	}
}

type controllerImportReq struct {
	Kind        string  `json:"kind"` // redfish|vsphere|hyperv|onvif|unifi|omada|ruckus|extreme|cucm
	IP          string  `json:"ip"`
	LocationID  *string `json:"location_id"`
	OmadaCID    string  `json:"omada_cid"`
	CUCMVersion string  `json:"cucm_version"`
	ExtremeBase string  `json:"extreme_base"`
}

// startControllerImport launches a single-target controller/host collection
// (UniFi/Ruckus/Omada/Extreme/vSphere/Hyper-V/Redfish/ONVIF/CUCM) as a
// background job, reusing internal/collect — the same cores the CLI uses.
func (s *Server) startControllerImport(w http.ResponseWriter, r *http.Request) {
	if s.reg == nil || s.fetcher == nil || s.cipher == nil {
		http.Error(w, "import not configured on this server (needs DB + encryption key)", http.StatusServiceUnavailable)
		return
	}
	var req controllerImportReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	ip, err := netip.ParseAddr(req.IP)
	if err != nil {
		http.Error(w, "invalid ip: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Kind == "" {
		http.Error(w, "kind is required", http.StatusBadRequest)
		return
	}
	locID := parseUUIDPtr(req.LocationID)
	job, err := s.queries.CreateDiscoveryJob(r.Context(), db.CreateDiscoveryJobParams{LocationID: locID})
	if err != nil {
		writeErr(w, err)
		return
	}
	_ = s.queries.UpdateDiscoveryJobStatus(r.Context(), db.UpdateDiscoveryJobStatusParams{
		ID: job.ID, Status: "running", HostCount: 1, FoundCount: 0,
	})
	opts := collect.ControllerOpts{OmadaCID: req.OmadaCID, CUCMVersion: req.CUCMVersion, ExtremeBase: req.ExtremeBase}
	go s.runControllerImport(job.ID, req.Kind, ip, locID, opts)
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) runControllerImport(jobID uuid.UUID, kind string, ip netip.Addr, locID *uuid.UUID, opts collect.ControllerOpts) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	res, err := collect.Controller(ctx, s.collectDeps(ctx), kind, ip, locID, opts)
	status, found := "completed", int32(1)
	var errMsg *string
	if err != nil {
		status, found = "failed", 0
		m := err.Error()
		errMsg = &m
	} else if res.DeviceID != uuid.Nil {
		// Record one result row so the device is linkable from the job.
		row, rerr := s.queries.CreateDiscoveryResult(ctx, db.CreateDiscoveryResultParams{
			JobID: jobID, Ip: ip, Outcome: "enrolled", ProbeData: []byte("{}"),
		})
		if rerr == nil {
			devID := res.DeviceID
			drv, cat := kind, kind
			_ = s.queries.UpdateDiscoveryResult(ctx, db.UpdateDiscoveryResultParams{
				ID: row.ID, Outcome: "enrolled", DeviceID: &devID, Driver: &drv, Category: &cat,
			})
		}
	}
	_ = s.queries.UpdateDiscoveryJobStatus(context.Background(), db.UpdateDiscoveryJobStatusParams{
		ID: jobID, Status: status, HostCount: 1, FoundCount: found, Error: errMsg,
	})
}

type adBrowseReq struct {
	DCHost string `json:"dc_host"`
	BaseDN string `json:"base_dn"` // optional; empty = directory root (RootDSE)
}

// browseAD binds the DC and returns its OU tree for the graphical browser.
func (s *Server) browseAD(w http.ResponseWriter, r *http.Request) {
	if s.fetcher == nil || s.cipher == nil {
		http.Error(w, "AD browse not configured (needs DB + encryption key)", http.StatusServiceUnavailable)
		return
	}
	var req adBrowseReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.DCHost == "" {
		http.Error(w, "dc_host is required", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	res, err := collect.ADBrowse(ctx, s.collectDeps(ctx), req.DCHost, req.BaseDN)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

type adImportReq struct {
	DCHost     string  `json:"dc_host"`
	BaseDN     string  `json:"base_dn"`
	LocationID *string `json:"location_id"`
}

// startADImport launches an AD computer-object import (LDAP, selected OU
// subtree) as a background job, reusing internal/collect.ADImport.
func (s *Server) startADImport(w http.ResponseWriter, r *http.Request) {
	if s.reg == nil || s.fetcher == nil || s.cipher == nil {
		http.Error(w, "import not configured on this server (needs DB + encryption key)", http.StatusServiceUnavailable)
		return
	}
	var req adImportReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.DCHost == "" || req.BaseDN == "" {
		http.Error(w, "dc_host and base_dn are required", http.StatusBadRequest)
		return
	}
	locID := parseUUIDPtr(req.LocationID)
	job, err := s.queries.CreateDiscoveryJob(r.Context(), db.CreateDiscoveryJobParams{LocationID: locID})
	if err != nil {
		writeErr(w, err)
		return
	}
	_ = s.queries.UpdateDiscoveryJobStatus(r.Context(), db.UpdateDiscoveryJobStatusParams{
		ID: job.ID, Status: "running", HostCount: 0, FoundCount: 0,
	})
	go s.runADImport(job.ID, req.DCHost, req.BaseDN, locID)
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) runADImport(jobID uuid.UUID, dcHost, baseDN string, locID *uuid.UUID) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	res, err := collect.ADImport(ctx, s.collectDeps(ctx), dcHost, baseDN, locID)
	status := "completed"
	var errMsg *string
	if err != nil {
		status = "failed"
		m := err.Error()
		errMsg = &m
	}
	_ = s.queries.UpdateDiscoveryJobStatus(context.Background(), db.UpdateDiscoveryJobStatusParams{
		ID: jobID, Status: status, HostCount: int32(res.Found), FoundCount: int32(res.Imported), Error: errMsg,
	})
}
