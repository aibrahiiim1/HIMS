package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coralsearesorts/hims/internal/classify"
	"github.com/coralsearesorts/hims/internal/credtest"
	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/osinv"
	"github.com/coralsearesorts/hims/internal/secret"
	"github.com/coralsearesorts/hims/internal/ssh"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// getOSInventory handles GET /devices/{id}/os-inventory — the full deep-inventory
// bundle (1:1 summary + all 1:N collections) for a device. "inventory" is null
// when the device has never been OS-inventoried, which the UI renders as
// "Not collected yet". Empty collections come back as [] (emit_empty_slices).
func (s *Server) getOSInventory(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	var invPtr *db.OsInventory
	if inv, err := s.queries.GetOSInventory(ctx, id); err == nil {
		invPtr = &inv
	} else if !errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, err)
		return
	}
	disks, _ := s.queries.ListOSDisks(ctx, id)
	nics, _ := s.queries.ListOSNics(ctx, id)
	svcs, _ := s.queries.ListOSServices(ctx, id)
	procs, _ := s.queries.ListOSProcesses(ctx, id)
	soft, _ := s.queries.ListOSSoftware(ctx, id)
	roles, _ := s.queries.ListOSRoles(ctx, id)
	writeJSON(w, http.StatusOK, map[string]any{
		"inventory": invPtr,
		"disks":     disks,
		"nics":      nics,
		"services":  svcs,
		"processes": procs,
		"software":  soft,
		"roles":     roles,
	})
}

// Deep OS Inventory — on-demand authenticated collection for a single device.
// Windows uses WinRM/PowerShell (Get-CimInstance, NTLM + message encryption),
// Linux uses SSH. Collection auto-tries the device's bound credential then any
// other matching credential and binds the one that works; HIMS never guesses
// passwords. Secrets are decrypted only to run the collection.

// sshRunnerOS runs a shell script over SSH (satisfies osinv.Runner).
type sshRunnerOS struct {
	host    string
	creds   ssh.Creds
	legacy  bool
	timeout time.Duration
}

func (r sshRunnerOS) Run(ctx context.Context, cmd string) (string, error) {
	return ssh.Run(ctx, r.host, 22, r.creds, r.legacy, cmd, r.timeout)
}

// collectOSInventory handles POST /devices/{id}/collect-os.
// osCollectResult is one device's deep-collection outcome. Failures carry an
// actionable reason code + human detail; nothing is faked — a device is only
// "collected" when the host actually answered and the data persisted.
type osCollectResult struct {
	DeviceID       string         `json:"device_id"`
	Name           string         `json:"name"`
	IP             string         `json:"ip"`
	Status         string         `json:"status"` // collected | failed
	Method         string         `json:"method,omitempty"`
	Reason         string         `json:"reason,omitempty"` // reason code (failed only)
	Detail         string         `json:"detail"`
	CredentialUsed string         `json:"credential_used,omitempty"`
	Counts         map[string]int `json:"counts,omitempty"`
	Roles          []string       `json:"roles,omitempty"`
}

func (r osCollectResult) ok() bool { return r.Status == "collected" }

// reasonHTTP maps a failure reason code to an HTTP status for the single-device
// endpoint (client-fixable → 400, remote/transport → 502).
func reasonHTTP(reason string) int {
	switch reason {
	case "unsupported_os", "no_credential", "decrypt_failed", "no_ip", "encryption_unavailable":
		return http.StatusBadRequest
	default:
		return http.StatusBadGateway
	}
}

