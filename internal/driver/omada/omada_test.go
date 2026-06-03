package omada

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/coralsearesorts/hims/internal/driver"
	oc "github.com/coralsearesorts/hims/internal/omada"
)

type fakeDoer struct{ devices string }

func (f fakeDoer) Do(req *http.Request) (*http.Response, error) {
	body := f.devices
	if strings.Contains(req.URL.Path, "/devices") {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"errorCode":0,"result":{"token":"t"}}`)), Header: make(http.Header)}, nil
}

func TestFingerprint_NoMatch(t *testing.T) {
	if New().Fingerprint(driver.Probe{HTTPServer: "Omada"}).Confidence != 0 {
		t.Fatal("omada fingerprint must be NoMatch (wlan_controller classifies)")
	}
}

func TestCollect_MapsAPs(t *testing.T) {
	dev := `{"errorCode":0,"result":[{"type":"ap","name":"AP1","model":"EAP245","mac":"M","ip":"10.0.0.9","status":1,"clientNum":3}]}`
	client := oc.NewClient("https://omada:8043", "cid", "Default", "u", "p", fakeDoer{devices: dev})
	f, err := New().Collect(&Session{Client: client, Ctx: context.Background()}, driver.Probe{})
	if err != nil {
		t.Fatal(err)
	}
	if f.WLAN == nil || f.WLAN.Vendor != "TP-Link" || f.WLAN.APCount != 1 || f.WLAN.ClientCount != 3 {
		t.Fatalf("WLAN summary wrong: %+v", f.WLAN)
	}
	if len(f.APs) != 1 || f.APs[0].Name != "AP1" || f.APs[0].Status != "online" {
		t.Fatalf("APs wrong: %+v", f.APs)
	}
}

func TestCollect_WrongSession(t *testing.T) {
	if _, err := New().Collect(&driver.SessionBase{}, driver.Probe{}); err == nil {
		t.Fatal("expected error for non-omada session")
	}
}
