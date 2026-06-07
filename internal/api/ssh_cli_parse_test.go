package api

import "testing"

// Real captured `show ap` output from a VE6120 / ExtremeCloud IQ Controller.
const sampleShowAP = `serial 03052207160281 03052207160281 AP305C-1-EG
serial 03052207160285 03052207160285 AP305C-1-EG
serial 03052208104498 03052208104498 AP410C-2-LOBBY
`

func TestParseCLIAPRows(t *testing.T) {
	aps := parseCLIAPRows(sampleShowAP)
	if len(aps) != 3 {
		t.Fatalf("aps = %d, want 3", len(aps))
	}
	if aps[0].Serial != "03052207160281" {
		t.Errorf("serial = %q, want 03052207160281", aps[0].Serial)
	}
	if aps[0].Name != "AP305C-1-EG" {
		t.Errorf("name = %q, want AP305C-1-EG", aps[0].Name)
	}
	if aps[0].Model != "AP305C" {
		t.Errorf("model = %q, want AP305C", aps[0].Model)
	}
	if aps[2].Model != "AP410C" {
		t.Errorf("ap[2] model = %q, want AP410C", aps[2].Model)
	}
}

// Real captured `show clients` column table (trimmed to a few columns/rows).
const sampleShowClients = `AP Serial: 03052207160281

Client IP                                Client MAC         Protocol       Radio  BSS MAC            SSID                User            RSS(dBm)
172.21.89.178,fe80::24ad:f7ff:fe9f:c100  26:AD:F7:9F:C1:00  5.0ac|L|S|T|M  2      78:7D:53:49:4D:60  CoralSea Aqua WiFi  Alisa-Tab-A9    -66
172.21.89.15                             16:42:36:D2:F8:80  2.4|L|T        1      78:7D:53:49:4D:61  CoralSea Staff      ReceptionPhone  -71
`

func TestParseCLIClientRows(t *testing.T) {
	clients, ssids := parseCLIClientRows(sampleShowClients)
	if len(clients) != 2 {
		t.Fatalf("clients = %d, want 2", len(clients))
	}
	c := clients[0]
	if c.MAC != "26:AD:F7:9F:C1:00" {
		t.Errorf("mac = %q", c.MAC)
	}
	if c.IP != "172.21.89.178" {
		t.Errorf("ip = %q, want 172.21.89.178 (IPv4 from comma list)", c.IP)
	}
	if c.SSID != "CoralSea Aqua WiFi" {
		t.Errorf("ssid = %q, want 'CoralSea Aqua WiFi' (spaces preserved)", c.SSID)
	}
	if c.Hostname != "Alisa-Tab-A9" {
		t.Errorf("hostname = %q, want Alisa-Tab-A9", c.Hostname)
	}
	if c.Band != "5GHz" {
		t.Errorf("band = %q, want 5GHz", c.Band)
	}
	if c.AP != "03052207160281" {
		t.Errorf("ap = %q, want 03052207160281 (from AP Serial header)", c.AP)
	}
	if c.RSSI == nil || *c.RSSI != -66 {
		t.Errorf("rssi = %v, want -66", c.RSSI)
	}
	// Distinct SSIDs derived from the client table (show wlan/ssid are rejected).
	if len(ssids) != 2 {
		t.Fatalf("derived ssids = %d (%v), want 2", len(ssids), ssids)
	}
}

func TestClassifyUnsupportedMarkers(t *testing.T) {
	for _, s := range []string{"Invalid command", "% Invalid input", "Ambiguous command", "command not found"} {
		if !reUnsupported.MatchString(s) {
			t.Errorf("expected %q to be classified unsupported", s)
		}
	}
	if reUnsupported.MatchString("serial 0305 AP305C") {
		t.Errorf("valid AP output wrongly flagged unsupported")
	}
}

func TestRedactSecrets(t *testing.T) {
	in := "username admin\npassword Sup3rSecret!\ncommunity public"
	out := redactSecrets(in, 1000)
	if containsCI(out, "Sup3rSecret") || containsCI(out, "public") {
		t.Fatalf("secret not redacted: %q", out)
	}
	if !containsCI(out, "admin") {
		t.Fatalf("non-secret username should be retained: %q", out)
	}
}

func containsCI(s, sub string) bool {
	return len(s) >= len(sub) && (indexCI(s, sub) >= 0)
}
func indexCI(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if equalCI(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}
func equalCI(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 32
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 32
		}
		if ca != cb {
			return false
		}
	}
	return true
}
