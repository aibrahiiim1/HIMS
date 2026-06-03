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
