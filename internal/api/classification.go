package api

import (
	"encoding/json"
	"errors"
	"net/http"

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
	// Fold in any SNMP sysDescr we already have stored (os_version often carries it).
	if d.OsVersion != nil && *d.OsVersion != "" {
		obs.SNMPSysDescr = *d.OsVersion
	}
	res := classify.FromEvidence(obs.Evidence())

	if res.Confidence == 0 || res.Category == string(domain.CatUnknown) {
		// No classifying signal — do NOT downgrade an existing category.
		writeJSON(w, http.StatusOK, map[string]any{
			"changed": false, "message": "no classifying signals from probe",
			"classification": toClassificationDTO(d),
		})
		return
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
