package api

import (
	"context"
	"crypto/tls"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/classify"
	"github.com/coralsearesorts/hims/internal/credtest"
	"github.com/coralsearesorts/hims/internal/discovery"
	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/onvif"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// Deep CCTV onboarding (Stage C). Tries ONVIF / HTTP credentials against a
// camera/NVR/DVR, and on success binds the credential, collects ONVIF device
// info (manufacturer/model/firmware/serial + media profiles), classifies
// camera vs NVR/DVR from the model, persists camera_info, and records every
// attempt to credential-test history. Secrets are decrypted in-memory only.

type cctvResult struct {
	Status         string // collected | failed
	Reason         string
	Detail         string
	CredentialUsed string
	Category       string // camera | nvr
}

func (r cctvResult) ok() bool { return r.Status == "collected" }

func (s *Server) runCCTVCollection(ctx context.Context, d db.Device) cctvResult {
	res := cctvResult{Status: "failed"}
	if d.PrimaryIp == nil || !d.PrimaryIp.IsValid() {
		res.Reason, res.Detail = "no_ip", "device has no IP to collect from"
		return res
	}
	ip := d.PrimaryIp.String()
	cph := s.cipher()
	if cph == nil {
		res.Reason, res.Detail = "encryption_unavailable", "encryption key not loaded; cannot decrypt credentials"
		return res
	}

	type cc struct {
		id         uuid.UUID
		name       string
		user, pass string
	}
	const maxCands = 6
	var cands []cc
	seen := map[uuid.UUID]bool{}
	add := func(c db.Credential) {
		if seen[c.ID] || len(cands) >= maxCands || (c.Kind != string(domain.CredONVIF) && c.Kind != string(domain.CredHTTPBasic)) {
			return
		}
		seen[c.ID] = true
		plain, err := cph.Open(c.EncryptedBlob, c.KeyID)
		if err != nil {
			return
		}
		u, p := credtest.SplitUserPass(string(plain))
		cands = append(cands, cc{c.ID, c.Name, u, p})
	}
	if d.CredentialID != nil {
		if c, err := s.queries.GetCredential(ctx, *d.CredentialID); err == nil {
			add(c)
		}
	}
	if all, err := s.queries.ListCredentials(ctx); err == nil {
		for _, c := range all {
			add(c)
		}
	}
	if len(cands) == 0 {
		res.Reason, res.Detail = "no_credential", "no usable ONVIF/HTTP credential — add one"
		return res
	}

	doer := &http.Client{
		Timeout:   15 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS10}},
	}

	var attempts []discovery.CredAttempt
	lastReason, lastDetail := "auth_failed", "ONVIF authentication rejected"
	for _, cd := range cands {
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		info, err := onvif.Collect(cctx, onvif.NewClient("http://"+ip, cd.user, cd.pass, doer))
		cancel()
		category, detail := "success", "ONVIF authenticated"
		if err != nil {
			category, detail = categorizeCollectErr("onvif", err.Error())
		}
		attempts = append(attempts, discovery.CredAttempt{
			CredentialID: cd.id, Kind: domain.CredONVIF, Protocol: "onvif",
			Success: err == nil, Category: category, Detail: detail,
		})
		if err != nil {
			lastReason, lastDetail = category, detail
			continue
		}

		// Classify camera vs NVR/DVR from the model (Hikvision DS-7/8/9 = recorder).
		cat := domain.CatCamera
		dc := "ip_camera"
		for _, e := range classify.ISAPIDeviceInfo("", info.Model) {
			if e.Category == string(domain.CatNVR) {
				cat, dc = domain.CatNVR, "nvr"
			}
		}
		if blob, merr := domain.MarshalEvidence(nil); merr == nil {
			conf := int16(88)
			_, _ = s.queries.UpdateDeviceClassification(ctx, db.UpdateDeviceClassificationParams{
				ID: d.ID, Category: string(cat), OsFamily: domain.OSFamilyEmbedded,
				DeviceClass: &dc, ConfidenceScore: &conf, ClassificationEvidence: blob,
			})
		}
		_ = s.queries.UpdateDeviceHardwareInfo(ctx, db.UpdateDeviceHardwareInfoParams{
			ID: d.ID, Vendor: info.Manufacturer, Model: info.Model, Serial: info.Serial,
		})
		mfr, model, resolution := info.Manufacturer, info.Model, info.Resolution()
		onvifURL := "http://" + ip + "/onvif/device_service"
		_, _ = s.queries.UpsertCameraInfo(ctx, db.UpsertCameraInfoParams{
			DeviceID: d.ID, Manufacturer: strPtrOrNil(mfr), Model: strPtrOrNil(model),
			Resolution: strPtrOrNil(resolution), OnvifUrl: &onvifURL,
		})
		cid := cd.id
		_ = s.queries.SetDeviceCredential(ctx, db.SetDeviceCredentialParams{ID: d.ID, CredentialID: &cid})
		_ = s.queries.UpdateDeviceMonitoringStatus(ctx, db.UpdateDeviceMonitoringStatusParams{ID: d.ID, Status: "up"})

		res = cctvResult{Status: "collected", CredentialUsed: cd.name, Category: string(cat),
			Detail: "collected via ONVIF using credential " + cd.name}
		s.persistScanCredAttempts(ctx, d, attempts)
		return res
	}
	s.persistScanCredAttempts(ctx, d, attempts)
	res.Reason, res.Detail = lastReason, lastDetail
	return res
}

func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// collectCCTV handles POST /devices/{id}/collect-cctv — operator-triggered ONVIF
// onboarding for a camera/NVR/DVR.
func (s *Server) collectCCTV(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	d, err := s.queries.GetDevice(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	res := s.runCCTVCollection(ctx, d)
	if res.ok() {
		s.audit(r, "inventory", "device.collect_cctv", "device", id.String(),
			"Collected ONVIF facts for "+d.Name, map[string]any{"category": res.Category})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"collected": res.ok(), "reason": res.Reason, "detail": res.Detail,
		"credential_used": res.CredentialUsed, "category": res.Category,
	})
}
