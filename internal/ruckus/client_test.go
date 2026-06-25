package ruckus

import "testing"

const apJSON = `{"totalCount":2,"list":[
  {"deviceName":"AP-Lobby","model":"R650","apMac":"AA:BB:CC:00:01:02","ip":"10.0.6.21","status":"Online","numClients":17},
  {"deviceName":"AP-Garage","model":"R550","apMac":"AA:BB:CC:00:01:03","ip":"10.0.6.22","status":"Offline","numClients":0}
]}`

func TestParseAPs(t *testing.T) {
	aps, err := parseAPs([]byte(apJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(aps) != 2 {
		t.Fatalf("got %d APs; want 2", len(aps))
	}
	if aps[0].Name != "AP-Lobby" || aps[0].Status != "online" || aps[0].ClientCount != 17 || aps[0].MAC != "AA:BB:CC:00:01:02" {
		t.Fatalf("AP-Lobby wrong: %+v", aps[0])
	}
	if aps[1].Status != "offline" {
		t.Fatalf("AP-Garage should be offline: %+v", aps[1])
	}
}

func TestParseAPs_Empty(t *testing.T) {
	aps, err := parseAPs([]byte(`{"totalCount":0,"list":[]}`))
	if err != nil || len(aps) != 0 {
		t.Fatalf("empty list → %v, %v", aps, err)
	}
}

// Fixture mirrors POST /query/wlan.
const wlanJSON = `{"totalCount":2,"list":[
  {"name":"Guest-Profile","ssid":"Guest","authMethod":"OPEN","encryptionType":"NONE","vlanId":30},
  {"name":"Corp-Profile","ssid":"Corp","authMethod":"8021X","encryptionType":"WPA2"}
]}`

func TestParseWLANs(t *testing.T) {
	ssids, err := parseWLANs([]byte(wlanJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(ssids) != 2 {
		t.Fatalf("got %d SSIDs; want 2", len(ssids))
	}
	if ssids[0].Name != "Guest" || ssids[0].VLAN != "30" || ssids[0].Security != "NONE" {
		t.Fatalf("Guest wrong: %+v", ssids[0])
	}
	if ssids[1].Name != "Corp" || ssids[1].Security != "WPA2" {
		t.Fatalf("Corp wrong: %+v", ssids[1])
	}
}

// Fixture mirrors POST /query/client.
const clientJSON = `{"totalCount":2,"list":[
  {"clientMac":"DE:AD:00:00:00:01","ipAddress":"10.0.6.50","hostname":"laptop","ssid":"Corp","apMac":"AA:BB:CC:00:01:02","rssi":-61,"radio":"5GHz"},
  {"clientMac":"DE:AD:00:00:00:02","ipAddress":"10.0.6.51","hostname":"phone","ssid":"Guest","apMac":"AA:BB:CC:00:01:02","rssi":-70,"radio":"2.4GHz"}
]}`

func TestParseClients(t *testing.T) {
	cl, err := parseClients([]byte(clientJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(cl) != 2 {
		t.Fatalf("got %d clients; want 2", len(cl))
	}
	if cl[0].Hostname != "laptop" || cl[0].SSID != "Corp" || cl[0].Band != "5" || cl[0].APMac != "AA:BB:CC:00:01:02" {
		t.Fatalf("client0 wrong: %+v", cl[0])
	}
	if cl[1].Band != "2.4" {
		t.Fatalf("client1 band: %+v", cl[1])
	}
}

// Fixture mirrors POST /query/ap (per-band radio fields).
const apRadioJSON = `{"totalCount":1,"list":[
  {"apMac":"AA:BB:CC:00:01:02","deviceName":"AP-Lobby",
   "channel24G":6,"numClients24G":4,"channelWidth24G":"20",
   "channel5G":44,"numClients5G":11,"channelWidth5G":"80"}
]}`

func TestParseRadios(t *testing.T) {
	radios, err := parseRadios([]byte(apRadioJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(radios) != 2 { // 2.4 + 5 (no 6G field present)
		t.Fatalf("got %d radios; want 2", len(radios))
	}
	if radios[0].Band != "2.4" || radios[0].Channel == nil || *radios[0].Channel != 6 || radios[0].ChannelWidth != "20" || radios[0].ClientCount != 4 {
		t.Fatalf("radio 2.4 wrong: %+v", radios[0])
	}
	if radios[1].Band != "5" || radios[1].ChannelWidth != "80" || radios[1].APName != "AP-Lobby" {
		t.Fatalf("radio 5 wrong: %+v", radios[1])
	}
}

// Fixture mirrors GET {apiBase}/controller.
const controllerJSON = `{"totalCount":1,"list":[
  {"model":"SZ144","serialNumber":"SZ-0001","version":"6.1.1.0.1469","hostName":"vsz-primary"}
]}`

func TestParseController(t *testing.T) {
	h, ok, err := parseController([]byte(controllerJSON))
	if err != nil || !ok {
		t.Fatalf("parseController → ok=%v err=%v", ok, err)
	}
	if h.Version != "6.1.1.0.1469" || h.Model != "SZ144" || h.Nodes != 1 {
		t.Fatalf("controller wrong: %+v", h)
	}
}

// Fixture mirrors POST /query/event.
const eventJSON = `{"totalCount":2,"list":[
  {"timestamp":1700000000000,"category":"AP","severity":"Major","description":"AP disconnected"},
  {"timestamp":1700000005000,"category":"Client","severity":"Informational","activity":"Client joined"}
]}`

func TestParseEvents(t *testing.T) {
	ev, err := parseEvents([]byte(eventJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(ev) != 2 {
		t.Fatalf("got %d events; want 2", len(ev))
	}
	if ev[0].Category != "AP" || ev[0].Severity != "Major" || ev[0].Message != "AP disconnected" || ev[0].AtMillis != 1700000000000 {
		t.Fatalf("event0 wrong: %+v", ev[0])
	}
	if ev[1].Message != "Client joined" { // falls back to activity
		t.Fatalf("event1 message: %+v", ev[1])
	}
}

func TestNewestAPIBase(t *testing.T) {
	raw := []byte(`{"apiSupportVersions":["v9_0","v9_1","v11_0","v10_0"]}`)
	if got := newestAPIBase(raw); got != "/wsg/api/public/v11_0" {
		t.Fatalf("newestAPIBase = %q; want v11_0", got)
	}
	if got := newestAPIBase([]byte(`{}`)); got != "" {
		t.Fatalf("empty apiInfo should yield empty base, got %q", got)
	}
}
