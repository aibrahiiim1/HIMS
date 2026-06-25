// Package aruba is a dependency-light client for Aruba Mobility Controllers /
// Mobility Conductor running ArubaOS 8 (the REST "showcommand" API). It logs in
// for a UIDARUBA session token, then runs read-only `show` commands and parses
// their JSON — the parsing is the testable core (injectable Doer), so AP and
// client inventory work against sample payloads with no real controller.
//
// Live-validation trigger: the login form, the UIDARUBA token plumbing, and the
// exact showcommand JSON keys vary across ArubaOS 8.x trains and between a
// Mobility Controller and a Conductor; validate against a real controller once a
// credential is bound. The struct field tags below follow the documented
// ArubaOS 8 REST API ("show ap database long", "show user-table").
package aruba

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Doer performs an HTTP request (a cookie-jar *http.Client in production carries
// the ArubaOS session cookie alongside the UIDARUBA token).
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client targets one ArubaOS 8 controller. The REST API lives under /v1.
type Client struct {
	BaseURL  string // https://host:4343
	Username string
	Password string
	Doer     Doer
	token    string // UIDARUBA session token
}

// NewClient builds a Client.
func NewClient(baseURL, user, pass string, doer Doer) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Username: user, Password: pass, Doer: doer}
}

type loginResp struct {
	GlobalResult struct {
		Status    string `json:"status"`
		StatusStr string `json:"status_str"`
		UIDARUBA  string `json:"UIDARUBA"`
	} `json:"_global_result"`
}

// Login authenticates and captures the UIDARUBA session token. ArubaOS returns
// status "0" on success.
func (c *Client) Login(ctx context.Context) error {
	form := url.Values{"username": {c.Username}, "password": {c.Password}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/api/login", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.Doer.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("aruba: login → %d", resp.StatusCode)
	}
	tok, err := parseLogin(raw)
	if err != nil {
		return err
	}
	c.token = tok
	return nil
}

func parseLogin(raw []byte) (string, error) {
	var lr loginResp
	if err := json.Unmarshal(raw, &lr); err != nil {
		return "", err
	}
	if lr.GlobalResult.Status != "0" || lr.GlobalResult.UIDARUBA == "" {
		return "", fmt.Errorf("aruba: login rejected (status %q)", lr.GlobalResult.Status)
	}
	return lr.GlobalResult.UIDARUBA, nil
}

// AP is one access point from "show ap database long".
type AP struct {
	Name   string
	Model  string
	IP     string
	Status string // online | offline
	Group  string
	Serial string
}

type apDatabaseResp struct {
	APDatabase []struct {
		Name   string `json:"Name"`
		Group  string `json:"Group"`
		APType string `json:"AP Type"`
		IP     string `json:"IP Address"`
		Status string `json:"Status"`
		Serial string `json:"Serial #"`
	} `json:"AP Database"`
}

// ListAPs runs "show ap database long" and returns the AP roster.
func (c *Client) ListAPs(ctx context.Context) ([]AP, error) {
	raw, err := c.show(ctx, "show ap database long")
	if err != nil {
		return nil, err
	}
	return parseAPDatabase(raw)
}

func parseAPDatabase(raw []byte) ([]AP, error) {
	var dr apDatabaseResp
	if err := json.Unmarshal(raw, &dr); err != nil {
		return nil, err
	}
	out := make([]AP, 0, len(dr.APDatabase))
	for _, a := range dr.APDatabase {
		out = append(out, AP{
			Name: a.Name, Model: a.APType, IP: a.IP, Status: arubaAPStatus(a.Status),
			Group: a.Group, Serial: a.Serial,
		})
	}
	return out, nil
}

// Station is one associated client from "show user-table".
type Station struct {
	MAC      string
	IP       string
	Hostname string
	SSID     string
	APName   string
	Band     string
}

type userTableResp struct {
	Users []struct {
		IP       string `json:"IP"`
		MAC      string `json:"MAC"`
		Name     string `json:"Name"`
		Role     string `json:"Role"`
		APName   string `json:"AP name"`
		EssidPhy string `json:"Essid/Bssid/Phy"`
	} `json:"Users"`
}

// ListClients runs "show user-table" and returns associated wireless clients.
func (c *Client) ListClients(ctx context.Context) ([]Station, error) {
	raw, err := c.show(ctx, "show user-table")
	if err != nil {
		return nil, err
	}
	return parseUserTable(raw)
}

func parseUserTable(raw []byte) ([]Station, error) {
	var ur userTableResp
	if err := json.Unmarshal(raw, &ur); err != nil {
		return nil, err
	}
	out := make([]Station, 0, len(ur.Users))
	for _, u := range ur.Users {
		essid, band := splitEssidPhy(u.EssidPhy)
		out = append(out, Station{
			MAC: u.MAC, IP: u.IP, Hostname: u.Name, SSID: essid, APName: u.APName, Band: band,
		})
	}
	return out, nil
}

// ---- Firmware / controller version (show version) --------------------------

