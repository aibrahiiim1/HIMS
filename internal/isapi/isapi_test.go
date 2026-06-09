package isapi

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

const nvrXML = `<?xml version="1.0" encoding="UTF-8"?>
<DeviceInfo xmlns="http://www.hikvision.com/ver20/XMLSchema" version="2.0">
<deviceName>Embedded Net DVR</deviceName>
<deviceID>x</deviceID>
<deviceDescription>NVR</deviceDescription>
<model>DS-7608NI-K2/8P</model>
<serialNumber>DS-7608NI1620200101AAWR123456789</serialNumber>
<macAddress>f0:b0:52:08:0c:70</macAddress>
<firmwareVersion>V4.30.005</firmwareVersion>
<deviceType>NVR</deviceType>
</DeviceInfo>`

const cameraXML = `<?xml version="1.0" encoding="UTF-8"?>
<DeviceInfo xmlns="http://www.hikvision.com/ver20/XMLSchema" version="2.0">
<deviceName>IP CAMERA</deviceName>
<model>DS-2CD2143G0-I</model>
<serialNumber>DS-2CD000</serialNumber>
<firmwareVersion>V5.6.3</firmwareVersion>
<deviceType>IPCamera</deviceType>
</DeviceInfo>`

func TestParseDeviceInfo_NVR(t *testing.T) {
	info, err := parseDeviceInfo([]byte(nvrXML))
	if err != nil {
		t.Fatal(err)
	}
	if info.DeviceType != "NVR" {
		t.Errorf("DeviceType = %q, want NVR", info.DeviceType)
	}
	if info.Model != "DS-7608NI-K2/8P" {
		t.Errorf("Model = %q", info.Model)
	}
	if info.Firmware != "V4.30.005" || info.Serial == "" || info.Manufacturer != "Hikvision" {
		t.Errorf("unexpected info %+v", info)
	}
}

func TestParseDeviceInfo_Camera(t *testing.T) {
	info, err := parseDeviceInfo([]byte(cameraXML))
	if err != nil {
		t.Fatal(err)
	}
	if info.DeviceType != "IPCamera" {
		t.Errorf("DeviceType = %q, want IPCamera", info.DeviceType)
	}
	if info.Model != "DS-2CD2143G0-I" {
		t.Errorf("Model = %q", info.Model)
	}
}

func TestParseDeviceInfo_DescriptionFallback(t *testing.T) {
	// Older firmware with no <deviceType> — fall back to <deviceDescription>.
	xml := `<DeviceInfo><model>DS-7716NI</model><deviceDescription>NVR</deviceDescription></DeviceInfo>`
	info, err := parseDeviceInfo([]byte(xml))
	if err != nil {
		t.Fatal(err)
	}
	if info.DeviceType != "NVR" {
		t.Errorf("DeviceType fallback = %q, want NVR (from deviceDescription)", info.DeviceType)
	}
}

func TestParseDeviceInfo_NotDeviceInfo(t *testing.T) {
	if _, err := parseDeviceInfo([]byte(`<ResponseStatus><statusCode>4</statusCode></ResponseStatus>`)); err == nil {
		t.Fatal("expected error for a non-deviceInfo document")
	}
}

// RFC 2617 §3.5 worked example — validates the Digest response computation.
func TestDigestResponse_RFC2617Vector(t *testing.T) {
	ch := map[string]string{
		"realm":  "testrealm@host.com",
		"nonce":  "dcd98b7102dd2f0e8b11d0f600bfb0c093",
		"qop":    "auth",
		"opaque": "5ccc069c403ebaf9f0171e9517f40e41",
	}
	got := digestResponse("Mufasa", ch["realm"], "Circle Of Life", "GET", "/dir/index.html", ch, "0a4f113b", "00000001")
	const want = "6629fae49393a05397450978507c4ef1"
	if got != want {
		t.Fatalf("digest response = %q, want %q", got, want)
	}
}

func TestParseChallenge(t *testing.T) {
	ch := parseChallenge(`Digest realm="testrealm@host.com", qop="auth,auth-int", nonce="abc", opaque="xyz", algorithm=MD5`)
	if ch["realm"] != "testrealm@host.com" || ch["nonce"] != "abc" || ch["opaque"] != "xyz" || ch["algorithm"] != "MD5" {
		t.Fatalf("parsed challenge = %+v", ch)
	}
	if !qopHasAuth(ch["qop"]) {
		t.Errorf("qopHasAuth should be true for %q", ch["qop"])
	}
}

// fakeDoer answers the 1st (unauth) request with 401+Digest challenge and the 2nd
// (with Authorization) with the deviceInfo body — exercising the digest round-trip.
type fakeDoer struct {
	calls   int
	lastReq *http.Request
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	f.calls++
	f.lastReq = req
	if req.Header.Get("Authorization") == "" {
		h := http.Header{}
		h.Set("WWW-Authenticate", `Digest realm="HIK", qop="auth", nonce="deadbeef"`)
		return &http.Response{StatusCode: 401, Header: h, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(nvrXML))}, nil
}

// always401 rejects every authenticated request (wrong password).
type always401 struct{ calls int }

func (a *always401) Do(req *http.Request) (*http.Response, error) {
	a.calls++
	h := http.Header{}
	h.Set("WWW-Authenticate", `Digest realm="x", qop="auth", nonce="n"`)
	return &http.Response{StatusCode: 401, Header: h, Body: io.NopCloser(strings.NewReader(""))}, nil
}

// A wrong credential must surface "authentication rejected" and short-circuit the
// port ladder (reaching a 401 means ISAPI was found — other ports won't help).
func TestCollectDeviceInfo_ShortCircuitsOnAuthReject(t *testing.T) {
	d := &always401{}
	_, err := CollectDeviceInfo(context.Background(), "10.0.0.10", "u", "wrong", d)
	if err == nil || !strings.Contains(err.Error(), "authentication rejected") {
		t.Fatalf("err = %v, want authentication rejected", err)
	}
	if d.calls != 2 { // one unauth + one digest on the first rung, then stop
		t.Fatalf("calls = %d, want 2 (short-circuit, not the whole ladder)", d.calls)
	}
}

func TestClient_DeviceInfo_DigestRoundTrip(t *testing.T) {
	f := &fakeDoer{}
	info, err := NewClient("https://10.0.0.10", "admin", "pw", f).DeviceInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if f.calls != 2 {
		t.Fatalf("expected 2 requests (challenge + authed), got %d", f.calls)
	}
	if auth := f.lastReq.Header.Get("Authorization"); !strings.HasPrefix(auth, "Digest ") || !strings.Contains(auth, `qop=auth`) {
		t.Fatalf("second request Authorization = %q", auth)
	}
	if info.DeviceType != "NVR" || info.Endpoint != "https://10.0.0.10" {
		t.Fatalf("info = %+v", info)
	}
}
