// Package arubacentral is a dependency-light client for the Aruba Central cloud
// monitoring API (the ArubaOS 10 / cloud-managed path, distinct from the on-prem
// ArubaOS 8 Mobility Controller showcommand API in internal/aruba). It
// authenticates with an OAuth2 bearer access token against a regional API
// gateway and reads AP / client / network inventory. The JSON parsing is the
// testable core (injectable Doer), so inventory works against sample payloads
// with no real tenant.
//
// Live-validation trigger: the Central API gateway host is region-specific and
// the OAuth2 token lifecycle (access/refresh, or a static download token) varies
// by deployment; validate against a real tenant once a token credential is
// bound. The struct field tags below follow the documented Central Monitoring
// API (/monitoring/v2/aps, /monitoring/v1/clients/wireless, /monitoring/v2/networks).
package arubacentral

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Doer performs an HTTP request.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client targets one Aruba Central tenant via its regional API gateway. The
// bearer token IS the credential (Central has no username/password login at this
// layer — the token is minted out-of-band in the Central UI / OAuth2 flow).
type Client struct {
	BaseURL string // https://apigw-prod2.central.arubanetworks.com
	Token   string // OAuth2 access token (bearer)
	Doer    Doer
}

// NewClient builds a Client.
func NewClient(baseURL, token string, doer Doer) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Token: token, Doer: doer}
}

// Ping validates the token + gateway by issuing the cheapest monitoring call.
// Returns the AP count so Test Connection can report it.
func (c *Client) Ping(ctx context.Context) (int, error) {
	aps, err := c.ListAPs(ctx)
	if err != nil {
		return 0, err
	}
	return len(aps), nil
}

// AP is one access point from /monitoring/v2/aps.
type AP struct {
	Name     string
	Model    string
	MAC      string
	IP       string
	Status   string // online | offline
	Serial   string
	Firmware string
}

type apResp struct {
	APs []struct {
		Name     string `json:"name"`
		Model    string `json:"model"`
		Macaddr  string `json:"macaddr"`
		IP       string `json:"ip_address"`
		Status   string `json:"status"` // "Up" | "Down"
		Serial   string `json:"serial"`
		Firmware string `json:"firmware_version"`
		Radios   []struct {
			Band    string `json:"band"` // "2.4GHz" | "5GHz" | "6GHz"
			Channel *int32 `json:"channel"`
			TxPower *int32 `json:"tx_power"`
			Clients int32  `json:"client_count"`
			ChWidth string `json:"channel_width"`
		} `json:"radios"`
	} `json:"aps"`
}

// ListAPs returns the tenant's access points.
func (c *Client) ListAPs(ctx context.Context) ([]AP, error) {
	raw, err := c.get(ctx, "/monitoring/v2/aps")
	if err != nil {
		return nil, err
	}
	return parseAPs(raw)
}

func parseAPs(raw []byte) ([]AP, error) {
	var r apResp
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	out := make([]AP, 0, len(r.APs))
	for _, a := range r.APs {
		out = append(out, AP{Name: a.Name, Model: a.Model, MAC: a.Macaddr, IP: a.IP, Status: centralStatus(a.Status), Serial: a.Serial, Firmware: a.Firmware})
	}
	return out, nil
}

// ---- Radios (from the /monitoring/v2/aps "radios" array) -------------------

// Radio is one AP radio's live state.
type Radio struct {
	APName       string
	Band         string // 2.4 | 5 | 6
	Channel      *int32
	Power        *int32
	ChannelWidth string
	Clients      int32
}

// ListRadios returns per-AP, per-band radios parsed from the AP monitoring feed
// (one call — the same /monitoring/v2/aps payload that lists APs).
func (c *Client) ListRadios(ctx context.Context) ([]Radio, error) {
	raw, err := c.get(ctx, "/monitoring/v2/aps")
	if err != nil {
		return nil, err
	}
	return parseRadios(raw)
}

func parseRadios(raw []byte) ([]Radio, error) {
	var r apResp
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	var out []Radio
	for _, a := range r.APs {
		for _, rd := range a.Radios {
			out = append(out, Radio{
				APName: a.Name, Band: centralBand(rd.Band), Channel: rd.Channel,
				Power: rd.TxPower, ChannelWidth: rd.ChWidth, Clients: rd.Clients,
			})
		}
	}
	return out, nil
}

// ---- Events / alerts (/central/v1/alerts) ----------------------------------

// Event is one Central alert/notification.
type Event struct {
	AtMillis int64
	Severity string
	Message  string
}

