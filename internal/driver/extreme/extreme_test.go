package extreme

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/coralsearesorts/hims/internal/driver"
	ec "github.com/coralsearesorts/hims/internal/extreme"
)

type fakeDoer struct{ devices string }

func (f fakeDoer) Do(req *http.Request) (*http.Response, error) {
	body := f.devices
	if strings.HasSuffix(req.URL.Path, "/login") {
		body = `{"access_token":"tok"}`
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
}

func TestFingerprint_NoMatch(t *testing.T) {
	if New().Fingerprint(driver.Probe{HTTPServer: "Extreme"}).Confidence != 0 {
		t.Fatal("extreme fingerprint must be NoMatch")
	}
}

func TestCollect_MapsAPs(t *testing.T) {
	devices := `{"data":[{"hostname":"AP1","device_function":"AP","product_type":"AP305C","ip_address":"10.0.0.5","connected":true,"active_clients":4}]}`
	client := ec.NewClient("", "u", "p", fakeDoer{devices: devices})
	f, err := New().Collect(&Session{Client: client, Ctx: context.Background()}, driver.Probe{})
	if err != nil {
		t.Fatal(err)
	}
	if f.WLAN == nil || f.WLAN.Vendor != "Extreme" || f.WLAN.APCount != 1 || f.WLAN.ClientCount != 4 {
		t.Fatalf("WLAN summary wrong: %+v", f.WLAN)
	}
	if len(f.APs) != 1 || f.APs[0].Name != "AP1" || f.APs[0].Status != "online" {
		t.Fatalf("APs wrong: %+v", f.APs)
	}
}

func TestCollect_WrongSession(t *testing.T) {
	if _, err := New().Collect(&driver.SessionBase{}, driver.Probe{}); err == nil {
		t.Fatal("expected error for non-extreme session")
	}
}
