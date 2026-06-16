package api

import (
	"strings"
	"testing"
)

// unknownNextAction must NEVER return the vague "insufficient evidence — re-scan"
// dead-end: when HIMS knows the open ports and any unauthenticated banner, the
// next action must name them and the precise reason management didn't complete.
// These cases are the real stragglers found in the Discovery Reliability pass:
//   - 150.0.0.41-44/.134: Telnet-only (unsupported)
//   - 172.21.96.28/.29/.41/.60: HTTP-only with a "Ruijie Easy-Smart Switch" title
//   - 172.21.96.127: SSH+HTTP with a "Rapid Logic" banner
func TestUnknownNextAction_EvidenceBearing(t *testing.T) {
	cases := []struct {
		name              string
		ports             []int
		httpServer, title string
		ssh               string
		mustContain       []string
		mustNotContain    []string
	}{
		{
			name:           "telnet-only",
			ports:          []int{23},
			mustContain:    []string{"Telnet", "unsupported"},
			mustNotContain: []string{"insufficient", "re-scan"},
		},
		{
			name:           "http-only-with-banner",
			ports:          []int{80},
			title:          "Ruijie Easy-Smart Switch",
			mustContain:    []string{"Ruijie Easy-Smart Switch", "80", "web UI"},
			mustNotContain: []string{"insufficient", "re-scan"},
		},
		{
			name:           "http-server-banner",
			ports:          []int{22, 80},
			httpServer:     "Rapid Logic/1.1",
			title:          "Log On",
			mustContain:    []string{"Rapid Logic", "80"},
			mustNotContain: []string{"insufficient"},
		},
		{
			name:           "ssh-no-cred",
			ports:          []int{22},
			ssh:            "SSH-2.0-OpenSSH_8.0",
			mustContain:    []string{"SSH", "OpenSSH"},
			mustNotContain: []string{"insufficient"},
		},
		{
			name:           "bare-ports",
			ports:          []int{902, 5000},
			mustContain:    []string{"902", "5000", "classify"},
			mustNotContain: []string{"insufficient"},
		},
	}
	for _, c := range cases {
		got := unknownNextAction(c.ports, c.httpServer, c.title, c.ssh)
		low := strings.ToLower(got)
		for _, want := range c.mustContain {
			if !strings.Contains(got, want) {
				t.Errorf("%s: %q must contain %q", c.name, got, want)
			}
		}
		for _, no := range c.mustNotContain {
			if strings.Contains(low, strings.ToLower(no)) {
				t.Errorf("%s: %q must NOT contain %q", c.name, got, no)
			}
		}
	}
}
