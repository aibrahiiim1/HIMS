// Package omada is a dependency-light client for TP-Link Omada SDN
// controllers. It logs in (token), lists the site's devices, and returns the
// access points. As with the UniFi client, the device-list JSON parsing is
// the testable core (injectable Doer); the token/CSRF flow is the
// live-validation-pending transport.
//
// Live-validation trigger: Omada's login + controller-id + CSRF-token flow
// varies across controller versions (v4 vs v5/OC200); validate against a real
// controller once a credential is bound. The device-list shape below follows
// the documented Omada Open API v2.
package omada

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Doer performs an HTTP request (a cookie-jar *http.Client in production).
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client targets one Omada controller + site.
type Client struct {
	BaseURL      string // https://host:8043
	ControllerID string // from /api/info (Omada-CID)
	Site         string
	Username     string
	Password     string
	Doer         Doer
	token        string
}

// NewClient builds a Client. Site defaults to "Default".
func NewClient(baseURL, controllerID, site, user, pass string, doer Doer) *Client {
	if site == "" {
		site = "Default"
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"), ControllerID: controllerID, Site: site,
		Username: user, Password: pass, Doer: doer,
	}
}

type loginResp struct {
	ErrorCode int `json:"errorCode"`
	Result    struct {
		Token string `json:"token"`
	} `json:"result"`
}

// Login authenticates and captures the session token (sent as Csrf-Token).
func (c *Client) Login(ctx context.Context) error {
	body, _ := json.Marshal(map[string]string{"username": c.Username, "password": c.Password})
	url := c.BaseURL + "/" + c.ControllerID + "/api/v2/login"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Doer.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("omada: login → %d", resp.StatusCode)
	}
	var lr loginResp
	if err := json.Unmarshal(raw, &lr); err != nil {
		return err
	}
	if lr.ErrorCode != 0 || lr.Result.Token == "" {
		return fmt.Errorf("omada: login errorCode %d", lr.ErrorCode)
	}
	c.token = lr.Result.Token
	return nil
}

// AP is one access point from the Omada device list.
type AP struct {
	Name        string
	Model       string
	MAC         string
	IP          string
	Status      string // online | offline
	ClientCount int32
}

type omadaDevice struct {
	Type      string `json:"type"` // "ap" | "switch" | "gateway"
	Name      string `json:"name"`
	Model     string `json:"model"`
	Mac       string `json:"mac"`
	IP        string `json:"ip"`
	Status    int    `json:"status"` // 0 disconnected, ≥1 connected/provisioning
	ClientNum int32  `json:"clientNum"`
}

type deviceResp struct {
	ErrorCode int           `json:"errorCode"`
	Result    []omadaDevice `json:"result"`
}

// ListAPs fetches the site's devices and returns the access points.
func (c *Client) ListAPs(ctx context.Context) ([]AP, error) {
	url := c.BaseURL + "/" + c.ControllerID + "/api/v2/sites/" + c.Site + "/devices"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Csrf-Token", c.token)
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
		return nil, fmt.Errorf("omada: devices → %d", resp.StatusCode)
	}
	return parseDevices(raw)
}

func parseDevices(raw []byte) ([]AP, error) {
	var dr deviceResp
	if err := json.Unmarshal(raw, &dr); err != nil {
		return nil, err
	}
	if dr.ErrorCode != 0 {
		return nil, fmt.Errorf("omada: devices errorCode %d", dr.ErrorCode)
	}
	var aps []AP
	for _, d := range dr.Result {
		if d.Type != "ap" {
			continue
		}
		status := "offline"
		if d.Status >= 1 {
			status = "online"
		}
		aps = append(aps, AP{Name: d.Name, Model: d.Model, MAC: d.Mac, IP: d.IP, Status: status, ClientCount: d.ClientNum})
	}
	return aps, nil
}

// ---- SSIDs / WLANs ---------------------------------------------------------

// SSID is one configured WLAN.
type SSID struct {
	Name     string
	Enabled  bool
	Security string // open | wpa-psk | wpa-enterprise (NEVER the key)
	Band     string // 2.4 | 5 | 6 | dual
	VLAN     string
}

type omadaSSID struct {
	Name        string `json:"name"`
	WlanID      string `json:"wlanId"`
	Security    int    `json:"security"` // 0 open, 1 wep, 2 wpa-psk, 3 wpa-enterprise
	Band        int    `json:"band"`     // bitmask: 1=2.4, 2=5, 4=6
	VlanEnable  bool   `json:"vlanEnable"`
	VlanID      int    `json:"vlanId"`
	GuestEnable bool   `json:"guestNetEnable"`
	WlanStatus  *bool  `json:"wlanScheduleEnable"` // best-effort enabled hint
}

type ssidResp struct {
	ErrorCode int `json:"errorCode"`
	Result    struct {
		Data []omadaSSID `json:"data"`
	} `json:"result"`
}

