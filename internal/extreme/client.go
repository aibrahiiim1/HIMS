// Package extreme is a dependency-light client for ExtremeCloud IQ (XIQ), the
// cloud controller for Extreme Networks wireless APs. It logs in for a bearer
// token and lists devices; the device-list JSON parsing is the testable core
// (injectable Doer), the login/token flow is the live-validation-pending
// transport.
//
// Live-validation trigger: XIQ's base host (api.extremecloudiq.com), token
// lifetime, and device-list paging/field names vary by API revision; validate
// against a real XIQ tenant once a credential is bound. The shapes below follow
// the documented XIQ v1 REST API. On-prem ExtremeCloud IQ Controller (XCC) has
// a different surface and is deferred to its own phase.
package extreme

import (
	"bytes"
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

// Client targets one XIQ tenant. BaseURL defaults to the public cloud.
type Client struct {
	BaseURL  string // https://api.extremecloudiq.com
	Username string
	Password string
	Doer     Doer
	token    string
}

// NewClient builds a Client. baseURL defaults to the XIQ public cloud.
func NewClient(baseURL, user, pass string, doer Doer) *Client {
	if baseURL == "" {
		baseURL = "https://api.extremecloudiq.com"
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Username: user, Password: pass, Doer: doer}
}

type loginResp struct {
	AccessToken string `json:"access_token"`
}

// Login exchanges credentials for a bearer token held on the client.
func (c *Client) Login(ctx context.Context) error {
	body, _ := json.Marshal(map[string]string{"username": c.Username, "password": c.Password})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/login", bytes.NewReader(body))
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
		return fmt.Errorf("extreme: login → %d", resp.StatusCode)
	}
	var lr loginResp
	if err := json.Unmarshal(raw, &lr); err != nil {
		return err
	}
	if lr.AccessToken == "" {
		return fmt.Errorf("extreme: login returned no access_token")
	}
	c.token = lr.AccessToken // bearer token, never logged
	return nil
}

// AP is one access point from the XIQ device list.
type AP struct {
	Name        string
	Model       string
	MAC         string
	IP          string
	Status      string // online | offline
	ClientCount int32
}

type xiqDevice struct {
	Hostname       string `json:"hostname"`
	DeviceFunction string `json:"device_function"` // "AP" | "SWITCH" | …
	ProductType    string `json:"product_type"`
	MACAddress     string `json:"mac_address"`
	IPAddress      string `json:"ip_address"`
	Connected      bool   `json:"connected"`
	ActiveClients  int32  `json:"active_clients"`
}

type deviceListResp struct {
	Data       []xiqDevice `json:"data"`
	TotalCount int         `json:"total_count"`
}

// ListAPs fetches the tenant's devices and returns only the APs.
func (c *Client) ListAPs(ctx context.Context) ([]AP, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/devices?page=1&limit=100&views=FULL", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
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
		return nil, fmt.Errorf("extreme: devices → %d", resp.StatusCode)
	}
	return parseAPs(raw)
}

func parseAPs(raw []byte) ([]AP, error) {
	var lr deviceListResp
	if err := json.Unmarshal(raw, &lr); err != nil {
		return nil, err
	}
	aps := make([]AP, 0, len(lr.Data))
	for _, d := range lr.Data {
		if !strings.EqualFold(d.DeviceFunction, "AP") {
			continue
		}
		status := "offline"
		if d.Connected {
			status = "online"
		}
		aps = append(aps, AP{
			Name: d.Hostname, Model: d.ProductType, MAC: d.MACAddress,
			IP: d.IPAddress, Status: status, ClientCount: d.ActiveClients,
		})
	}
	return aps, nil
}
