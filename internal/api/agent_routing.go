package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// Relay-Agent scan routing. When the main HIMS server cannot collect a Windows
// host directly (legacy WSMan 2.0, or WinRM disabled/unreachable), and the
// device belongs to a site that has an online Relay Agent, HIMS enqueues a
// collect_os job for that agent instead of leaving the host on an honest gate.
// The agent (running inside the site) pulls the job, collects locally, and posts
// the inventory back — so collection completes asynchronously, out of band from
// the scan. We never fake collection: the scan result honestly reports
// "dispatched to site agent" (queued), "agent offline", or "agent missing".

// relayAgentOnline reports whether an agent should be treated as online for
// routing + display: enabled, DB status online, and a heartbeat within the
// freshness window. Single source of truth shared by the DTO and the router so
// the UI badge and the routing decision never disagree.
func relayAgentOnline(a db.RelayAgent) bool {
	return a.Enabled && a.Status == "online" &&
		a.LastHeartbeat != nil && timeSince(*a.LastHeartbeat) < agentOnlineWindow
}

// agentRouteOutcome is the result of attempting to route a device's collection
// to its site agent. handled=true means a job was enqueued (or one was already
// in flight) and the caller should stop and return res; handled=false means no
// online agent was available and res carries the honest reason (agent_offline /
// agent_missing) for the caller to fold into its gate.
func (s *Server) routeViaSiteAgent(ctx context.Context, d db.Device, ip, protocol string) (res osCollectResult, handled bool) {
	res = osCollectResult{DeviceID: d.ID.String(), Name: d.Name, IP: ip, Status: "failed"}

	if d.LocationID == nil {
		// NOT agent_missing. Relay routing is per-site, so a device with no site
		// is refused before any agent is even looked at — reporting this as a
		// missing agent sends the operator to install one, which changes nothing.
		// That misdiagnosis cost a full troubleshooting cycle in production: the
		// site's agent was installed, online and on the very same subnet as the
		// devices, while 78 of them sat with location_id = NULL.
		res.Reason = "device_no_site"
		res.Detail = "this device is not assigned to a site, and relay-agent routing is per-site — no agent can be selected for it. " +
			"Map its subnet under Locations → Subnets (then re-scan), or set the site directly in Edit Device."
		return res, false
	}

	// Enqueuing an agent job is a quick DB write that MUST complete even when the
	// caller's per-host scan budget is already exhausted — the direct WinRM attempt
	// against a filtered 5985 port can burn the whole budget before we get here, so
	// using the caller's ctx made ResolveSiteAgent / CreateAgentJob fail on a dead
	// context and the host silently fell back to a misleading auth_failed instead of
	// being dispatched to the online agent. Resolve + enqueue on a FRESH, independent
	// context so routing always completes. Mirrors the enroll-on-fresh-ctx fix.
	actx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Per-device COLLECTOR OVERRIDE: an operator can PIN this device to a SPECIFIC relay agent
	// (one with proven protocol reachability) instead of the site's default agent — for a host the
	// default site agent cannot reach over DCOM/RPC (e.g. a LocalSystem agent has no network
	// identity to authenticate a remote workgroup local-admin account, or the agent is on a
	// different segment). This is an evidence-based operator choice, never a guess, and does NOT
	// change the site/agent architecture for other devices. Stored as a 'collector.agent_id' fact.
	var target db.RelayAgent
	pinned := false
	if oid := s.deviceCollectorAgent(actx, d.ID); oid != uuid.Nil {
		if a, e := s.queries.GetRelayAgent(actx, oid); e == nil {
			target, pinned = a, true
		}
	}
	// Resolve the site agent to QUEUE for (unless pinned). Prefer an online agent; but if the
	// site's assigned agent is only momentarily offline (heartbeat lag under load), STILL queue
	// for it — it drains when it next polls. Only a site with NO assigned agent is a hard gate.
	matchKind := agentMatchExact
	viaSite := ""
	if !pinned {
		// Prefer an ONLINE agent, then fall back to an assigned-but-offline one
		// (it drains when it next polls). Both passes use the same scope rules:
		// exact first, then the nearest ancestor that opted in to descendants.
		match, competing, ok := s.resolveSiteAgentScoped(actx, *d.LocationID, relayAgentOnline)
		if !ok && len(competing) == 0 {
			match, competing, ok = s.resolveSiteAgentScoped(actx, *d.LocationID, nil)
		}
		if len(competing) > 0 {
			// Never pick arbitrarily: an unintended collector is worse than an
			// honest stop, and far harder to diagnose after the fact.
			res.Reason = "agent_scope_ambiguous"
			res.Detail = "more than one Relay Agent inherits this site from the same parent (" + agentNames(competing) + "), so the correct collector is not determined. " +
				"Assign one agent directly to this site, or turn off “Include descendant sites” on all but one of them."
			return res, false
		}
		if !ok {
			res.Reason = "agent_missing"
			res.Detail = "no Relay Agent serves this site — install or assign one, or enable “Include descendant sites” on an agent at a parent site"
			return res, false
		}
		target, matchKind = match.Agent, match.Kind
		if match.Kind == agentMatchInherited {
			viaSite = locationNameOf(s.locationsByID(actx), match.Via)
		}
	}
	pinNote := ""
	switch {
	case pinned:
		pinNote = " (pinned collector override)"
	case matchKind == agentMatchInherited:
		// Say plainly that this agent was inherited, not assigned here — otherwise
		// an operator cannot tell why THIS collector was chosen.
		pinNote = " (inherited from parent site " + nameOr(viaSite, "above") + ")"
	}

	// The chosen agent must actually be able to speak this protocol. WMI/DCOM is
	// Windows-only, so a Linux agent (e.g. one installed on the HIMS server
	// itself) cannot collect a legacy Windows host no matter how long it runs.
	// Queuing anyway would replace an honest "no agent" gate with jobs that fail
	// and retry forever — the same misleading state, harder to diagnose.
	if !agentSupportsProtocol(target, protocol) {
		res.Reason = "agent_capability_missing"
		res.Detail = "site agent " + target.Name + " (" + agentOSLabel(target) + ") cannot collect over " + protocol +
			"; it advertises [" + strings.Join(relayAgentCaps(target), ", ") + "]"
		if protocol == "wmi" {
			res.Detail += ". WMI/DCOM requires the Relay Agent to run on a WINDOWS host inside this site — a Linux agent is WinRM-only"
		}
		return res, false
	}

	// Avoid piling up duplicate jobs when the same device is re-scanned before its
	// previous job ran.
	if n, _ := s.queries.CountActiveDeviceAgentJobs(actx, &d.ID); n > 0 {
		res.Status, res.Method = "queued", "relay-agent"
		res.Reason, res.AgentName = "via_agent", target.Name
		res.Detail = "collection already queued for agent " + target.Name + pinNote + " — awaiting agent poll"
		return res, true
	}

	credID := s.pickAgentCredID(actx, d, protocol)
	job, err := s.queries.CreateAgentJob(actx, db.CreateAgentJobParams{
		AgentID: target.ID, DeviceID: &d.ID, CredentialID: credID,
		Kind: "collect_os", Protocol: protocol, Target: ip, Request: []byte("{}"),
	})
	if err != nil {
		res.Reason, res.Detail = "agent_enqueue_failed", "could not queue a job for site agent "+target.Name+": "+err.Error()
		return res, false
	}
	res.Status, res.Method = "queued", "relay-agent"
	res.Reason, res.AgentName = "via_agent", target.Name
	res.Detail = "queued for agent " + target.Name + pinNote + " via " + protocol + " (job " + job.ID.String() + ") — inventory appears when the agent reports back"
	return res, true
}

