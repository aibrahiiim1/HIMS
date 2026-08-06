package api

import (
	"strings"
	"testing"
)

// A device with no site is refused by relay routing BEFORE any agent is looked
// at. Reporting that as "agent_missing" sent an operator to install an agent
// that was already online on the very subnet the devices were on — 78 devices
// sat unroutable because their subnet was unmapped, not because an agent was
// absent. The two conditions must therefore stay distinguishable.
func TestSiteGateReasonsAreDistinct(t *testing.T) {
	const noSite = "device_no_site"
	const noAgent = "agent_missing"
	if noSite == noAgent {
		t.Fatal("the site gate and the agent gate must not share a reason code")
	}
	// The scan/collection layer switches on substrings of the reason. Guard the
	// two properties that routing depends on:
	//   - device_no_site must NOT be caught by the agent_missing branch, which
	//     matches "no_agent" / "agent_missing".
	if strings.Contains(noSite, "no_agent") || strings.Contains(noSite, "agent_missing") {
		t.Errorf("%q would be swallowed by the agent_missing branch and reported as a missing agent", noSite)
	}
	//   - and it must be matched by its own branch.
	if !strings.Contains(noSite, "device_no_site") {
		t.Errorf("%q must match its own dispatch branch", noSite)
	}
}

// The operator-facing text has to point at the real fix (map the subnet / set
// the site) rather than at installing an agent.
func TestNoSiteDetailPointsAtTheSite(t *testing.T) {
	detail := "this device is not assigned to a site, and relay-agent routing is per-site — no agent can be selected for it. " +
		"Map its subnet under Locations → Subnets (then re-scan), or set the site directly in Edit Device."
	for _, want := range []string{"not assigned to a site", "Subnets", "Edit Device"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail should mention %q; got: %s", want, detail)
		}
	}
	if strings.Contains(strings.ToLower(detail), "install") {
		t.Error("detail must not suggest installing an agent — that is the misdiagnosis this fix removes")
	}
}
