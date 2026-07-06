package api

import (
	"strings"
	"testing"

	"github.com/coralsearesorts/hims/internal/isapi"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

// TestChannelAlertAction locks the transition-only contract: alert on
// online->offline (enabled), resolve on recovery, and stay silent for the
// pre-existing offline fleet, unknown status, and disabled channels.
func TestChannelAlertAction(t *testing.T) {
	cases := []struct {
		status, prior string
		enabled       bool
		want          string
	}{
		{"offline", "online", true, "open"},    // the transition we alert on
		{"offline", "offline", true, "none"},   // pre-existing offline -> no flood
		{"offline", "", true, "none"},          // first sight already offline -> no page
		{"offline", "online", false, "none"},   // disabled channel is not a fault
		{"online", "offline", true, "resolve"}, // recovery
		{"online", "online", true, "resolve"},  // steady online clears stale alerts
		{"unknown", "online", true, "none"},    // indeterminate -> never open/resolve
		{"unknown", "offline", true, "none"},
	}
	for _, c := range cases {
		if got := channelAlertAction(c.status, c.prior, c.enabled); got != c.want {
			t.Errorf("channelAlertAction(%q,%q,enabled=%v)=%q want %q", c.status, c.prior, c.enabled, got, c.want)
		}
	}
}

func TestChannelStatusAndMessage(t *testing.T) {
	on, off := true, false
	if channelStatus(isapi.Channel{Online: &on}) != "online" || channelStatus(isapi.Channel{Online: &off}) != "offline" || channelStatus(isapi.Channel{}) != "unknown" {
		t.Fatal("channelStatus mapping wrong")
	}
	nvr := db.Device{ID: uuid.New(), Name: "NVR-B1"}
	msg := channelOfflineMessage(nvr, isapi.Channel{No: 1, Name: "B1-1101", IP: "172.21.210.48", Online: &off, DetectResult: "netUnreachable"})
	for _, sub := range []string{"B1-1101", "172.21.210.48", "NVR-B1", "network unreachable"} {
		if !strings.Contains(msg, sub) {
			t.Fatalf("offline message %q missing %q", msg, sub)
		}
	}
}