// ListSSIDs returns the site's WLANs. The pre-shared key is never requested.
func (c *Client) ListSSIDs(ctx context.Context) ([]SSID, error) {
	url := c.BaseURL + "/" + c.ControllerID + "/api/v2/sites/" + c.Site + "/setting/wlans/ssids"
	raw, err := c.get(ctx, url)
	if err != nil {
		return nil, err
	}
	return parseSSIDs(raw)
}

func parseSSIDs(raw []byte) ([]SSID, error) {
	var sr ssidResp
	if err := json.Unmarshal(raw, &sr); err != nil {
		return nil, err
	}
	if sr.ErrorCode != 0 {
		return nil, fmt.Errorf("omada: ssids errorCode %d", sr.ErrorCode)
	}
	out := make([]SSID, 0, len(sr.Result.Data))
	for _, s := range sr.Result.Data {
		out = append(out, SSID{
			Name: s.Name, Enabled: true, Security: omadaSecurity(s.Security),
			Band: omadaBand(s.Band), VLAN: omadaVLAN(s.VlanEnable, s.VlanID),
		})
	}
	return out, nil
}

// ---- Clients ---------------------------------------------------------------

// Station is one associated wireless client.
type Station struct {
	MAC      string
	IP       string
	Hostname string
	SSID     string
	APName   string
	RSSI     *int32
	SNR      *int32
	RxBytes  *int64
	TxBytes  *int64
	Band     string
}

type omadaClient struct {
	Mac         string `json:"mac"`
	IP          string `json:"ip"`
	Name        string `json:"name"`
	HostName    string `json:"hostName"`
	SSID        string `json:"ssid"`
	ApName      string `json:"apName"`
	Wireless    bool   `json:"wireless"`
	Rssi        *int32 `json:"rssi"`
	SignalLevel *int32 `json:"signalLevel"`
	SignalRank  *int32 `json:"signalRank"`
	TrafficDown *int64 `json:"trafficDown"`
	TrafficUp   *int64 `json:"trafficUp"`
	WifiMode    int    `json:"wifiMode"`
	RadioID     int    `json:"radioId"` // 0=2.4, 1=5, 2=6
}

type clientResp struct {
	ErrorCode int `json:"errorCode"`
	Result    struct {
		Data []omadaClient `json:"data"`
	} `json:"result"`
}

// ListClients returns associated wireless clients (paginated; one large page).
func (c *Client) ListClients(ctx context.Context) ([]Station, error) {
	url := c.BaseURL + "/" + c.ControllerID + "/api/v2/sites/" + c.Site + "/clients?currentPage=1&currentPageSize=1000&filters.active=true"
	raw, err := c.get(ctx, url)
	if err != nil {
		return nil, err
	}
	return parseClients(raw)
}

func parseClients(raw []byte) ([]Station, error) {
	var cr clientResp
	if err := json.Unmarshal(raw, &cr); err != nil {
		return nil, err
	}
	if cr.ErrorCode != 0 {
		return nil, fmt.Errorf("omada: clients errorCode %d", cr.ErrorCode)
	}
	var out []Station
	for _, c := range cr.Result.Data {
		if !c.Wireless {
			continue
		}
		host := c.HostName
		if host == "" {
			host = c.Name
		}
		rssi := c.Rssi
		if rssi == nil {
			rssi = c.SignalLevel
		}
		out = append(out, Station{
			MAC: c.Mac, IP: c.IP, Hostname: host, SSID: c.SSID, APName: c.ApName,
			RSSI: rssi, RxBytes: c.TrafficDown, TxBytes: c.TrafficUp, Band: omadaRadioBand(c.RadioID),
		})
	}
	return out, nil
}

// ---- Radios (per-AP detail GET /eaps/{mac}) --------------------------------

// Radio is one AP radio's live state.
type Radio struct {
	APName       string
	Band         string // 2.4 | 5 | 6
	Channel      *int32
	Power        *int32
	ChannelWidth string
	Clients      int32
}

type radioDetailResp struct {
	ErrorCode int `json:"errorCode"`
	Result    struct {
		Name      string `json:"name"`
		RadioList []struct {
			RadioID   int    `json:"radioId"` // 0=2.4,1=5,2=6
			Channel   *int32 `json:"channel"`
			TxPower   *int32 `json:"txPower"`
			ClientNum int32  `json:"clientNum"`
			BandWidth string `json:"bandWidth"`
		} `json:"radioList"`
	} `json:"result"`
}

