package api

import (
	"testing"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// WMI/DCOM is Windows-only. A Linux relay agent — the obvious thing to install
// when HIMS itself runs on Linux — is a WinRM-only collector, and routing a
// legacy Windows host to it produces jobs that can never succeed.
func TestAgentSupportsProtocol(t *testing.T) {
	linux := db.RelayAgent{Os: "linux", Capabilities: []byte(`["winrm"]`)}
	windows := db.RelayAgent{Os: "windows", Capabilities: []byte(`["winrm","wmi"]`)}

	if !agentSupportsProtocol(linux, "winrm") {
		t.Error("a linux agent does WinRM and must be routable for it")
	}
	if agentSupportsProtocol(linux, "wmi") {
		t.Error("a linux agent cannot do WMI/DCOM and must NOT be routed WMI jobs")
	}
	if !agentSupportsProtocol(windows, "wmi") || !agentSupportsProtocol(windows, "winrm") {
		t.Error("a windows agent does both")
	}

	// Back-compat: an agent registered before capabilities were reported must
	// keep working rather than being refused on missing metadata.
	for _, legacy := range []db.RelayAgent{
		{Os: "windows"},
		{Os: "windows", Capabilities: []byte(``)},
		{Os: "windows", Capabilities: []byte(`not json`)},
	} {
		if !agentSupportsProtocol(legacy, "wmi") {
			t.Errorf("agent with capabilities %q must be assumed capable, not refused", string(legacy.Capabilities))
		}
	}

	// Case/whitespace tolerance — capabilities are agent-reported strings.
	if !agentSupportsProtocol(db.RelayAgent{Capabilities: []byte(`[" WinRM "]`)}, "winrm") {
		t.Error("capability matching must tolerate case and surrounding space")
	}
}

func TestAgentOSLabel(t *testing.T) {
	if got := agentOSLabel(db.RelayAgent{Os: "linux"}); got != "linux" {
		t.Errorf("got %q", got)
	}
	if got := agentOSLabel(db.RelayAgent{Os: "  "}); got != "unknown OS" {
		t.Errorf("blank OS should read 'unknown OS', got %q", got)
	}
}
