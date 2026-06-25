// Package unifi is a dependency-light client for Ubiquiti UniFi Network
// controllers. It logs in and lists devices over the controller REST API
// using an injectable Doer (a cookie-jar http.Client in production), so the
// device-list JSON parsing — the part that actually matters — is unit-testable
// against sample payloads with no real controller.
//
// Live-validation trigger: the field shapes follow the documented UniFi
// controller API (/api/login + /api/s/<site>/stat/device); validate against a
// real controller once a credential is bound. Omada and Ruckus use different
// APIs and are deferred to their own phases.
package unifi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Doer performs an HTTP request. A cookie-jar *http.Client fits (the UniFi
// login sets a session cookie the subsequent calls reuse).
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client targets one UniFi controller + site.
type Client struct {
	BaseURL  string
	Site     string
	Username string
	Password string
	Doer     Doer
}

// NewClient builds a Client. Site defaults to "default".
func NewClient(baseURL, site, user, pass string, doer Doer) *Client {
	if site == "" {
		site = "default"
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Site: site, Username: user, Password: pass, Doer: doer}
}

// Login authenticates; the Doer's cookie jar carries the session afterward.
func (c *Client) Login(ctx context.Context) error {
	body, _ := json.Marshal(map[string]string{"username": c.Username, "password": c.Password})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/login", bytes.NewReader(body))
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
		return fmt.Errorf("unifi: login → %d", resp.StatusCode)
	}
	return nil
}

// AP is one access point from the controller's device list.
type AP struct {
	Name        string
	Model       string
	MAC         string
	IP          string
	Status      string // online | offline
	ClientCount int32
}

// device mirrors the relevant /stat/device fields.
type device struct {
	Type   string `json:"type"` // "uap" = access point
	Name   string `json:"name"`
	Model  string `json:"model"`
	Mac    string `json:"mac"`
	IP     string `json:"ip"`
	State  int    `json:"state"`   // 1 = connected
	NumSta int32  `json:"num_sta"` // associated clients
}

type deviceResp struct {
	Data []device `json:"data"`
}

// ListAPs fetches the controller's devices and returns the access points.
func (c *Client) ListAPs(ctx context.Context) ([]AP, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/s/"+c.Site+"/stat/device", nil)
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
		return nil, fmt.Errorf("unifi: stat/device → %d", resp.StatusCode)
	}
	return parseDevices(raw)
}

func parseDevices(raw []byte) ([]AP, error) {
	var dr deviceResp
	if err := json.Unmarshal(raw, &dr); err != nil {
		return nil, err
	}
	var aps []AP
	for _, d := range dr.Data {
		if d.Type != "uap" { // only access points
			continue
		}
		status := "offline"
		if d.State == 1 {
			status = "online"
		}
		aps = append(aps, AP{
			Name: d.Name, Model: d.Model, MAC: d.Mac, IP: d.IP,
			Status: status, ClientCount: d.NumSta,
		})
	}
	return aps, nil
}

// ---- SSIDs (rest/wlanconf) -------------------------------------------------

// SSID is one configured WLAN.
type SSID struct {
	Name     string
	Enabled  bool
	Security string // open | wpapsk | wpaeap (NEVER the passphrase)
	Band     string // 2.4 | 5 | 6 | dual
	VLAN     string
}

type wlanConf struct {
	Name        string `json:"name"`
	Enabled     bool   `json:"enabled"`
	Security    string `json:"security"`  // "open" | "wpapsk" | "wpaeap"
	WPAMode     string `json:"wpa_mode"`  // e.g. "wpa2"
	WLANBand    string `json:"wlan_band"` // "both" | "2g" | "5g" | "6g"
	VLAN        int    `json:"vlan"`
	VLANEnabled bool   `json:"vlan_enabled"`
}

type wlanConfResp struct {
	Data []wlanConf `json:"data"`
}

// ListSSIDs returns the controller's configured WLANs. The passphrase is never
// requested or surfaced — only name/enabled/security/band/vlan.
func (c *Client) ListSSIDs(ctx context.Context) ([]SSID, error) {
	raw, err := c.get(ctx, "/api/s/"+c.Site+"/rest/wlanconf")
	if err != nil {
		return nil, err
	}
	return parseWLANConf(raw)
}

