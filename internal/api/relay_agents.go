package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/auth"
	"github.com/coralsearesorts/hims/internal/credtest"
	"github.com/coralsearesorts/hims/internal/discovery"
	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/osinv"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// HIMS Relay Agent / Site Collector. One installable agent runs on a trusted
// machine inside a site and collects from devices the main HIMS API can't reach
// directly (legacy Windows, WMI/DCOM, local SNMP/SSH, VMware, CCTV). The agent
// PULLS jobs (NAT-friendly), executes locally, and posts structured results back.
// It authenticates with a per-agent bearer token (only the SHA-256 hash is stored;
// the token is shown to the operator once at registration). No secret is logged.

// --- management DTO (operator-facing; no token) ------------------------------

type agentDTO struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	LocationID    string   `json:"location_id,omitempty"`
	Hostname      string   `json:"hostname,omitempty"`
	IP            string   `json:"ip,omitempty"`
	OS            string   `json:"os,omitempty"`
	Version       string   `json:"version,omitempty"`
	Capabilities  []string `json:"capabilities"`
	Status        string   `json:"status"`
	Enabled       bool     `json:"enabled"`
	LastHeartbeat string   `json:"last_heartbeat,omitempty"`
	LastError     string   `json:"last_error,omitempty"`
	Online        bool     `json:"online"`
	FailedJobs    int64    `json:"failed_jobs,omitempty"` // count of failed collection jobs
}

// agentOnlineWindow: a heartbeat older than this flips the agent to "offline"
// for display even if the stored status still says online.
const agentOnlineWindow = 2 * time.Minute

func toAgentDTO(a db.RelayAgent) agentDTO {
	d := agentDTO{
		ID: a.ID.String(), Name: a.Name, LocationID: uuidPtrStr(a.LocationID),
		Hostname: a.Hostname, IP: a.Ip, OS: a.Os, Version: a.Version,
		Status: a.Status, Enabled: a.Enabled, LastError: a.LastError,
	}
	d.Capabilities = []string{}
	if len(a.Capabilities) > 0 {
		_ = json.Unmarshal(a.Capabilities, &d.Capabilities)
	}
	if a.LastHeartbeat != nil {
		d.LastHeartbeat = a.LastHeartbeat.Format(time.RFC3339)
		d.Online = a.Enabled && a.Status == "online" && timeSince(*a.LastHeartbeat) < agentOnlineWindow
	}
	return d
}

func timeSince(t time.Time) time.Duration { return time.Now().UTC().Sub(t) }

func genToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// --- management endpoints (operator session; credentials.manage) -------------

func (s *Server) listRelayAgents(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListRelayAgents(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]agentDTO, 0, len(rows))
	for _, a := range rows {
		dto := toAgentDTO(a)
		if n, err := s.queries.CountFailedAgentJobs(r.Context(), a.ID); err == nil {
			dto.FailedJobs = n
		}
		out = append(out, dto)
	}
	writeJSON(w, http.StatusOK, out)
}

// getRelayAgent returns one agent's detail (DTO) plus job rollups for the detail
// page: how many jobs are in flight (queued/dispatched) and how many failed.
func (s *Server) getRelayAgent(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	a, err := s.queries.GetRelayAgent(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	failed, _ := s.queries.CountFailedAgentJobs(r.Context(), id)
	var running int64
	if rows, rerr := s.queries.ListAgentJobs(r.Context(), db.ListAgentJobsParams{AgentID: id, Limit: 200}); rerr == nil {
		for _, j := range rows {
			if j.Status == "queued" || j.Status == "dispatched" {
				running++
			}
		}
	}
	dto := toAgentDTO(a)
	dto.FailedJobs = failed
	writeJSON(w, http.StatusOK, map[string]any{
		"agent": dto, "failed_jobs": failed, "running_jobs": running,
	})
}

// createRelayAgent registers a new agent and returns its enrollment token ONCE.
func (s *Server) createRelayAgent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name       string `json:"name"`
		LocationID string `json:"location_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	token := genToken()
	a, err := s.queries.CreateRelayAgent(r.Context(), db.CreateRelayAgentParams{
		Name: req.Name, LocationID: parseUUIDPtr(&req.LocationID), TokenHash: auth.HashToken(token),
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "credential", "agent.register", "relay_agent", a.ID.String(), "Registered relay agent "+a.Name, nil)
	// The token is returned exactly once — it is never stored or shown again.
	writeJSON(w, http.StatusOK, map[string]any{"agent": toAgentDTO(a), "token": token})
}

func (s *Server) patchRelayAgent(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	var req struct {
		Enabled    *bool   `json:"enabled"`
		LocationID *string `json:"location_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Enabled != nil {
		_ = s.queries.SetRelayAgentEnabled(r.Context(), db.SetRelayAgentEnabledParams{ID: id, Enabled: *req.Enabled})
	}
	if req.LocationID != nil {
		_ = s.queries.SetRelayAgentLocation(r.Context(), db.SetRelayAgentLocationParams{ID: id, LocationID: parseUUIDPtr(req.LocationID)})
	}
	a, err := s.queries.GetRelayAgent(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toAgentDTO(a))
}

