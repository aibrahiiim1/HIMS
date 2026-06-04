package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coralsearesorts/hims/internal/credtest"
	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/osinv"
	"github.com/coralsearesorts/hims/internal/ssh"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/jackc/pgx/v5"
	"github.com/masterzen/winrm"
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
// Windows uses WinRM/PowerShell (Get-CimInstance), Linux uses SSH. The device's
// BOUND credential is used (find/bind one via Credential Testing first); HIMS
// never guesses passwords. The secret is decrypted only to run the collection.

// winRunner runs PowerShell over WinRM (satisfies osinv.Runner).
type winRunner struct{ c *winrm.Client }

func (r winRunner) Run(ctx context.Context, script string) (string, error) {
	stdout, stderr, code, err := r.c.RunPSWithContext(ctx, script)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("winrm exit %d: %s", code, strings.TrimSpace(stderr))
	}
	return stdout, nil
}

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
	if d.PrimaryIp == nil || !d.PrimaryIp.IsValid() {
		http.Error(w, "device has no IP to collect from", http.StatusBadRequest)
		return
	}

	// Method by OS family. Deep collection only applies to Windows/Linux hosts.
	method := ""
	switch d.OsFamily {
	case domain.OSFamilyWindows:
		method = "winrm"
	case domain.OSFamilyLinux:
		method = "ssh"
	default:
		http.Error(w, "device os_family is not windows/linux — classify it first (Re-classify or set OS)", http.StatusBadRequest)
		return
	}

	// Bound credential, decrypted server-side only to run the collection.
	if d.CredentialID == nil {
		http.Error(w, "no credential bound to this device — bind one (use Credential Testing to find a working credential)", http.StatusBadRequest)
		return
	}
	cph := s.cipher()
	if cph == nil {
		http.Error(w, "encryption key not loaded; cannot decrypt the bound credential", http.StatusServiceUnavailable)
		return
	}
	cred, err := s.queries.GetCredential(ctx, *d.CredentialID)
	if err != nil {
		writeErr(w, err)
		return
	}
	plain, err := cph.Open(cred.EncryptedBlob, cred.KeyID)
	if err != nil {
		http.Error(w, "bound credential could not be decrypted (re-enter its secret)", http.StatusBadRequest)
		return
	}
	user, pass := credtest.SplitUserPass(string(plain))
	host := d.PrimaryIp.String()

	// Run the collector through the right transport.
	var rep osinv.Report
	switch method {
	case "winrm":
		ep := winrm.NewEndpoint(host, 5985, false, false, nil, nil, nil, 90*time.Second)
		cl, e := winrm.NewClient(ep, user, pass)
		if e != nil {
			http.Error(w, "winrm client: "+e.Error(), http.StatusBadGateway)
			return
		}
		rep, err = osinv.CollectWindows(ctx, winRunner{c: cl})
	case "ssh":
		rep, err = osinv.CollectLinux(ctx, sshRunnerOS{host: host, creds: ssh.Creds{Username: user, Password: pass}, timeout: 45 * time.Second})
	}
	if err != nil {
		// Honest failure — nothing is persisted; report the reason.
		http.Error(w, "collection failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	poll := time.Now().UTC()
	if err := osinv.Persist(ctx, s.queries, id, rep, poll); err != nil {
		writeErr(w, err)
		return
	}

	roles := osinv.DetectRoles(rep)
	s.audit(r, "inventory", "device.collect_os", "device", id.String(),
		"Collected deep OS inventory for "+d.Name+" via "+method,
		map[string]any{"method": method, "disks": len(rep.Disks), "services": len(rep.Services),
			"processes": len(rep.Processes), "software": len(rep.Software), "roles": len(roles)})

	writeJSON(w, http.StatusOK, map[string]any{
		"collected": true,
		"method":    method,
		"counts": map[string]int{
			"disks": len(rep.Disks), "nics": len(rep.Nics), "services": len(rep.Services),
			"processes": len(rep.Processes), "software": len(rep.Software),
		},
		"roles": roles,
	})
}
