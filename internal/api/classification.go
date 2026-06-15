package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/coralsearesorts/hims/internal/classify"
	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/osdiscovery"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/jackc/pgx/v5"
)

// Device classification surface (the OS/NVR-discovery add-on): expose the stored
// classification + evidence trail, re-run the evidence-based classifier from a
// live probe, and let an operator lock a classification so auto-discovery won't
// overwrite their manual decision.

type classificationDTO struct {
	Category    string                          `json:"category"`
	OSFamily    string                          `json:"os_family"`
	DeviceClass string                          `json:"device_class"`
	Confidence  *int16                          `json:"confidence_score"`
	Locked      bool                            `json:"classification_locked"`
	Evidence    []domain.ClassificationEvidence `json:"evidence"`
}

func toClassificationDTO(d db.Device) classificationDTO {
	ev, _ := domain.UnmarshalEvidence(d.ClassificationEvidence)
	dc := ""
	if d.DeviceClass != nil {
		dc = *d.DeviceClass
	}
	return classificationDTO{
		Category: d.Category, OSFamily: d.OsFamily, DeviceClass: dc,
		Confidence: d.ConfidenceScore, Locked: d.ClassificationLocked, Evidence: ev,
	}
}

// getClassification returns the device's stored classification + parsed evidence.
func (s *Server) getClassification(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	d, err := s.queries.GetDevice(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toClassificationDTO(d))
}