// categorizeCollectErr turns a collector error into an actionable reason code +
// detail, specialised by transport (WinRM vs SSH).
func categorizeCollectErr(method, errStr string) (reason, detail string) {
	e := strings.ToLower(errStr)
	switch {
	case strings.Contains(e, "unable to authenticate") || strings.Contains(e, "permission denied") ||
		strings.Contains(e, "unauthorized") || strings.Contains(e, "access is denied") ||
		strings.Contains(e, "401") || strings.Contains(e, "403") || strings.Contains(e, "logon"):
		return "auth_failed", "authentication rejected — check the bound credential"
	case strings.Contains(e, "refused") || strings.Contains(e, "actively refused") || strings.Contains(e, "reset"):
		switch method {
		case "winrm":
			return "winrm_disabled", "WinRM not responding on 5985 — enable PowerShell Remoting / open the port"
		case "vsphere", "vmware":
			return "connection_refused", "vSphere connection refused — check the vCenter/ESXi URL and that 443 is open"
		case "onvif":
			return "connection_refused", "ONVIF/HTTP connection refused — check the device address and that the HTTP port is open"
		}
		return "ssh_unreachable", "SSH connection refused on 22 — enable sshd / open the port"
	case strings.Contains(e, "timeout") || strings.Contains(e, "deadline") || strings.Contains(e, "i/o timeout"):
		switch method {
		case "winrm":
			return "winrm_timeout", "WinRM timed out (host slow, firewalled, or 5985 filtered)"
		case "vsphere", "vmware":
			return "vsphere_timeout", "vSphere timed out (host slow, firewalled, or 443 filtered)"
		case "onvif":
			return "onvif_timeout", "ONVIF timed out (host slow, firewalled, or HTTP port filtered)"
		}
		return "ssh_timeout", "SSH timed out (host slow, firewalled, or 22 filtered)"
	case strings.Contains(e, "no route") || strings.Contains(e, "no such host") || strings.Contains(e, "unreachable"):
		return "unreachable", "host unreachable from the collector"
	case strings.Contains(e, "kex") || strings.Contains(e, "handshake"):
		return "handshake_failed", "SSH/TLS handshake failed (legacy algorithms?)"
	default:
		return "collection_error", strings.TrimSpace(errStr)
	}
}

