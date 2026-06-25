// Package ruckus is a dependency-light client for Ruckus SmartZone (vSZ/SZ)
// controllers. It opens a session and lists APs; the AP-list JSON parsing is
// the testable core (injectable Doer), the session flow is the
// live-validation-pending transport.
//
// Live-validation trigger: SmartZone's session endpoint + API version path
// (/wsg/api/public/v<ver>/…) vary by firmware; validate against a real
// controller once a credential is bound. The AP-list shape below follows the
// documented SmartZone public API.
package ruckus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// Doer performs an HTTP request (a cookie-jar *http.Client in production
// carries the SmartZone session cookie).
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client targets one SmartZone controller. APIBase defaults to the v9_1 path.
type Client struct {
	BaseURL  string // https://host:8443
	APIBase  string // /wsg/api/public/v9_1
	Username string
	Password string
	Doer     Doer
}

// NewClient builds a Client.
func NewClient(baseURL, apiBase, user, pass string, doer Doer) *Client {
	if apiBase == "" {
		apiBase = "/wsg/api/public/v9_1"
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), APIBase: apiBase, Username: user, Password: pass, Doer: doer}
}

// Login opens a SmartZone session; the Doer's cookie jar carries it.
func (c *Client) Login(ctx context.Context) error {
	body, _ := json.Marshal(map[string]string{"username": c.Username, "password": c.Password})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+c.APIBase+"/session", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Doer.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ruckus: session → %d", resp.StatusCode)
	}
	return nil
}

// AP is one access point from the SmartZone AP list.
type AP struct {
	Name        string
	Model       string
	MAC         string
	IP          string
	Status      string // online | offline
	ClientCount int32
}

type szAP struct {
	DeviceName string `json:"deviceName"`
	Model      string `json:"model"`
	ApMac      string `json:"apMac"`
	IP         string `json:"ip"`
	Status     string `json:"status"` // "Online" | "Offline" | "Flagged"
	NumClients int32  `json:"numClients"`
}

type apListResp struct {
	TotalCount int    `json:"totalCount"`
	List       []szAP `json:"list"`
}

// ListAPs fetches the controller's APs.
func (c *Client) ListAPs(ctx context.Context) ([]AP, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+c.APIBase+"/aps", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.Doer.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ruckus: aps → %d", resp.StatusCode)
	}
	return parseAPs(raw)
}

func parseAPs(raw []byte) ([]AP, error) {
	var lr apListResp
	if err := json.Unmarshal(raw, &lr); err != nil {
		return nil, err
	}
	aps := make([]AP, 0, len(lr.List))
	for _, a := range lr.List {
		status := "offline"
		if strings.EqualFold(a.Status, "Online") {
			status = "online"
		}
		aps = append(aps, AP{Name: a.DeviceName, Model: a.Model, MAC: a.ApMac, IP: a.IP, Status: status, ClientCount: a.NumClients})
	}
	return aps, nil
}

// ---- WLANs / SSIDs (POST /query/wlan) --------------------------------------

// SSID is one configured WLAN.
type SSID struct {
	Name     string
	Enabled  bool
	Security string // open | wpa2 | wpa3 | … (NEVER the passphrase)
	VLAN     string
}

type szWLAN struct {
	Name           string `json:"name"`
	SSID           string `json:"ssid"`
	AuthMethod     string `json:"authMethod"`
	EncryptionType string `json:"encryptionType"`
	VlanID         *int32 `json:"vlanId"`
}

type wlanQueryResp struct {
	TotalCount int      `json:"totalCount"`
	List       []szWLAN `json:"list"`
}

// ListSSIDs returns the controller's WLANs via the cross-zone query endpoint.
func (c *Client) ListSSIDs(ctx context.Context) ([]SSID, error) {
	raw, err := c.query(ctx, "/query/wlan")
	if err != nil {
		return nil, err
	}
	return parseWLANs(raw)
}

func parseWLANs(raw []byte) ([]SSID, error) {
	var qr wlanQueryResp
	if err := json.Unmarshal(raw, &qr); err != nil {
		return nil, err
	}
	out := make([]SSID, 0, len(qr.List))
	for _, w := range qr.List {
		name := w.SSID
		if name == "" {
			name = w.Name
		}
		sec := w.EncryptionType
		if sec == "" {
			sec = w.AuthMethod
		}
		vlan := ""
		if w.VlanID != nil && *w.VlanID > 0 {
			vlan = fmt.Sprintf("%d", *w.VlanID)
		}
		out = append(out, SSID{Name: name, Enabled: true, Security: sec, VLAN: vlan})
	}
	return out, nil
}

// ---- Clients (POST /query/client) ------------------------------------------

// Station is one associated wireless client.
type Station struct {
	MAC      string
	IP       string
	Hostname string
	SSID     string
	APMac    string
	RSSI     *int32
	Band     string
}