// reclassifyDevice probes the device live, runs the evidence-based classifier,
// and persists the result — unless the device is classification-locked, in which
// case it returns the current classification untouched.
func (s *Server) reclassifyDevice(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	d, err := s.queries.GetDevice(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if d.ClassificationLocked {
		writeJSON(w, http.StatusOK, map[string]any{
			"locked": true, "changed": false,
			"message":        "classification is locked; unlock to re-classify",
			"classification": toClassificationDTO(d),
		})
		return
	}
	if d.PrimaryIp == nil || !d.PrimaryIp.IsValid() {
		http.Error(w, "device has no IP to probe", http.StatusBadRequest)
		return
	}

	obs := osdiscovery.Probe(ctx, *d.PrimaryIp, osdiscovery.Options{})
	evidence := obs.Evidence()

	// Authoritative override: if we have a deep OS inventory (a successful
	// authenticated WinRM/SSH collection), its OS caption is the strongest OS
	// signal we have — it distinguishes a Windows *client* edition (workstation)
	// from a *Server* edition, which a network port probe cannot. Without this a
	// Win11 workstation that exposes WinRM/RDP would be relabelled a "server" off
	// the open-port heuristic alone. Fold the caption in at high confidence.
	hasCaption := false
	if oi, err := s.queries.GetOSInventory(ctx, id); err == nil && oi.OsCaption != nil && *oi.OsCaption != "" {
		evidence = append(evidence, classify.OSCaption(*oi.OsCaption)...)
		hasCaption = true
	}
	// Fall back to any stored SNMP sysDescr only when we have no authenticated
	// caption (os_version often carries the SNMP sysDescr for SNMP-only devices).
	if !hasCaption && d.OsVersion != nil && *d.OsVersion != "" {
		evidence = append(evidence, classify.SNMPSysDescr(*d.OsVersion)...)
	}
	res := classify.FromEvidence(evidence)

	// Vendor-fingerprint override (req #5: fingerprints must affect reclassify too).
	// Build evidence from the device's stored RAW SNMP identity facts (sysObjectID
	// / sysDescr / sysName) and the live HTTP banner, then match the effective
	// library (operator ∪ built-in). A product fingerprint (e.g. ExtremeCloud IQ
	// Controller, OID .1916.2.284) overrides the generic enterprise-prefix category
	// and supplies the canonical vendor + model — the same logic a scan applies.
	fp := s.reclassifyFingerprint(ctx, d, obs)
	if fp.category != "" && fp.confidence >= res.Confidence {
		res.Category = fp.category
		res.Confidence = fp.confidence
		res.OSFamily = "" // a network appliance has no OS family to assert
		res.Evidence = append(res.Evidence, domain.ClassificationEvidence{
			Source: fp.source, Signal: fp.detail, Category: fp.category, Confidence: fp.confidence,
		})
	}

	// Switch subtype (access / distribution / core): switches & routers earn a
	// subtype from their collected topology — interface/VLAN/neighbour counts —
	// even when the live port-probe yields no category signal. This is why their
	// Classification card was previously blank.
	if d.Category == string(domain.CatSwitch) || d.Category == string(domain.CatRouter) || d.Category == string(domain.CatISPRouter) {
		if sub, sconf, sev := s.switchSubtypeFromInventory(ctx, d); sub != "" {
			res.Category = d.Category
			if int(sconf) > res.Confidence {
				res.Confidence = int(sconf)
			}
			res.Subtype = sub
			res.Evidence = append(res.Evidence, sev...)
		}
	}

	if res.Confidence == 0 || res.Category == string(domain.CatUnknown) {
		// No classifying signal — do NOT downgrade an existing category.
		writeJSON(w, http.StatusOK, map[string]any{
			"changed": false, "message": "no classifying signals from probe",
			"classification": toClassificationDTO(d),
		})
		return
	}

	// Persist the fingerprint's canonical vendor/model (COALESCE-enrich: blanks
	// never clobber). Skipped when the device is locked (handled above) so this
	// only runs on an unlocked reclassify.
	if fp.vendor != "" || fp.model != "" {
		_ = s.queries.UpdateDeviceHardwareInfo(ctx, db.UpdateDeviceHardwareInfoParams{
			ID: id, Vendor: fp.vendor, Model: fp.model,
		})
	}

	evBlob, err := domain.MarshalEvidence(res.Evidence)
	if err != nil {
		writeErr(w, err)
		return
	}
	devClass := res.Subtype
	if devClass == "" && d.DeviceClass != nil {
		devClass = *d.DeviceClass // don't clear an operator's subtype when the classifier has none
	}
	conf := int16(res.Confidence)
	var devClassPtr *string
	if devClass != "" {
		devClassPtr = &devClass
	}
	updated, err := s.queries.UpdateDeviceClassification(ctx, db.UpdateDeviceClassificationParams{
		ID: id, Category: res.Category, OsFamily: res.OSFamily,
		DeviceClass: devClassPtr, ConfidenceScore: &conf, ClassificationEvidence: evBlob,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) { // raced with a lock
			writeJSON(w, http.StatusOK, map[string]any{"locked": true, "changed": false, "classification": toClassificationDTO(d)})
			return
		}
		writeErr(w, err)
		return
	}
	s.audit(r, "inventory", "device.reclassify", "device", id.String(),
		"Re-classified "+updated.Name+" → "+res.Category,
		map[string]any{"category": res.Category, "os_family": res.OSFamily, "confidence": res.Confidence})
	writeJSON(w, http.StatusOK, map[string]any{"changed": true, "classification": toClassificationDTO(updated)})
}

// switchRouterIdentities returns the lowercased names and the IPs of every
// known switch/router/isp_router device — used to tell an infrastructure uplink
// (neighbour is a switch) from an edge/downlink (neighbour is an AP/phone/camera).
func (s *Server) switchRouterIdentities(ctx context.Context) (names map[string]bool, ips map[string]bool) {
	names, ips = map[string]bool{}, map[string]bool{}
	devs, err := s.queries.ListAllDevices(ctx)
	if err != nil {
		return
	}
	for _, x := range devs {
		if x.Category != string(domain.CatSwitch) && x.Category != string(domain.CatRouter) && x.Category != string(domain.CatISPRouter) {
			continue
		}
		if x.Name != "" {
			names[strings.ToLower(strings.TrimSpace(x.Name))] = true
		}
		if x.PrimaryIp != nil && x.PrimaryIp.IsValid() {
			ips[x.PrimaryIp.String()] = true
		}
	}
	return
}

// switchSubtypeFromInventory scores a switch/router's access/distribution/core
// subtype from its collected interfaces, VLANs and LLDP/CDP neighbours.
func (s *Server) switchSubtypeFromInventory(ctx context.Context, d db.Device) (string, int16, []domain.ClassificationEvidence) {
	ifs, _ := s.queries.ListInterfaces(ctx, d.ID)
	vlans, _ := s.queries.ListVlans(ctx, d.ID)
	neigh, _ := s.queries.ListNeighbors(ctx, d.ID)
	up := 0
	for _, i := range ifs {
		if i.OperStatus != nil && *i.OperStatus == 1 {
			up++
		}
	}
	// Infrastructure uplinks = local ports whose LLDP/CDP neighbour is another
	// switch/router we know (APs, phones and cameras also speak LLDP, so counting
	// every neighbour would mislabel a floor access switch as core).
	swNames, swIPs := s.switchRouterIdentities(ctx)
	uplinkPorts := map[int32]bool{}
	for _, n := range neigh {
		if n.LocalIfIndex == nil {
			continue
		}
		nm := ""
		if n.RemSysName != nil {
			nm = strings.ToLower(strings.TrimSpace(*n.RemSysName))
		}
		ip := ""
		if n.RemMgmtIp != nil {
			ip = n.RemMgmtIp.String()
		}
		if (nm != "" && swNames[nm]) || (ip != "" && swIPs[ip]) {
			uplinkPorts[*n.LocalIfIndex] = true
		}
	}
	vendor, model := "", ""
	if d.Vendor != nil {
		vendor = *d.Vendor
	}
	if d.Model != nil {
		model = *d.Model
	}
	sub, conf, ev := classify.SwitchSubtype(classify.SwitchSignals{
		Vendor: vendor, Model: model, Hostname: d.Name,
		PortsTotal: len(ifs), PortsUp: up, VLANs: len(vlans), Uplinks: len(uplinkPorts),
	})
	return sub, int16(conf), ev
}

type lockReq struct {
	Locked bool `json:"locked"`
}

// setClassificationLock toggles the manual-override lock on a device's
// classification (operator override preservation).
func (s *Server) setClassificationLock(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	var req lockReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	d, err := s.queries.SetClassificationLock(ctx, db.SetClassificationLockParams{ID: id, ClassificationLocked: req.Locked})
	if err != nil {
		writeErr(w, err)
		return
	}
	verb := "unlocked"
	if req.Locked {
		verb = "locked"
	}
	s.audit(r, "inventory", "device.classification_lock", "device", id.String(),
		"Classification "+verb+" for "+d.Name, map[string]any{"locked": req.Locked})
	writeJSON(w, http.StatusOK, toClassificationDTO(d))
}
