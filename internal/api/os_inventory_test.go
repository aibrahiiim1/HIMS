package api

import "testing"

func TestCategorizeCollectErr(t *testing.T) {
	cases := []struct {
		method, errStr, wantReason string
	}{
		{"winrm", "http error: 401 unauthorized", "auth_failed"},
		{"ssh", "ssh: unable to authenticate, attempted methods [none password]", "auth_failed"},
		{"winrm", "dial tcp 10.0.0.5:5985: connectex: No connection could be made (actively refused)", "winrm_disabled"},
		// A closed WinRM port surfaces from the client as an AUTH error wrapping a
		// connection refusal ("failed to authenticate ... refused"). The connection
		// cause must win over the "authenticate" substring — else it mislabels as
		// auth_failed and (pre-fix) blocked the site-agent fallback for the host.
		{"winrm", "failed to authenticate: Post \"http://10.0.0.5:5985/wsman\": dial tcp 10.0.0.5:5985: connectex: No connection could be made because the target machine actively refused it", "winrm_disabled"},
		{"ssh", "dial tcp 10.0.0.5:22: connect: connection refused", "ssh_unreachable"},
		{"winrm", "context deadline exceeded (Client.Timeout)", "winrm_timeout"},
		{"ssh", "dial 10.0.0.5:22: i/o timeout", "ssh_timeout"},
		{"ssh", "ssh: handshake failed: no common algorithm for key exchange", "handshake_failed"},
		{"ssh", "dial tcp: lookup host: no such host", "unreachable"},
		// vSphere/ESXi govmomi SOAP login rejection — names neither "authentication" nor
		// "401", so it MUST be matched explicitly or it falls through to collection_error
		// (the 150.0.0.0/24 ESXi mis-diagnosis). Must classify as a credential problem.
		{"vsphere", "ServerFaultCode: Cannot complete login due to an incorrect user name or password.", "auth_failed"},
		{"vmware", "ServerFaultCode: Login failure", "auth_failed"},
		{"winrm", "some unexpected protocol fault", "collection_error"},
	}
	for _, c := range cases {
		got, detail := categorizeCollectErr(c.method, c.errStr)
		if got != c.wantReason {
			t.Errorf("categorizeCollectErr(%s, %q) reason = %q, want %q", c.method, c.errStr, got, c.wantReason)
		}
		if detail == "" {
			t.Errorf("categorizeCollectErr(%s, %q) gave empty detail", c.method, c.errStr)
		}
	}
}

func TestReasonHTTP(t *testing.T) {
	for _, r := range []string{"unsupported_os", "no_credential", "decrypt_failed", "no_ip", "encryption_unavailable"} {
		if reasonHTTP(r) != 400 {
			t.Errorf("%s should map to 400", r)
		}
	}
	for _, r := range []string{"auth_failed", "winrm_disabled", "ssh_timeout", "collection_error"} {
		if reasonHTTP(r) != 502 {
			t.Errorf("%s should map to 502", r)
		}
	}
}
