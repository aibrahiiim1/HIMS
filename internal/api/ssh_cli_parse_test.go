package api

import "testing"

// Real captured `show ap` output from a VE6120 / ExtremeCloud IQ Controller.
const sampleShowAP = `serial 03052207160281 03052207160281 AP305C-1-EG
serial 03052207160285 03052207160285 AP305C-1-EG
serial 03052208104498 03052208104498 AP410C-2-LOBBY
`

func TestParseCLIAPRows(t *testing.T) {
	aps, _ := parseCLIAPRows(sampleShowAP)
	if len(aps) != 3 {
		t.Fatalf("aps = %d, want 3", len(aps))
	}
	if aps[0].Serial != "03052207160281" {
		t.Errorf("serial = %q, want 03052207160281", aps[0].Serial)
	}
	// AP is named by its (unique) serial; the trailing token is the model.
	if aps[0].Name != "03052207160281" {
		t.Errorf("name = %q, want serial 03052207160281 (model token is not unique)", aps[0].Name)
	}
	if aps[0].Model != "AP305C-1-EG" {
		t.Errorf("model = %q, want AP305C-1-EG", aps[0].Model)
	}
	if aps[2].Model != "AP410C-2-LOBBY" {
		t.Errorf("ap[2] model = %q, want AP410C-2-LOBBY", aps[2].Model)
	}
}

// Real captured `show clients` column table (trimmed to a few columns/rows).
const sampleShowClients = `AP Serial: 03052207160281

Client IP                                Client MAC         Protocol       Radio  BSS MAC            SSID                User            RSS(dBm)
172.21.89.178,fe80::24ad:f7ff:fe9f:c100  26:AD:F7:9F:C1:00  5.0ac|L|S|T|M  2      78:7D:53:49:4D:60  CoralSea Aqua WiFi  Alisa-Tab-A9    -66
172.21.89.15                             16:42:36:D2:F8:80  2.4|L|T        1      78:7D:53:49:4D:61  CoralSea Staff      ReceptionPhone  -71
`

func TestParseCLIClientRows(t *testing.T) {
	clients, ssids, _ := parseCLIClientRows(sampleShowClients)
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
	// The XCC "User" column maps to Username; persist falls back hostname←username.
	if c.Username != "Alisa-Tab-A9" {
		t.Errorf("username = %q, want Alisa-Tab-A9 (from User column)", c.Username)
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

// Two AP groups in one client dump — proves per-AP client counts are grouped by
// the `AP Serial:` header, not lumped together.
const sampleShowClientsTwoAPs = `AP Serial: 03052207160281

Client IP      Client MAC         Protocol  Radio  BSS MAC            SSID                User
172.21.89.10   26:AD:F7:9F:C1:00  5.0ac     2      78:7D:53:49:4D:60  CoralSea Aqua WiFi  Tab-A
172.21.89.11   16:42:36:D2:F8:80  2.4       1      78:7D:53:49:4D:61  CoralSea Staff      Phone-1

AP Serial: 03052207160285

Client IP      Client MAC         Protocol  Radio  BSS MAC            SSID                User
172.21.89.20   26:AD:F7:9F:C2:00  5.0ac     2      78:7D:53:49:4E:60  CoralSea Aqua WiFi  Tab-B
`

func TestClientCountsByAP(t *testing.T) {
	clients, _, _ := parseCLIClientRows(sampleShowClientsTwoAPs)
	if len(clients) != 3 {
		t.Fatalf("clients = %d, want 3", len(clients))
	}
	counts := clientCountsByAP(clients)
	if counts["03052207160281"] != 2 {
		t.Errorf("AP 03052207160281 client count = %d, want 2", counts["03052207160281"])
	}
	if counts["03052207160285"] != 1 {
		t.Errorf("AP 03052207160285 client count = %d, want 1", counts["03052207160285"])
	}
	if len(counts) != 2 {
		t.Errorf("distinct APs with clients = %d, want 2 (%v)", len(counts), counts)
	}
}

// Real captured `show wlans` from the VE6120 — the full WLAN list (4 SSIDs incl.
// the disabled "test" WLAN), which `show clients` can't see (only 2 have clients).
const sampleShowWlans = `Name                Service Type  Enabled   SSID                Privacy  Auth Mode  Radio Mode

CoralSea Aqua WiFi  std           enabled   CoralSea Aqua WiFi  none     disabled
Admin-IT            std           enabled   IT                  wpa-psk  ****
chr                 std           enabled   chr                 wpa-psk  ****
test                std           disabled  test                none     disabled
`

func TestParseWLANRows(t *testing.T) {
	wl, _ := parseWLANRows(sampleShowWlans)
	if len(wl) != 4 {
		t.Fatalf("wlans = %d, want 4 (%v)", len(wl), wl)
	}
	bySSID := map[string]wlanInfo{}
	for _, w := range wl {
		bySSID[w.SSID] = w
	}
	for _, ssid := range []string{"CoralSea Aqua WiFi", "IT", "chr", "test"} {
		if _, ok := bySSID[ssid]; !ok {
			t.Errorf("missing SSID %q in %v", ssid, bySSID)
		}
	}
	if bySSID["test"].Status != "inactive" {
		t.Errorf("test WLAN status = %q, want inactive (disabled)", bySSID["test"].Status)
	}
	if bySSID["CoralSea Aqua WiFi"].Status != "active" {
		t.Errorf("CoralSea WLAN status = %q, want active", bySSID["CoralSea Aqua WiFi"].Status)
	}
	if bySSID["IT"].Security != "wpa-psk" {
		t.Errorf("IT WLAN security = %q, want wpa-psk", bySSID["IT"].Security)
	}
}

func TestParseActiveAPSerials(t *testing.T) {
	in := `Active Wireless APs
Serial          Name        Status
03052207160281  AP305C-1    active
03052207160285  AP305C-2    active
`
	got := parseActiveAPSerials(in)
	if len(got) != 2 {
		t.Fatalf("active serials = %d (%v), want 2", len(got), got)
	}
	if got[0] != "03052207160281" || got[1] != "03052207160285" {
		t.Errorf("serials = %v, want [03052207160281 03052207160285]", got)
	}
}

func TestParseKindCoversInvestigationCandidates(t *testing.T) {
	// The confirmed candidate commands must route to a parser so their output is
	// interpreted (not silently dropped) when the controller supports them.
	cases := map[string]string{
		"show wlans":                      "wlans",
		"show report active_wireless_aps": "active_aps",
		"show ap":                         "aps",
		"show clients":                    "clients",
	}
	for cmd, want := range cases {
		if got := cliParseKind(cmd); got != want {
			t.Errorf("cliParseKind(%q) = %q, want %q", cmd, got, want)
		}
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
