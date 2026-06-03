// Package cucm is a thin Cisco Unified Communications Manager (CUCM) AXL
// client: it POSTs an AXL SOAP `listPhone` request (HTTP Basic + SOAPAction)
// over an injectable Doer and parses the phone registry. As with the ONVIF
// client, the SOAP-response parsing is the testable core; the AXL transport
// (versioned namespace + cert + the AXL service account) is live-validation-
// pending.
//
// Live-validation trigger: the AXL schema version in the SOAPAction +
// namespace must match the CUCM release (8.x–15.x); validate against a real
// CUCM once an AXL credential is bound. listPhone is paged on large clusters —
// v1 fetches the first page.
package cucm

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Doer performs an HTTP request (an http.Client with TLS-insecure in prod).
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client targets one CUCM publisher's AXL service.
type Client struct {
	BaseURL  string // https://cucm:8443
	Username string
	Password string
	Version  string // AXL schema version, e.g. "12.5"
	Doer     Doer
}

// NewClient builds a Client. Version defaults to "12.5".
func NewClient(baseURL, user, pass, version string, doer Doer) *Client {
	if version == "" {
		version = "12.5"
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Username: user, Password: pass, Version: version, Doer: doer}
}

// Phone is one registered phone from the CUCM device registry.
type Phone struct {
	Name        string
	Model       string
	Description string
	DevicePool  string
}

type phoneXML struct {
	Name        string `xml:"name"`
	Model       string `xml:"model"`
	Description string `xml:"description"`
	DevicePool  string `xml:"devicePoolName"`
}

type listPhoneResp struct {
	Phones []phoneXML `xml:"Body>listPhoneResponse>return>phone"`
	Fault  string     `xml:"Body>Fault>faultstring"`
}

// ListPhones runs AXL listPhone and returns the parsed registry.
func (c *Client) ListPhones(ctx context.Context) ([]Phone, error) {
	ns := "http://www.cisco.com/AXL/API/" + c.Version
	body := `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/" xmlns:ns="` + ns + `">` +
		`<soapenv:Header/><soapenv:Body><ns:listPhone>` +
		`<searchCriteria><name>%</name></searchCriteria>` +
		`<returnedTags><name/><model/><description/><devicePoolName/></returnedTags>` +
		`</ns:listPhone></soapenv:Body></soapenv:Envelope>`

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/axl/", strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPAction", `CUCM:DB ver=`+c.Version+` listPhone`)
	if c.Username != "" {
		req.SetBasicAuth(c.Username, c.Password) // never logged
	}
	resp, err := c.Doer.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("cucm: AXL auth failed (401)")
	}
	return parsePhones(raw)
}

func parsePhones(raw []byte) ([]Phone, error) {
	var lr listPhoneResp
	if err := xml.Unmarshal(raw, &lr); err != nil {
		return nil, err
	}
	if lr.Fault != "" {
		return nil, fmt.Errorf("cucm: AXL fault: %s", lr.Fault)
	}
	out := make([]Phone, 0, len(lr.Phones))
	for _, p := range lr.Phones {
		out = append(out, Phone{Name: p.Name, Model: p.Model, Description: p.Description, DevicePool: p.DevicePool})
	}
	return out, nil
}
