// Package redfish is a dependency-free Redfish (DMTF) client + collector for
// out-of-band server management controllers — HPE iLO and Dell iDRAC. It
// speaks plain HTTP/JSON with HTTP Basic auth over an injectable Doer, so the
// collector is fully unit-testable against sample payloads with no real BMC,
// and the transport is reusable by future vendor-REST drivers
// (UniFi/Omada/Ruckus).
//
// Live-validation trigger: the field shapes below are taken from the DMTF
// Redfish schema + published HPE/Dell examples; validate against a real iLO 5
// and iDRAC 9 once a BMC credential is bound (see PROGRESS Redfish phase).
package redfish

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Doer performs an HTTP request. *http.Client satisfies it; tests inject a
// fake that routes paths to sample payloads.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client talks to one Redfish service. BaseURL is the scheme+host (e.g.
// "https://10.0.0.50"); paths are absolute Redfish paths ("/redfish/v1/...").
type Client struct {
	BaseURL  string
	Username string
	Password string
	Doer     Doer
}

// NewClient builds a Client. A nil doer uses an http.Client that accepts the
// self-signed certs BMCs ship with (BMCs are reached over the mgmt LAN; this
// matches every Redfish tool's default) and a short timeout.
func NewClient(baseURL, user, pass string, doer Doer) *Client {
	if doer == nil {
		doer = &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // BMC self-signed certs on mgmt LAN
			},
		}
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Username: user, Password: pass, Doer: doer}
}

// GetJSON fetches a Redfish path and decodes it into v. A non-2xx is an error.
func (c *Client) GetJSON(ctx context.Context, path string, v any) error {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if c.Username != "" {
		req.SetBasicAuth(c.Username, c.Password) // credentials never logged
	}
	resp, err := c.Doer.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // 4MiB cap
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("redfish: GET %s → %d", path, resp.StatusCode)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("redfish: decode %s: %w", path, err)
	}
	return nil
}
