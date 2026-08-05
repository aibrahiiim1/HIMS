package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"sync"

	"github.com/coralsearesorts/hims/internal/credtest"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

// Bulk credential assignment for the Device Access page.
//
// This deliberately TESTS before it binds. "Assign this credential to these 40
// devices" must never write a binding that was merely asserted: a bound
// credential is the system's claim that it can manage the device, and an
// unverified claim shows up later as a device that looks managed and is not.
// So each selected device is authenticated first and bound ONLY on success;
// failures are reported per device with the reason.

const assignMaxDevices = 500

type assignCredReq struct {
	CredentialID string   `json:"credential_id"`
	DeviceIDs    []string `json:"device_ids"`
	LegacyKEX    bool     `json:"legacy_kex"`
}

type assignCredResult struct {
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	IP         string `json:"ip"`
	Protocol   string `json:"protocol"`
	Category   string `json:"category"`
	Detail     string `json:"detail"`
	Success    bool   `json:"success"`
	Bound      bool   `json:"bound"`
	LatencyMS  int64  `json:"latency_ms"`
}

// assignCredentialToDevices handles POST /devices/credential-assign.
func (s *Server) assignCredentialToDevices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req assignCredReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	credID, err := uuid.Parse(req.CredentialID)
	if err != nil {
		http.Error(w, "invalid credential_id", http.StatusBadRequest)
		return
	}
	if len(req.DeviceIDs) == 0 {
		http.Error(w, "select at least one device", http.StatusBadRequest)
		return
	}
	if len(req.DeviceIDs) > assignMaxDevices {
		http.Error(w, "too many devices selected; assign in smaller batches", http.StatusBadRequest)
		return
	}
	cph := s.cipher()
	if cph == nil {
		http.Error(w, "encryption key not loaded; cannot decrypt the credential to verify it", http.StatusServiceUnavailable)
		return
	}
	cred, err := s.queries.GetCredential(ctx, credID)
	if err != nil {
		writeErr(w, err)
		return
	}
	plain, err := cph.Open(cred.EncryptedBlob, cred.KeyID)
	if err != nil {
		http.Error(w, "cannot decrypt this credential (encryption key rotated?) — re-enter it before assigning", http.StatusConflict)
		return
	}
	secret := string(plain)

	// Resolve devices, then apply site scope (body IDs bypass the path middleware).
	var devRows []db.Device
	for _, idStr := range req.DeviceIDs {
		id, perr := uuid.Parse(idStr)
		if perr != nil {
			http.Error(w, "invalid device id: "+idStr, http.StatusBadRequest)
			return
		}
		d, gerr := s.queries.GetDevice(ctx, id)
		if gerr != nil {
			writeErr(w, gerr)
			return
		}
		devRows = append(devRows, d)
	}
	devRows = s.scopeDevices(ctx, devRows)

	results := make([]assignCredResult, len(devRows))
	sem := make(chan struct{}, 12)
	var wg sync.WaitGroup
	for i, d := range devRows {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, d db.Device) {
			defer wg.Done()
			defer func() { <-sem }()
			res := assignCredResult{DeviceID: d.ID.String(), DeviceName: d.Name}
			if d.PrimaryIp != nil && d.PrimaryIp.IsValid() {
				res.IP = d.PrimaryIp.String()
			}
			if res.IP == "" {
				res.Category, res.Detail = credtest.CatError, "device has no IP to probe"
				results[i] = res
				return
			}
			wmiURL, wmiTok := wmiCollectorConfig()
			o := credtest.Test(ctx, cred.Kind, secret, res.IP, credtest.Options{
				LegacyKEX: req.LegacyKEX, CredentialName: cred.Name,
				WMICollectorURL: wmiURL, WMICollectorToken: wmiTok,
			})
			res.Protocol, res.Category, res.Detail, res.LatencyMS = o.Protocol, o.Category, o.Detail, o.LatencyMS
			res.Success = res.Category == credtest.CatSuccess
			results[i] = res
		}(i, d)
	}
	wg.Wait()

	// Bind ONLY the devices that actually authenticated.
	bound := 0
	for i := range results {
		if !results[i].Success {
			continue
		}
		id, perr := uuid.Parse(results[i].DeviceID)
		if perr != nil {
			continue
		}
		if berr := s.queries.SetDeviceCredential(ctx, db.SetDeviceCredentialParams{ID: id, CredentialID: &credID}); berr == nil {
			results[i].Bound = true
			bound++
		} else {
			results[i].Detail = "authenticated, but the binding could not be saved: " + berr.Error()
		}
	}

	sort.SliceStable(results, func(i, j int) bool { return results[i].DeviceName < results[j].DeviceName })

	// Reuse the credential-test history pipeline so these attempts show up in
	// Credential Health exactly like any other test. Outcome metadata only.
	hist := make([]credTestResult, 0, len(results))
	successes := 0
	for _, r0 := range results {
		if r0.Success {
			successes++
		}
		hist = append(hist, credTestResult{
			CredentialID: credID.String(), CredentialName: cred.Name, Kind: cred.Kind,
			DeviceID: r0.DeviceID, DeviceName: r0.DeviceName, IP: r0.IP,
			Protocol: r0.Protocol, Category: r0.Category, Success: r0.Success,
			Detail: r0.Detail, LatencyMS: r0.LatencyMS,
		})
	}
	runID := s.persistCredentialTest(ctx, s.actor(r), hist, successes)

	s.audit(r, "credential", "credential.assign", "credential", credID.String(),
		"Assigned credential "+cred.Name+" to selected devices (bound on successful authentication only)",
		map[string]any{"devices": len(devRows), "authenticated": successes, "bound": bound})

	writeJSON(w, http.StatusOK, map[string]any{
		"run_id":        runID,
		"results":       results,
		"devices":       len(devRows),
		"authenticated": successes,
		"bound":         bound,
		"failed":        len(devRows) - successes,
	})
}
