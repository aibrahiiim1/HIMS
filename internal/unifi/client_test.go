package unifi

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type fakeDoer struct {
	loginCode int
	devices   string
	devCode   int
}

func (f fakeDoer) Do(req *http.Request) (*http.Response, error) {
	if strings.Contains(req.URL.Path, "/api/login") {
		code := f.loginCode
		if code == 0 {
			code = 200
		}
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader("{}")), Header: make(http.Header)}, nil
	}
	code := f.devCode
	if code == 0 {
		code = 200
	}
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(f.devices)), Header: make(http.Header)}, nil
}

const deviceJSON = `{"data":[
  {"type":"uap","name":"AP-Lobby","model":"U6-Lite","mac":"aa:bb:cc:00:11:22","ip":"10.0.0.21","state":1,"num_sta":14},
  {"type":"uap","name":"AP-Pool","model":"U6-Pro","mac":"aa:bb:cc:00:11:33","ip":"10.0.0.22","state":0,"num_sta":0},
  {"type":"usw","name":"Switch-1","model":"US-24","mac":"aa:bb:cc:00:99:99","state":1}
]}`

func TestListAPs_ParsesAndFiltersToUAP(t *testing.T) {
	c := NewClient("https://unifi", "", "admin", "pw", fakeDoer{devices: deviceJSON})
	if err := c.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	aps, err := c.ListAPs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(aps) != 2 { // the usw switch is filtered out
		t.Fatalf("got %d APs; want 2 (uap only)", len(aps))
	}
	if aps[0].Name != "AP-Lobby" || aps[0].Status != "online" || aps[0].ClientCount != 14 || aps[0].Model != "U6-Lite" {
		t.Fatalf("AP-Lobby wrong: %+v", aps[0])
	}
	if aps[1].Status != "offline" { // state 0
		t.Fatalf("AP-Pool should be offline: %+v", aps[1])
	}
}

func TestLogin_Non2xxFails(t *testing.T) {
	c := NewClient("https://unifi", "", "admin", "bad", fakeDoer{loginCode: 401})
	if err := c.Login(context.Background()); err == nil {
		t.Fatal("expected login failure on 401")
	}
}

func TestListAPs_DeviceErrorFails(t *testing.T) {
	c := NewClient("https://unifi", "", "admin", "pw", fakeDoer{devCode: 500})
	if _, err := c.ListAPs(context.Background()); err == nil {
		t.Fatal("expected error on 500")
	}
}

// Fixture mirrors GET /api/s/<site>/rest/wlanconf (passphrase intentionally
// present in the source to prove it is NEVER surfaced).
const wlanConfJSON = `{"data":[
  {"name":"Guest","enabled":true,"security":"open","wlan_band":"2g","vlan_enabled":true,"vlan":30,"x_passphrase":"SECRET"},
  {"name":"Corp","enabled":true,"security":"wpapsk","wpa_mode":"wpa2","wlan_band":"both","x_passphrase":"SECRET2"},
  {"name":"Legacy","enabled":false,"security":"wpaeap","wlan_band":"5g"}
]}`