// runOSCollection performs the deep collection for one already-fetched device
// and returns a structured result. No HTTP, no panics — used by both the single
// and bulk endpoints. It persists on success only.
func (s *Server) runOSCollection(ctx context.Context, d db.Device) osCollectResult {
	res := osCollectResult{DeviceID: d.ID.String(), Name: d.Name, Status: "failed"}
	if d.PrimaryIp != nil && d.PrimaryIp.IsValid() {
		res.IP = d.PrimaryIp.String()
	}

	switch d.OsFamily {
	case domain.OSFamilyWindows:
		res.Method = "winrm"
	case domain.OSFamilyLinux:
		res.Method = "ssh"
	default:
		// os_family not set yet (e.g. immediately after a discovery scan bound a
		// credential but hasn't classified the OS). Fall back to the bound
		// credential's kind so a WinRM/SSH bind still collects + then classifies.
		if d.CredentialID != nil {
			if c, err := s.queries.GetCredential(ctx, *d.CredentialID); err == nil {
				switch c.Kind {
				case string(domain.CredWinRM):
					res.Method = "winrm"
				case string(domain.CredSSH):
					res.Method = "ssh"
				}
			}
		}
		if res.Method == "" {
			res.Reason, res.Detail = "unsupported_os", "OS family is not windows/linux — classify the device or bind a WinRM/SSH credential first"
			return res
		}
	}
	if res.IP == "" {
		res.Reason, res.Detail = "no_ip", "device has no IP to collect from"
		return res
	}
	cph := s.cipher()
	if cph == nil {
		res.Reason, res.Detail = "encryption_unavailable", "encryption key not loaded; cannot decrypt credentials"
		return res
	}

	// Build the candidate credential list: the device's BOUND credential first
	// (operator's explicit choice), then every other credential whose kind suits
	// the method. The collector tries each until one authenticates and binds the
	// winner — so the operator only needs ONE working credential in the system,
	// not the exact one pre-bound to each device.
	cands := s.osCandidateCreds(ctx, cph, d, res.Method)
	if len(cands) == 0 {
		res.Reason, res.Detail = "no_credential", "no usable "+res.Method+" credential — add one (Administration → Credentials) and ensure the encryption key is loaded"
		return res
	}

	lastReason, lastDetail := "auth_failed", "no credential authenticated"
	for _, cd := range cands {
		rep, err := s.collectWithCred(ctx, res.Method, res.IP, cd.user, cd.pass)
		if err != nil {
			lastReason, lastDetail = categorizeCollectErr(res.Method, err.Error())
			continue
		}
		if err := osinv.Persist(ctx, s.queries, d.ID, rep, time.Now().UTC()); err != nil {
			res.Reason, res.Detail = "persist_error", "collected but failed to save: "+err.Error()
			return res
		}
		// Enrich the device row's identity/hardware fields from the collection so
		// the Inventory list columns (Vendor / Model / OS) populate — the deep
		// inventory previously only landed in os_inventory and never surfaced on
		// the device row. COALESCE(NULLIF…) in the query means blanks never wipe
		// existing values, so this only adds detail.
		_ = s.queries.UpdateDeviceHardwareInfo(ctx, db.UpdateDeviceHardwareInfoParams{
			ID:        d.ID,
			Vendor:    rep.Hardware.Manufacturer,
			Model:     rep.Hardware.Model,
			Serial:    rep.Hardware.Serial,
			OsVersion: rep.OS.Caption,
			Hostname:  rep.Identity.Hostname,
		})
		// Bind-on-success so the working credential sticks to this device.
		cid := cd.id
		_ = s.queries.SetDeviceCredential(ctx, db.SetDeviceCredentialParams{ID: d.ID, CredentialID: &cid})
		// A successful authenticated collection is proof the host is up right now
		// — reflect that so a stale 'down' from a discovery probe doesn't linger.
		_ = s.queries.UpdateDeviceMonitoringStatus(ctx, db.UpdateDeviceMonitoringStatusParams{ID: d.ID, Status: "up"})
		// Auto-correct classification from the authoritative OS caption we just
		// collected. This closes the discovery loop: a Windows box that came in as
		// "server" (SNMP sysDescr) or "unknown" (no SNMP) is reclassified to its
		// true workstation-vs-server class the moment it's collected — the operator
		// no longer has to click Reclassify by hand. The UpdateDeviceClassification
		// query is an atomic no-op on classification-locked devices, so a manual
		// operator override is never overwritten.
		s.reclassifyFromCaption(ctx, d, rep.OS.Caption)
		res.Status = "collected"
		res.CredentialUsed = cd.name
		res.Roles = osinv.DetectRoles(rep)
		res.Counts = map[string]int{"disks": len(rep.Disks), "nics": len(rep.Nics), "services": len(rep.Services), "processes": len(rep.Processes), "software": len(rep.Software)}
		res.Detail = "collected via " + res.Method + " using credential " + cd.name
		return res
	}
	res.Reason, res.Detail = lastReason, lastDetail+" (tried "+strconv.Itoa(len(cands))+" credential(s))"
	return res
}

// reclassifyFromCaption corrects a device's category/os_family/device_class from
// the authenticated OS caption (the strongest OS signal HIMS has). Best-effort:
// any error or an unrecognised caption leaves the existing classification
// untouched, and the underlying query no-ops on classification-locked devices.
func (s *Server) reclassifyFromCaption(ctx context.Context, d db.Device, caption string) {
	if caption == "" {
		return
	}
	cres := classify.FromEvidence(classify.OSCaption(caption))
	if cres.Confidence == 0 || cres.Category == string(domain.CatUnknown) {
		return
	}
	blob, err := domain.MarshalEvidence(cres.Evidence)
	if err != nil {
		return
	}
	var dcPtr *string
	if cres.Subtype != "" {
		dcPtr = &cres.Subtype
	}
	conf := int16(cres.Confidence)
	_, _ = s.queries.UpdateDeviceClassification(ctx, db.UpdateDeviceClassificationParams{
		ID: d.ID, Category: cres.Category, OsFamily: cres.OSFamily,
		DeviceClass: dcPtr, ConfidenceScore: &conf, ClassificationEvidence: blob,
	})
}

type osCred struct {
	id         uuid.UUID
	name       string
	user, pass string
}

