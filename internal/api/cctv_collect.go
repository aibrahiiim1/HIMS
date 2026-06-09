package api

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/classify"
	"github.com/coralsearesorts/hims/internal/credtest"
	"github.com/coralsearesorts/hims/internal/discovery"
	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/isapi"
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
		kind       string
		user, pass string
	}
	const maxCands = 12 // sites can have many onvif/http_basic creds; try enough to find the camera/NVR's web login
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
		cands = append(cands, cc{id: c.ID, name: c.Name, kind: c.Kind, user: u, pass: p})
	}
	if d.CredentialID != nil {
		if c, err := s.queries.GetCredential(ctx, *d.CredentialID); err == nil {
			add(c)
		}
	}
	// Only spray ALL stored credentials when none is bound to the device. Cameras/
	// NVRs (notably Hikvision) lock out a source IP after a few failed logins, so
	// once an operator binds the correct credential we try ONLY that one — both to
	// avoid the lockout and to respect the operator's choice.
	if len(cands) == 0 {
		if all, err := s.queries.ListCredentials(ctx); err == nil {
			for _, c := range all {
				add(c)
			}
		}
	}
	if len(cands) == 0 {
		res.Reason, res.Detail = "no_credential", "no usable ONVIF/HTTP credential — add one and bind it to this device"
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

	// ONVIF unavailable (e.g. disabled, or no plain-HTTP/:80) — fall back to
	// Hikvision ISAPI over HTTPS. It yields the definitive deviceType (NVR/DVR vs
	// IPCamera) + identity AND, for recorders, the full inventory (channels, HDDs,
	// recording/health). Bound-credential-only (above) avoids the lockout.
	for _, cd := range cands {
		ictx, cancel := context.WithTimeout(ctx, 90*time.Second) // deviceInfo + channel/storage endpoints
		nvr, err := isapi.Collect(ictx, ip, cd.user, cd.pass, nil)
		cancel()
		category, detail := "success", "ISAPI authenticated"
		if err != nil {
			category, detail = categorizeCollectErr("isapi", err.Error())
			if category == "auth_failed" {
				detail += " — use the device WEB login (the ONVIF user is separate); repeated failures can trigger a Hikvision IP lockout"
			}
		}
		akind := domain.CredHTTPBasic
		if cd.kind == string(domain.CredONVIF) {
			akind = domain.CredONVIF
		}
		attempts = append(attempts, discovery.CredAttempt{
			CredentialID: cd.id, Kind: akind, Protocol: "isapi",
			Success: err == nil, Category: category, Detail: detail,
		})
		if err != nil {
			lastReason, lastDetail = category, detail
			continue
		}
		info := nvr.Info
		// deviceType is the definitive NVR vs camera signal (model corroborates).
		cat := domain.CatCamera
		dc := "ip_camera"
		for _, e := range classify.ISAPIDeviceInfo(info.DeviceType, info.Model) {
			if e.Category == string(domain.CatNVR) {
				cat, dc = domain.CatNVR, "nvr"
				break
			}
		}
		if blob, merr := domain.MarshalEvidence(nil); merr == nil {
			conf := int16(92)
			_, _ = s.queries.UpdateDeviceClassification(ctx, db.UpdateDeviceClassificationParams{
				ID: d.ID, Category: string(cat), OsFamily: domain.OSFamilyEmbedded,
				DeviceClass: &dc, ConfidenceScore: &conf, ClassificationEvidence: blob,
			})
		}
		vendor := info.Manufacturer
		if vendor == "" {
			vendor = "Hikvision"
		}
		_ = s.queries.UpdateDeviceHardwareInfo(ctx, db.UpdateDeviceHardwareInfoParams{
			ID: d.ID, Vendor: vendor, Model: info.Model, Serial: info.Serial,
		})
		_, _ = s.queries.UpsertCameraInfo(ctx, db.UpsertCameraInfoParams{
			DeviceID: d.ID, Manufacturer: strPtrOrNil(vendor), Model: strPtrOrNil(info.Model),
		})
		s.persistNVR(ctx, d, vendor, nvr) // nvr_info + channels + HDDs (recorders)
		cid := cd.id
		_ = s.queries.SetDeviceCredential(ctx, db.SetDeviceCredentialParams{ID: d.ID, CredentialID: &cid})
		_ = s.queries.UpdateDeviceMonitoringStatus(ctx, db.UpdateDeviceMonitoringStatusParams{ID: d.ID, Status: "up"})

		res = cctvResult{Status: "collected", CredentialUsed: cd.name, Category: string(cat),
			Detail: nvrDetail(cat, vendor, info, nvr)}
		s.persistScanCredAttempts(ctx, d, attempts)
		return res
	}

	s.persistScanCredAttempts(ctx, d, attempts)
	res.Reason, res.Detail = lastReason, lastDetail
	return res
}