type alertResp struct {
	Alerts []struct {
		Timestamp   int64  `json:"timestamp"` // epoch (seconds or ms)
		Severity    string `json:"severity"`
		Description string `json:"description"`
	} `json:"alerts"`
}

// ListEvents returns recent Central alerts.
func (c *Client) ListEvents(ctx context.Context) ([]Event, error) {
	raw, err := c.get(ctx, "/central/v1/alerts")
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
	out := make([]Event, 0, len(ar.Alerts))
	for _, a := range ar.Alerts {
		ms := a.Timestamp
		if ms < 1_000_000_000_000 { // looks like seconds → to ms
			ms *= 1000
		}
		out = append(out, Event{AtMillis: ms, Severity: a.Severity, Message: a.Description})
	}
	return out, nil
}

// mostCommonFirmware returns the firmware most APs report, as a controller-level
// version proxy (Central manages a fleet, not a single controller node).
func mostCommonFirmware(aps []AP) string {
	counts := map[string]int{}
	best, bestN := "", 0
	for _, a := range aps {
		if a.Firmware == "" {
			continue
		}
		counts[a.Firmware]++
		if counts[a.Firmware] > bestN {
			best, bestN = a.Firmware, counts[a.Firmware]
		}
	}
	return best
}

// MostCommonFirmware is the exported fleet-firmware helper for collectors.
func MostCommonFirmware(aps []AP) string { return mostCommonFirmware(aps) }

// Station is one associated wireless client from /monitoring/v1/clients/wireless.
type Station struct {
	MAC      string
	IP       string
	Hostname string
	SSID     string
	APName   string
	Band     string
	RSSI     *int32
}

type clientResp struct {
	Clients []struct {
		Macaddr          string `json:"macaddr"`
		Name             string `json:"name"`
		IP               string `json:"ip_address"`
		Network          string `json:"network"` // SSID
		AssociatedDevice string `json:"associated_device_name"`
		RadioBand        string `json:"band"` // "2.4" | "5" | "6"
		SNR              *int32 `json:"snr"`
		Signal           *int32 `json:"signal_db"`
	} `json:"clients"`
}

// ListClients returns associated wireless clients.
func (c *Client) ListClients(ctx context.Context) ([]Station, error) {
	raw, err := c.get(ctx, "/monitoring/v1/clients/wireless")
	if err != nil {
		return nil, err
	}
	return parseClients(raw)
}

func parseClients(raw []byte) ([]Station, error) {
	var r clientResp
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	out := make([]Station, 0, len(r.Clients))
	for _, c := range r.Clients {
		out = append(out, Station{
			MAC: c.Macaddr, IP: c.IP, Hostname: c.Name, SSID: c.Network,
			APName: c.AssociatedDevice, Band: centralBand(c.RadioBand), RSSI: c.Signal,
		})
	}
	return out, nil
}

// SSID is one WLAN from /monitoring/v2/networks.
type SSID struct {
	Name     string
	Enabled  bool
	Security string
	Band     string
}

type networkResp struct {
	Networks []struct {
		ESSID    string `json:"essid"`
		Security string `json:"security"`
		Type     string `json:"type"`
		Band     string `json:"band"`
		Enabled  *bool  `json:"enabled"`
	} `json:"networks"`
}

// ListSSIDs returns the tenant's WLANs.
func (c *Client) ListSSIDs(ctx context.Context) ([]SSID, error) {
	raw, err := c.get(ctx, "/monitoring/v2/networks")
	if err != nil {
		return nil, err
	}
	return parseNetworks(raw)
}

func parseNetworks(raw []byte) ([]SSID, error) {
	var r networkResp
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	out := make([]SSID, 0, len(r.Networks))
	for _, n := range r.Networks {
		enabled := true
		if n.Enabled != nil {
			enabled = *n.Enabled
		}
		out = append(out, SSID{Name: n.ESSID, Enabled: enabled, Security: n.Security, Band: centralBand(n.Band)})
	}
	return out, nil
}

// get performs an authenticated bearer GET and returns the body, enforcing 2xx.
func (c *Client) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
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
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("aruba central: %s → %d (token invalid/expired)", path, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("aruba central: %s → %d", path, resp.StatusCode)
	}
	return raw, nil
}

func centralStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "up", "online":
		return "online"
	case "down", "offline":
		return "offline"
	default:
		return "unknown"
	}
}

func centralBand(b string) string {
	b = strings.TrimSpace(b)
	switch {
	case strings.HasPrefix(b, "2.4"), b == "2":
		return "2.4"
	case strings.HasPrefix(b, "5"):
		return "5"
	case strings.HasPrefix(b, "6"):
		return "6"
	default:
		return ""
	}
}
