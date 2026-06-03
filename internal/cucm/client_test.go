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
