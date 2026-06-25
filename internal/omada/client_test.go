package omada

import "testing"

const deviceJSON = `{"errorCode":0,"result":[
  {"type":"ap","name":"AP-Bar","model":"EAP245","mac":"AA-BB-CC-00-01-02","ip":"10.0.5.21","status":1,"clientNum":9},
  {"type":"ap","name":"AP-Spa","model":"EAP660","mac":"AA-BB-CC-00-01-03","ip":"10.0.5.22","status":0,"clientNum":0},
  {"type":"switch","name":"SW-Core","model":"TL-SG3428","mac":"AA-BB-CC-00-09-09","status":1}
]}`

func TestParseDevices_FiltersAPsAndStatus(t *testing.T) {
	aps, err := parseDevices([]byte(deviceJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(aps) != 2 { // switch excluded
		t.Fatalf("got %d APs; want 2", len(aps))
	}
	if aps[0].Name != "AP-Bar" || aps[0].Status != "online" || aps[0].ClientCount != 9 || aps[0].Model != "EAP245" {
		t.Fatalf("AP-Bar wrong: %+v", aps[0])
	}
	if aps[1].Status != "offline" { // status 0
		t.Fatalf("AP-Spa should be offline: %+v", aps[1])
	}
}

func TestParseDevices_ErrorCode(t *testing.T) {
	if _, err := parseDevices([]byte(`{"errorCode":-1442,"result":[]}`)); err == nil {
		t.Fatal("non-zero errorCode should fail")
	}
}

// Fixture mirrors GET /{cid}/api/v2/sites/{site}/setting/wlans/ssids.
const ssidJSON = `{"errorCode":0,"result":{"data":[
  {"name":"Guest","wlanId":"w1","security":0,"band":1,"vlanEnable":true,"vlanId":30},
  {"name":"Staff","wlanId":"w2","security":2,"band":3},
  {"name":"IoT","wlanId":"w3","security":3,"band":7}
]}}`

func TestParseSSIDs(t *testing.T) {
	ssids, err := parseSSIDs([]byte(ssidJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(ssids) != 3 {
		t.Fatalf("got %d SSIDs; want 3", len(ssids))
	}
	if ssids[0].Name != "Guest" || ssids[0].Security != "open" || ssids[0].Band != "2.4" || ssids[0].VLAN != "30" {
		t.Fatalf("Guest wrong: %+v", ssids[0])
	}
	if ssids[1].Security != "wpa-psk" || ssids[1].Band != "dual" { // band mask 3 = 2.4+5
		t.Fatalf("Staff wrong: %+v", ssids[1])
	}
	if ssids[2].Security != "wpa-enterprise" || ssids[2].Band != "dual" { // mask 7 = 2.4+5+6
		t.Fatalf("IoT wrong: %+v", ssids[2])
	}
}

// Fixture mirrors GET /{cid}/api/v2/sites/{site}/clients (wired client excluded).
const clientJSON = `{"errorCode":0,"result":{"data":[
  {"mac":"DE-AD-00-00-00-01","ip":"10.0.5.50","hostName":"laptop","ssid":"Staff","apName":"AP-Bar","wireless":true,"rssi":-58,"trafficDown":1000,"trafficUp":500,"radioId":1},
  {"mac":"DE-AD-00-00-00-02","ip":"10.0.5.60","name":"wired-pc","wireless":false}
]}}`

func TestParseClients(t *testing.T) {
	cl, err := parseClients([]byte(clientJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(cl) != 1 { // wired excluded
		t.Fatalf("got %d clients; want 1 (wireless only)", len(cl))
	}
	if cl[0].Hostname != "laptop" || cl[0].SSID != "Staff" || cl[0].APName != "AP-Bar" || cl[0].Band != "5" {
		t.Fatalf("client wrong: %+v", cl[0])
	}
	if cl[0].RSSI == nil || *cl[0].RSSI != -58 {
		t.Fatalf("client RSSI: %+v", cl[0].RSSI)
	}
}

// Fixture mirrors GET /{cid}/api/v2/sites/{site}/eaps/{mac} radioList.
const radioDetailJSON = `{"errorCode":0,"result":{"name":"AP-Bar","radioList":[
  {"radioId":0,"channel":1,"txPower":20,"clientNum":3,"bandWidth":"20"},
  {"radioId":1,"channel":36,"txPower":23,"clientNum":7,"bandWidth":"80"}
]}}`

func TestParseRadioDetail(t *testing.T) {
	radios := parseRadioDetail([]byte(radioDetailJSON), "AP-Bar")
	if len(radios) != 2 {
		t.Fatalf("got %d radios; want 2", len(radios))
	}
	if radios[0].APName != "AP-Bar" || radios[0].Band != "2.4" || radios[0].Channel == nil || *radios[0].Channel != 1 || radios[0].ChannelWidth != "20" || radios[0].Clients != 3 {
		t.Fatalf("radio0 wrong: %+v", radios[0])
	}
	if radios[1].Band != "5" || radios[1].Power == nil || *radios[1].Power != 23 {
		t.Fatalf("radio1 wrong: %+v", radios[1])
	}
}

func TestParseInfo(t *testing.T) {
	if v := parseInfo([]byte(`{"errorCode":0,"result":{"controllerVer":"5.9.31","omadacId":"abc"}}`)); v != "5.9.31" {
		t.Fatalf("version = %q; want 5.9.31", v)
	}
	if v := parseInfo([]byte(`{"errorCode":-1,"result":{}}`)); v != "" {
		t.Fatalf("error response should yield empty version, got %q", v)
	}
}

// Fixture mirrors GET /{cid}/api/v2/sites/{site}/alerts.
const alertJSON = `{"errorCode":0,"result":{"data":[
  {"time":1700000000000,"level":"error","content":"AP-Bar disconnected"},
  {"time":1700000005000,"level":"warning","msg":"High channel utilization"}
]}}`

func TestParseAlerts(t *testing.T) {
	ev, err := parseAlerts([]byte(alertJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(ev) != 2 {
		t.Fatalf("got %d events; want 2", len(ev))
	}
	if ev[0].Level != "error" || ev[0].Message != "AP-Bar disconnected" || ev[0].AtMillis != 1700000000000 {
		t.Fatalf("event0 wrong: %+v", ev[0])
	}
	if ev[1].Message != "High channel utilization" { // falls back to msg
		t.Fatalf("event1 message: %+v", ev[1])
	}
}
