package onvif

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/coralsearesorts/hims/internal/driver"
	ov "github.com/coralsearesorts/hims/internal/onvif"
)

func TestFingerprint_AlwaysNoMatch(t *testing.T) {
	// The cctv driver classifies cameras; onvif is collection-only.
	if New().Fingerprint(driver.Probe{HTTPServer: "Hikvision", OpenTCPPorts: []int{80}}).Confidence != 0 {
		t.Fatal("onvif fingerprint must be NoMatch")
	}
}

type fakeDoer struct{ body string }

func (f fakeDoer) Do(req *http.Request) (*http.Response, error) {
	// device_service returns info; everything else 404 (profiles best-effort).
	if strings.Contains(req.URL.Path, "device_service") {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(f.body)), Header: make(http.Header)}, nil
	}
	return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("<f/>")), Header: make(http.Header)}, nil
}

func TestCollect_MapsCameraSnap(t *testing.T) {
	body := `<e:Envelope xmlns:e="http://www.w3.org/2003/05/soap-envelope"><e:Body>
	  <tds:GetDeviceInformationResponse xmlns:tds="http://www.onvif.org/ver10/device/wsdl">
	    <tds:Manufacturer>Dahua</tds:Manufacturer><tds:Model>IPC-HDW</tds:Model>
	    <tds:FirmwareVersion>2.8</tds:FirmwareVersion><tds:SerialNumber>SN9</tds:SerialNumber>
	  </tds:GetDeviceInformationResponse></e:Body></e:Envelope>`
	client := ov.NewClient("http://10.0.0.60", "admin", "pass", fakeDoer{body: body})
	f, err := New().Collect(&Session{Client: client, Ctx: context.Background()}, driver.Probe{})
	if err != nil {
		t.Fatal(err)
	}
	if f.Camera == nil || f.Camera.Manufacturer != "Dahua" || f.Camera.Model != "IPC-HDW" {
		t.Fatalf("camera snap not mapped: %+v", f.Camera)
	}
	if f.Vendor != "Dahua" || f.Serial != "SN9" || f.Camera.ONVIFUrl != "http://10.0.0.60" {
		t.Fatalf("identity/onvif url wrong: %+v", f)
	}
}

func TestCollect_WrongSession(t *testing.T) {
	if _, err := New().Collect(&driver.SessionBase{}, driver.Probe{}); err == nil {
		t.Fatal("expected error for non-onvif session")
	}
}
