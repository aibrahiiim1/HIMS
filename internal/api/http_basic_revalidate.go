package api

import (
	"net/http"

	"github.com/coralsearesorts/hims/internal/credtest"
	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

// httpBasicKind is the credential kind string for HTTP basic auth.
const httpBasicKind = string(domain.CredHTTPBasic)

// revalidateHTTPBasic (POST /credentials/http-basic/revalidate) re-tests every
// device currently "managed via http_basic" against the new strict 2xx-only rule
// and demotes any whose latest http_basic success was actually a redirect / weak
// response (302/401/403/5xx). It does NOT delete credentials, never touches
// reachability, and writes ONE audit entry summarizing how many stale statuses
// were corrected.
//
// Why this works end-to-end: management status, Management Access Coverage and
// Data Quality all derive from the LATEST persisted credential-test result per
// (device, kind). Recording a fresh non-success result flips the device off
// "managed via http_basic" automatically — no separate state to mutate.
func (s *Server) revalidateHTTPBasic(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cph := s.cipher()
	if cph == nil {
		http.Error(w, "encryption key not loaded; cannot decrypt credentials to re-test", http.StatusServiceUnavailable)
		return
	}

	// Devices whose latest http_basic test SUCCEEDED = currently managed via
	// http_basic through the test-result source (the path the redirect bug poisoned).
	latest, err := s.queries.LatestDeviceKindResults(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	candidates := map[uuid.UUID]bool{}
	for _, row := range latest {
		if row.Kind == httpBasicKind && row.Success {
			candidates[row.DeviceID] = true
		}
	}

	// Device map for IP/name + site-scope enforcement (body-driven, so scope here).
	devs, err := s.queries.ListAllDevices(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	devs = s.scopeDevices(ctx, devs)
	devByID := make(map[uuid.UUID]db.Device, len(devs))
	for _, d := range devs {
		devByID[d.ID] = d
	}

	var results []credTestResult
	checked, corrected := 0, 0
	for devID := range candidates {
		d, ok := devByID[devID]
		if !ok || d.PrimaryIp == nil || !d.PrimaryIp.IsValid() {
			continue // out of scope, gone, or no IP to probe
		}
		// The credential behind the latest http_basic test for this device.
		hist, herr := s.queries.ListDeviceCredentialTests(ctx, db.ListDeviceCredentialTestsParams{DeviceID: devID, Limit: 50})
		if herr != nil {
			continue
		}
		var credID *uuid.UUID
		var credName string
		for _, h := range hist {
			if h.Kind == httpBasicKind && h.CredentialID != nil {
				credID, credName = h.CredentialID, h.CredentialName
				break // newest http_basic row (history is tested_at DESC)
			}
		}
		if credID == nil {
			continue
		}
		c, cerr := s.queries.GetCredential(ctx, *credID)
		if cerr != nil {
			continue
		}
		res := credTestResult{
			CredentialID: credID.String(), CredentialName: credName, Kind: httpBasicKind,
			DeviceID: devID.String(), DeviceName: d.Name, IP: d.PrimaryIp.String(),
		}
		plain, derr := cph.Open(c.EncryptedBlob, c.KeyID)
		if derr != nil {
			res.Category, res.Detail = credtest.CatError, "credential could not be decrypted (re-enter secret)"
		} else {
			o := credtest.Test(ctx, httpBasicKind, string(plain), res.IP, credtest.Options{CredentialName: credName})
			res.Protocol, res.Category, res.Detail, res.LatencyMS = o.Protocol, o.Category, o.Detail, o.LatencyMS
		}
		res.Success = res.Category == credtest.CatSuccess
		checked++
		if !res.Success {
			corrected++ // was managed-via-http_basic, now demoted (redirect/weak response)
		}
		results = append(results, res)
	}

	stillManaged := checked - corrected
	actor := s.actor(r)
	// Persist the fresh results so the latest-result read model flips demoted
	// devices off "managed via http_basic" (Coverage + Data Quality recompute live).
	runID := s.persistCredentialTest(ctx, actor, results, stillManaged)
	s.audit(r, "credential", "credential.revalidate_http_basic", "credential", "",
		"Revalidated http_basic-managed devices under strict 2xx rule",
		map[string]any{"checked": checked, "corrected": corrected, "still_managed": stillManaged, "run_id": runID})

	writeJSON(w, http.StatusOK, map[string]any{
		"checked":       checked,
		"corrected":     corrected,
		"still_managed": stillManaged,
		"run_id":        runID,
		"results":       results,
	})
}