type showVersionResp struct {
	Data []string `json:"_data"`
}

// Version runs "show version" and returns the ArubaOS version (empty if absent).
func (c *Client) Version(ctx context.Context) (string, error) {
	raw, err := c.show(ctx, "show version")
	if err != nil {
		return "", err
	}
	return parseVersion(raw), nil
}

func parseVersion(raw []byte) string {
	var r showVersionResp
	if err := json.Unmarshal(raw, &r); err != nil {
		return ""
	}
	for _, line := range r.Data {
		if i := strings.Index(line, "Version "); i >= 0 {
			if f := strings.Fields(strings.TrimSpace(line[i+len("Version "):])); len(f) > 0 {
				return f[0]
			}
		}
	}
	return ""
}

// ---- Radios (show ap bss-table) --------------------------------------------

// Radio is one AP radio derived from the BSS table (one entry per AP per band).
type Radio struct {
	APName  string
	Band    string // 2.4 | 5 | 6
	Channel *int32
}

// ListRadios runs "show ap bss-table" and derives per-AP per-band radios
// (deduplicated across the per-SSID BSS rows). channel comes from the "ch"
// column; band from the "phy" column.
func (c *Client) ListRadios(ctx context.Context) ([]Radio, error) {
	raw, err := c.show(ctx, "show ap bss-table")
	if err != nil {
		return nil, err
	}
	return parseBSSTable(raw)
}

func parseBSSTable(raw []byte) ([]Radio, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, err
	}
	// ArubaOS names the array "Aruba AP BSS Table" (varies); find the BSS array.
	var rowsRaw json.RawMessage
	for k, v := range top {
		if strings.Contains(strings.ToLower(k), "bss") {
			rowsRaw = v
			break
		}
	}
	if rowsRaw == nil {
		return nil, nil
	}
	var rows []map[string]any
	if err := json.Unmarshal(rowsRaw, &rows); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []Radio
	for _, m := range rows {
		ap := asString(m["ap name"])
		if ap == "" {
			ap = asString(m["ap_name"])
		}
		band := phyBand(asString(m["phy"]))
		if ap == "" || band == "" {
			continue
		}
		key := ap + "|" + band
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, Radio{APName: ap, Band: band, Channel: asIntPtr(m["ch"])})
	}
	return out, nil
}

// phyBand maps an ArubaOS phy code (a-VHT-80 / g-HT-20 / 6-…) to a band.
func phyBand(phy string) string {
	p := strings.ToLower(strings.TrimSpace(phy))
	switch {
	case strings.HasPrefix(p, "a"):
		return "5"
	case strings.HasPrefix(p, "g"), strings.HasPrefix(p, "b"):
		return "2.4"
	case strings.HasPrefix(p, "6"):
		return "6"
	default:
		return ""
	}
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func asIntPtr(v any) *int32 {
	switch t := v.(type) {
	case float64:
		n := int32(t)
		return &n
	case string:
		// channel may carry a suffix (e.g. "6", "44E"); take the leading digits.
		num := 0
		got := false
		for _, r := range t {
			if r < '0' || r > '9' {
				break
			}
			num = num*10 + int(r-'0')
			got = true
		}
		if !got {
			return nil
		}
		n := int32(num)
		return &n
	}
	return nil
}

// show runs a read-only showcommand and returns the JSON body. The command is
// URL-encoded and the UIDARUBA token is passed as a query parameter (ArubaOS
// also accepts it as a cookie, which the Doer's jar carries).
func (c *Client) show(ctx context.Context, command string) ([]byte, error) {
	q := url.Values{"command": {command}, "UIDARUBA": {c.token}}
	u := c.BaseURL + "/v1/configuration/showcommand?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.Doer.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("aruba: showcommand %q → %d", command, resp.StatusCode)
	}
	return raw, nil
}

// arubaAPStatus maps an ArubaOS AP status ("Up 3d:4h:5m" / "Down") to online/offline.
func arubaAPStatus(s string) string {
	low := strings.ToLower(strings.TrimSpace(s))
	switch {
	case strings.HasPrefix(low, "up"):
		return "online"
	case strings.HasPrefix(low, "down"):
		return "offline"
	default:
		return "unknown"
	}
}

// splitEssidPhy splits the user-table "Essid/Bssid/Phy" column into the ESSID and
// a normalized band derived from the PHY code (a*→5GHz, g*/b*→2.4GHz, 6*→6GHz).
func splitEssidPhy(v string) (essid, band string) {
	parts := strings.Split(v, "/")
	if len(parts) > 0 {
		essid = strings.TrimSpace(parts[0])
	}
	phy := ""
	if len(parts) >= 3 {
		phy = strings.ToLower(strings.TrimSpace(parts[2]))
	}
	switch {
	case strings.HasPrefix(phy, "a"):
		band = "5"
	case strings.HasPrefix(phy, "g"), strings.HasPrefix(phy, "b"):
		band = "2.4"
	case strings.HasPrefix(phy, "6"):
		band = "6"
	}
	return essid, band
}
