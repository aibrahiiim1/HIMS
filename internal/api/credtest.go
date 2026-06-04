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

// Universal credential testing: try selected credentials against selected
// devices (any combination — 1:N, N:1, N:M) and report a per-pair outcome.
// Secrets are decrypted server-side only to run the probe; they are NEVER
// returned or logged. Site-scoped requesters can only target their own devices.

type credTestReq struct {
	CredentialIDs []string `json:"credential_ids"`
	DeviceIDs     []string `json:"device_ids"`
	LegacyKEX     bool     `json:"legacy_kex"`
}

type credTestResult struct {
	CredentialID   string `json:"credential_id"`
	CredentialName string `json:"credential_name"`
	Kind           string `json:"kind"`
	DeviceID       string `json:"device_id"`
	DeviceName     string `json:"device_name"`
	IP             string `json:"ip"`
	Protocol       string `json:"protocol"`
	Category       string `json:"category"`
	Success        bool   `json:"success"`
	Detail         string `json:"detail"`
	LatencyMS      int64  `json:"latency_ms"`
}

const credTestMaxPairs = 500 // bound a matrix run (creds × devices)

func (s *Server) testCredentials(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req credTestReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.CredentialIDs) == 0 || len(req.DeviceIDs) == 0 {
		http.Error(w, "provide credential_ids and device_ids", http.StatusBadRequest)
		return
	}
	if len(req.CredentialIDs)*len(req.DeviceIDs) > credTestMaxPairs {
		http.Error(w, "too many combinations; reduce the selection", http.StatusBadRequest)
		return
	}

	cph := s.cipher()
	if cph == nil {
		http.Error(w, "encryption key not loaded; cannot decrypt credentials to test", http.StatusServiceUnavailable)
		return
	}

	// Resolve + decrypt credentials (server-side only).
	type cred struct {
		id     uuid.UUID
		name   string
		kind   string
		secret string
	}
	var creds []cred
	for _, idStr := range req.CredentialIDs {
		id, err := uuid.Parse(idStr)
		if err != nil {
			http.Error(w, "invalid credential id: "+idStr, http.StatusBadRequest)
			return
		}
		c, err := s.queries.GetCredential(ctx, id)
		if err != nil {
			writeErr(w, err)
			return
		}
		plain, err := cph.Open(c.EncryptedBlob, c.KeyID)
		if err != nil {
			// Can't decrypt (wrong key / re-entry needed) — report, don't fail the batch.
			creds = append(creds, cred{id: id, name: c.Name, kind: c.Kind, secret: ""})
			continue
		}
		creds = append(creds, cred{id: id, name: c.Name, kind: c.Kind, secret: string(plain)})
	}

	// Resolve devices, then enforce site scope (body IDs bypass path middleware).
	var devRows []db.Device
	for _, idStr := range req.DeviceIDs {
		id, err := uuid.Parse(idStr)
		if err != nil {
			http.Error(w, "invalid device id: "+idStr, http.StatusBadRequest)
			return
		}
		d, err := s.queries.GetDevice(ctx, id)
		if err != nil {
			writeErr(w, err)
			return
		}
		devRows = append(devRows, d)
	}
	devRows = s.scopeDevices(ctx, devRows)

	// Run the matrix with bounded concurrency.
	type job struct {
		c cred
		d db.Device
	}
	var jobs []job
	for _, c := range creds {
		for _, d := range devRows {
			jobs = append(jobs, job{c, d})
		}
	}
	results := make([]credTestResult, len(jobs))
	sem := make(chan struct{}, 12)
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, j job) {
			defer wg.Done()
			defer func() { <-sem }()
			res := credTestResult{
				CredentialID: j.c.id.String(), CredentialName: j.c.name, Kind: j.c.kind,
				DeviceID: j.d.ID.String(), DeviceName: j.d.Name,
			}
			if j.d.PrimaryIp != nil && j.d.PrimaryIp.IsValid() {
				res.IP = j.d.PrimaryIp.String()
			}
			switch {
			case j.c.secret == "":
				res.Category, res.Detail = credtest.CatError, "credential could not be decrypted (re-enter secret)"
			case res.IP == "":
				res.Category, res.Detail = credtest.CatError, "device has no IP to probe"
			default:
				o := credtest.Test(ctx, j.c.kind, j.c.secret, res.IP, credtest.Options{LegacyKEX: req.LegacyKEX})
				res.Protocol, res.Category, res.Detail, res.LatencyMS = o.Protocol, o.Category, o.Detail, o.LatencyMS
			}
			res.Success = res.Category == credtest.CatSuccess
			results[i] = res
		}(i, j)
	}
	wg.Wait()

	// Stable order: by device, then credential.
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].DeviceName != results[j].DeviceName {
			return results[i].DeviceName < results[j].DeviceName
		}
		return results[i].CredentialName < results[j].CredentialName
	})

	successes := 0
	for _, r := range results {
		if r.Success {
			successes++
		}
	}
	// Audit the action — counts only, never secrets.
	s.audit(r, "credential", "credential.test", "credential", "",
		"Tested credentials against devices",
		map[string]any{"credentials": len(creds), "devices": len(devRows), "pairs": len(results), "successes": successes})

	writeJSON(w, http.StatusOK, map[string]any{
		"results":   results,
		"pairs":     len(results),
		"successes": successes,
		"failures":  len(results) - successes,
	})
}
