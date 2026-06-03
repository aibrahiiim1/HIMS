package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"time"

	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/apply"
	"github.com/coralsearesorts/hims/internal/discovery"
	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/scan"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

const scanMaxHosts = 4096

type scanReq struct {
	CIDR        string  `json:"cidr"`
	LocationID  *string `json:"location_id"`
	Concurrency int     `json:"concurrency"`
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
	prefix, err := netip.ParsePrefix(req.CIDR)
	if err != nil {
		http.Error(w, "invalid cidr: "+err.Error(), http.StatusBadRequest)
		return
	}
	hosts, err := discovery.ExpandCIDR(prefix, scanMaxHosts)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	locID := parseUUIDPtr(req.LocationID)
	concurrency := req.Concurrency
	if concurrency < 1 || concurrency > 64 {
		concurrency = 16
	}

	job, err := s.queries.CreateDiscoveryJob(r.Context(), db.CreateDiscoveryJobParams{
		LocationID: locID, ScopeCidr: &prefix,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	_ = s.queries.UpdateDiscoveryJobStatus(r.Context(), db.UpdateDiscoveryJobStatusParams{
		ID: job.ID, Status: "running", HostCount: int32(len(hosts)), FoundCount: 0,
	})

	go s.runScanJob(job.ID, hosts, locID, concurrency)
	writeJSON(w, http.StatusAccepted, job)
}

// runScanJob is the background scan worker. It owns its own context (the HTTP
// request's is long gone) and records per-host outcomes + a final job status.
func (s *Server) runScanJob(jobID uuid.UUID, hosts []netip.Addr, locID *uuid.UUID, concurrency int) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	cfg := discovery.PipelineConfig{
		Registry: s.reg, Fetcher: s.fetcher, Decrypt: s.scanDecrypt,
		PingTimeout: 2 * time.Second, SNMPTimeout: 3 * time.Second,
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
	if s.cipher == nil {
		return discovery.DecryptedCred{}, errBadRequest("no encryption key configured")
	}
	cred, err := s.queries.GetCredential(ctx, id)
	if err != nil {
		return discovery.DecryptedCred{}, err
	}
	plain, err := s.cipher.Open(cred.EncryptedBlob, cred.KeyID)
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