func parseWLANConf(raw []byte) ([]SSID, error) {
	var wr wlanConfResp
	if err := json.Unmarshal(raw, &wr); err != nil {
		return nil, err
	}
	out := make([]SSID, 0, len(wr.Data))
	for _, w := range wr.Data {
		vlan := ""
		if w.VLANEnabled && w.VLAN > 0 {
			vlan = fmt.Sprintf("%d", w.VLAN)
		}
		out = append(out, SSID{Name: w.Name, Enabled: w.Enabled, Security: w.Security, Band: unifiBand(w.WLANBand), VLAN: vlan})
	}
	return out, nil
}

// ---- Clients (stat/sta) ----------------------------------------------------

// Client (station) is one associated wireless client.
type Station struct {
	MAC      string
	IP       string
	Hostname string
	SSID     string
	APMac    string
	RSSI     *int32
	SNR      *int32
	RxBytes  *int64
	TxBytes  *int64
	Band     string
}

type sta struct {
	Mac      string `json:"mac"`
	IP       string `json:"ip"`
	Hostname string `json:"hostname"`
	Name     string `json:"name"`
	ESSID    string `json:"essid"`
	APMac    string `json:"ap_mac"`
	RSSI     *int32 `json:"rssi"`
	Signal   *int32 `json:"signal"`
	Noise    *int32 `json:"noise"`
	RxBytes  *int64 `json:"rx_bytes"`
	TxBytes  *int64 `json:"tx_bytes"`
	Radio    string `json:"radio"` // "ng" | "na" | "6e"
	Channel  *int32 `json:"channel"`
}

type staResp struct {
	Data []sta `json:"data"`
}

// ListClients returns associated wireless stations.
func (c *Client) ListClients(ctx context.Context) ([]Station, error) {
	raw, err := c.get(ctx, "/api/s/"+c.Site+"/stat/sta")
	if err != nil {
		return nil, err
	}
	return parseStations(raw)
}

func parseStations(raw []byte) ([]Station, error) {
	var sr staResp
	if err := json.Unmarshal(raw, &sr); err != nil {
		return nil, err
	}
	out := make([]Station, 0, len(sr.Data))
	for _, s := range sr.Data {
		host := s.Hostname
		if host == "" {
			host = s.Name
		}
		// UniFi reports signal (dBm) and noise; SNR = signal − noise when both exist.
		var snr *int32
		if s.Signal != nil && s.Noise != nil {
			v := *s.Signal - *s.Noise
			snr = &v
		}
		rssi := s.RSSI
		if rssi == nil {
			rssi = s.Signal
		}
		out = append(out, Station{
			MAC: s.Mac, IP: s.IP, Hostname: host, SSID: s.ESSID, APMac: s.APMac,
			RSSI: rssi, SNR: snr, RxBytes: s.RxBytes, TxBytes: s.TxBytes, Band: unifiRadioBand(s.Radio),
		})
	}
	return out, nil
}

// ---- Radios (stat/device → radio_table_stats) ------------------------------

// Radio is one AP radio's live state.
type Radio struct {
	APName      string
	APMac       string
	Radio       string // "ng" | "na" | "6e"
	Band        string // 2.4 | 5 | 6
	Channel     *int32
	PowerDbm    *int32
	ClientCount int32
}

type deviceRadios struct {
	Name            string `json:"name"`
	Mac             string `json:"mac"`
	Type            string `json:"type"`
	RadioTableStats []struct {
		Name    string `json:"name"`
		Radio   string `json:"radio"` // "ng" | "na" | "6e"
		Channel *int32 `json:"channel"`
		TxPower *int32 `json:"tx_power"`
		NumSta  int32  `json:"num_sta"`
	} `json:"radio_table_stats"`
}

type deviceRadiosResp struct {
	Data []deviceRadios `json:"data"`
}

// ListRadios returns per-AP radio state, parsed from the same device endpoint.
func (c *Client) ListRadios(ctx context.Context) ([]Radio, error) {
	raw, err := c.get(ctx, "/api/s/"+c.Site+"/stat/device")
	if err != nil {
		return nil, err
	}
	return parseRadios(raw)
}