func (s *Server) deleteRelayAgent(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := s.queries.DeleteRelayAgent(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// enqueueAgentTest enqueues a no-op test job so the operator can confirm the
// agent is polling + responding.
func (s *Server) enqueueAgentTest(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	job, err := s.queries.CreateAgentJob(r.Context(), db.CreateAgentJobParams{
		AgentID: id, Kind: "test", Request: []byte("{}"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "credential", "agent.test", "relay_agent", id.String(), "Queued agent test job", nil)
	writeJSON(w, http.StatusOK, map[string]any{"job_id": job.ID.String(), "status": "queued"})
}

type agentJobDTO struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Protocol  string `json:"protocol,omitempty"`
	Target    string `json:"target,omitempty"`
	Status    string `json:"status"`
	Category  string `json:"category,omitempty"`
	Error     string `json:"error,omitempty"`
	CreatedAt string `json:"created_at"`
}

func (s *Server) listRelayAgentJobs(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	rows, err := s.queries.ListAgentJobs(r.Context(), db.ListAgentJobsParams{AgentID: id, Limit: 50})
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]agentJobDTO, 0, len(rows))
	for _, j := range rows {
		d := agentJobDTO{ID: j.ID.String(), Kind: j.Kind, Protocol: j.Protocol, Target: j.Target, Status: j.Status, Category: j.Category, Error: j.Error, CreatedAt: j.CreatedAt.Format(time.RFC3339)}
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, out)
}

// --- agent protocol (bearer agent-token auth; /api/v1/agent/*) ---------------

// authAgent resolves the calling agent from its bearer token, or nil.
func (s *Server) authAgent(r *http.Request) *db.RelayAgent {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return nil
	}
	tok := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	if tok == "" {
		return nil
	}
	a, err := s.queries.GetRelayAgentByToken(r.Context(), auth.HashToken(tok))
	if err != nil || !a.Enabled {
		return nil
	}
	return &a
}

