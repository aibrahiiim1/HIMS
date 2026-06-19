package api

import (
	"testing"

	"github.com/coralsearesorts/hims/internal/credtest"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

// Regression suite for the 172.21.60.0/24 scan-hardening pass. Each test pins one
// class of bug that previously left enrolled Windows endpoints silently
// unmanaged or mislabeled. Pure functions only (no DB) — the live acceptance
// scan covers the SQL/orchestration paths these guard.

// Class 1 — a Windows host must get an OS-collection attempt even when the scan
// captured NO management port. osCollectionCandidate owns the decision and takes
// no port list, so it cannot regress to port-gating (the original bug that left
// late/slow-probed endpoints enrolled with zero attempts → stuck needs_credential).
func TestOSCollectionCandidate_NoPortGating(t *testing.T) {
	cases := []struct {
		name                          string
		dev                           db.Device
		boundOS, legacyWSMan, special bool
		want                          bool
	}{
		// os_family=windows, no ports, no bound cred → still a candidate.
		{"windows-osfamily", db.Device{OsFamily: "windows"}, false, false, false, true},
		// The .106/.119 case: enrolled endpoint whose os_family is still blank
		// (not yet collected) — must still be attempted.
		{"endpoint-blank-osfamily", db.Device{Category: "endpoint"}, false, false, false, true},
		// Bound WinRM/SSH credential on a non-endpoint (e.g. server) → candidate.
		{"bound-os-cred", db.Device{Category: "server"}, true, false, false, true},
		// Legacy WSMan-2.0 host (auth ok, op fault) → candidate (routes to agent).
		{"legacy-wsman", db.Device{Category: "server"}, false, true, false, true},
		// Non-Windows, non-bound (printer) → NOT a generic OS-collection candidate.
		{"printer", db.Device{Category: "printer", OsFamily: ""}, false, false, false, false},
		// Specialized appliance takes its own branch even if endpoint-like.
		{"specialized-camera", db.Device{Category: "endpoint"}, false, false, true, false},
	}
	for _, c := range cases {
		if got := osCollectionCandidate(c.dev, c.boundOS, c.legacyWSMan, c.special); got != c.want {
			t.Errorf("%s: osCollectionCandidate=%v want %v", c.name, got, c.want)
		}
	}
}

// Class 3 + 5 — a successful OS inventory whose collection_method is winrm OR wmi
// must produce a PROVEN access signal, and both the direct-WinRM and agent-WMI
// paths must derive the SAME managed state. access.sql maps os_inventory
// collection_method → protocol (winrm/winrm-native → "winrm", wmi → "wmi") with
// source "evidence"; this asserts buildAccessMap + deriveManagement treat that
// evidence as proven-managed for BOTH protocols.
func TestEvidenceWinRMAndWMIBothManaged(t *testing.T) {
	winID, wmiID := uuid.New(), uuid.New()
	rows := []db.ListDeviceAccessSignalsRow{
		{DeviceID: winID, Protocol: "winrm", Source: "evidence"}, // direct WinRM collect
		{DeviceID: wmiID, Protocol: "wmi", Source: "evidence"},   // agent-WMI collect
	}
	am := buildAccessMap(rows)
	if !am[winID].provenHas("winrm") {
		t.Error("winrm evidence must be proven-managed")
	}
	if !am[wmiID].provenHas("wmi") {
		t.Error("wmi evidence must be proven-managed")
	}

	mk := func(id uuid.UUID) *statusMaps {
		return &statusMaps{access: am, test: map[uuid.UUID]*deviceTestStatus{},
			onlineSites: map[uuid.UUID]bool{}, anySites: map[uuid.UUID]bool{}}
	}
	if st, by := mk(winID).deriveManagement(db.Device{ID: winID, OsFamily: "windows", Category: "endpoint"}); st != MgmtManaged || len(by) != 1 || by[0] != "winrm" {
		t.Errorf("direct-WinRM host: got %s %v, want managed/[winrm]", st, by)
	}
	if st, by := mk(wmiID).deriveManagement(db.Device{ID: wmiID, OsFamily: "windows", Category: "endpoint"}); st != MgmtManaged || len(by) != 1 || by[0] != "wmi" {
		t.Errorf("agent-WMI host: got %s %v, want managed/[wmi]", st, by)
	}
}

// Class 4 — a device that was ACTUALLY attempted must never show the misleading
// needs_credential: an auth rejection → credential_failed; a bound credential
// that never collected → collection_failed. needs_credential is reserved for a
// credentialed-class host with NO attempt and NO binding (genuinely "supply a
// credential"), never for a host whose credential was tried.
func TestAttemptedHostNotLabeledNeedsCredential(t *testing.T) {
	id, cred := uuid.New(), uuid.New()
	mk := func(ts *deviceTestStatus) *statusMaps {
		m := &statusMaps{access: map[uuid.UUID]*deviceAccess{}, test: map[uuid.UUID]*deviceTestStatus{},
			onlineSites: map[uuid.UUID]bool{}, anySites: map[uuid.UUID]bool{}}
		if ts != nil {
			m.test[id] = ts
		}
		return m
	}
	// Auth was attempted and rejected → credential_failed (NOT needs_credential).
	authFail := &deviceTestStatus{tested: true, authFailed: true, kindCategory: map[string]string{"winrm": "auth_failed"}}
	if st, _ := mk(authFail).deriveManagement(db.Device{ID: id, OsFamily: "windows", Category: "endpoint"}); st != MgmtCredentialFailed {
		t.Errorf("attempted+auth_failed: got %s, want credential_failed", st)
	}
	// The real 172.21.60.49/.50 case: a Windows endpoint the agent TRIED (WMI/WinRM
	// over New-CimSession) but the host firewall blocked it — a non-auth failure
	// (wmi_error), no credential bound. Must be collection_failed (→ "WMI blocked"
	// in the report), NOT the misleading needs_credential that points at credentials.
	wmiBlocked := &deviceTestStatus{tested: true, authFailed: false, failedKinds: map[string]bool{"wmi": true}, kindCategory: map[string]string{"wmi": "wmi_error"}}
	if st, _ := mk(wmiBlocked).deriveManagement(db.Device{ID: id, OsFamily: "windows", Category: "endpoint"}); st != MgmtCollectionFailed {
		t.Errorf("attempted+wmi_error (firewall block), unbound: got %s, want collection_failed (not needs_credential)", st)
	}
	// A credential is bound but nothing collected → collection_failed (NOT needs_credential).
	if st, _ := mk(&deviceTestStatus{}).deriveManagement(db.Device{ID: id, Category: "server", CredentialID: &cred}); st != MgmtCollectionFailed {
		t.Errorf("bound-but-uncollected: got %s, want collection_failed", st)
	}
	// Sanity: a credentialed-class host with NO attempt and NO binding IS the
	// honest needs_credential ("supply a credential") — the only path to it.
	if st, _ := mk(nil).deriveManagement(db.Device{ID: id, Category: "server"}); st != MgmtNeedsCredential {
		t.Errorf("no-attempt credentialed class: got %s, want needs_credential", st)
	}
}

// Class 3 — a Windows-like host that was enrolled but never attempted (no active
// job, no evidence, no auth attempt, no binding) must report not_attempted, NOT
// the misleading needs_credential. (A non-Windows credentialed class still shows
// needs_credential — that genuinely needs a credential added.)
func TestWindowsNeverAttemptedIsNotAttempted(t *testing.T) {
	id := uuid.New()
	empty := func() *statusMaps {
		return &statusMaps{access: map[uuid.UUID]*deviceAccess{}, test: map[uuid.UUID]*deviceTestStatus{},
			onlineSites: map[uuid.UUID]bool{}, anySites: map[uuid.UUID]bool{}, activeCollect: map[uuid.UUID]bool{}}
	}
	// Windows endpoint, nothing attempted → not_attempted.
	if st, _ := empty().deriveManagement(db.Device{ID: id, Category: "endpoint"}); st != MgmtNotAttempted {
		t.Errorf("windows never-attempted: got %s, want not_attempted", st)
	}
	if st, _ := empty().deriveManagement(db.Device{ID: id, OsFamily: "windows", Category: "server"}); st != MgmtNotAttempted {
		t.Errorf("windows(os_family) never-attempted: got %s, want not_attempted", st)
	}
	// A switch with no credential still legitimately needs one.
	if st, _ := empty().deriveManagement(db.Device{ID: id, Category: "switch"}); st != MgmtNeedsCredential {
		t.Errorf("switch never-attempted: got %s, want needs_credential", st)
	}
}

// Class 6 — a device with an in-flight collect_os job (queued/dispatched) must
// report pending_collection, NOT a terminal failure derived from the stale
// direct-probe attempt that already failed. This is the core fix: the scan's
// immediate auth_failed/wmi_error must not mask that collection is still running.
func TestActiveAgentJobIsPendingNotFailed(t *testing.T) {
	id := uuid.New()
	m := &statusMaps{
		access: map[uuid.UUID]*deviceAccess{},
		// Stale direct-probe signal that WOULD otherwise yield credential_failed.
		test: map[uuid.UUID]*deviceTestStatus{
			id: {tested: true, authFailed: true, kindCategory: map[string]string{"winrm": "auth_failed"}},
		},
		onlineSites: map[uuid.UUID]bool{}, anySites: map[uuid.UUID]bool{},
		activeCollect: map[uuid.UUID]bool{id: true}, // a collect_os job is queued/dispatched
	}
	if st, _ := m.deriveManagement(db.Device{ID: id, OsFamily: "windows", Category: "endpoint"}); st != MgmtPendingCollection {
		t.Errorf("in-flight job: got %s, want pending_collection (not a terminal failure)", st)
	}
}

// Class 6c — an in-flight job PARKED on an offline site agent must report
// agent_offline (honest: waiting for the agent to return), while the same job with
// an online site agent reports pending_collection (actively draining). Pins the
// distinction added after the not_attempted from-zero gap.
func TestActiveJobPendingVsParkedOnOfflineAgent(t *testing.T) {
	id, loc := uuid.New(), uuid.New()
	base := func(online bool) *statusMaps {
		m := &statusMaps{
			access: map[uuid.UUID]*deviceAccess{}, test: map[uuid.UUID]*deviceTestStatus{},
			onlineSites: map[uuid.UUID]bool{}, anySites: map[uuid.UUID]bool{loc: true},
			activeCollect: map[uuid.UUID]bool{id: true},
		}
		if online {
			m.onlineSites[loc] = true
		}
		return m
	}
	dev := db.Device{ID: id, OsFamily: "windows", Category: "endpoint", LocationID: &loc}
	if st, _ := base(true).deriveManagement(dev); st != MgmtPendingCollection {
		t.Errorf("active job + online agent: got %s, want pending_collection", st)
	}
	if st, _ := base(false).deriveManagement(dev); st != MgmtAgentOffline {
		t.Errorf("active job + offline agent: got %s, want agent_offline (parked, not pending)", st)
	}
}

// Class 8 — proven collection evidence outranks everything: a device that is
// managed-by-evidence AND also has an in-flight job (and a failed history) stays
// managed. A later transient failure must never silently un-manage proven evidence.
func TestProvenEvidenceOutranksPendingAndFailure(t *testing.T) {
	id := uuid.New()
	am := buildAccessMap([]db.ListDeviceAccessSignalsRow{{DeviceID: id, Protocol: "winrm", Source: "evidence"}})
	m := &statusMaps{
		access:      am,
		test:        map[uuid.UUID]*deviceTestStatus{id: {tested: true, authFailed: true}},
		onlineSites: map[uuid.UUID]bool{}, anySites: map[uuid.UUID]bool{},
		activeCollect: map[uuid.UUID]bool{id: true},
	}
	if st, by := m.deriveManagement(db.Device{ID: id, OsFamily: "windows", Category: "endpoint"}); st != MgmtManaged || len(by) != 1 || by[0] != "winrm" {
		t.Errorf("proven evidence must win over pending/failed: got %s %v, want managed/[winrm]", st, by)
	}
}

// Class 7 — bounded retry classification: transient collection failures are
// retried; auth/authorization/unsupported are terminal (retrying a rejected
// credential is pointless).
func TestAgentJobRetryable(t *testing.T) {
	transient := []string{"wmi_error", "namespace_unavailable", "unreachable", "timeout", "rpc_unreachable", "error", ""}
	for _, c := range transient {
		if !agentJobRetryable(c) {
			t.Errorf("category %q should be retryable (transient)", c)
		}
	}
	terminal := []string{credtest.CatAuthFailed, credtest.CatUnsupported, "wmi_access_denied", "access_denied", "lockout_suspected"}
	for _, c := range terminal {
		if agentJobRetryable(c) {
			t.Errorf("category %q must NOT be retryable (auth/authz/unsupported is terminal)", c)
		}
	}
}

// Class 9 — per-agent dispatch budget never exceeds the cap and never goes
// negative, so a from-zero scan can never dump a thundering herd on one agent.
func TestAgentPollBudgetRespectsCap(t *testing.T) {
	cases := map[int]int{0: agentDispatchCap, 1: agentDispatchCap - 1, agentDispatchCap: 0, agentDispatchCap + 5: 0}
	for inflight, want := range cases {
		if got := agentPollBudget(inflight); got != want {
			t.Errorf("agentPollBudget(%d) = %d, want %d", inflight, got, want)
		}
		if got := agentPollBudget(inflight); got < 0 || got > agentDispatchCap {
			t.Errorf("agentPollBudget(%d) = %d out of [0,%d]", inflight, got, agentDispatchCap)
		}
	}
}

// Layer 4 — honest scan phase. The UI must never show "complete" while any AUTOMATIC
// collection remains: collecting while jobs are in flight, self_healing while terminal
// transient failures still await automatic re-collection (incl. the cooldown window
// where no job is in flight), complete only when both are zero.
func TestScanPhaseHonesty(t *testing.T) {
	cases := []struct {
		status           string
		pending, healing int64
		want             string
	}{
		{"running", 0, 0, "discovering"},
		{"running", 5, 0, "discovering"}, // probe phase dominates regardless of collection
		{"completed", 9, 0, "collecting"},
		{"completed", 0, 3, "self_healing"}, // cooldown window: no in-flight job but self-heal pending
		{"completed", 2, 4, "collecting"},   // in-flight beats self-heal in label precedence
		{"completed", 0, 0, "complete"},
		{"failed", 0, 0, "failed"},
		{"cancelled", 0, 0, "cancelled"},
		{"pending", 0, 0, "queued"},
	}
	for _, c := range cases {
		if got := scanPhase(c.status, c.pending, c.healing); got != c.want {
			t.Errorf("scanPhase(%q, p=%d, h=%d) = %q, want %q", c.status, c.pending, c.healing, got, c.want)
		}
	}
	// The critical gate: a "completed" probe phase with self-heal still eligible must
	// NOT read as complete (the false-complete the operator must never see).
	if scanPhase("completed", 0, 1) == "complete" {
		t.Fatal("self-heal still eligible must never show phase=complete")
	}
}

// Layer 2 — adaptive load governor. When load-induced transient backoff is high the
// dispatch budget is clamped to a trickle (so concurrent WinRM negotiations drop and
// listeners recover); when it is low the full in-flight budget is handed out. Never
// negative, never above the cap.
func TestAgentPollBudgetAdaptiveGovernor(t *testing.T) {
	// Low backoff → identical to the plain in-flight budget (no throttle).
	for inflight := 0; inflight <= agentDispatchCap; inflight++ {
		if got, want := agentPollBudgetAdaptive(inflight, 0), agentPollBudget(inflight); got != want {
			t.Errorf("no-load: adaptive(%d,0)=%d, want plain budget %d", inflight, got, want)
		}
	}
	// High backoff with capacity free → clamped to the throttled trickle.
	if got := agentPollBudgetAdaptive(0, agentLoadThrottleAt); got != agentThrottledBudget {
		t.Errorf("storm with idle agent: adaptive(0,%d)=%d, want throttled %d", agentLoadThrottleAt, got, agentThrottledBudget)
	}
	// The governor never manufactures capacity: a full agent stays at 0 even under load.
	if got := agentPollBudgetAdaptive(agentDispatchCap, agentLoadThrottleAt+10); got != 0 {
		t.Errorf("full agent under load: adaptive=%d, want 0", got)
	}
	// Just below the throttle threshold → not throttled (full budget).
	if agentLoadThrottleAt > 0 {
		if got, want := agentPollBudgetAdaptive(0, agentLoadThrottleAt-1), agentPollBudget(0); got != want {
			t.Errorf("below threshold: adaptive(0,%d)=%d, want %d", agentLoadThrottleAt-1, got, want)
		}
	}
	// Throttled budget must be a real, bounded trickle.
	if agentThrottledBudget < 1 || agentThrottledBudget > agentDispatchCap {
		t.Errorf("agentThrottledBudget %d out of (0,%d]", agentThrottledBudget, agentDispatchCap)
	}
}

// Class 12 — deriveManagement always returns a defined, non-empty state for every
// representative input (a from-zero scan can never produce an "unknown reason").
func TestDeriveManagementNeverEmpty(t *testing.T) {
	id, cred := uuid.New(), uuid.New()
	valid := map[string]bool{
		MgmtManaged: true, MgmtPartiallyManaged: true, MgmtUnmanaged: true,
		MgmtNeedsCredential: true, MgmtCredentialFailed: true, MgmtNeedsAgent: true,
		MgmtAgentOffline: true, MgmtCollectionFailed: true, MgmtPendingCollection: true,
		MgmtNotAttempted: true, MgmtVirtual: true,
	}
	provenWinRM := buildAccessMap([]db.ListDeviceAccessSignalsRow{{DeviceID: id, Protocol: "winrm", Source: "evidence"}})
	mk := func(access map[uuid.UUID]*deviceAccess, ts *deviceTestStatus, active bool) *statusMaps {
		m := &statusMaps{access: access, test: map[uuid.UUID]*deviceTestStatus{}, onlineSites: map[uuid.UUID]bool{}, anySites: map[uuid.UUID]bool{}, activeCollect: map[uuid.UUID]bool{}}
		if access == nil {
			m.access = map[uuid.UUID]*deviceAccess{}
		}
		if ts != nil {
			m.test[id] = ts
		}
		if active {
			m.activeCollect[id] = true
		}
		return m
	}
	devs := []db.Device{
		{ID: id, Category: "endpoint", OsFamily: "windows"},
		{ID: id, Category: "switch"},
		{ID: id, Category: "printer"},
		{ID: id, Category: "server", CredentialID: &cred},
		{ID: id, Category: "camera"},
		{ID: id, IsVirtual: true},
	}
	maps := []*statusMaps{
		mk(nil, nil, false),
		mk(nil, &deviceTestStatus{tested: true, authFailed: true}, false),
		mk(nil, &deviceTestStatus{tested: true}, false),
		mk(nil, nil, true),
		mk(provenWinRM, nil, false),
	}
	for _, d := range devs {
		for _, m := range maps {
			st, _ := m.deriveManagement(d)
			if st == "" || !valid[st] {
				t.Errorf("deriveManagement(%s) returned undefined state %q", d.Category, st)
			}
		}
	}
}

// Multi-credential agent path — pins the invariants behind making the agent WMI
// path equivalent to the direct WinRM path (the .106/.119 fix).

// Test: direct WinRM and agent WMI resolve the SAME candidate credential set —
// both winrm and wmi methods match both winrm+wmi (Windows) credential kinds, so
// the two paths converge. SSH stays separate.
func TestCredKindMatchesMethod_WindowsConverge(t *testing.T) {
	for _, m := range []string{"winrm", "wmi"} {
		if !credKindMatchesMethod("winrm", m) || !credKindMatchesMethod("wmi", m) {
			t.Errorf("method %q must match both winrm and wmi credential kinds", m)
		}
		if credKindMatchesMethod("ssh", m) || credKindMatchesMethod("snmp_v2c", m) {
			t.Errorf("method %q must NOT match ssh/snmp credential kinds", m)
		}
	}
	if !credKindMatchesMethod("ssh", "ssh") || !credKindMatchesMethod("cli", "ssh") {
		t.Error("ssh method must match ssh/cli kinds")
	}
	if credKindMatchesMethod("winrm", "ssh") {
		t.Error("ssh method must NOT match winrm kind")
	}
}

// Test: access-denied is an AUTH/authz failure (→ credential_failed), while a
// non-auth transport error (wmi_error/unreachable/timeout) is NOT — so when the
// agent tries every applicable credential and they are all rejected it reports
// credential_failed, but a firewall/transport failure stays collection_failed.
func TestCategoryIsAuthFailure(t *testing.T) {
	auth := []string{"auth_failed", "access_denied", "wmi_access_denied"}
	for _, c := range auth {
		if !categoryIsAuthFailure(c) {
			t.Errorf("category %q must be an auth failure (→ credential_failed)", c)
		}
	}
	nonAuth := []string{"wmi_error", "unreachable", "timeout", "rpc_unreachable", "auth_ok_operation_fault", "namespace_unavailable", "error", "success"}
	for _, c := range nonAuth {
		if categoryIsAuthFailure(c) {
			t.Errorf("category %q must NOT be an auth failure", c)
		}
	}
}

// Test: classification of authenticated-but-denied vs wrong-password vs transport.
// access_denied means the credential AUTHENTICATED but the host denied access (UAC /
// DCOM / policy) → not_authorized (NOT credential_failed — it is not a wrong password).
// A clean auth_failed → credential_failed. A non-auth wmi_error → collection_failed.
func TestAccessDeniedDerivesNotAuthorized(t *testing.T) {
	id := uuid.New()
	mk := func(category string) *statusMaps {
		return &statusMaps{
			access: map[uuid.UUID]*deviceAccess{}, cred: map[uuid.UUID]credSignal{},
			test: map[uuid.UUID]*deviceTestStatus{
				id: {tested: true, authFailed: categoryIsAuthFailure(category),
					failedKinds: map[string]bool{"wmi": true}, kindCategory: map[string]string{"wmi": category}},
			},
			onlineSites: map[uuid.UUID]bool{}, anySites: map[uuid.UUID]bool{}, activeCollect: map[uuid.UUID]bool{},
		}
	}
	dev := db.Device{ID: id, OsFamily: "windows", Category: "endpoint"}
	if st, _ := mk("wmi_access_denied").deriveManagement(dev); st != MgmtNotAuthorized {
		t.Errorf("all-creds access_denied: got %s, want not_authorized (authenticated, denied — not wrong password)", st)
	}
	if st, _ := mk("auth_failed").deriveManagement(dev); st != MgmtCredentialFailed {
		t.Errorf("clean auth_failed: got %s, want credential_failed", st)
	}
	if st, _ := mk("wmi_error").deriveManagement(dev); st != MgmtCollectionFailed {
		t.Errorf("non-auth wmi_error: got %s, want collection_failed", st)
	}
}

// Class 6b — the Connectivity report (which lists failed credential attempts as
// history) must not contradict Device Detail's management state. Management is
// derived from PROVEN evidence, so a managed device that also has a failed
// credential test on record stays managed — the failure is history, not a
// downgrade. This pins that the two views share one source of truth.
func TestManagedDespiteFailedCredentialHistory(t *testing.T) {
	id := uuid.New()
	am := buildAccessMap([]db.ListDeviceAccessSignalsRow{
		{DeviceID: id, Protocol: "winrm", Source: "evidence"}, // proven by collection
	})
	m := &statusMaps{
		access: am,
		test: map[uuid.UUID]*deviceTestStatus{
			// An earlier WMI credential attempt failed — present in report history.
			id: {tested: true, authFailed: true, failedKinds: map[string]bool{"wmi": true},
				kindCategory: map[string]string{"wmi": "auth_failed"}},
		},
		onlineSites: map[uuid.UUID]bool{}, anySites: map[uuid.UUID]bool{},
	}
	if st, by := m.deriveManagement(db.Device{ID: id, OsFamily: "windows", Category: "endpoint"}); st != MgmtManaged || len(by) != 1 || by[0] != "winrm" {
		t.Errorf("managed-with-failed-history: got %s %v, want managed/[winrm]", st, by)
	}
}