// collectCCTVProfile onboards a camera/NVR/DVR using a Vendor Connection Profile:
// authenticate to the PROFILE's target URL/IP with the profile's bound ONVIF/HTTP
// credential, collect manufacturer/model/firmware/serial, classify camera vs NVR
// vs DVR from authenticated device evidence, bind on success, and record the
// attempt to Credential Test History. No secrets logged; binds only on success.
func (s *Server) collectCCTVProfile(ctx context.Context, p db.VendorConnectionProfile, d db.Device) profileCollect {
	out := profileCollect{Category: string(domain.CatCamera)}
	user, pass, credID, kind, ok := s.vendorProfileCred(ctx, p)
	if !ok {
		out.Detail = "no usable credential bound to this profile (or encryption key not loaded)"
		return out
	}
	host := stripScheme(strings.TrimRight(p.TargetUrl, "/"))
	if host == "" && d.PrimaryIp != nil && d.PrimaryIp.IsValid() {
		host = d.PrimaryIp.String()
	}
	doer := &http.Client{
		Timeout:   15 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS10}},
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	info, err := onvif.Collect(cctx, onvif.NewClient("http://"+host, user, pass, doer))
	cancel()
	category, detail := "success", "ONVIF authenticated"
	if err != nil {
		category, detail = categorizeCollectErr("onvif", err.Error())
	}
	s.persistScanCredAttempts(ctx, d, []discovery.CredAttempt{{
		CredentialID: credID, Kind: kind, Protocol: "onvif",
		Success: err == nil, Category: category, Detail: detail,
	}})
	if err != nil {
		// ONVIF unavailable — fall back to full Hikvision ISAPI collection over HTTPS.
		ictx, icancel := context.WithTimeout(ctx, 90*time.Second)
		nvr, ierr := isapi.Collect(ictx, host, user, pass, nil) // nil → permissive TLS
		icancel()
		icat, idet := "success", "ISAPI authenticated"
		if ierr != nil {
			icat, idet = categorizeCollectErr("isapi", ierr.Error())
		}
		s.persistScanCredAttempts(ctx, d, []discovery.CredAttempt{{
			CredentialID: credID, Kind: kind, Protocol: "isapi",
			Success: ierr == nil, Category: icat, Detail: idet,
		}})
		if ierr != nil {
			out.Detail = "ONVIF failed: " + detail + "; ISAPI failed: " + idet
			return out
		}
		out.AuthOK = true
		info2 := nvr.Info
		cat := domain.CatCamera
		dc := "ip_camera"
		for _, e := range classify.ISAPIDeviceInfo(info2.DeviceType, info2.Model) {
			if e.Category == string(domain.CatNVR) {
				cat, dc = domain.CatNVR, "nvr"
				break
			}
		}
		if blob, merr := domain.MarshalEvidence(nil); merr == nil {
			conf := int16(92)
			_, _ = s.queries.UpdateDeviceClassification(ctx, db.UpdateDeviceClassificationParams{
				ID: d.ID, Category: string(cat), OsFamily: domain.OSFamilyEmbedded,
				DeviceClass: &dc, ConfidenceScore: &conf, ClassificationEvidence: blob,
			})
		}
		vendor := info2.Manufacturer
		if vendor == "" {
			vendor = "Hikvision"
		}
		_ = s.queries.UpdateDeviceHardwareInfo(ctx, db.UpdateDeviceHardwareInfoParams{ID: d.ID, Vendor: vendor, Model: info2.Model, Serial: info2.Serial})
		_, _ = s.queries.UpsertCameraInfo(ctx, db.UpsertCameraInfoParams{DeviceID: d.ID, Manufacturer: strPtrOrNil(vendor), Model: strPtrOrNil(info2.Model)})
		s.persistNVR(ctx, d, vendor, nvr)
		if p.CredentialID != nil {
			_ = s.queries.SetDeviceCredential(ctx, db.SetDeviceCredentialParams{ID: d.ID, CredentialID: p.CredentialID})
		}
		_ = s.queries.UpdateDeviceMonitoringStatus(ctx, db.UpdateDeviceMonitoringStatusParams{ID: d.ID, Status: "up"})
		out.CollectionOK = true
		out.Category = string(cat)
		out.Detail = "via profile " + p.Name + " — " + nvrDetail(cat, vendor, info2, nvr)
		return out
	}
	out.AuthOK = true

	// Classify camera vs NVR/DVR from the authenticated model (recorder families).
	cat := domain.CatCamera
	dc := "ip_camera"
	for _, e := range classify.ISAPIDeviceInfo("", info.Model) {
		if e.Category == string(domain.CatNVR) {
			cat, dc = domain.CatNVR, "nvr"
		}
	}
	if blob, merr := domain.MarshalEvidence(nil); merr == nil {
		conf := int16(90)
		_, _ = s.queries.UpdateDeviceClassification(ctx, db.UpdateDeviceClassificationParams{
			ID: d.ID, Category: string(cat), OsFamily: domain.OSFamilyEmbedded,
			DeviceClass: &dc, ConfidenceScore: &conf, ClassificationEvidence: blob,
		})
	}
	_ = s.queries.UpdateDeviceHardwareInfo(ctx, db.UpdateDeviceHardwareInfoParams{
		ID: d.ID, Vendor: info.Manufacturer, Model: info.Model, Serial: info.Serial,
	})
	onvifURL := "http://" + host + "/onvif/device_service"
	_, _ = s.queries.UpsertCameraInfo(ctx, db.UpsertCameraInfoParams{
		DeviceID: d.ID, Manufacturer: strPtrOrNil(info.Manufacturer), Model: strPtrOrNil(info.Model),
		Resolution: strPtrOrNil(info.Resolution()), OnvifUrl: &onvifURL,
	})
	if p.CredentialID != nil {
		_ = s.queries.SetDeviceCredential(ctx, db.SetDeviceCredentialParams{ID: d.ID, CredentialID: p.CredentialID})
	}
	_ = s.queries.UpdateDeviceMonitoringStatus(ctx, db.UpdateDeviceMonitoringStatusParams{ID: d.ID, Status: "up"})

	out.CollectionOK = true
	out.Category = string(cat)
	out.Detail = string(cat) + " collected via profile " + p.Name + " — " +
		nz(info.Manufacturer, "?") + " " + nz(info.Model, "")
	return out
}

