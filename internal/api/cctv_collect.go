package api

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
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
	Category       string        // camera | nvr
	Probes         []isapi.Probe // per-endpoint ISAPI outcomes (recorders) — Collection-health diagnostics
}

func (r cctvResult) ok() bool { return r.Status == "collected" }

// runCCTVCollection collects a camera/NVR/DVR. selectedCreds is an optional,
// operator-chosen set of credentials to TRY (each in turn) from the per-device
// Collect UI; when empty the collection falls back to the single credential bound
// to the device (the bound-credential-only default that cannot spray). The fleet
// collector always passes nil to keep bulk runs lockout-safe.
// liveOpenPorts are the ports just discovered for this device on THIS scan (the
// pipeline's r.OpenPorts). They are passed in because the scan persists the
// discovery_result probe_data only AFTER collection runs, so webCandidateBases'
// DB lookup is stale/empty mid-scan — without this a recorder whose only web
// surface is a non-standard port (e.g. a Hikvision NVR on 8015) had its ISAPI
// ladder blind-sweep 8000-8020 and time out before reaching the real port. Pass
// nil from the per-device/fleet paths (the device row already has its ports).
func (s *Server) runCCTVCollection(ctx context.Context, d db.Device, selectedCreds []uuid.UUID, source string, liveOpenPorts ...int) cctvResult {
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
	var cands []cc
	seen := map[uuid.UUID]bool{}
	add := func(c db.Credential) {
		if seen[c.ID] || (c.Kind != string(domain.CredONVIF) && c.Kind != string(domain.CredHTTPBasic)) {
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
	// Build the candidate list. Two modes:
	//   1. Operator selected credentials to TRY (per-device Collect UI) — try each,
	//      in the given order, and bind the first that authenticates. This is a
	//      deliberate, operator-driven choice (not automatic spraying); the UI warns
	//      when more than 3 of the same kind are selected, since each failed attempt
	//      counts toward a Hikvision IP lockout.
	//   2. No selection — bound-credential-only: try ONLY the credential bound to the
	//      device. This is the safe default and the only mode the fleet collector
	//      ever uses, so a bulk run can never spray.
	if len(selectedCreds) > 0 {
		for _, cid := range selectedCreds {
			if c, err := s.queries.GetCredential(ctx, cid); err == nil {
				add(c)
			}
		}
	} else {
		// No explicit selection → prefer the durable CCTV web credential, then fall
		// back to the generic bound credential. The generic one may have drifted to
		// an SNMP credential (discovery/monitoring bind-on-success), which add()
		// filters out — so cctv_credential_id is what keeps CCTV collection working.
		pref := d.CctvCredentialID
		if pref == nil {
			pref = d.CredentialID
		}
		if pref != nil {
			if c, err := s.queries.GetCredential(ctx, *pref); err == nil {
				add(c)
			}
		}
	}
	if len(cands) == 0 {
		res.Reason, res.Detail = "no_credential",
			"no usable ONVIF/HTTP web credential — select the device's web-login credential(s) to try (or bind one), then collect. Only ONVIF/HTTP-Basic credentials can authenticate a camera/NVR."
		return res
	}

	// Hard overall ceiling so a slow or half-reachable device (e.g. a recorder that
	// accepts TCP but stalls every HTTP request) can't run the ONVIF + ISAPI phases
	// for many minutes. Applied UNCONDITIONALLY: context.WithTimeout only ever
	// tightens, so when the caller already passed a bounded context (the scan/fleet
	// paths) the shorter deadline wins, and when the per-device Collect button passes
	// a context with only a long server/middleware deadline this still bounds it.
	// Scales with the number of creds tried (~one ISAPI budget each), firmly capped.
	// Having a deadline also activates the ONVIF phase's budget-reservation check.
	budget := time.Duration(len(cands))*130*time.Second + 20*time.Second
	if budget > 5*time.Minute {
		budget = 5 * time.Minute
	}
	bctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	ctx = bctx

	doer := &http.Client{
		Timeout:   15 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS10}},
	}
	// Ordered web endpoints to try first: device last-OK + override + scanned-open
	// web ports + configured candidate ports ("use discovered ports before
	// guessing"). The ISAPI ladder is the fallback after these.
	prefer := s.webCandidateBases(ctx, d)
	// Seed the prefer list from THIS scan's just-discovered open ports (not yet
	// persisted to probe_data when called from the scan path) so the device's
	// actual web port is tried FIRST, not swept for. This is what lets a recorder
	// on a non-standard port (e.g. 8015) collect on a fresh scan instead of
	// timing out.
	if len(liveOpenPorts) > 0 {
		prefer = dedupeStrings(append(webBasesForPorts(ip, d.Category, liveOpenPorts), prefer...))
	}

	// ONVIF can live on a non-standard port — budget gSOAP cameras commonly serve
	// the device_service on :8000 (not :80). Try the device's discovered/candidate
	// web bases, not just http://ip:80 — same "discovered before guessing" rule as
	// the ISAPI ladder. http://ip first (the common case), then the candidates.
	onvifBases := dedupeStrings(append([]string{"http://" + ip}, prefer...))
	if len(onvifBases) > 4 {
		onvifBases = onvifBases[:4] // bound ONVIF probing so it fits the per-device budget
	}
	// ONVIF authenticates with the ONVIF user (WS-Security), usually a dedicated
	// account — try ONVIF-kind creds before http_basic web logins so the right one
	// is reached early. (The old order ground through wrong http_basic creds first,
	// exhausting the 90s budget before the real ONVIF cred — and before ISAPI.)
	sort.SliceStable(cands, func(i, j int) bool {
		return cands[i].kind == string(domain.CredONVIF) && cands[j].kind != string(domain.CredONVIF)
	})

	var attempts []discovery.CredAttempt
	lastReason, lastDetail := "auth_failed", "ONVIF authentication rejected"
	// If ONVIF authenticates a device that turns out to be a recorder (NVR/DVR),
	// we keep its identity but still run the ISAPI walk below for the channel/HDD
	// inventory (ONVIF doesn't expose it). onvifResult is the fallback returned if
	// that ISAPI pass can't be reached.
	var onvifOK bool
	var onvifResult cctvResult
	for _, cd := range cands {
		if d.WebPrefProto == "isapi" || d.Category == string(domain.CatNVR) || d.Category == string(domain.CatDVR) {
			// Operator forced ISAPI, OR the device is an already-classified recorder:
			// ISAPI is the correct and richer path for NVRs/DVRs (full channel/HDD/
			// recording inventory), and ONVIF SOAP probing against a recorder's non-
			// ONVIF web port is the main source of multi-minute collection hangs. Skip
			// straight to ISAPI. (Unknown/camera devices still try ONVIF first.)
			break
		}
		// Reserve budget for the ISAPI fallback: a long ONVIF phase (many creds ×
		// bases) must not leave ISAPI a dead context. When the per-device deadline is
		// near, stop ONVIF and let ISAPI run. (No deadline ⇒ standalone/fleet collect
		// with a generous budget ⇒ try everything.)
		if dl, ok := ctx.Deadline(); ok && time.Until(dl) < 25*time.Second {
			break
		}
		var info onvif.CameraInfo
		err := fmt.Errorf("no onvif endpoint answered")
		okBase := ""
		for _, base := range onvifBases {
			cctx, cancel := context.WithTimeout(ctx, 6*time.Second) // a port speaks ONVIF fast or not at all
			info, err = onvif.Collect(cctx, onvif.NewClient(base, cd.user, cd.pass, doer))
			cancel()
			if err == nil {
				okBase = base
				break
			}
		}
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

		// Classify camera vs NVR vs DVR from the model (Hikvision DS-7/8/9 = recorder).
		cat, dc := recorderCategory(classify.ISAPIDeviceInfo("", info.Model))
		if blob, merr := domain.MarshalEvidence(nil); merr == nil {
			conf := int16(88)
			_, _ = s.queries.UpdateDeviceClassification(ctx, db.UpdateDeviceClassificationParams{
				ID: d.ID, Category: string(cat), OsFamily: domain.OSFamilyEmbedded,
				DeviceClass: &dc, ConfidenceScore: &conf, ClassificationEvidence: blob,
			})
		}
		_ = s.queries.UpdateDeviceHardwareInfo(ctx, db.UpdateDeviceHardwareInfoParams{
			ID: d.ID, Vendor: domain.CanonicalVendor(info.Manufacturer), Model: info.Model, Serial: info.Serial,
		})
		mfr, model, resolution := domain.CanonicalVendor(info.Manufacturer), info.Model, info.Resolution()
		onvifURL := okBase + "/onvif/device_service"
		_, _ = s.queries.UpsertCameraInfo(ctx, db.UpsertCameraInfoParams{
			DeviceID: d.ID, Manufacturer: strPtrOrNil(mfr), Model: strPtrOrNil(model),
			Resolution: strPtrOrNil(resolution), OnvifUrl: &onvifURL,
		})
		cid := cd.id
		_ = s.queries.SetDeviceCredential(ctx, db.SetDeviceCredentialParams{ID: d.ID, CredentialID: &cid})
		_ = s.queries.SetDeviceCCTVCredential(ctx, db.SetDeviceCCTVCredentialParams{ID: d.ID, CctvCredentialID: &cid}) // durable CCTV web credential
		s.recordWebSuccess(ctx, d.ID, "onvif", okBase, &cid)                                                           // real source = ONVIF (actual port)
		_ = s.queries.UpdateDeviceMonitoringStatus(ctx, db.UpdateDeviceMonitoringStatusParams{ID: d.ID, Status: "up"})

		onvifRes := cctvResult{Status: "collected", CredentialUsed: cd.name, Category: string(cat),
			Detail: "collected via ONVIF using credential " + cd.name}
		if cat == domain.CatNVR || cat == domain.CatDVR {
			// Recorder: ONVIF gave us identity (already persisted + credential bound),
			// but channels/HDDs/recording come ONLY from the ISAPI walk. Don't return —
			// fall through to the ISAPI loop below to collect the full inventory. If
			// ISAPI can't be reached, onvifResult is returned as the fallback so the
			// device still lands managed with its ONVIF identity.
			onvifOK, onvifResult = true, onvifRes
			break
		}
		// Plain camera: ONVIF is the complete result — there are no channels to walk.
		res = onvifRes
		s.persistScanCredAttempts(ctx, d, attempts, source)
		return res
	}

	// ONVIF unavailable (e.g. disabled, or no plain-HTTP/:80), OR a recorder whose
	// channel/HDD inventory we still need — fall back to
	// Hikvision ISAPI over HTTPS. It yields the definitive deviceType (NVR/DVR vs
	// IPCamera) + identity AND, for recorders, the full inventory (channels, HDDs,
	// recording/health). Bound-credential-only (above) avoids the lockout.
	for _, cd := range cands {
		if dl, ok := ctx.Deadline(); ok && time.Until(dl) < 6*time.Second {
			break // out of budget — don't fire an ISAPI request that will insta-timeout
		}
		ictx, cancel := context.WithTimeout(ctx, 90*time.Second) // deviceInfo + channel/storage (bounded by remaining budget)
		nvr, err := isapi.Collect(ictx, ip, cd.user, cd.pass, nil, prefer)
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
		// deviceType is the definitive recorder-vs-camera signal (model corroborates).
		cat, dc := recorderCategory(classify.ISAPIDeviceInfo(info.DeviceType, info.Model))
		if blob, merr := domain.MarshalEvidence(nil); merr == nil {
			conf := int16(92)
			_, _ = s.queries.UpdateDeviceClassification(ctx, db.UpdateDeviceClassificationParams{
				ID: d.ID, Category: string(cat), OsFamily: domain.OSFamilyEmbedded,
				DeviceClass: &dc, ConfidenceScore: &conf, ClassificationEvidence: blob,
			})
		}
		vendor := domain.CanonicalVendor(info.Manufacturer)
		if vendor == "" {
			vendor = "Hikvision"
		}
		_ = s.queries.UpdateDeviceHardwareInfo(ctx, db.UpdateDeviceHardwareInfoParams{
			ID: d.ID, Vendor: vendor, Model: info.Model, Serial: info.Serial,
		})
		_, _ = s.queries.UpsertCameraInfo(ctx, db.UpsertCameraInfoParams{
			DeviceID: d.ID, Manufacturer: strPtrOrNil(vendor), Model: strPtrOrNil(info.Model),
		})
		// Enrichment: NIC (MAC/IP/mask/gw/DNS) + firmware/serial + time/NTP from ISAPI.
		_ = s.queries.UpsertCameraEnrichment(ctx, db.UpsertCameraEnrichmentParams{
			DeviceID: d.ID, DeviceName: strPtrOrNil(info.DeviceName), Firmware: strPtrOrNil(info.Firmware),
			Serial: strPtrOrNil(info.Serial), MacAddress: strPtrOrNil(info.MAC),
			IpAddress: strPtrOrNil(nvr.Net.IP), SubnetMask: strPtrOrNil(nvr.Net.Mask), Gateway: strPtrOrNil(nvr.Net.Gateway),
			DnsServer: strPtrOrNil(nvr.Net.DNS), NtpServer: strPtrOrNil(nvr.TimeCfg.NTPServer), TimeZone: strPtrOrNil(nvr.TimeCfg.TimeZone),
		})
		s.persistNVR(ctx, d, vendor, nvr) // nvr_info + channels + HDDs (recorders)
		cid := cd.id
		_ = s.queries.SetDeviceCredential(ctx, db.SetDeviceCredentialParams{ID: d.ID, CredentialID: &cid})
		_ = s.queries.SetDeviceCCTVCredential(ctx, db.SetDeviceCCTVCredentialParams{ID: d.ID, CctvCredentialID: &cid}) // durable CCTV web credential
		s.recordWebSuccess(ctx, d.ID, "isapi", info.Endpoint, &cid)                                                    // real source = ISAPI
		_ = s.queries.UpdateDeviceMonitoringStatus(ctx, db.UpdateDeviceMonitoringStatusParams{ID: d.ID, Status: "up"})

		res = cctvResult{Status: "collected", CredentialUsed: cd.name, Category: string(cat),
			Detail: nvrDetail(cat, vendor, info, nvr), Probes: nvr.Probes}
		s.persistScanCredAttempts(ctx, d, attempts, source)
		return res
	}

	s.persistScanCredAttempts(ctx, d, attempts, source)
	if onvifOK {
		// Recorder identity was collected over ONVIF but the ISAPI channel/HDD walk
		// couldn't be reached this run — return the identity success (device stays
		// managed) rather than a hard failure; channels can fill in on a later run.
		onvifResult.Detail += "; channel/HDD inventory unavailable (ISAPI not reachable this run)"
		return onvifResult
	}
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
	}}, "default")
	if err != nil {
		// ONVIF unavailable — fall back to full Hikvision ISAPI collection over HTTPS.
		ictx, icancel := context.WithTimeout(ctx, 90*time.Second)
		nvr, ierr := isapi.Collect(ictx, host, user, pass, nil, s.webCandidateBases(ctx, d)) // nil → permissive TLS
		icancel()
		icat, idet := "success", "ISAPI authenticated"
		if ierr != nil {
			icat, idet = categorizeCollectErr("isapi", ierr.Error())
		}
		s.persistScanCredAttempts(ctx, d, []discovery.CredAttempt{{
			CredentialID: credID, Kind: kind, Protocol: "isapi",
			Success: ierr == nil, Category: icat, Detail: idet,
		}}, "default")
		if ierr != nil {
			out.Detail = "ONVIF failed: " + detail + "; ISAPI failed: " + idet
			return out
		}
		out.AuthOK = true
		info2 := nvr.Info
		cat, dc := recorderCategory(classify.ISAPIDeviceInfo(info2.DeviceType, info2.Model))
		if blob, merr := domain.MarshalEvidence(nil); merr == nil {
			conf := int16(92)
			_, _ = s.queries.UpdateDeviceClassification(ctx, db.UpdateDeviceClassificationParams{
				ID: d.ID, Category: string(cat), OsFamily: domain.OSFamilyEmbedded,
				DeviceClass: &dc, ConfidenceScore: &conf, ClassificationEvidence: blob,
			})
		}
		vendor := domain.CanonicalVendor(info2.Manufacturer)
		if vendor == "" {
			vendor = "Hikvision"
		}
		_ = s.queries.UpdateDeviceHardwareInfo(ctx, db.UpdateDeviceHardwareInfoParams{ID: d.ID, Vendor: vendor, Model: info2.Model, Serial: info2.Serial})
		_, _ = s.queries.UpsertCameraInfo(ctx, db.UpsertCameraInfoParams{DeviceID: d.ID, Manufacturer: strPtrOrNil(vendor), Model: strPtrOrNil(info2.Model)})
		s.persistNVR(ctx, d, vendor, nvr)
		if p.CredentialID != nil {
			_ = s.queries.SetDeviceCredential(ctx, db.SetDeviceCredentialParams{ID: d.ID, CredentialID: p.CredentialID})
			_ = s.queries.SetDeviceCCTVCredential(ctx, db.SetDeviceCCTVCredentialParams{ID: d.ID, CctvCredentialID: p.CredentialID}) // durable CCTV web credential
		}
		s.recordWebSuccess(ctx, d.ID, "isapi", info2.Endpoint, p.CredentialID)
		_ = s.queries.UpdateDeviceMonitoringStatus(ctx, db.UpdateDeviceMonitoringStatusParams{ID: d.ID, Status: "up"})
		out.CollectionOK = true
		out.Category = string(cat)
		out.Detail = "via profile " + p.Name + " — " + nvrDetail(cat, vendor, info2, nvr)
		return out
	}
	out.AuthOK = true

	// Classify camera vs NVR vs DVR from the authenticated model (recorder families).
	cat, dc := recorderCategory(classify.ISAPIDeviceInfo("", info.Model))
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
		_ = s.queries.SetDeviceCCTVCredential(ctx, db.SetDeviceCCTVCredentialParams{ID: d.ID, CctvCredentialID: p.CredentialID}) // durable CCTV web credential
	}
	s.recordWebSuccess(ctx, d.ID, "onvif", "http://"+host, p.CredentialID)
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