type szClient struct {
	MAC      string `json:"clientMac"`
	IP       string `json:"ipAddress"`
	Hostname string `json:"hostname"`
	SSID     string `json:"ssid"`
	APMac    string `json:"apMac"`
	RSSI     *int32 `json:"rssi"`
	Radio    string `json:"radio"` // "2.4GHz" | "5GHz" | "6GHz"
}

type clientQueryResp struct {
	TotalCount int        `json:"totalCount"`
	List       []szClient `json:"list"`
}

// ListClients returns associated wireless clients via the query endpoint.
func (c *Client) ListClients(ctx context.Context) ([]Station, error) {
	raw, err := c.query(ctx, "/query/client")
	if err != nil {
		return nil, err
	}
	return parseClients(raw)
}

func parseClients(raw []byte) ([]Station, error) {
	var qr clientQueryResp
	if err := json.Unmarshal(raw, &qr); err != nil {
		return nil, err
	}
	out := make([]Station, 0, len(qr.List))
	for _, c := range qr.List {
		out = append(out, Station{
			MAC: c.MAC, IP: c.IP, Hostname: c.Hostname, SSID: c.SSID, APMac: c.APMac,
			RSSI: c.RSSI, Band: szBand(c.Radio),
		})
	}
	return out, nil
}

// query POSTs an empty filter to a SmartZone query endpoint and returns the body.
// SmartZone query endpoints accept a JSON body with paging/filters; an empty
// object returns the default (first) page, which is sufficient for inventory.
func (c *Client) query(ctx context.Context, path string) ([]byte, error) {
	body := []byte(`{"page":1,"limit":1000}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+c.APIBase+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
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
		return nil, fmt.Errorf("ruckus: POST %s → %d", path, resp.StatusCode)
	}
	return raw, nil
}

// szBand normalizes a SmartZone radio label to a band number.
func szBand(r string) string {
	switch {
	case strings.HasPrefix(r, "2.4"):
		return "2.4"
	case strings.HasPrefix(r, "5"):
		return "5"
	case strings.HasPrefix(r, "6"):
		return "6"
	default:
		return ""
	}
}

// ---- Radios (POST /query/ap — per-AP per-band channel/power/width) ----------

// Radio is one AP radio's live state.
type Radio struct {
	APName       string
	Band         string // 2.4 | 5 | 6
	Channel      *int32
	PowerDbm     *int32
	ChannelWidth string
	ClientCount  int32
}

// szAPRadio mirrors the per-band fields the AP query exposes. Power is reported
// by SmartZone as a label (e.g. "Full", "-3dB") more often than a dBm number, so
// it is parsed leniently and left nil unless numeric.
type szAPRadio struct {
	ApMac          string `json:"apMac"`
	DeviceName     string `json:"deviceName"`
	Channel24G     *int32 `json:"channel24G"`
	Channel5G      *int32 `json:"channel5G"`
	Channel6G      *int32 `json:"channel6G"`
	NumClients24G  *int32 `json:"numClients24G"`
	NumClients5G   *int32 `json:"numClients5G"`
	NumClients6G   *int32 `json:"numClients6G"`
	ChannelWidth24 string `json:"channelWidth24G"`
	ChannelWidth5  string `json:"channelWidth5G"`
	ChannelWidth6  string `json:"channelWidth6G"`
}

type apRadioQueryResp struct {
	TotalCount int         `json:"totalCount"`
	List       []szAPRadio `json:"list"`
}

// ListRadios returns per-AP, per-band radio state from the AP query (one call).
func (c *Client) ListRadios(ctx context.Context) ([]Radio, error) {
	raw, err := c.query(ctx, "/query/ap")
	if err != nil {
		return nil, err
	}
	return parseRadios(raw)
}

func parseRadios(raw []byte) ([]Radio, error) {
	var qr apRadioQueryResp
	if err := json.Unmarshal(raw, &qr); err != nil {
		return nil, err
	}
	var out []Radio
	for _, a := range qr.List {
		name := a.DeviceName
		if name == "" {
			name = a.ApMac
		}
		if a.Channel24G != nil {
			out = append(out, Radio{APName: name, Band: "2.4", Channel: a.Channel24G, ChannelWidth: a.ChannelWidth24, ClientCount: deref32(a.NumClients24G)})
		}
		if a.Channel5G != nil {
			out = append(out, Radio{APName: name, Band: "5", Channel: a.Channel5G, ChannelWidth: a.ChannelWidth5, ClientCount: deref32(a.NumClients5G)})
		}
		if a.Channel6G != nil {
			out = append(out, Radio{APName: name, Band: "6", Channel: a.Channel6G, ChannelWidth: a.ChannelWidth6, ClientCount: deref32(a.NumClients6G)})
		}
	}
	return out, nil
}

func deref32(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

// ---- Events / alarms (POST /query/event) -----------------------------------

// Event is one SmartZone event/alarm.
type Event struct {
	AtMillis int64
	Category string
	Severity string
	Message  string
}

type szEvent struct {
	Time        int64  `json:"timestamp"` // epoch ms
	Category    string `json:"category"`
	Severity    string `json:"severity"`
	Description string `json:"description"`
	Activity    string `json:"activity"`
}

type eventQueryResp struct {
	TotalCount int       `json:"totalCount"`
	List       []szEvent `json:"list"`
}

// ListEvents returns recent controller events via the query endpoint.
func (c *Client) ListEvents(ctx context.Context) ([]Event, error) {
	raw, err := c.query(ctx, "/query/event")
	if err != nil {
		return nil, err
	}
	return parseEvents(raw)
}

func parseEvents(raw []byte) ([]Event, error) {
	var qr eventQueryResp
	if err := json.Unmarshal(raw, &qr); err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(qr.List))
	for _, e := range qr.List {
		msg := e.Description
		if msg == "" {
			msg = e.Activity
		}
		out = append(out, Event{AtMillis: e.Time, Category: e.Category, Severity: e.Severity, Message: msg})
	}
	return out, nil
}

// ---- Controller / API health (GET {apiBase}/controller) --------------------

// ControllerHealth is the controller node summary (health + firmware version).
type ControllerHealth struct {
	Model   string
	Serial  string
	Version string
	Host    string
	Nodes   int
}

type szControllerResp struct {
	TotalCount int `json:"totalCount"`
	List       []struct {
		Model        string `json:"model"`
		SerialNumber string `json:"serialNumber"`
		Version      string `json:"version"`
		HostName     string `json:"hostName"`
	} `json:"list"`
}

// GetControllerHealth fetches the controller node summary, proving the API is
// healthy and surfacing the firmware version. Returns ok=false if the endpoint
// is not exposed on this firmware (honest endpoint-level health).
func (c *Client) GetControllerHealth(ctx context.Context) (ControllerHealth, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+c.APIBase+"/controller", nil)
	if err != nil {
		return ControllerHealth{}, false, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.Doer.Do(req)
	if err != nil {
		return ControllerHealth{}, false, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode == http.StatusNotFound {
		return ControllerHealth{}, false, nil // endpoint not exposed on this firmware
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ControllerHealth{}, false, fmt.Errorf("ruckus: controller → %d", resp.StatusCode)
	}
	return parseController(raw)
}

func parseController(raw []byte) (ControllerHealth, bool, error) {
	var cr szControllerResp
	if err := json.Unmarshal(raw, &cr); err != nil {
		return ControllerHealth{}, false, err
	}
	h := ControllerHealth{Nodes: len(cr.List)}
	if len(cr.List) > 0 {
		n := cr.List[0]
		h.Model, h.Serial, h.Version, h.Host = n.Model, n.SerialNumber, n.Version, n.HostName
	}
	return h, len(cr.List) > 0, nil
}

// ---- API version detection (GET /wsg/api/public/apiInfo) -------------------

type apiInfoResp struct {
	APISupportVersions []string `json:"apiSupportVersions"`
}

// DetectAPIBase queries the version-independent apiInfo endpoint and returns the
// newest supported public-API base path (e.g. "/wsg/api/public/v11_0"), so the
// driver is NOT pinned to a single hardcoded SmartZone API version. Returns ""
// (and no error) when apiInfo is unavailable — the caller keeps its configured
// api_base.
func (c *Client) DetectAPIBase(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/wsg/api/public/apiInfo", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.Doer.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil
	}
	return newestAPIBase(raw), nil
}

// newestAPIBase picks the highest vMAJOR_MINOR from an apiInfo payload.
func newestAPIBase(raw []byte) string {
	var ai apiInfoResp
	if err := json.Unmarshal(raw, &ai); err != nil {
		return ""
	}
	best, bestMaj, bestMin := "", -1, -1
	for _, v := range ai.APISupportVersions {
		maj, min := parseVer(v)
		if maj > bestMaj || (maj == bestMaj && min > bestMin) {
			best, bestMaj, bestMin = v, maj, min
		}
	}
	if best == "" {
		return ""
	}
	return "/wsg/api/public/" + best
}

// parseVer parses "v9_1" / "v11_0" into (major, minor); (-1,-1) when unparseable.
func parseVer(v string) (int, int) {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, "_", 2)
	maj, err := strconv.Atoi(parts[0])
	if err != nil {
		return -1, -1
	}
	min := 0
	if len(parts) == 2 {
		min, _ = strconv.Atoi(parts[1])
	}
	return maj, min
}
