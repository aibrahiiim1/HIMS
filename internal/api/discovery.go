package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"time"

	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/apply"
	"github.com/coralsearesorts/hims/internal/credresolver"
	"github.com/coralsearesorts/hims/internal/discovery"
	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/scan"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

const scanMaxHosts = 4096

// scanReq drives every network-scan input mode. The operator supplies ONE of:
//   - Targets: free-text single IP / IP-range / CIDR / mixed list (mode "targets")
//   - LocationID with Mode "site_subnets": scan every subnet bound to that site
//   - CIDR: legacy single-CIDR field (back-compat; equivalent to Targets)
//
// CredentialIDs optionally pins which stored credentials this scan may use
// (highest-priority tier; the resolver still orders + binds per-probe). When
// empty, the scan auto-tries ALL stored credentials. CredentialGroupIDs is the
// older group-based selector, still honored if supplied.
type scanReq struct {
	Targets            string   `json:"targets"`
	CIDR               string   `json:"cidr"` // legacy / single-CIDR
	Mode               string   `json:"mode"` // "targets" | "site_subnets"
	LocationID         *string  `json:"location_id"`
	CredentialIDs      []string `json:"credential_ids"`
	CredentialGroupIDs []string `json:"credential_group_ids"`
	Concurrency        int      `json:"concurrency"`
}

