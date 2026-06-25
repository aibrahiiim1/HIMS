package arubacentral

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type fakeDoer struct {
	body    string
	code    int
	gotAuth string
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	f.gotAuth = req.Header.Get("Authorization")
	code := f.code
	if code == 0 {
		code = 200
	}
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(f.body)), Header: make(http.Header)}, nil
}

const apJSON = `{"aps":[
  {"name":"AP-Lobby","model":"AP-535","macaddr":"aa:bb:cc:00:01:02","ip_address":"10.0.8.21","status":"Up","serial":"CN0001"},
  {"name":"AP-Roof","model":"AP-577","macaddr":"aa:bb:cc:00:01:03","ip_address":"10.0.8.22","status":"Down","serial":"CN0002"}
]}`

func TestParseAPs(t *testing.T) {
	aps, err := parseAPs([]byte(apJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(aps) != 2 {
		t.Fatalf("got %d APs; want 2", len(aps))
	}
	if aps[0].Name != "AP-Lobby" || aps[0].Status != "online" || aps[0].Serial != "CN0001" {
		t.Fatalf("AP-Lobby wrong: %+v", aps[0])
	}
	if aps[1].Status != "offline" {
		t.Fatalf("AP-Roof offline: %+v", aps[1])
	}
}

func TestListAPs_SendsBearer(t *testing.T) {
	f := &fakeDoer{body: apJSON}
	c := NewClient("https://apigw.example.com", "tok-xyz", f)
	if _, err := c.ListAPs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.gotAuth != "Bearer tok-xyz" {
		t.Fatalf("missing/incorrect bearer header: %q", f.gotAuth)
	}
}

func TestGet_UnauthorizedSurfaced(t *testing.T) {
	c := NewClient("https://apigw.example.com", "bad", &fakeDoer{code: 401})
	if _, err := c.ListAPs(context.Background()); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("expected token-invalid error, got %v", err)
	}
}

const clientJSON = `{"clients":[
  {"macaddr":"de:ad:00:00:00:01","name":"laptop","ip_address":"10.0.8.50","network":"Corp","associated_device_name":"AP-Lobby","band":"5","signal_db":-58},
  {"macaddr":"de:ad:00:00:00:02","name":"phone","ip_address":"10.0.8.51","network":"Guest","associated_device_name":"AP-Lobby","band":"2.4"}
]}`

func TestParseClients(t *testing.T) {
	cl, err := parseClients([]byte(clientJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(cl) != 2 {
		t.Fatalf("got %d clients; want 2", len(cl))
	}
	if cl[0].Hostname != "laptop" || cl[0].SSID != "Corp" || cl[0].Band != "5" || cl[0].APName != "AP-Lobby" {
		t.Fatalf("client0 wrong: %+v", cl[0])
	}
	if cl[0].RSSI == nil || *cl[0].RSSI != -58 {
		t.Fatalf("client0 RSSI: %+v", cl[0].RSSI)
	}
	if cl[1].Band != "2.4" {
		t.Fatalf("client1 band: %+v", cl[1])
	}
}

const networkJSON = `{"networks":[
  {"essid":"Corp","security":"wpa2-enterprise","type":"employee","band":"all","enabled":true},
  {"essid":"Guest","security":"open","type":"guest","band":"2.4","enabled":false}
]}`

func TestParseNetworks(t *testing.T) {
	ssids, err := parseNetworks([]byte(networkJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(ssids) != 2 {
		t.Fatalf("got %d SSIDs; want 2", len(ssids))
	}
	if ssids[0].Name != "Corp" || ssids[0].Security != "wpa2-enterprise" || !ssids[0].Enabled {
		t.Fatalf("Corp wrong: %+v", ssids[0])
	}
	if ssids[1].Enabled {
		t.Fatalf("Guest should be disabled: %+v", ssids[1])
	}
}

func TestParseAlerts(t *testing.T) {
	raw := []byte(`{"alerts":[
	  {"timestamp":1700000000,"severity":"Critical","description":"AP down"},
	  {"timestamp":1700000005000,"severity":"Minor","description":"High CPU"}
	]}`)
	ev, err := parseAlerts(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(ev) != 2 {
		t.Fatalf("got %d events; want 2", len(ev))
	}
	if ev[0].Severity != "Critical" || ev[0].Message != "AP down" || ev[0].AtMillis != 1700000000000 { // seconds → ms
		t.Fatalf("event0 wrong: %+v", ev[0])
	}
	if ev[1].AtMillis != 1700000005000 { // already ms, unchanged
		t.Fatalf("event1 ts: %+v", ev[1])
	}
}

const apRadioJSON = `{"aps":[
  {"name":"AP-Lobby","macaddr":"aa:bb:cc:00:01:02","status":"Up","firmware_version":"10.4.0.0","radios":[
     {"band":"2.4GHz","channel":6,"tx_power":11,"channel_width":"20MHz","client_count":4},
     {"band":"5GHz","channel":44,"tx_power":15,"channel_width":"80MHz","client_count":12}
  ]}
]}`

func TestParseRadios(t *testing.T) {
	radios, err := parseRadios([]byte(apRadioJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(radios) != 2 {
		t.Fatalf("got %d radios; want 2", len(radios))
	}
	if radios[0].APName != "AP-Lobby" || radios[0].Band != "2.4" || radios[0].Channel == nil || *radios[0].Channel != 6 || radios[0].ChannelWidth != "20MHz" || radios[0].Clients != 4 {
		t.Fatalf("radio0 wrong: %+v", radios[0])
	}
	if radios[1].Band != "5" || radios[1].Power == nil || *radios[1].Power != 15 {
		t.Fatalf("radio1 wrong: %+v", radios[1])
	}
}

func TestMostCommonFirmware(t *testing.T) {
	aps := []AP{{Firmware: "10.4.0.0"}, {Firmware: "10.4.0.0"}, {Firmware: "10.3.0.0"}, {Firmware: ""}}
	if v := MostCommonFirmware(aps); v != "10.4.0.0" {
		t.Fatalf("most common = %q; want 10.4.0.0", v)
	}
}