// agentRegister updates the agent's identity + capabilities on startup.
func (s *Server) agentRegister(w http.ResponseWriter, r *http.Request) {
	a := s.authAgent(r)
	if a == nil {
		http.Error(w, "agent authentication required", http.StatusUnauthorized)
		return
	}
	var req struct {
		Hostname     string   `json:"hostname"`
		IP           string   `json:"ip"`
		OS           string   `json:"os"`
		Version      string   `json:"version"`
		Capabilities []string `json:"capabilities"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	caps, _ := json.Marshal(req.Capabilities)
	_ = s.queries.UpdateRelayAgentIdentity(r.Context(), db.UpdateRelayAgentIdentityParams{
		ID: a.ID, Hostname: req.Hostname, Ip: req.IP, Os: req.OS, Version: req.Version, Capabilities: caps,
	})
	writeJSON(w, http.StatusOK, map[string]any{"agent_id": a.ID.String(), "name": a.Name})
}

func (s *Server) agentHeartbeat(w http.ResponseWriter, r *http.Request) {
	a := s.authAgent(r)
	if a == nil {
		http.Error(w, "agent authentication required", http.StatusUnauthorized)
		return
	}
	var req struct {
		Version   string `json:"version"`
		LastError string `json:"last_error"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	_ = s.queries.RelayAgentHeartbeat(r.Context(), db.RelayAgentHeartbeatParams{ID: a.ID, Column2: req.Version, Column3: req.LastError})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// agentJobOut is one job handed to the agent. It includes the decrypted
// credential (over the authenticated, ideally-TLS channel) so the agent can run
// the collection locally. The secret is never logged here.
type agentJobOut struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Protocol string `json:"protocol"`
	Target   string `json:"target"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// agentDispatchCap bounds how many collect jobs the server hands a single relay
// agent at once (the agent runs them serially). Keeps a from-zero scan's ~one
// job-per-Windows-host draining in controlled batches instead of a thundering herd.
const agentDispatchCap = 4

// staleDispatchedAfter is how long a job may sit 'dispatched' (handed to an agent,
// never reported back) before the reaper requeues/fails it. Longer than the agent's
// 4-minute per-job timeout plus margin so we never reap a job that is still running.
const staleDispatchedAfter = 8 * time.Minute

// agentPollBudget returns how many new collect jobs may be dispatched to an agent
// given the count currently in flight — never below zero, never above the cap.
// This is the throttle that keeps a from-zero scan draining in bounded batches.
func agentPollBudget(inflight int) int {
	if b := agentDispatchCap - inflight; b > 0 {
		return b
	}
	return 0
}

// agentJobRetryable reports whether a failed collect job should be retried. Auth
// and authorization rejections are terminal (the same credential keeps being
// rejected); connection/timeout/RPC/WMI/transient errors are worth a bounded retry.
func agentJobRetryable(category string) bool {
	switch category {
	case credtest.CatAuthFailed, credtest.CatUnsupported,
		"wmi_access_denied", "access_denied", "lockout_suspected":
		return false
	}
	return true
}

// agentRetryBackoff returns the wait before re-dispatching a transiently-failed
// job, growing with the attempt number to ease pressure on a saturated agent.
func agentRetryBackoff(attempt int) time.Duration {
	switch attempt {
	case 0:
		return 30 * time.Second
	case 1:
		return 2 * time.Minute
	default:
		return 5 * time.Minute
	}
}

func (s *Server) agentPollJobs(w http.ResponseWriter, r *http.Request) {
	a := s.authAgent(r)
	if a == nil {
		http.Error(w, "agent authentication required", http.StatusUnauthorized)
		return
	}
	// heartbeat-on-poll: a polling agent is alive.
	_ = s.queries.RelayAgentHeartbeat(r.Context(), db.RelayAgentHeartbeatParams{ID: a.ID})

	// Per-agent dispatch budget: never hand one agent more than agentDispatchCap
	// jobs in flight at once. The agent runs jobs SERIALLY (one PowerShell /
	// New-CimSession at a time) and many target hosts are lockout-prone, so a
	// from-zero subnet scan that enqueues ~70 collect_os jobs must drain in bounded
	// batches — this is the throttle that prevents the thundering herd. The reaper
	// (RequeueStaleAgentJobs) frees the budget if an agent dies holding jobs.
	inflight, _ := s.queries.CountDispatchedAgentJobs(r.Context(), a.ID)
	budget := agentPollBudget(int(inflight))
	if budget <= 0 {
		writeJSON(w, http.StatusOK, []agentJobOut{})
		return
	}
	rows, err := s.queries.ListRunnableAgentJobs(r.Context(), db.ListRunnableAgentJobsParams{AgentID: a.ID, Limit: int32(budget)})
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]agentJobOut, 0, len(rows))
	cph := s.cipher()
	for _, j := range rows {
		o := agentJobOut{ID: j.ID.String(), Kind: j.Kind, Protocol: j.Protocol, Target: j.Target}
		if j.CredentialID != nil && cph != nil {
			if c, err := s.queries.GetCredential(r.Context(), *j.CredentialID); err == nil {
				if plain, derr := cph.Open(c.EncryptedBlob, c.KeyID); derr == nil {
					o.Username, o.Password = credtest.SplitUserPass(string(plain))
				}
			}
		}
		out = append(out, o)
		_ = s.queries.MarkAgentJobDispatched(r.Context(), j.ID)
	}
	writeJSON(w, http.StatusOK, out)
}

// agentJobResult receives a completed job's structured result and persists it.
func (s *Server) agentJobResult(w http.ResponseWriter, r *http.Request) {
	a := s.authAgent(r)
	if a == nil {
		http.Error(w, "agent authentication required", http.StatusUnauthorized)
		return
	}
	jobID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid job id", http.StatusBadRequest)
		return
	}
	job, err := s.queries.GetAgentJob(r.Context(), jobID)
	if err != nil {
		writeErr(w, err)
		return
	}
	if job.AgentID != a.ID {
		http.Error(w, "job does not belong to this agent", http.StatusForbidden)
		return
	}
	var req struct {
		Success  bool            `json:"success"`
		Category string          `json:"category"`
		Error    string          `json:"error"`
		Report   json.RawMessage `json:"report"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// The request body is fully read; detach the persist + job-completion work from
	// the request context. A relay agent collecting a slow legacy host can drop the
	// result connection right after we persist the inventory — if these writes ran
	// on r.Context() that cancellation would commit the inventory but leave the job
	// stuck "dispatched" forever, blocking CountActiveDeviceAgentJobs (no future
	// re-collection). A short detached context keeps inventory-persisted and
	// job-completed atomic from the agent's perspective.
	pctx, pcancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
	defer pcancel()

	status := "failed"
	if req.Success {
		status = "done"
		// Persist the inventory the agent collected (collect_os jobs).
		if job.Kind == "collect_os" && job.DeviceID != nil && len(req.Report) > 0 {
			var rep osinv.Report
			if jerr := json.Unmarshal(req.Report, &rep); jerr == nil {
				if perr := osinv.Persist(pctx, s.queries, *job.DeviceID, rep, time.Now().UTC()); perr == nil {
					_ = s.queries.UpdateDeviceMonitoringStatus(pctx, db.UpdateDeviceMonitoringStatusParams{ID: *job.DeviceID, Status: "up"})
					if job.CredentialID != nil {
						_ = s.queries.SetDeviceCredential(pctx, db.SetDeviceCredentialParams{ID: *job.DeviceID, CredentialID: job.CredentialID})
					}
					s.reclassifyFromCaption(pctx, db.Device{ID: *job.DeviceID}, rep.OS.Caption)
				} else {
					status, req.Error = "failed", "agent collected but HIMS failed to persist: "+perr.Error()
				}
			} else {
				status, req.Error = "failed", "invalid inventory JSON from agent"
			}
		}
	}
	// Transient failure → bounded retry with backoff instead of a TERMINAL 'failed'.
	// Under a from-zero scan the agent can blip (queue saturation, temporary
	// WinRM/WMI/RPC error, timeout under concurrency pressure); a single blip must
	// not strand a reachable host as failed forever. The job goes back to 'queued'
	// with a backoff deadline, so the device keeps the pending_collection state
	// (an in-flight job) rather than misreporting a terminal failure. Auth/authz
	// rejections are NOT retried (the same credential will keep being rejected).
	if status == "failed" && job.Kind == "collect_os" &&
		agentJobRetryable(req.Category) && int(job.Attempt)+1 < int(job.MaxAttempts) {
		next := time.Now().Add(agentRetryBackoff(int(job.Attempt)))
		_ = s.queries.RequeueAgentJob(pctx, db.RequeueAgentJobParams{
			ID: jobID, NextAttemptAt: &next, Error: req.Error, Category: req.Category,
		})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "requeued", "attempt": int(job.Attempt) + 1})
		return
	}

	// Record a credential-test attempt (success or failure) so Credential Health /
	// Coverage reflect the agent path. Protocol maps to a credential kind.
	if job.DeviceID != nil && job.CredentialID != nil && job.Kind == "collect_os" {
		cat := req.Category
		if cat == "" {
			if status == "done" {
				cat = "success"
			} else {
				cat = "error"
			}
		}
		s.persistScanCredAttempts(pctx, db.Device{ID: *job.DeviceID}, []discovery.CredAttempt{{
			CredentialID: *job.CredentialID, Kind: domain.CredentialKind(job.Protocol),
			Protocol: job.Protocol, Success: status == "done", Category: cat,
			Detail: "via relay agent " + a.Name,
		}}, "default")
	}
	_ = s.queries.CompleteAgentJob(pctx, db.CompleteAgentJobParams{
		ID: jobID, Status: status, Result: nilIfEmpty(req.Report), Category: req.Category, Error: req.Error,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": status})
}

func nilIfEmpty(b json.RawMessage) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
}