// dedupeStrings drops empties + duplicates, preserving first-seen order.
func dedupeStrings(xs []string) []string {
	seen := make(map[string]bool, len(xs))
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		if x == "" || seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	return out
}

// schemePortOf splits a base endpoint URL into its scheme + port (defaulting to
// the scheme's standard port when none is present).
func schemePortOf(endpoint string) (scheme string, port int) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", 0
	}
	scheme = u.Scheme
	if p := u.Port(); p != "" {
		port, _ = strconv.Atoi(p)
	} else if scheme == "https" {
		port = 443
	} else {
		port = 80
	}
	return scheme, port
}

// recordWebSuccess stamps EXACTLY what worked for a web-managed device — protocol
// (isapi/onvif/http), scheme, port, endpoint, and the credential — so the UI shows
// the real source and the next collect/scan prefers the known-good endpoint.
func (s *Server) recordWebSuccess(ctx context.Context, devID uuid.UUID, proto, endpoint string, credID *uuid.UUID) {
	if endpoint == "" {
		return
	}
	scheme, port := schemePortOf(endpoint)
	var pp *int32
	if port > 0 {
		p := int32(port)
		pp = &p
	}
	_ = s.queries.SetDeviceWebSuccess(ctx, db.SetDeviceWebSuccessParams{
		ID: devID, WebLastProto: proto, WebLastScheme: scheme, WebLastPort: pp,
		WebLastOk: endpoint, WebLastCredentialID: credID,
	})
}

