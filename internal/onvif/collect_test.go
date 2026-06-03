package onvif

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"
)

type fakeDoer struct{ routes map[string]string }

func (f fakeDoer) Do(req *http.Request) (*http.Response, error) {
	body, ok := f.routes[req.URL.Path]
	code := http.StatusOK
	if !ok {
		body, code = `<fault/>`, http.StatusNotFound
	}
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
}

const deviceInfoXML = `<?xml version="1.0"?>
<env:Envelope xmlns:env="http://www.w3.org/2003/05/soap-envelope">
 <env:Body>
  <tds:GetDeviceInformationResponse xmlns:tds="http://www.onvif.org/ver10/device/wsdl">
   <tds:Manufacturer>HIKVISION</tds:Manufacturer>
   <tds:Model>DS-2CD2143G0-I</tds:Model>
   <tds:FirmwareVersion>V5.6.3</tds:FirmwareVersion>
   <tds:SerialNumber>DS-2CD20231234</tds:SerialNumber>
   <tds:HardwareId>88</tds:HardwareId>
  </tds:GetDeviceInformationResponse>
 </env:Body>
</env:Envelope>`

const profilesXML = `<?xml version="1.0"?>
<env:Envelope xmlns:env="http://www.w3.org/2003/05/soap-envelope">
 <env:Body>
  <trt:GetProfilesResponse xmlns:trt="http://www.onvif.org/ver10/media/wsdl">
   <trt:Profiles token="Profile_1">
    <Name>mainStream</Name>
    <VideoEncoderConfiguration>
     <Encoding>H264</Encoding>
     <Resolution><Width>2688</Width><Height>1520</Height></Resolution>
    </VideoEncoderConfiguration>
   </trt:Profiles>
   <trt:Profiles token="Profile_2"><Name>subStream</Name></trt:Profiles>
  </trt:GetProfilesResponse>
 </env:Body>
</env:Envelope>`

func TestCollect_DeviceInfoAndProfiles(t *testing.T) {
	c := NewClient("http://10.0.0.60", "admin", "pass", fakeDoer{routes: map[string]string{
		"/onvif/device_service": deviceInfoXML,
		"/onvif/Media":          profilesXML,
	}})
	info, err := Collect(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if info.Manufacturer != "HIKVISION" || info.Model != "DS-2CD2143G0-I" || info.Serial != "DS-2CD20231234" {
		t.Fatalf("device info wrong: %+v", info)
	}
	if len(info.Profiles) != 2 || info.Profiles[0].Encoding != "H264" || info.Profiles[0].Width != 2688 {
		t.Fatalf("profiles wrong: %+v", info.Profiles)
	}
	if info.Resolution() != "2688x1520" {
		t.Fatalf("resolution = %q; want 2688x1520", info.Resolution())
	}
}

func TestCollect_ProfilesBestEffort(t *testing.T) {
	// Media service unavailable (404) → device info still returns, no profiles.
	c := NewClient("http://10.0.0.60", "admin", "pass", fakeDoer{routes: map[string]string{
		"/onvif/device_service": deviceInfoXML,
	}})
	info, err := Collect(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if info.Manufacturer != "HIKVISION" || len(info.Profiles) != 0 {
		t.Fatalf("expected device info + no profiles: %+v", info)
	}
}

func TestCollect_DeviceServiceErrorFails(t *testing.T) {
	c := NewClient("http://10.0.0.60", "admin", "pass", fakeDoer{routes: map[string]string{}})
	if _, err := Collect(context.Background(), c); err == nil {
		t.Fatal("device-service error should fail the collect")
	}
}

func TestPasswordDigest_KnownVector(t *testing.T) {
	// Deterministic digest from fixed inputs (the WS-Security formula).
	nonce, _ := base64.StdEncoding.DecodeString("LKqI6G/AikKCQrN0zqZFlg==")
	got := passwordDigest(nonce, "2010-09-16T07:50:45Z", "userpassword")
	if got != "tuOSpGlFlIXsozq4HFNeeGeFLEI=" {
		t.Fatalf("digest = %q; want the known WS-Security test vector", got)
	}
}
