package api

import (
	"context"
	"errors"
	"strings"

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

	// Is there an online agent for this site? ResolveSiteAgent returns the newest
	// enabled+online row, but DB status can be stale, so re-check heartbeat.
	online, ok := s.onlineSiteAgent(ctx, *d.LocationID)
	if !ok {
		// Distinguish "an agent is assigned but offline" from "no agent at all".
		if s.siteHasAnyAgent(ctx, *d.LocationID) {
			res.Reason, res.Detail = "agent_offline", "the Relay Agent assigned to this site is offline (no recent heartbeat) — start/repair it, or assign another"
		} else {
			res.Reason, res.Detail = "agent_missing", "no Relay Agent is assigned to this site — install or assign one to collect legacy/local Windows hosts"
		}
		return res, false
	}

	// Avoid piling up duplicate jobs when the same device is re-scanned before its
	// previous job ran.
	if n, _ := s.queries.CountActiveDeviceAgentJobs(ctx, &d.ID); n > 0 {
		res.Status, res.Method = "queued", "relay-agent"
		res.Reason, res.AgentName = "via_agent", online.Name
		res.Detail = "collection already queued for site agent " + online.Name + " — awaiting agent poll"
		return res, true
	}

	credID := s.pickAgentCredID(ctx, d, protocol)
	job, err := s.queries.CreateAgentJob(ctx, db.CreateAgentJobParams{
		AgentID: online.ID, DeviceID: &d.ID, CredentialID: credID,
		Kind: "collect_os", Protocol: protocol, Target: ip, Request: []byte("{}"),
	})
	if err != nil {
		res.Reason, res.Detail = "agent_enqueue_failed", "could not queue a job for site agent "+online.Name+": "+err.Error()
		return res, false
	}
	res.Status, res.Method = "queued", "relay-agent"
	res.Reason, res.AgentName = "via_agent", online.Name
	res.Detail = "dispatched to site agent " + online.Name + " via " + protocol + " (job " + job.ID.String() + ") — inventory will appear when the agent reports back"
	return res, true
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
