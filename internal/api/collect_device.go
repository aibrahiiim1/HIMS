package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/collect"
	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// deviceCollectReq optionally overrides the collector kind + a few vendor knobs.
// All fields are optional: with an empty body the kind is inferred from the
// device's category/vendor and ALL stored credentials are tried automatically.
type deviceCollectReq struct {
	Kind        string `json:"kind"` // vsphere|hyperv|redfish|onvif|unifi|omada|ruckus|extreme|cucm
	OmadaCID    string `json:"omada_cid"`
	CUCMVersion string `json:"cucm_version"`
	ExtremeBase string `json:"extreme_base"`
}

// collectDevice handles POST /devices/{id}/collect — a ONE-CLICK, profile-free
// deep collection for any device. It reuses internal/collect.Controller (the
// same core the controller-import and CLI use), which auto-tries every stored
// credential and persists the result; no Vendor Connection Profile is required.
// The collector kind is taken from the request body or inferred from the
// device's category/vendor. This is the "operator gave the inputs, the system
// does the protocol work" path applied to vSphere / Hyper-V / Redfish BMC /
// ONVIF cameras / UniFi / Omada / Ruckus / Extreme / CUCM.
func (s *Server) collectDevice(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	if s.reg == nil || s.fetcher == nil || s.cipher() == nil {
		http.Error(w, "collection not configured on this server (needs DB + encryption key)", http.StatusServiceUnavailable)
		return
	}
	dev, err := s.queries.GetDevice(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if dev.PrimaryIp == nil || !dev.PrimaryIp.IsValid() {
		writeJSON(w, http.StatusOK, map[string]any{"collected": false, "detail": "device has no IP address to collect from"})
		return
	}

	var req deviceCollectReq
	_ = json.NewDecoder(r.Body).Decode(&req)
	kind := strings.TrimSpace(req.Kind)
	if kind == "" {
		kind = inferControllerKind(dev)
	}
	if kind == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"collected": false,
			"detail":    "could not infer how to collect this device from its type/vendor — choose a collector kind (vsphere, hyperv, redfish, onvif, unifi, omada, ruckus, extreme, cucm).",
		})
		return
	}

	// Detach from the request lifecycle: deep collection can run for minutes and
	// a client navigation must not abort it. Generous deadline still bounds it.
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	defer cancel()

	// vSphere/ESXi has a dedicated collector that also accepts ssh-kind root
	// credentials (the common ESXi case) — prefer it over the generic core.
	if kind == "vsphere" {
		vr := s.runVSphereCollection(cctx, dev)
		if !vr.ok() {
			writeJSON(w, http.StatusOK, map[string]any{"collected": false, "kind": kind, "detail": nz(vr.Detail, vr.Reason)})
			return
		}
		s.audit(r, "inventory", "device.collect", "device", id.String(), "Collected vsphere for "+dev.Name, map[string]any{"kind": kind})
		writeJSON(w, http.StatusOK, map[string]any{"collected": true, "kind": kind, "detail": vr.Detail, "device_id": dev.ID.String()})
		return
	}

	opts := collect.ControllerOpts{OmadaCID: req.OmadaCID, CUCMVersion: req.CUCMVersion, ExtremeBase: req.ExtremeBase}
	res, cerr := collect.Controller(cctx, s.collectDeps(cctx), kind, *dev.PrimaryIp, dev.LocationID, opts)
	if cerr != nil {
		writeJSON(w, http.StatusOK, map[string]any{"collected": false, "kind": kind, "detail": "collection via " + kind + " failed: " + shortErr(cerr)})
		return
	}
	s.audit(r, "inventory", "device.collect", "device", id.String(), "Collected "+kind+" for "+dev.Name, map[string]any{"kind": kind})
	writeJSON(w, http.StatusOK, map[string]any{"collected": true, "kind": kind, "detail": nz(res.Summary, "collected via "+kind), "device_id": res.DeviceID.String()})
}

// collectViaController runs a profile's deep collection through the
// internal/collect orchestrator (auth-by-IP, all stored credentials, persists).
// Used by run-collection for vendor types whose collector is the shared core
// (redfish / hyperv / onvif / unifi / omada / ruckus / extreme). config knobs
// (controller_id, version, api_base) map onto collect.ControllerOpts.
func (s *Server) collectViaController(ctx context.Context, kind string, dev db.Device, cfg vpConfig) (bool, string) {
	if dev.PrimaryIp == nil || !dev.PrimaryIp.IsValid() {
		return false, "device has no IP address to collect from"
	}
	opts := collect.ControllerOpts{OmadaCID: cfg.ControllerID, CUCMVersion: cfg.Version, ExtremeBase: cfg.APIBase}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	defer cancel()
	res, err := collect.Controller(cctx, s.collectDeps(cctx), kind, *dev.PrimaryIp, dev.LocationID, opts)
	if err != nil {
		return false, "collection via " + kind + " failed: " + shortErr(err)
	}
	return res.DeviceID != uuid.Nil, nz(res.Summary, "collected via "+kind)
}

// inferControllerKind picks a collect.Controller kind from a device's category
// and vendor. Returns "" when the type is ambiguous (the caller then asks the
// operator to choose). vSphere/ONVIF/CUCM map straight from category; wireless
// and BMC need a vendor hint to pick the right client.
func inferControllerKind(d db.Device) string {
	lc := func(p *string) string {
		if p == nil {
			return ""
		}
		return strings.ToLower(*p)
	}
	vendor, model, class := lc(d.Vendor), lc(d.Model), lc(d.DeviceClass)
	hay := vendor + " " + model + " " + class

	// Class/vendor hints win first — a host classified "server" but with an
	// esxi/vcenter/hyperv device_class is still a hypervisor.
	switch {
	case strings.Contains(hay, "esxi"), strings.Contains(hay, "vcenter"), strings.Contains(hay, "vsphere"):
		return "vsphere"
	case strings.Contains(hay, "hyper-v"), strings.Contains(hay, "hyperv"):
		return "hyperv"
	}

	switch domain.DeviceCategory(d.Category) {
	case domain.CatVirtualHost:
		return "vsphere"
	case domain.CatCamera, domain.CatNVR, domain.CatDVR:
		return "onvif"
	case domain.CatPBX, domain.CatVoiceGateway:
		return "cucm"
	}

	// Wireless controllers/APs: pick the REST client from the vendor.
	if d.Category == string(domain.CatWirelessController) || d.Category == string(domain.CatAccessPoint) {
		switch {
		case strings.Contains(hay, "ubiquiti"), strings.Contains(hay, "unifi"):
			return "unifi"
		case strings.Contains(hay, "omada"), strings.Contains(hay, "tp-link"), strings.Contains(hay, "tplink"):
			return "omada"
		case strings.Contains(hay, "ruckus"):
			return "ruckus"
		case strings.Contains(hay, "extreme"), strings.Contains(hay, "aerohive"):
			return "extreme"
		}
		return ""
	}

	// Servers exposing a BMC (iLO / iDRAC / generic Redfish).
	if d.Category == string(domain.CatServer) {
		if strings.Contains(hay, "ilo") || strings.Contains(hay, "idrac") || strings.Contains(hay, "redfish") ||
			strings.Contains(hay, "bmc") || strings.Contains(hay, "hpe") || strings.Contains(hay, "supermicro") {
			return "redfish"
		}
	}
	return ""
}