func parseRadios(raw []byte) ([]Radio, error) {
	var dr deviceRadiosResp
	if err := json.Unmarshal(raw, &dr); err != nil {
		return nil, err
	}
	var out []Radio
	for _, d := range dr.Data {
		if d.Type != "uap" {
			continue
		}
		for _, rt := range d.RadioTableStats {
			out = append(out, Radio{
				APName: d.Name, APMac: d.Mac, Radio: rt.Radio, Band: unifiRadioBand(rt.Radio),
				Channel: rt.Channel, PowerDbm: rt.TxPower, ClientCount: rt.NumSta,
			})
		}
	}
	return out, nil
}

// ---- Firmware / controller version (stat/sysinfo) --------------------------

type sysinfoResp struct {
	Data []struct {
		Version string `json:"version"`
	} `json:"data"`
}

// Version returns the controller software version (empty when not exposed).
func (c *Client) Version(ctx context.Context) (string, error) {
	raw, err := c.get(ctx, "/api/s/"+c.Site+"/stat/sysinfo")
	if err != nil {
		return "", err
	}
	return parseSysinfo(raw), nil
}

func parseSysinfo(raw []byte) string {
	var sr sysinfoResp
	if err := json.Unmarshal(raw, &sr); err != nil {
		return ""
	}
	if len(sr.Data) > 0 {
		return sr.Data[0].Version
	}
	return ""
}

// ---- Subsystem health (stat/health) ----------------------------------------

// Subsystem is one controller subsystem's health (wlan/www/lan/…).
type Subsystem struct {
	Name    string
	Status  string
	NumAP   int32
	NumUser int32
}

type healthResp struct {
	Data []struct {
		Subsystem string `json:"subsystem"`
		Status    string `json:"status"`
		NumAP     int32  `json:"num_ap"`
		NumUser   int32  `json:"num_user"`
	} `json:"data"`
}

// Health returns per-subsystem health. ok=false when the endpoint is empty.
func (c *Client) Health(ctx context.Context) ([]Subsystem, error) {
	raw, err := c.get(ctx, "/api/s/"+c.Site+"/stat/health")
	if err != nil {
		return nil, err
	}
	return parseHealth(raw), nil
}

func parseHealth(raw []byte) []Subsystem {
	var hr healthResp
	if err := json.Unmarshal(raw, &hr); err != nil {
		return nil
	}
	out := make([]Subsystem, 0, len(hr.Data))
	for _, h := range hr.Data {
		out = append(out, Subsystem{Name: h.Subsystem, Status: h.Status, NumAP: h.NumAP, NumUser: h.NumUser})
	}
	return out
}

// ---- Events (stat/event) ---------------------------------------------------

// Event is one controller event/alarm.
type Event struct {
	AtMillis  int64
	Key       string
	Message   string
	Subsystem string
}

type eventResp struct {
	Data []struct {
		Time      int64  `json:"time"` // epoch ms
		Key       string `json:"key"`
		Msg       string `json:"msg"`
		Subsystem string `json:"subsystem"`
	} `json:"data"`
}

// ListEvents returns recent controller events.
func (c *Client) ListEvents(ctx context.Context) ([]Event, error) {
	raw, err := c.get(ctx, "/api/s/"+c.Site+"/stat/event")
	if err != nil {
		return nil, err
	}
	return parseEvents(raw), nil
}

func parseEvents(raw []byte) []Event {
	var er eventResp
	if err := json.Unmarshal(raw, &er); err != nil {
		return nil
	}
	out := make([]Event, 0, len(er.Data))
	for _, e := range er.Data {
		out = append(out, Event{AtMillis: e.Time, Key: e.Key, Message: e.Msg, Subsystem: e.Subsystem})
	}
	return out
}

// get performs an authenticated GET and returns the body, enforcing 2xx.
func (c *Client) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
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
		return nil, fmt.Errorf("unifi: GET %s → %d", path, resp.StatusCode)
	}
	return raw, nil
}

// unifiBand maps a wlanconf wlan_band to a normalized band label.
func unifiBand(b string) string {
	switch strings.ToLower(b) {
	case "2g":
		return "2.4"
	case "5g":
		return "5"
	case "6g", "6e":
		return "6"
	case "both":
		return "dual"
	default:
		return ""
	}
}

// unifiRadioBand maps a UniFi radio code (ng/na/6e) to a band label.
func unifiRadioBand(r string) string {
	switch strings.ToLower(r) {
	case "ng":
		return "2.4"
	case "na":
		return "5"
	case "6e", "ax6", "6g":
		return "6"
	default:
		return ""
	}
}