func TestParseWLANConf(t *testing.T) {
	ssids, err := parseWLANConf([]byte(wlanConfJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(ssids) != 3 {
		t.Fatalf("got %d SSIDs; want 3", len(ssids))
	}
	if ssids[0].Name != "Guest" || ssids[0].Band != "2.4" || ssids[0].VLAN != "30" || !ssids[0].Enabled || ssids[0].Security != "open" {
		t.Fatalf("Guest wrong: %+v", ssids[0])
	}
	if ssids[1].Band != "dual" { // wlan_band "both"
		t.Fatalf("Corp band: %+v", ssids[1])
	}
	if ssids[2].Enabled { // disabled
		t.Fatalf("Legacy should be disabled: %+v", ssids[2])
	}
	// The passphrase must never appear anywhere in the parsed output.
	for _, s := range ssids {
		if strings.Contains(s.Name+s.Security+s.VLAN+s.Band, "SECRET") {
			t.Fatalf("passphrase leaked into SSID: %+v", s)
		}
	}
}

// Fixture mirrors GET /api/s/<site>/stat/sta.
const staJSON = `{"data":[
  {"mac":"de:ad:be:ef:00:01","ip":"10.0.0.50","hostname":"laptop-1","essid":"Corp","ap_mac":"aa:bb:cc:00:11:22","signal":-55,"noise":-95,"rx_bytes":1024,"tx_bytes":2048,"radio":"na"},
  {"mac":"de:ad:be:ef:00:02","ip":"10.0.0.51","name":"phone","essid":"Guest","ap_mac":"aa:bb:cc:00:11:22","rssi":40,"radio":"ng"}
]}`

func TestParseStations(t *testing.T) {
	cl, err := parseStations([]byte(staJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(cl) != 2 {
		t.Fatalf("got %d clients; want 2", len(cl))
	}
	if cl[0].Hostname != "laptop-1" || cl[0].SSID != "Corp" || cl[0].Band != "5" {
		t.Fatalf("client0 wrong: %+v", cl[0])
	}
	if cl[0].SNR == nil || *cl[0].SNR != 40 { // signal(-55) - noise(-95) = 40
		t.Fatalf("client0 SNR: %+v", cl[0].SNR)
	}
	if cl[1].Hostname != "phone" || cl[1].Band != "2.4" { // falls back to name
		t.Fatalf("client1 wrong: %+v", cl[1])
	}
}

// Fixture mirrors radio_table_stats inside GET /api/s/<site>/stat/device.
const radioJSON = `{"data":[
  {"type":"uap","name":"AP-Lobby","mac":"aa:bb:cc:00:11:22","radio_table_stats":[
     {"name":"wifi0","radio":"ng","channel":6,"tx_power":20,"num_sta":5},
     {"name":"wifi1","radio":"na","channel":36,"tx_power":23,"num_sta":9}
  ]},
  {"type":"usw","name":"Switch-1","mac":"aa:bb:cc:00:99:99"}
]}`

func TestParseRadios(t *testing.T) {
	radios, err := parseRadios([]byte(radioJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(radios) != 2 { // switch contributes none
		t.Fatalf("got %d radios; want 2", len(radios))
	}
	if radios[0].Band != "2.4" || radios[0].Channel == nil || *radios[0].Channel != 6 || radios[0].ClientCount != 5 {
		t.Fatalf("radio0 wrong: %+v", radios[0])
	}
	if radios[1].Band != "5" || radios[1].PowerDbm == nil || *radios[1].PowerDbm != 23 {
		t.Fatalf("radio1 wrong: %+v", radios[1])
	}
}

func TestParseSysinfo(t *testing.T) {
	if v := parseSysinfo([]byte(`{"data":[{"version":"8.0.28"}]}`)); v != "8.0.28" {
		t.Fatalf("version = %q; want 8.0.28", v)
	}
	if v := parseSysinfo([]byte(`{"data":[]}`)); v != "" {
		t.Fatalf("empty sysinfo should yield empty version, got %q", v)
	}
}

func TestParseHealth(t *testing.T) {
	raw := []byte(`{"data":[{"subsystem":"wlan","status":"ok","num_ap":12,"num_user":140},{"subsystem":"www","status":"ok"}]}`)
	hs := parseHealth(raw)
	if len(hs) != 2 || hs[0].Name != "wlan" || hs[0].Status != "ok" || hs[0].NumAP != 12 {
		t.Fatalf("health wrong: %+v", hs)
	}
}

func TestParseEvents(t *testing.T) {
	raw := []byte(`{"data":[{"time":1700000000000,"key":"EVT_AP_Lost_Contact","msg":"AP[Lobby] lost contact","subsystem":"wlan"}]}`)
	ev := parseEvents(raw)
	if len(ev) != 1 || ev[0].Key != "EVT_AP_Lost_Contact" || ev[0].AtMillis != 1700000000000 || ev[0].Subsystem != "wlan" {
		t.Fatalf("event wrong: %+v", ev)
	}
}