// startScan launches a background subnet scan and returns the job immediately
// (202). The scan runs in its own goroutine writing progress to the
// discovery_jobs / discovery_results tables; the UI polls the job.
func (s *Server) startScan(w http.ResponseWriter, r *http.Request) {
	if s.reg == nil || s.fetcher == nil {
		http.Error(w, "discovery not configured on this server", http.StatusServiceUnavailable)
		return
	}
	var req scanReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	locID := parseUUIDPtr(req.LocationID)

	// Resolve the input mode into a host list (+ a scope label for the job).
	hosts, scopeLabel, err := s.resolveScanHosts(r.Context(), req, locID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(hosts) == 0 {
		http.Error(w, "no hosts in scan scope", http.StatusBadRequest)
		return
	}

	// Build the explicit credential tier: the operator-selected credentials, or
	// (when none are selected) ALL stored credentials — the "auto-detect from
	// the whole credential list" default. Group selection, if supplied, is
	// merged in too.
	extra, err := s.scanCredentialTier(r.Context(), req.CredentialIDs, req.CredentialGroupIDs)
	if err != nil {
		if _, ok := err.(*badRequest); ok {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeErr(w, err)
		return
	}

	// Timeouts + default concurrency come from operator Settings.
	snmpTO, portTO, defConcurrency := s.scanSettings(r.Context())
	concurrency := req.Concurrency
	if concurrency < 1 || concurrency > 64 {
		concurrency = defConcurrency
	}

	var scopePrefix *netip.Prefix
	if p, perr := netip.ParsePrefix(scopeLabel); perr == nil {
		scopePrefix = &p
	}
	job, err := s.queries.CreateDiscoveryJob(r.Context(), db.CreateDiscoveryJobParams{
		LocationID: locID, ScopeCidr: scopePrefix,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	_ = s.queries.UpdateDiscoveryJobStatus(r.Context(), db.UpdateDiscoveryJobStatusParams{
		ID: job.ID, Status: "running", HostCount: int32(len(hosts)), FoundCount: 0,
	})
	// Persist the spec so the job can be re-run verbatim (any mode, not just CIDR).
	if spec, err := json.Marshal(rerunSpec{
		Mode: req.Mode, Targets: req.Targets, CIDR: req.CIDR,
		CredentialIDs: req.CredentialIDs, CredentialGroupIDs: req.CredentialGroupIDs,
	}); err == nil {
		_ = s.queries.SetDiscoveryJobMetadata(r.Context(), db.SetDiscoveryJobMetadataParams{ID: job.ID, Metadata: spec})
	}

	go s.runScanJob(job.ID, hosts, locID, concurrency, extra, snmpTO, portTO)
	s.audit(r, "discovery", "discovery.scan", "discovery_job", job.ID.String(), "Launched discovery scan ("+scopeLabel+")", map[string]any{"hosts": len(hosts), "mode": req.Mode})
	writeJSON(w, http.StatusAccepted, job)
}

// rerunSpec is the scan request persisted in a job's metadata for re-runs.
type rerunSpec struct {
	Mode               string   `json:"mode"`
	Targets            string   `json:"targets"`
	CIDR               string   `json:"cidr"`
	CredentialIDs      []string `json:"credential_ids"`
	CredentialGroupIDs []string `json:"credential_group_ids"`
}

// resolveScanHosts expands the request's input mode into a host list. It
// returns a scope label (a CIDR string when the scope is a single prefix, for
// the job record; otherwise a free-text summary).
func (s *Server) resolveScanHosts(ctx context.Context, req scanReq, locID *uuid.UUID) ([]netip.Addr, string, error) {
	if req.Mode == "site_subnets" {
		if locID == nil {
			return nil, "", errBadRequest("site_subnets mode requires location_id")
		}
		subnets, err := s.queries.ListSubnetsByLocation(ctx, *locID)
		if err != nil {
			return nil, "", err
		}
		if len(subnets) == 0 {
			return nil, "", errBadRequest("no subnets configured for this site")
		}
		var all []netip.Addr
		for _, sn := range subnets {
			hosts, err := discovery.ExpandCIDR(sn.Cidr, scanMaxHosts)
			if err != nil {
				return nil, "", err
			}
			all = append(all, hosts...)
			if len(all) > scanMaxHosts {
				return nil, "", errBadRequest("site subnets expand beyond the scan cap; scan a subset")
			}
		}
		return all, "site_subnets", nil
	}

	spec := req.Targets
	if spec == "" {
		spec = req.CIDR // legacy single-CIDR field
	}
	if spec == "" {
		return nil, "", errBadRequest("provide targets (IP / range / CIDR) or location_id with mode=site_subnets")
	}
	hosts, err := discovery.ParseTargets(spec, scanMaxHosts)
	if err != nil {
		return nil, "", err
	}
	return hosts, spec, nil
}

// explicitGroups loads the operator-selected credential groups' members into
// the resolver-input shape, as a single highest-specificity ScopedGroup. The
// secrets are NOT decrypted here — only the candidate refs are loaded; the
// pipeline decrypts a credential only when it is about to try it.
// scanCredentialTier builds the explicit credential candidate tier for a scan:
// the operator-selected credentials, or — when none are selected — ALL stored
// credentials (the "auto-detect from the whole credential list" default). Any
// selected credential groups are merged in as an additional tier. All tiers
// sit above scope-resolved candidates; the resolver still orders by
// fingerprint/weakness/priority and binds on first success.
func (s *Server) scanCredentialTier(ctx context.Context, credIDStrs, groupIDStrs []string) ([]credresolver.ScopedGroup, error) {
	var out []credresolver.ScopedGroup

	// Credential tier: selected ids, else all.
	var members []credresolver.CredRef
	if len(credIDStrs) > 0 {
		ids := make([]uuid.UUID, 0, len(credIDStrs))
		for _, str := range credIDStrs {
			id, err := uuid.Parse(str)
			if err != nil {
				return nil, errBadRequest("invalid credential_id: " + str)
			}
			ids = append(ids, id)
		}
		rows, err := s.queries.ListCredentialCandidatesByIDs(ctx, ids)
		if err != nil {
			return nil, err
		}
		for _, m := range rows {
			members = append(members, credresolver.CredRef{ID: m.ID, Kind: domain.CredentialKind(m.Kind), Weak: m.Weak})
		}
	} else {
		rows, err := s.queries.ListCredentialCandidates(ctx)
		if err != nil {
			return nil, err
		}
		for _, m := range rows {
			members = append(members, credresolver.CredRef{ID: m.ID, Kind: domain.CredentialKind(m.Kind), Weak: m.Weak})
		}
	}
	if len(members) > 0 {
		out = append(out, credresolver.ScopedGroup{Specificity: 100, Members: members})
	}

	// Optional group tier (older selector; still honored if supplied).
	if len(groupIDStrs) > 0 {
		ids := make([]uuid.UUID, 0, len(groupIDStrs))
		for _, str := range groupIDStrs {
			id, err := uuid.Parse(str)
			if err != nil {
				return nil, errBadRequest("invalid credential_group_id: " + str)
			}
			ids = append(ids, id)
		}
		rows, err := s.queries.ListCredentialGroupMembers(ctx, ids)
		if err != nil {
			return nil, err
		}
		gm := make([]credresolver.CredRef, 0, len(rows))
		for _, m := range rows {
			gm = append(gm, credresolver.CredRef{ID: m.ID, Kind: domain.CredentialKind(m.Kind), Priority: int(m.Priority), Weak: m.Weak})
		}
		if len(gm) > 0 {
			out = append(out, credresolver.ScopedGroup{Specificity: 100, Members: gm})
		}
	}
	return out, nil
}

// runScanJob is the background scan worker. It owns its own context (the HTTP
// request's is long gone) and records per-host outcomes + a final job status.
func (s *Server) runScanJob(jobID uuid.UUID, hosts []netip.Addr, locID *uuid.UUID, concurrency int, extraGroups []credresolver.ScopedGroup, snmpTO, portTO time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	cfg := discovery.PipelineConfig{
		Registry: s.reg, Fetcher: s.fetcher, Decrypt: s.scanDecrypt,
		ExtraGroups: extraGroups,
		SNMPTimeout: snmpTO, PortTimeout: portTO,
	}
	applier := apply.New(s.queries)

	res := scan.Scope(ctx, hosts, concurrency, func(ctx context.Context, ip netip.Addr) (uuid.UUID, error) {
		hctx, hcancel := context.WithTimeout(ctx, 45*time.Second)
		defer hcancel()
		r := discovery.Run(hctx, ip, locID, cfg)
		id, err := applier.Apply(hctx, r, locID)
		if r.Alive {
			s.recordResult(hctx, jobID, ip, r, id, err)
		}
		// A WinRM/SSH bind means we onboarded a Windows/Linux host. Run a deep OS
		// collection to refine its classification (workstation vs server) and
		// enrich vendor/model/OS — reusing the just-bound credential. Best-effort
		// with its own budget so it never stalls or fails the scan.
		if err == nil && id != uuid.Nil && r.BoundCred != nil && s.cipher() != nil &&
			(r.BoundCred.Kind == domain.CredWinRM || r.BoundCred.Kind == domain.CredSSH) {
			if dev, derr := s.queries.GetDevice(ctx, id); derr == nil {
				cctx, ccancel := context.WithTimeout(ctx, 2*time.Minute)
				_ = s.runOSCollection(cctx, dev)
				ccancel()
			}
		}
		return id, err
	})

	status, errMsg := "completed", (*string)(nil)
	if ctx.Err() != nil {
		status = "failed"
		m := ctx.Err().Error()
		errMsg = &m
	}
	_ = s.queries.UpdateDiscoveryJobStatus(context.Background(), db.UpdateDiscoveryJobStatusParams{
		ID: jobID, Status: status, HostCount: int32(len(hosts)), FoundCount: int32(res.Persisted), Error: errMsg,
	})
}

// recordResult writes one discovery_results row for an alive host.
func (s *Server) recordResult(ctx context.Context, jobID uuid.UUID, ip netip.Addr, r discovery.HostResult, id uuid.UUID, applyErr error) {
	outcome := "alive"
	switch {
	case applyErr != nil:
		outcome = "failed"
	case id != uuid.Nil:
		outcome = "enrolled"
	case r.MatchedDrv != nil:
		outcome = "classified"
	}
	row, err := s.queries.CreateDiscoveryResult(ctx, db.CreateDiscoveryResultParams{
		JobID: jobID, Ip: ip, Outcome: outcome, ProbeData: []byte("{}"),
	})
	if err != nil {
		return
	}
	var drv, cat, errStr *string
	if r.MatchedDrv != nil {
		n := r.MatchedDrv.Name()
		c := string(r.Match.Category)
		drv, cat = &n, &c
	}
	if applyErr != nil {
		m := applyErr.Error()
		errStr = &m
	}
	var devID *uuid.UUID
	if id != uuid.Nil {
		devID = &id
	}
	_ = s.queries.UpdateDiscoveryResult(ctx, db.UpdateDiscoveryResultParams{
		ID: row.ID, Outcome: outcome, DeviceID: devID, Driver: drv, Category: cat, Error: errStr,
	})
}

// scanDecrypt opens a credential's secret in memory for the scan pipeline. It
// requires the server's cipher; the plaintext community is never logged.
func (s *Server) scanDecrypt(ctx context.Context, id uuid.UUID) (discovery.DecryptedCred, error) {
	c := s.cipher()
	if c == nil {
		return discovery.DecryptedCred{}, errBadRequest("no encryption key configured")
	}
	cred, err := s.queries.GetCredential(ctx, id)
	if err != nil {
		return discovery.DecryptedCred{}, err
	}
	plain, err := c.Open(cred.EncryptedBlob, cred.KeyID)
	if err != nil {
		return discovery.DecryptedCred{}, err
	}
	dc := discovery.DecryptedCred{ID: id, Kind: domain.CredentialKind(cred.Kind), Weak: cred.Weak}
	if cred.Kind == string(domain.CredSNMPv3) {
		if v3, err := discovery.ParseSNMPv3(plain); err == nil {
			dc.V3 = v3
		}
	} else {
		dc.Community = string(plain)
	}
	return dc, nil
}

func (s *Server) listDiscoveryJobs(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListDiscoveryJobs(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// deleteDiscoveryJob removes a job + its results.
func (s *Server) deleteDiscoveryJob(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if err := s.queries.DeleteDiscoveryJob(ctx, id); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// rerunDiscoveryJob re-runs a prior scan job over its original scope (the saved
// scope_cidr, scoped to its location), as a fresh job.
func (s *Server) rerunDiscoveryJob(w http.ResponseWriter, r *http.Request) {
	if s.reg == nil || s.fetcher == nil {
		http.Error(w, "discovery not configured on this server", http.StatusServiceUnavailable)
		return
	}
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	prev, err := s.queries.GetDiscoveryJob(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	// Rebuild the original request: prefer the persisted spec (covers single IP /
	// range / list / site modes); fall back to the saved CIDR scope.
	var req scanReq
	if len(prev.Metadata) > 0 {
		var spec rerunSpec
		if json.Unmarshal(prev.Metadata, &spec) == nil {
			req = scanReq{Mode: spec.Mode, Targets: spec.Targets, CIDR: spec.CIDR, CredentialIDs: spec.CredentialIDs, CredentialGroupIDs: spec.CredentialGroupIDs}
		}
	}
	if req.Targets == "" && req.CIDR == "" && req.Mode != "site_subnets" {
		if prev.ScopeCidr == nil {
			http.Error(w, "this job has no re-runnable network scope (controller/AD imports aren't re-run here)", http.StatusBadRequest)
			return
		}
		req.CIDR = prev.ScopeCidr.String()
	}
	hosts, scopeLabel, err := s.resolveScanHosts(ctx, req, prev.LocationID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	extra, err := s.scanCredentialTier(ctx, req.CredentialIDs, req.CredentialGroupIDs)
	if err != nil {
		writeErr(w, err)
		return
	}
	snmpTO, portTO, defConc := s.scanSettings(ctx)
	var scopePrefix *netip.Prefix
	if p, perr := netip.ParsePrefix(scopeLabel); perr == nil {
		scopePrefix = &p
	}
	job, err := s.queries.CreateDiscoveryJob(ctx, db.CreateDiscoveryJobParams{LocationID: prev.LocationID, ScopeCidr: scopePrefix})
	if err != nil {
		writeErr(w, err)
		return
	}
	_ = s.queries.UpdateDiscoveryJobStatus(ctx, db.UpdateDiscoveryJobStatusParams{ID: job.ID, Status: "running", HostCount: int32(len(hosts)), FoundCount: 0})
	_ = s.queries.SetDiscoveryJobMetadata(ctx, db.SetDiscoveryJobMetadataParams{ID: job.ID, Metadata: prev.Metadata})
	go s.runScanJob(job.ID, hosts, prev.LocationID, defConc, extra, snmpTO, portTO)
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) getDiscoveryJob(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	job, err := s.queries.GetDiscoveryJob(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	results, _ := s.queries.ListDiscoveryResults(ctx, id)
	writeJSON(w, http.StatusOK, map[string]any{"job": job, "results": results})
}
