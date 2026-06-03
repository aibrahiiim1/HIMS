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