// collectionQueueSummary — GET /reports/collection-queue. Fleet-wide and per-agent
// rollup of collect-job status (queued / dispatched / done / failed) plus the
// dispatch cap, so the operator and the acceptance report can see the live
// collection backlog and drain rate instead of guessing. Read-only.
func (s *Server) collectionQueueSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	fleet := map[string]int64{}
	if rows, err := s.queries.AgentJobStatusCounts(ctx); err == nil {
		for _, c := range rows {
			fleet[c.Status] = c.N
		}
	}
	type agentQueue struct {
		ID     string           `json:"id"`
		Name   string           `json:"name"`
		Online bool             `json:"online"`
		Counts map[string]int64 `json:"counts"`
	}
	agents := []agentQueue{}
	if list, err := s.queries.ListRelayAgents(ctx); err == nil {
		for _, a := range list {
			counts := map[string]int64{}
			if rows, cerr := s.queries.CountAgentJobsByStatusForAgent(ctx, a.ID); cerr == nil {
				for _, c := range rows {
					counts[c.Status] = c.N
				}
			}
			agents = append(agents, agentQueue{ID: a.ID.String(), Name: a.Name, Online: relayAgentOnline(a), Counts: counts})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"fleet":        fleet, // {queued, dispatched, done, failed}
		"agents":       agents,
		"dispatch_cap": agentDispatchCap,
	})
}