// recorderCategory picks the device category from ISAPI classification evidence:
// the first NVR/DVR signal wins (deviceType evidence is emitted first and is the
// strongest), else the device stays a camera. Returns the category + device_class.
func recorderCategory(evs []domain.ClassificationEvidence) (domain.DeviceCategory, string) {
	for _, e := range evs {
		switch e.Category {
		case string(domain.CatNVR):
			return domain.CatNVR, "nvr"
		case string(domain.CatDVR):
			return domain.CatDVR, "dvr"
		}
	}
	return domain.CatCamera, "ip_camera"
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
		var camDevID *uuid.UUID
		if a, err := netip.ParseAddr(strings.TrimSpace(ch.IP)); err == nil {
			ipp = &a
			// Link the channel to an already-discovered standalone camera device at
			// this IP, if one exists (so the NVR channel and the camera device cross-
			// reference). If none exists, the channel stays NVR-only — we never
			// synthesize a fake discovered device from a channel.
			if dev, lerr := s.queries.LiveDeviceByIP(ctx, &a); lerr == nil && dev.ID != d.ID {
				id := dev.ID
				camDevID = &id
			}
		}
		_, _ = s.queries.UpsertNVRChannel(ctx, db.UpsertNVRChannelParams{
			NvrDeviceID: d.ID, ChannelNo: int32(ch.No), CameraName: strPtrOrNil(ch.Name),
			CameraIp: ipp, CameraDeviceID: camDevID, Status: status, Enabled: ch.Enabled,
			Recording: ch.Recording, Resolution: strings.TrimSpace(ch.Resolution),
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
	if cat == domain.CatNVR || cat == domain.CatDVR {
		d += fmt.Sprintf(" (%s): %d channel(s), %d HDD(s)", strings.ToUpper(info.DeviceType), len(nvr.Channels), len(nvr.Storage))
	}
	return d
}

// collectCCTV handles POST /devices/{id}/collect-cctv — operator-triggered
// ONVIF/ISAPI collection for a camera/NVR/DVR. An optional body
// {"credential_ids":[...]} selects which credentials to try (each in turn); an
// empty/absent list falls back to the credential bound to the device.
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
	var body struct {
		CredentialIDs []string `json:"credential_ids"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body) // absent/empty body ⇒ bound-credential-only
	var creds []uuid.UUID
	for _, cs := range body.CredentialIDs {
		if cid, perr := uuid.Parse(strings.TrimSpace(cs)); perr == nil {
			creds = append(creds, cid)
		}
	}
	collectSource := "manual" // operator opened per-device Collect and chose credentials
	if len(creds) == 0 {
		collectSource = "bound" // empty selection ⇒ device's bound credential only
	}
	res := s.runCCTVCollection(ctx, d, creds, collectSource)
	if res.ok() {
		s.audit(r, "inventory", "device.collect_cctv", "device", id.String(),
			"Collected ONVIF facts for "+d.Name, map[string]any{"category": res.Category, "credentials_tried": len(creds)})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"collected": res.ok(), "reason": res.Reason, "detail": res.Detail,
		"credential_used": res.CredentialUsed, "category": res.Category,
		"probes": res.Probes, // per-endpoint ISAPI outcomes + body samples (recorders)
	})
}