func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// persistNVR writes recorder inventory collected over ISAPI: NVR identity
// (model/serial/firmware/deviceType + channel & HDD counts + recording/health),
// per-channel camera rows, and per-HDD storage. A plain camera with no recorder
// data is a no-op here (its identity already lives in camera_info).
func (s *Server) persistNVR(ctx context.Context, d db.Device, vendor string, nvr isapi.NVR) {
	if !nvr.IsRecorder() && len(nvr.Channels) == 0 && len(nvr.Storage) == 0 {
		return
	}
	_, _ = s.queries.UpsertNVRInfo(ctx, db.UpsertNVRInfoParams{
		DeviceID: d.ID, Manufacturer: strPtrOrNil(vendor), Model: strPtrOrNil(nvr.Info.Model),
		Serial: strPtrOrNil(nvr.Info.Serial), Firmware: strPtrOrNil(nvr.Info.Firmware),
		DeviceType:   strPtrOrNil(nvr.Info.DeviceType),
		ChannelCount: int32(len(nvr.Channels)), HddCount: int32(len(nvr.Storage)),
		Recording: nvr.Recording, Health: nvr.Health, Source: "isapi",
	})
	for _, ch := range nvr.Channels {
		status := "unknown"
		if ch.Online != nil {
			if *ch.Online {
				status = "online"
			} else {
				status = "offline"
			}
		}
		var ipp *netip.Addr
		if a, err := netip.ParseAddr(strings.TrimSpace(ch.IP)); err == nil {
			ipp = &a
		}
		_, _ = s.queries.UpsertNVRChannel(ctx, db.UpsertNVRChannelParams{
			NvrDeviceID: d.ID, ChannelNo: int32(ch.No), CameraName: strPtrOrNil(ch.Name),
			CameraIp: ipp, Status: status, Enabled: ch.Enabled,
		})
	}
	for _, h := range nvr.Storage {
		_, _ = s.queries.UpsertNVRStorage(ctx, db.UpsertNVRStorageParams{
			NvrDeviceID: d.ID, HddID: int32(h.ID), Name: strPtrOrNil(h.Name),
			Status: nz(h.Status, "unknown"), CapacityMb: h.CapacityMB, FreeMb: h.FreeMB,
			Property: h.Property, Source: "isapi",
		})
	}
}

// nvrDetail builds the operator-facing success line.
func nvrDetail(cat domain.DeviceCategory, vendor string, info isapi.DeviceInfo, nvr isapi.NVR) string {
	d := fmt.Sprintf("collected via ISAPI — %s", strings.TrimSpace(vendor+" "+info.Model))
	if cat == domain.CatNVR {
		d += fmt.Sprintf(" (%s): %d channel(s), %d HDD(s)", strings.ToUpper(info.DeviceType), len(nvr.Channels), len(nvr.Storage))
	}
	return d
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
