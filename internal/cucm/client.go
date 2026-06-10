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
	"strconv"
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

// Phone is one phone from the CUCM device registry.
type Phone struct {
	Name        string
	Model       string
	Description string
	DevicePool  string
	Extension   string // primary line directory number (numplan.dnorpattern)
	MAC         string // derived from a SEP<mac> hardware-phone name
	IP          string // registered IP (real-time, best-effort; blank if unavailable)
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

// ListPhones returns the CUCM phone registry. It negotiates a supported AXL
// schema version (legacy CUCM rejects a newer version with HTTP 599 and lists
// the ones it serves), runs listPhone, and — when a release doesn't serve the
// typed listPhone method (legacy 7.x faults "No method found", AXL code 5003) —
// falls back to executeSQLQuery against the CUCM (Informix) DB, which every
// release from 6.x to 15.x supports. Verified live against CUCM 7.1.3.
func (c *Client) ListPhones(ctx context.Context) ([]Phone, error) {
	listInner := `<ns:listPhone>` +
		`<searchCriteria><name>%</name></searchCriteria>` +
		`<returnedTags><name/><model/><description/><devicePoolName/></returnedTags>` +
		`</ns:listPhone>`
	raw, err := c.call(ctx, "listPhone", listInner)
	if err != nil {
		return nil, err
	}
	phones, perr := parsePhones(raw)
	if perr == nil {
		return phones, nil
	}
	if !isUnsupportedMethod(perr) {
		return nil, perr
	}
	return c.listPhonesViaSQL(ctx) // legacy release: typed listPhone not served
}

// call POSTs one AXL request, transparently re-negotiating the schema version
// when the box reports the requested one unavailable (HTTP 599 + "Available
// versions are …"). The chosen version is cached for subsequent calls.
func (c *Client) call(ctx context.Context, method, inner string) ([]byte, error) {
	raw, status, err := c.post(ctx, method, inner)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		if v := highestAvailableVersion(raw); v != "" && v != c.Version {
			c.Version = v
			if raw, status, err = c.post(ctx, method, inner); err != nil {
				return nil, err
			}
		}
	}
	if status == http.StatusUnauthorized {
		return nil, fmt.Errorf("cucm: AXL auth failed (401)")
	}
	// A non-200 is an error — NOT an empty result. Cisco returns an HTML error
	// page (not a SOAP body); surface it instead of silently reporting nothing.
	if status != http.StatusOK {
		return nil, fmt.Errorf("cucm: AXL HTTP %d — %s", status, axlErrorSummary(raw))
	}
	return raw, nil
}

func (c *Client) post(ctx context.Context, method, inner string) ([]byte, int, error) {
	ns := "http://www.cisco.com/AXL/API/" + c.Version
	body := `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/" xmlns:ns="` + ns + `">` +
		`<soapenv:Header/><soapenv:Body>` + inner + `</soapenv:Body></soapenv:Envelope>`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/axl/", strings.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPAction", `CUCM:DB ver=`+c.Version+` `+method)
	if c.Username != "" {
		req.SetBasicAuth(c.Username, c.Password) // never logged
	}
	resp, err := c.Doer.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, 0, err
	}
	return raw, resp.StatusCode, nil
}

