package discovery

import (
	"testing"

	"github.com/coralsearesorts/hims/internal/domain"
)

// TestPlanProtocols_ApplianceVsLinux pins the rule that an SSH-reachable network
// appliance (mgmt web port or vendor/role marker) is SNMP-probed, while a plain
// Linux server stays SSH-only — so we don't regress into either skipping SNMP on
// controllers (the Extreme .100 case) or spraying SNMP at ordinary servers.
func TestPlanProtocols_ApplianceVsLinux(t *testing.T) {
	cases := []struct {
		name       string
		ports      []int
		sshBanner  string
		httpServer string
		httpTitle  string
		wantCand   string
		wantSNMP   bool
	}{
		{
			name: "extreme controller: ssh + 8443 mgmt web", ports: []int{22, 8443},
			sshBanner: "SSH-2.0-OpenSSH_7.4", httpServer: "Apache-Coyote/1.1",
			wantCand: "appliance", wantSNMP: true,
		},
		{
			name: "ssh + 8000 mgmt web", ports: []int{22, 443, 8000},
			sshBanner: "SSH-2.0-OpenSSH_8.0", wantCand: "appliance", wantSNMP: true,
		},
		{
			name: "ssh + 443 with vendor marker", ports: []int{22, 443},
			sshBanner: "SSH-2.0-OpenSSH_8.4", httpTitle: "Aruba Wireless Controller",
			wantCand: "appliance", wantSNMP: true,
		},
		{
			name: "plain linux server: ssh only", ports: []int{22},
			sshBanner: "SSH-2.0-OpenSSH_8.9 Ubuntu", wantCand: "linux", wantSNMP: false,
		},
		{
			name: "plain linux web server: ssh + 443, no marker", ports: []int{22, 443},
			sshBanner: "SSH-2.0-OpenSSH_8.9 Ubuntu", httpServer: "nginx/1.24",
			wantCand: "linux", wantSNMP: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := planProtocols(tc.ports, tc.sshBanner, tc.httpServer, tc.httpTitle, "")
			if p.Candidate != tc.wantCand {
				t.Errorf("candidate = %q, want %q", p.Candidate, tc.wantCand)
			}
			if got := p.SNMPRelevant(); got != tc.wantSNMP {
				t.Errorf("SNMPRelevant = %v, want %v", got, tc.wantSNMP)
			}
			// SSH must always remain relevant for these SSH-reachable hosts.
			if !p.Relevant(domain.CredSSH) {
				t.Errorf("expected SSH relevant for %s", tc.name)
			}
		})
	}
}
