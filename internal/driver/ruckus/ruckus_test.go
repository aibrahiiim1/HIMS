package ruckus

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/coralsearesorts/hims/internal/driver"
	rc "github.com/coralsearesorts/hims/internal/ruckus"
)

type fakeDoer struct{ aps string }

func (f fakeDoer) Do(req *http.Request) (*http.Response, error) {
	if strings.HasSuffix(req.URL.Path, "/aps") {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(f.aps)), Header: make(http.Header)}, nil
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}")), Header: make(http.Header)}, nil
}

func TestFingerprint_NoMatch(t *testing.T) {
	if New().Fingerprint(driver.Probe{HTTPServer: "Ruckus"}).Confidence != 0 {
		t.Fatal("ruckus fingerprint must be NoMatch")
	}
}

func TestCollect_MapsAPs(t *testing.T) {
	aps := `{"totalCount":1,"list":[{"deviceName":"AP9","model":"R650","apMac":"MAC","ip":"10.0.0.9","status":"Online","numClients":5}]}`
	client := rc.NewClient("https://sz:8443", "", "u", "p", fakeDoer{aps: aps})
	f, err := New().Collect(&Session{Client: client, Ctx: context.Background()}, driver.Probe{})
	if err != nil {
		t.Fatal(err)
	}
	if f.WLAN == nil || f.WLAN.Vendor != "Ruckus" || f.WLAN.APCount != 1 || f.WLAN.ClientCount != 5 {
		t.Fatalf("WLAN summary wrong: %+v", f.WLAN)
	}
	if len(f.APs) != 1 || f.APs[0].Name != "AP9" || f.APs[0].Status != "online" {
		t.Fatalf("APs wrong: %+v", f.APs)
	}
}

func TestCollect_WrongSession(t *testing.T) {
	if _, err := New().Collect(&driver.SessionBase{}, driver.Probe{}); err == nil {
		t.Fatal("expected error for non-ruckus session")
	}
}
