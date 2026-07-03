package api

import (
	"testing"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

// TestLiveFailedJobs_SupersededByNewerSuccess is a regression guard for the
// 172.21.210.26 case: a failed relay job that has since been collected successfully
// must NOT be reported as an outstanding failure. Input is newest-first.
func TestLiveFailedJobs_SupersededByNewerSuccess(t *testing.T) {
	dev := uuid.New()
	other := uuid.New()
	jobs := []db.ListRecentAgentJobsAllRow{
		{DeviceID: &dev, Target: "172.21.210.26", Status: "done"},   // newest: success for dev
		{DeviceID: &dev, Target: "172.21.210.26", Status: "failed"}, // older failure -> superseded
		{DeviceID: &dev, Target: "172.21.210.26", Status: "failed"}, // older failure -> superseded
		{DeviceID: &other, Target: "172.21.60.9", Status: "failed"}, // no later success -> live
	}
	live := liveFailedJobs(jobs)
	if len(live) != 1 {
		t.Fatalf("expected 1 live failure, got %d", len(live))
	}
	if live[0].Target != "172.21.60.9" {
		t.Errorf("wrong live failure surfaced: %s", live[0].Target)
	}
}

// A failure with NO later success stays reported; and a failure NEWER than a success
// (host regressed) is still reported.
func TestLiveFailedJobs_KeepsGenuineFailures(t *testing.T) {
	a := uuid.New()
	jobs := []db.ListRecentAgentJobsAllRow{
		{DeviceID: &a, Target: "x", Status: "failed"}, // newest: a fresh failure (host regressed)
		{DeviceID: &a, Target: "x", Status: "done"},   // older success -> does NOT excuse the newer failure
	}
	live := liveFailedJobs(jobs)
	if len(live) != 1 {
		t.Fatalf("a newer failure after an older success must still be reported; got %d", len(live))
	}
	// No-device fallback keys on target.
	jobs2 := []db.ListRecentAgentJobsAllRow{
		{DeviceID: nil, Target: "10.0.0.5", Status: "done"},
		{DeviceID: nil, Target: "10.0.0.5", Status: "failed"},
	}
	if n := len(liveFailedJobs(jobs2)); n != 0 {
		t.Errorf("target-keyed supersede failed: expected 0 live, got %d", n)
	}
}

// TestLiveFailedJobs_MixedIdentifiers is the exact 172.21.210.26 regression: a NEW success
// carries a device_id, but older stale failures for the SAME host IP recorded device_id=NULL
// (target-only). Marking both device_id AND target on success must supersede all of them.
func TestLiveFailedJobs_MixedIdentifiers(t *testing.T) {
	dev := uuid.New()
	jobs := []db.ListRecentAgentJobsAllRow{
		{DeviceID: &dev, Target: "172.21.210.26", Status: "done"},   // newest success (has device_id)
		{DeviceID: &dev, Target: "172.21.210.26", Status: "failed"}, // failure WITH device_id -> superseded
		{DeviceID: nil, Target: "172.21.210.26", Status: "failed"},  // OLD failure, device_id NULL -> must also be superseded (by target)
		{DeviceID: nil, Target: "172.21.210.26", Status: "failed"},  // ditto
	}
	if n := len(liveFailedJobs(jobs)); n != 0 {
		t.Fatalf("all .26 failures (device_id AND target-only) must be superseded by the newer success; got %d live", n)
	}
}
