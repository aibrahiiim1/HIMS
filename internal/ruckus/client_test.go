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