// listPhonesViaSQL pulls the registry with a raw query against the CUCM DB
// (Informix; tkclass=1 is a phone). The numplan join yields the primary line's
// directory number (extension); the MAC is derived from the SEP<mac> device name.
// Column aliases map onto sqlPhoneRow.
func (c *Client) listPhonesViaSQL(ctx context.Context) ([]Phone, error) {
	const q = `select d.name as name, d.description as description, tm.name as model, dp.name as pool, np.dnorpattern as dn ` +
		`from device as d inner join typemodel as tm on d.tkmodel=tm.enum ` +
		`inner join devicepool as dp on d.fkdevicepool=dp.pkid ` +
		`left outer join devicenumplanmap as dnpm on dnpm.fkdevice=d.pkid and dnpm.numplanindex=1 ` +
		`left outer join numplan as np on np.pkid=dnpm.fknumplan ` +
		`where d.tkclass=1`
	raw, err := c.call(ctx, "executeSQLQuery", `<ns:executeSQLQuery><sql>`+q+`</sql></ns:executeSQLQuery>`)
	if err != nil {
		return nil, err
	}
	var sr sqlPhoneResp
	if err := xml.Unmarshal(raw, &sr); err != nil {
		return nil, err
	}
	if sr.Fault != "" {
		return nil, fmt.Errorf("cucm: AXL executeSQLQuery fault: %s", sr.Fault)
	}
	out := make([]Phone, 0, len(sr.Rows))
	for _, r := range sr.Rows {
		out = append(out, Phone{
			Name: r.Name, Model: r.Model, Description: r.Description, DevicePool: r.Pool,
			Extension: r.DN, MAC: macFromDeviceName(r.Name),
		})
	}
	return out, nil
}

// macFromDeviceName turns a Cisco hardware-phone device name ("SEP6C504DDA6A82")
// into a colon-separated MAC ("6c:50:4d:da:6a:82"). Non-SEP names (analog/SIP
// trunk endpoints) have no MAC and return "".
func macFromDeviceName(name string) string {
	if len(name) != 15 || !strings.HasPrefix(name, "SEP") {
		return ""
	}
	h := strings.ToLower(name[3:])
	for _, r := range h {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return ""
		}
	}
	var b strings.Builder
	for i := 0; i < len(h); i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(h[i : i+2])
	}
	return b.String()
}

type sqlPhoneRow struct {
	Name        string `xml:"name"`
	Description string `xml:"description"`
	Model       string `xml:"model"`
	Pool        string `xml:"pool"`
	DN          string `xml:"dn"`
}

type sqlPhoneResp struct {
	Rows  []sqlPhoneRow `xml:"Body>executeSQLQueryResponse>return>row"`
	Fault string        `xml:"Body>Fault>faultstring"`
}

// isUnsupportedMethod reports whether a parse error is CUCM's "method not served
// by this release" fault (AXL code 5003 / "No method found for processing request").
func isUnsupportedMethod(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "no method") || strings.Contains(s, "5003")
}

// highestAvailableVersion parses a legacy-CUCM HTTP 599 body ("…Available
// versions are 1.0, 6.0, 6.1, 7.0 and 7.1") and returns the newest one, or "".
func highestAvailableVersion(raw []byte) string {
	s := string(raw)
	i := strings.Index(s, "Available versions are")
	if i < 0 {
		return ""
	}
	tail := s[i+len("Available versions are"):]
	if j := strings.IndexAny(tail, "<\n"); j >= 0 { // stop before the next tag; keep version dots
		tail = tail[:j]
	}
	tail = strings.NewReplacer(" and ", ",", " ", "").Replace(tail)
	var best string
	var bestF float64
	for _, v := range strings.Split(tail, ",") {
		if v = strings.TrimSpace(v); v == "" {
			continue
		}
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= bestF {
			bestF, best = f, v
		}
	}
	return best
}

// axlErrorSummary pulls a short human message out of a Cisco error response —
// the AXL <axl:message> for a SOAP fault, else the visible bit of an HTML page.
func axlErrorSummary(raw []byte) string {
	s := string(raw)
	if i := strings.Index(s, "HTTP Status"); i >= 0 { // Cisco HTML error report
		end := strings.IndexAny(s[i:], "<\n")
		if end < 0 {
			end = len(s) - i
		}
		return strings.TrimSpace(s[i : i+end])
	}
	if lr := (listPhoneResp{}); xml.Unmarshal(raw, &lr) == nil && lr.Fault != "" {
		return lr.Fault
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return strings.TrimSpace(s)
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
