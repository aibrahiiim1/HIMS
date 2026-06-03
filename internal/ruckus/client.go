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