// ListRadios returns per-AP radios by calling the per-AP detail endpoint for each
// MAC (Omada exposes radio state only on the device detail). macToName maps each
// AP MAC to its display name. Bounded to maxAPs detail calls to keep collection
// snappy on large fleets (the overflow is reported by the caller).
func (c *Client) ListRadios(ctx context.Context, macToName map[string]string, maxAPs int) ([]Radio, error) {
	var out []Radio
	n := 0
	for mac, name := range macToName {
		if maxAPs > 0 && n >= maxAPs {
			break
		}
		n++
		url := c.BaseURL + "/" + c.ControllerID + "/api/v2/sites/" + c.Site + "/eaps/" + mac
		raw, err := c.get(ctx, url)
		if err != nil {
			continue // skip an AP whose detail failed; never abort the whole roster
		}
		out = append(out, parseRadioDetail(raw, name)...)
	}
	return out, nil
}

func parseRadioDetail(raw []byte, apName string) []Radio {
	var rd radioDetailResp
	if err := json.Unmarshal(raw, &rd); err != nil || rd.ErrorCode != 0 {
		return nil
	}
	name := apName
	if name == "" {
		name = rd.Result.Name
	}
	var out []Radio
	for _, r := range rd.Result.RadioList {
		out = append(out, Radio{
			APName: name, Band: omadaRadioBand(r.RadioID), Channel: r.Channel,
			Power: r.TxPower, ChannelWidth: r.BandWidth, Clients: r.ClientNum,
		})
	}
	return out
}

// ---- Controller info / firmware / health (GET /api/info) -------------------

type infoResp struct {
	ErrorCode int `json:"errorCode"`
	Result    struct {
		ControllerVer string `json:"controllerVer"`
		OmadacID      string `json:"omadacId"`
	} `json:"result"`
}

// ControllerVersion returns the controller firmware version from the pre-auth
// /api/info endpoint (also proves controller/API health). Empty when not exposed.
func (c *Client) ControllerVersion(ctx context.Context) (string, error) {
	raw, err := c.get(ctx, c.BaseURL+"/api/info")
	if err != nil {
		return "", err
	}
	return parseInfo(raw), nil
}

func parseInfo(raw []byte) string {
	var ir infoResp
	if err := json.Unmarshal(raw, &ir); err != nil || ir.ErrorCode != 0 {
		return ""
	}
	return ir.Result.ControllerVer
}

// ---- Events / alerts -------------------------------------------------------

// Event is one controller alert/event.
type Event struct {
	AtMillis int64
	Level    string
	Message  string
}

type alertResp struct {
	ErrorCode int `json:"errorCode"`
	Result    struct {
		Data []struct {
			Time    int64  `json:"time"` // epoch ms
			Level   string `json:"level"`
			Content string `json:"content"`
			Msg     string `json:"msg"`
		} `json:"data"`
	} `json:"result"`
}

// ListEvents returns recent site alerts/events.
func (c *Client) ListEvents(ctx context.Context) ([]Event, error) {
	url := c.BaseURL + "/" + c.ControllerID + "/api/v2/sites/" + c.Site + "/alerts?currentPage=1&currentPageSize=100"
	raw, err := c.get(ctx, url)
	if err != nil {
		return nil, err
	}
	return parseAlerts(raw)
}

func parseAlerts(raw []byte) ([]Event, error) {
	var ar alertResp
	if err := json.Unmarshal(raw, &ar); err != nil {
		return nil, err
	}
	if ar.ErrorCode != 0 {
		return nil, fmt.Errorf("omada: alerts errorCode %d", ar.ErrorCode)
	}
	out := make([]Event, 0, len(ar.Result.Data))
	for _, a := range ar.Result.Data {
		msg := a.Content
		if msg == "" {
			msg = a.Msg
		}
		out = append(out, Event{AtMillis: a.Time, Level: a.Level, Message: msg})
	}
	return out, nil
}

// get performs an authenticated GET with the CSRF token, returning the body.
func (c *Client) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Csrf-Token", c.token)
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
		return nil, fmt.Errorf("omada: GET → %d", resp.StatusCode)
	}
	return raw, nil
}

func omadaSecurity(s int) string {
	switch s {
	case 0:
		return "open"
	case 1:
		return "wep"
	case 2:
		return "wpa-psk"
	case 3:
		return "wpa-enterprise"
	default:
		return ""
	}
}

// omadaBand decodes the band bitmask (1=2.4, 2=5, 4=6) to a normalized label.
func omadaBand(mask int) string {
	var b []string
	if mask&1 != 0 {
		b = append(b, "2.4")
	}
	if mask&2 != 0 {
		b = append(b, "5")
	}
	if mask&4 != 0 {
		b = append(b, "6")
	}
	switch len(b) {
	case 0:
		return ""
	case 1:
		return b[0]
	default:
		return "dual"
	}
}

func omadaRadioBand(id int) string {
	switch id {
	case 0:
		return "2.4"
	case 1:
		return "5"
	case 2:
		return "6"
	default:
		return ""
	}
}

func omadaVLAN(enabled bool, id int) string {
	if enabled && id > 0 {
		return fmt.Sprintf("%d", id)
	}
	return ""
}
