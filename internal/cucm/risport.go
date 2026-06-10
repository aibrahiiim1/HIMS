package cucm

// RisPort (Real-time Information Server) gives the live registration view that
// the AXL DB does not: each phone's current IP address + registration status.
// On legacy CUCM (7.x) it lives at /realtimeservice/services/RisPort (Apache
// Axis). Two quirks learned against CUCM 7.1.3: the SOAPAction header must be
// PRESENT (even as ""), and SelectItems items are flat strings (<item>*</item>),
// not nested elements — otherwise Axis 500s.

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// DeviceStatus is one device's real-time registration snapshot.
type DeviceStatus struct {
	IP     string // registered IP, "" if not registered / unknown
	Status string // Registered | UnRegistered | Rejected | Unknown | PartiallyRegistered
}

type risDevice struct {
	Name   string `xml:"Name"`
	IP     string `xml:"IpAddress"`
	Status string `xml:"Status"`
}
type risResp struct {
	Nodes []struct {
		Devices []risDevice `xml:"CmDevices>item"`
	} `xml:"Body>SelectCmDeviceResponse>SelectCmDeviceResult>CmNodes>item"`
	StateInfo string `xml:"Body>SelectCmDeviceResponse>StateInfo"`
	Fault     string `xml:"Body>Fault>faultstring"`
}

// RegisteredPhoneStatus queries RisPort SelectCmDevice for all phones and returns
// a map keyed by device name (SEP<mac>) → {IP, Status}. Best-effort: a RisPort
// error returns it so the caller can proceed without IPs. Pages via StateInfo.
func (c *Client) RegisteredPhoneStatus(ctx context.Context) (map[string]DeviceStatus, error) {
	out := map[string]DeviceStatus{}
	state := ""
	for page := 0; page < 8; page++ { // 8×1000 covers any realistic cluster
		resp, err := c.risSelect(ctx, state)
		if err != nil {
			return out, err
		}
		if resp.Fault != "" {
			return out, fmt.Errorf("cucm: RisPort fault: %s", resp.Fault)
		}
		added := 0
		for _, n := range resp.Nodes {
			for _, d := range n.Devices {
				if d.Name == "" {
					continue
				}
				if _, seen := out[d.Name]; !seen {
					added++
				}
				out[d.Name] = DeviceStatus{IP: d.IP, Status: d.Status}
			}
		}
		// Stop when a page adds nothing new or the cluster echoes a stable state.
		if added == 0 || resp.StateInfo == "" || resp.StateInfo == state {
			break
		}
		state = resp.StateInfo
	}
	return out, nil
}

func (c *Client) risSelect(ctx context.Context, stateInfo string) (*risResp, error) {
	base := strings.TrimRight(c.BaseURL, "/") + "/realtimeservice/services/RisPort"
	body := `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/" xmlns:soap="http://schemas.cisco.com/ast/soap/">` +
		`<soapenv:Body><soap:SelectCmDevice><soap:StateInfo>` + xmlEscape(stateInfo) + `</soap:StateInfo>` +
		`<soap:CmSelectionCriteria>` +
		`<soap:MaxReturnedDevices>1000</soap:MaxReturnedDevices>` +
		`<soap:Class>Phone</soap:Class><soap:Model>255</soap:Model><soap:Status>Any</soap:Status>` +
		`<soap:NodeName></soap:NodeName><soap:SelectBy>Name</soap:SelectBy>` +
		`<soap:SelectItems><soap:item>*</soap:item></soap:SelectItems>` +
		`</soap:CmSelectionCriteria></soap:SelectCmDevice></soapenv:Body></soapenv:Envelope>`

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPAction", `""`) // Axis requires the header present, even empty
	if c.Username != "" {
		req.SetBasicAuth(c.Username, c.Password) // never logged
	}
	hr, err := c.Doer.Do(req)
	if err != nil {
		return nil, err
	}
	defer hr.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(hr.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if hr.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("cucm: RisPort auth failed (401)")
	}
	var r risResp
	if err := xml.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("cucm: RisPort parse: %w", err)
	}
	return &r, nil
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
