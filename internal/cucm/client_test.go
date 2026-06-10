package cucm

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type fakeDoer struct {
	body string
	code int
}

func (f fakeDoer) Do(*http.Request) (*http.Response, error) {
	code := f.code
	if code == 0 {
		code = 200
	}
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(f.body)), Header: make(http.Header)}, nil
}

// legacyRouteDoer simulates a CUCM 7.1 box: it 599s a too-new schema version,
// faults "No method found" on listPhone, and serves rows via executeSQLQuery.
type legacyRouteDoer struct{}

func (legacyRouteDoer) Do(req *http.Request) (*http.Response, error) {
	b, _ := io.ReadAll(req.Body)
	s := string(b)
	resp := func(code int, body string) (*http.Response, error) {
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	}
	switch {
	case strings.Contains(s, "AXL/API/12.5"): // newer version rejected
		return resp(599, `<div>HTTP Status 599 - The specified version is not available.  Available versions are 1.0, 6.0, 6.1, 7.0 and 7.1</div>`)
	case strings.Contains(s, "executeSQLQuery"):
		return resp(200, `<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/"><SOAP-ENV:Body>`+
			`<axl:executeSQLQueryResponse xmlns:axl="http://www.cisco.com/AXL/API/7.1"><return>`+
			`<row><name>SEP001</name><description>Front Desk</description><model>Cisco 7911</model><pool>Coral-PUB-DP</pool></row>`+
			`<row><name>SEP002</name><description>Bar</description><model>Cisco 8845</model><pool>Coral-PUB-DP</pool></row>`+
			`</return></axl:executeSQLQueryResponse></SOAP-ENV:Body></SOAP-ENV:Envelope>`)
	case strings.Contains(s, "listPhone"): // typed method not served by 7.x
		return resp(200, `<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/"><SOAP-ENV:Body>`+
			`<SOAP-ENV:Fault><faultstring>No method found for processing request</faultstring></SOAP-ENV:Fault></SOAP-ENV:Body></SOAP-ENV:Envelope>`)
	}
	return resp(200, "")
}

const listPhoneXML = `<?xml version="1.0"?>
<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/">
 <soapenv:Body>
  <ns:listPhoneResponse xmlns:ns="http://www.cisco.com/AXL/API/12.5">
   <return>
    <phone uuid="{abc}"><name>SEP001122334455</name><model>Cisco 8845</model><description>Front Desk</description><devicePoolName>HotelA-DP</devicePoolName></phone>
    <phone uuid="{def}"><name>SEP00aabbccddee</name><model>Cisco 7841</model><description>Housekeeping</description><devicePoolName>HotelA-DP</devicePoolName></phone>
   </return>
  </ns:listPhoneResponse>
 </soapenv:Body>
</soapenv:Envelope>`

func TestListPhones_Parse(t *testing.T) {
	c := NewClient("https://cucm:8443", "axladmin", "pw", "12.5", fakeDoer{body: listPhoneXML})
	phones, err := c.ListPhones(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(phones) != 2 {
		t.Fatalf("got %d phones; want 2", len(phones))
	}
	if phones[0].Name != "SEP001122334455" || phones[0].Model != "Cisco 8845" || phones[0].Description != "Front Desk" || phones[0].DevicePool != "HotelA-DP" {
		t.Fatalf("phone[0] wrong: %+v", phones[0])
	}
}

func TestListPhones_Fault(t *testing.T) {
	fault := `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Body>
	  <soapenv:Fault><faultstring>Unknown error</faultstring></soapenv:Fault></soapenv:Body></soapenv:Envelope>`
	c := NewClient("https://cucm:8443", "u", "p", "12.5", fakeDoer{body: fault})
	if _, err := c.ListPhones(context.Background()); err == nil {
		t.Fatal("SOAP fault should error")
	}
}

func TestListPhones_AuthFailed(t *testing.T) {
	c := NewClient("https://cucm:8443", "u", "bad", "12.5", fakeDoer{code: 401})
	if _, err := c.ListPhones(context.Background()); err == nil {
		t.Fatal("401 should error")
	}
}

// Legacy CUCM 7.x: listPhone at the default (newer) version is rejected with
// HTTP 599 + a list of supported versions; at the negotiated version listPhone
// faults "No method found"; the client must then fall back to executeSQLQuery
// and return the phones from the raw DB rows.
func TestListPhones_LegacyVersionNegotiationAndSQLFallback(t *testing.T) {
	c := NewClient("https://cucm:8443", "administrator", "pw", "12.5", legacyRouteDoer{})
	phones, err := c.ListPhones(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(phones) != 2 {
		t.Fatalf("got %d phones; want 2 (via SQL fallback)", len(phones))
	}
	if phones[0].Name != "SEP001" || phones[0].Model != "Cisco 7911" || phones[0].Description != "Front Desk" || phones[0].DevicePool != "Coral-PUB-DP" {
		t.Fatalf("phone[0] wrong: %+v", phones[0])
	}
	if c.Version != "7.1" {
		t.Fatalf("version should have negotiated to 7.1, got %q", c.Version)
	}
}

func TestHighestAvailableVersion(t *testing.T) {
	body := []byte(`<div>HTTP Status 599 - The specified version is not available.  Available versions are 1.0, 6.0, 6.1, 7.0 and 7.1            </div>`)
	if got := highestAvailableVersion(body); got != "7.1" {
		t.Fatalf("highestAvailableVersion = %q; want 7.1", got)
	}
	if got := highestAvailableVersion([]byte("no version hint here")); got != "" {
		t.Fatalf("expected empty for no-hint body, got %q", got)
	}
}

// A non-200 Cisco error page (legacy CUCM returns HTTP 599 + HTML when the AXL
// schema version is wrong) must be an ERROR, never a silent "0 phones".
func TestListPhones_VersionError599(t *testing.T) {
	html := `<html><head><title>Cisco System - Error report</title></head><body>` +
		`HTTP Status 599 - The specified version is not available.  Available versions are 1.0, 6.0, 6.1, 7.0 and 7.1</body></html>`
	c := NewClient("https://cucm:8443", "u", "p", "12.5", fakeDoer{body: html, code: 599})
	phones, err := c.ListPhones(context.Background())
	if err == nil {
		t.Fatalf("HTTP 599 version error must error, got %d phones", len(phones))
	}
	if !strings.Contains(err.Error(), "599") || !strings.Contains(err.Error(), "version") {
		t.Fatalf("error should surface the Cisco status/message, got: %v", err)
	}
}
