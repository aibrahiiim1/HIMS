package api

import (
	"context"
	"errors"
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
		res.Reason, res.Detail = "agent_missing", "device is not assigned to a site — assign it to a site that has a Relay Agent, or install one"
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

	// Resolve the agent to QUEUE for. Prefer an online agent; but if the site's
	// assigned agent is only momentarily offline (its heartbeat lagged while it was
	// busy collecting under a from-zero scan), STILL queue the job for it — the agent
	// picks it up when it next polls. Refusing to enqueue while the agent was briefly
	// behind silently DROPPED the work and left hosts "not_attempted" (the exact
	// from-zero gap). Only a site with NO assigned agent is a hard gate.
	target, ok := s.onlineSiteAgent(actx, *d.LocationID)
	if !ok {
		assigned, has := s.assignedSiteAgent(actx, *d.LocationID)
		if !has {
			res.Reason, res.Detail = "agent_missing", "no Relay Agent is assigned to this site — install or assign one to collect legacy/local Windows hosts"
			return res, false
		}
		target = assigned // assigned but offline → queue anyway; it drains when the agent returns
	}

	// Avoid piling up duplicate jobs when the same device is re-scanned before its
	// previous job ran.
	if n, _ := s.queries.CountActiveDeviceAgentJobs(actx, &d.ID); n > 0 {
		res.Status, res.Method = "queued", "relay-agent"
		res.Reason, res.AgentName = "via_agent", target.Name
		res.Detail = "collection already queued for site agent " + target.Name + " — awaiting agent poll"
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
	res.Detail = "queued for site agent " + target.Name + " via " + protocol + " (job " + job.ID.String() + ") — inventory appears when the agent reports back"
	return res, true
}

// assignedSiteAgent returns the site's assigned, ENABLED agent regardless of its
// current online status (newest heartbeat wins). Used to queue collection for an
// agent that is only momentarily offline instead of dropping the work — the job
// waits in the queue and the agent collects it when it next polls.
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
			return kind == "winrm" || kind == "wmi"
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
