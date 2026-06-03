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