// osCandidateCreds returns decryptable credentials to try for a method: the
// device's bound credential first, then all others of a matching kind. Capped.
func (s *Server) osCandidateCreds(ctx context.Context, cph *secret.Cipher, d db.Device, method string) []osCred {
	const maxCands = 8
	var out []osCred
	seen := map[uuid.UUID]bool{}
	add := func(c db.Credential) {
		if seen[c.ID] || len(out) >= maxCands {
			return
		}
		plain, err := cph.Open(c.EncryptedBlob, c.KeyID)
		if err != nil {
			return // undecryptable (wrong key / needs re-entry) — skip
		}
		u, p := credtest.SplitUserPass(string(plain))
		out = append(out, osCred{id: c.ID, name: c.Name, user: u, pass: p})
		seen[c.ID] = true
	}
	if d.CredentialID != nil {
		if c, err := s.queries.GetCredential(ctx, *d.CredentialID); err == nil {
			add(c)
		}
	}
	all, _ := s.queries.ListCredentials(ctx)
	for _, c := range all {
		if credKindMatchesMethod(c.Kind, method) {
			add(c)
		}
	}
	return out
}

func credKindMatchesMethod(kind, method string) bool {
	if method == "winrm" {
		return kind == "winrm"
	}
	return kind == "ssh" || kind == "cli" // linux
}

// collectWithCred runs the deep collection for one credential over the method's
// transport (WinRM NTLM+encryption, or SSH).
func (s *Server) collectWithCred(ctx context.Context, method, ip, user, pass string) (osinv.Report, error) {
	if method == "winrm" {
		start := time.Now()
		cl, err := osinv.NewWinRMClient(ip, user, pass, 120*time.Second)
		var rep osinv.Report
		if err == nil {
			rep, err = osinv.CollectWindows(ctx, osinv.WinRMRunner{C: cl})
		}
		osinv.LogWinRMAttempt(ip, user, "deep-collect", 120*time.Second, time.Since(start), pass, err)
		return rep, err
	}
	return osinv.CollectLinux(ctx, sshRunnerOS{host: ip, creds: ssh.Creds{Username: user, Password: pass}, timeout: 45 * time.Second})
}

// collectOSInventory handles POST /devices/{id}/collect-os (single device).
func (s *Server) collectOSInventory(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	d, err := s.queries.GetDevice(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	res := s.runOSCollection(ctx, d)
	if !res.ok() {
		http.Error(w, res.Detail, reasonHTTP(res.Reason))
		return
	}
	s.audit(r, "inventory", "device.collect_os", "device", id.String(),
		"Collected deep OS inventory for "+d.Name+" via "+res.Method,
		map[string]any{"method": res.Method, "credential": res.CredentialUsed, "roles": len(res.Roles)})
	writeJSON(w, http.StatusOK, map[string]any{"collected": true, "method": res.Method, "credential_used": res.CredentialUsed, "counts": res.Counts, "roles": res.Roles})
}

type bulkCollectOSReq struct {
	DeviceIDs []string `json:"device_ids"`
}

const bulkCollectOSMax = 100

// bulkCollectOS handles POST /data-quality/collect-os — run deep OS collection
// across selected devices (the Data Quality "OS not inventoried" bulk action).
// Returns a per-device result with an actionable reason on failure. Site-scoped.
func (s *Server) bulkCollectOS(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req bulkCollectOSReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.DeviceIDs) == 0 {
		http.Error(w, "provide device_ids", http.StatusBadRequest)
		return
	}
	if len(req.DeviceIDs) > bulkCollectOSMax {
		http.Error(w, "too many devices in one run (max 100)", http.StatusBadRequest)
		return
	}

	// Resolve + site-scope the devices (body IDs bypass the path middleware).
	var devs []db.Device
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
		devs = append(devs, d)
	}
	devs = s.scopeDevices(ctx, devs)

	// Bounded concurrency — collection is slow network I/O.
	results := make([]osCollectResult, len(devs))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, d := range devs {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, d db.Device) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = s.runOSCollection(ctx, d)
		}(i, d)
	}
	wg.Wait()

	collected := 0
	for _, r := range results {
		if r.ok() {
			collected++
		}
	}
	s.audit(r, "inventory", "device.collect_os_bulk", "device", "",
		"Bulk deep OS collection", map[string]any{"devices": len(results), "collected": collected, "failed": len(results) - collected})
	writeJSON(w, http.StatusOK, map[string]any{
		"results": results, "collected": collected, "failed": len(results) - collected,
	})
}