// setDeviceCollectorAgent (POST /devices/{id}/collector-agent) pins this device's OS collection
// to a specific relay agent (evidence-based operator choice — one with proven reachability to a
// host the site agent cannot reach over DCOM/RPC). An empty/absent agent_id CLEARS the pin and
// reverts to the site's default agent. Never a guess; does not affect other devices.
func (s *Server) setDeviceCollectorAgent(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	var req struct {
		AgentID string `json:"agent_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	aid := strings.TrimSpace(req.AgentID)
	if aid == "" {
		// Clear the pin: an empty fact value → deviceCollectorAgent parses to uuid.Nil → site default.
		empty := ""
		if err := s.queries.UpsertDeviceFact(ctx, db.UpsertDeviceFactParams{DeviceID: id, Key: "collector.agent_id", Value: &empty, Driver: "operator"}); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"pinned": false, "detail": "collector pin cleared — reverts to the site's default relay agent"})
		return
	}
	agentID, err := uuid.Parse(aid)
	if err != nil {
		http.Error(w, "invalid agent_id", http.StatusBadRequest)
		return
	}
	agent, err := s.queries.GetRelayAgent(ctx, agentID)
	if err != nil {
		http.Error(w, "relay agent not found", http.StatusBadRequest)
		return
	}
	v := agentID.String()
	if err := s.queries.UpsertDeviceFact(ctx, db.UpsertDeviceFactParams{DeviceID: id, Key: "collector.agent_id", Value: &v, Driver: "operator"}); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pinned": true, "agent_id": v, "agent_name": agent.Name, "detail": "collection pinned to relay agent " + agent.Name})
}

// deviceCollectorAgent returns the operator-pinned relay-agent id for this device (the
// 'collector.agent_id' fact), or uuid.Nil when none is set. The pin overrides the site's default
// agent so a host the site agent cannot reach over DCOM/RPC is collected by a reachable agent.
func (s *Server) deviceCollectorAgent(ctx context.Context, id uuid.UUID) uuid.UUID {
	facts, err := s.queries.ListDeviceFacts(ctx, id)
	if err != nil {
		return uuid.Nil
	}
	for _, f := range facts {
		if f.Key == "collector.agent_id" && f.Value != nil {
			if aid, e := uuid.Parse(strings.TrimSpace(*f.Value)); e == nil {
				return aid
			}
		}
	}
	return uuid.Nil
}

// assignedSiteAgent returns the site's assigned, ENABLED agent regardless of its
// current online status (newest heartbeat wins). Used to queue collection for an
// agent that is only momentarily offline instead of dropping the work — the job
// waits in the queue and the agent collects it when it next polls.
// relayAgentCaps decodes an agent's advertised capabilities. An agent that
// registered before capabilities were reported honestly (or with none at all)
// returns an empty list, which agentSupportsProtocol treats permissively — we
// must not break existing working agents on missing metadata.
func relayAgentCaps(a db.RelayAgent) []string {
	if len(a.Capabilities) == 0 {
		return nil
	}
	var caps []string
	if err := json.Unmarshal(a.Capabilities, &caps); err != nil {
		return nil
	}
	return caps
}

// agentSupportsProtocol reports whether the agent advertises this protocol.
// Unknown/absent capabilities => assume capable (an older agent that never
// reported them still collects fine; refusing to route would be a regression).
func agentSupportsProtocol(a db.RelayAgent, protocol string) bool {
	caps := relayAgentCaps(a)
	if len(caps) == 0 {
		return true
	}
	for _, c := range caps {
		if strings.EqualFold(strings.TrimSpace(c), protocol) {
			return true
		}
	}
	return false
}

// agentOSLabel is the agent's reported OS, for operator-facing messages.
func agentOSLabel(a db.RelayAgent) string {
	if strings.TrimSpace(a.Os) == "" {
		return "unknown OS"
	}
	return a.Os
}

func (s *Server) assignedSiteAgent(ctx context.Context, loc uuid.UUID) (db.RelayAgent, bool) {
	all, err := s.queries.ListRelayAgents(ctx)
	if err != nil {
		return db.RelayAgent{}, false
	}
	var best db.RelayAgent
	found := false
	for _, a := range all {
		if a.LocationID == nil || *a.LocationID != loc || !a.Enabled {
			continue
		}
		if !found || hbAfter(a.LastHeartbeat, best.LastHeartbeat) {
			best, found = a, true
		}
	}
	return best, found
}

// hbAfter reports whether heartbeat a is later than b (nil = never).
func hbAfter(a, b *time.Time) bool {
	if a == nil {
		return false
	}
	if b == nil {
		return true
	}
	return a.After(*b)
}

// onlineSiteAgent returns the freshest online agent assigned to a location.
func (s *Server) onlineSiteAgent(ctx context.Context, loc uuid.UUID) (db.RelayAgent, bool) {
	a, err := s.queries.ResolveSiteAgent(ctx, &loc)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.RelayAgent{}, false
		}
		return db.RelayAgent{}, false
	}
	if !relayAgentOnline(a) {
		return db.RelayAgent{}, false
	}
	return a, true
}

// siteHasAnyAgent reports whether any agent (online or not) is assigned to a site.
func (s *Server) siteHasAnyAgent(ctx context.Context, loc uuid.UUID) bool {
	all, err := s.queries.ListRelayAgents(ctx)
	if err != nil {
		return false
	}
	for _, a := range all {
		if a.LocationID != nil && *a.LocationID == loc {
			return true
		}
	}
	return false
}

// pickAgentCredID chooses the credential the agent should use: the device's
// bound credential first, else the first stored credential whose kind suits the
// protocol. nil means "no credential" (the agent will report auth failure, which
// is honest). The secret never leaves the server here — only the credential id.
func (s *Server) pickAgentCredID(ctx context.Context, d db.Device, protocol string) *uuid.UUID {
	if d.CredentialID != nil {
		return d.CredentialID
	}
	all, err := s.queries.ListCredentials(ctx)
	if err != nil {
		return nil
	}
	want := func(kind string) bool {
		switch protocol {
		case "winrm", "wmi":
			return kind == "windows" || kind == "winrm" || kind == "wmi"
		case "ssh":
			return kind == "ssh" || kind == "cli"
		}
		return false
	}
	for _, c := range all {
		if want(c.Kind) {
			id := c.ID
			return &id
		}
	}
	return nil
}

// agentProtocolFor picks the protocol the site agent should use given why direct
// collection could not proceed. Legacy WSMan 2.0 and WinRM-disabled/unreachable
// Windows hosts are collected via WMI/DCOM (the agent's pure-Go WinRM would hit
// the same WSMan fault); other Windows cases retry modern WinRM through the
// agent's local vantage point.
func agentProtocolFor(method, reason string) string {
	if method == "ssh" {
		return "ssh"
	}
	if strings.Contains(reason, "legacy") || strings.Contains(reason, "wsman") ||
		strings.Contains(reason, "disabled") || strings.Contains(reason, "wmi") {
		return "wmi"
	}
	return "winrm"
}
