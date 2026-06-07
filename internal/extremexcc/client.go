// Package extremexcc is a dependency-light client for the ON-PREM ExtremeCloud
// IQ Controller (XCC / VE6120) management REST API — distinct from the cloud XIQ
// client in internal/extreme. The exact API path + auth flow varies by firmware
// (10.x), and on a fresh appliance the GUI (/Admin/) is all that answers
// unauthenticated. So this package has two modes:
//
//   - Explore: a SAFE, operator-driven discovery that authenticates if it can,
//     detects the login method, and probes a small list of likely read-only API
//     endpoints, reporting status codes + content types ONLY. It never logs or
//     returns secrets and never dumps response bodies. Its job is to reveal the
//     real API surface so the operator can confirm/save the base path.
//   - Collect: once a base API path is known, fetch controller identity + AP /
//     SSID / client / radio rosters, tolerating endpoints a given firmware
//     doesn't expose (honest partial results, never fabricated data).
package extremexcc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Doer performs an HTTP request (injectable for tests + TLS-insecure mgmt LAN).
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client targets one on-prem XCC appliance.
type Client struct {
	BaseURL  string // https://ip:8443
	APIBase  string // discovered/confirmed API base path (e.g. /management/v1); empty until known
	Username string
	Password string
	Token    string // optional pre-issued API token (used as Bearer when set)
	Doer     Doer

	bearer      string // obtained at login; never logged/returned
	loginMethod string // basic | bearer | token | none
}

// NewClient builds a Client. baseURL should include scheme + host(:port).
func NewClient(baseURL, apiBase, user, pass, token string, doer Doer) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"), APIBase: strings.Trim(apiBase, "/"),
		Username: user, Password: pass, Token: token, Doer: doer,
	}
}

// Probe is one endpoint probe outcome — status + content type only, NO body.
type Probe struct {
	Path        string `json:"path"`
	Method      string `json:"method"`
	Status      int    `json:"status"`
	ContentType string `json:"content_type"`
	JSON        bool   `json:"json"`  // response looked like JSON (by content-type)
	Note        string `json:"note,omitempty"`
}

// ExploreResult is the safe-discovery report shown to the operator.
type ExploreResult struct {
	Reachable        bool    `json:"reachable"`
	AuthMethod       string  `json:"auth_method"`       // basic | bearer | token | none | unknown
	Authenticated    bool    `json:"authenticated"`
	SuggestedAPIBase string  `json:"suggested_api_base"`
	Probes           []Probe `json:"probes"`
	Summary          string  `json:"summary"`
}

// candidate API roots to probe, in priority order. These are documented/observed
// XCC + ExtremeWireless/Aerohive-lineage shapes; the probe reveals which (if any)
// this firmware actually serves.
var candidateRoots = []string{
	"/management/v1", "/management/v3", "/management/v2", "/management",
	"/v1", "/rest/v1", "/api/v1", "/api", "/rest", "/services",
}

// roster sub-paths tried under a working root to spot the AP/SSID/client surface.
var rosterSubPaths = []string{"/aps", "/devices", "/stations", "/clients", "/services", "/sites", "/wlans"}

// Explore performs the safe discovery. It never returns secrets or bodies.
func (c *Client) Explore(ctx context.Context) ExploreResult {
	res := ExploreResult{AuthMethod: "none"}

	// 1. Reachability: the GUI root always answers on a live appliance.
	if p, ok := c.probe(ctx, http.MethodGet, "/Admin/"); ok {
		res.Reachable = res.Reachable || p.Status > 0
		res.Probes = append(res.Probes, p)
	}
	if root, ok := c.probe(ctx, http.MethodGet, "/"); ok {
		res.Reachable = res.Reachable || root.Status > 0
		res.Probes = append(res.Probes, root)
	}

	// 2. Attempt authentication (best-effort, no secret ever surfaced).
	res.AuthMethod, res.Authenticated = c.tryAuth(ctx)

	// 3. Probe candidate API roots (authenticated if we got a bearer).
	for _, root := range candidateRoots {
		p, ok := c.probe(ctx, http.MethodGet, root)
		if !ok {
			continue
		}
		res.Probes = append(res.Probes, p)
		// A JSON response (even 401/403) signals a real API surface.
		if p.JSON && res.SuggestedAPIBase == "" && (p.Status == 200 || p.Status == 401 || p.Status == 403) {
			res.SuggestedAPIBase = root
		}
	}

	// 4. If we found a likely root, probe its roster sub-paths to confirm.
	if res.SuggestedAPIBase != "" {
		for _, sp := range rosterSubPaths {
			if p, ok := c.probe(ctx, http.MethodGet, res.SuggestedAPIBase+sp); ok {
				res.Probes = append(res.Probes, p)
			}
		}
	}

	res.Summary = c.summarize(res)
	return res
}

func (c *Client) summarize(res ExploreResult) string {
	switch {
	case !res.Reachable:
		return "Controller did not respond on the management port. Check the URL/port and network reachability."
	case res.SuggestedAPIBase != "" && res.Authenticated:
		return "Authenticated and found a JSON API at " + res.SuggestedAPIBase + ". Save this base path and run collection."
	case res.SuggestedAPIBase != "":
		return "Found a JSON API surface at " + res.SuggestedAPIBase + " but could not authenticate — check credentials. Save the base path and retry."
	case res.Authenticated:
		return "Authenticated, but no JSON API root was found among the probed paths. The GUI is reachable (/Admin/); this firmware may expose the API at a non-standard path — confirm it and save it."
	default:
		return "Reachable (GUI at /Admin/) but no API surface auto-detected and authentication did not succeed. Provide controller API credentials, or confirm the API base path manually."
	}
}

// tryAuth attempts, in order: a pre-issued token (Bearer), then an OAuth2
// password-grant token, then HTTP Basic. Returns the method + whether a usable
// session was established. The token value is held on the client, never returned.
func (c *Client) tryAuth(ctx context.Context) (method string, ok bool) {
	if c.Token != "" {
		c.bearer = c.Token
		c.loginMethod = "token"
		// Validate the token against a candidate root.
		if c.authProbe(ctx) {
			return "token", true
		}
		c.bearer = ""
	}
	if c.Username != "" {
		// OAuth2 password grant at the common XCC token endpoints.
		for _, tp := range []string{"/management/v1/oauth2/token", "/management/v3/oauth2/token"} {
			if tok := c.oauthToken(ctx, tp); tok != "" {
				c.bearer = tok
				c.loginMethod = "bearer"
				return "bearer", true
			}
		}
		// HTTP Basic: credentials are sent per-request, so we proceed optimistically;
		// the roster fetches surface a 401/403 honestly if the credential is wrong.
		c.loginMethod = "basic"
		return "basic", true
	}
	return "none", false
}

// oauthToken posts a password grant and returns the access token (or "").
func (c *Client) oauthToken(ctx context.Context, path string) string {
	form := url.Values{"grantType": {"PASSWORD"}, "userId": {c.Username}, "password": {c.Password}}
	body := strings.NewReader(form.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, body)
	if err != nil {
		return ""
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.Doer.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ""
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var t struct {
		AccessToken string `json:"access_token"`
		Token       string `json:"token"`
		IDToken     string `json:"id_token"`
	}
	_ = json.Unmarshal(raw, &t)
	switch {
	case t.AccessToken != "":
		return t.AccessToken
	case t.Token != "":
		return t.Token
	case t.IDToken != "":
		return t.IDToken
	}
	return ""
}

// authProbe issues a lightweight authenticated GET to confirm the session works.
func (c *Client) authProbe(ctx context.Context) bool {
	for _, root := range candidateRoots {
		p, ok := c.probe(ctx, http.MethodGet, root)
		if ok && p.Status == 200 && p.JSON {
			return true
		}
	}
	return false
}

// probe issues one request and returns status + content-type only. Applies the
// current auth (bearer or basic). Never reads/returns the body.
func (c *Client) probe(ctx context.Context, method, path string) (Probe, bool) {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, nil)
	if err != nil {
		return Probe{}, false
	}
	req.Header.Set("Accept", "application/json")
	switch c.loginMethod {
	case "bearer", "token":
		if c.bearer != "" {
			req.Header.Set("Authorization", "Bearer "+c.bearer)
		}
	case "basic":
		if c.Username != "" {
			req.SetBasicAuth(c.Username, c.Password)
		}
	}
	resp, err := c.Doer.Do(req)
	if err != nil {
		return Probe{Path: path, Method: method, Note: shortErr(err)}, true
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096)) // drain a little; never inspect
	ct := resp.Header.Get("Content-Type")
	return Probe{
		Path: path, Method: method, Status: resp.StatusCode, ContentType: ct,
		JSON: strings.Contains(strings.ToLower(ct), "json"),
	}, true
}

func shortErr(err error) string {
	s := err.Error()
	// Strip anything resembling credentials from a transport error string.
	if i := strings.Index(s, "@"); i >= 0 {
		s = "connection error"
	}
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

// authErr is returned by Collect when no usable session could be established.
var errNoAuth = fmt.Errorf("extremexcc: could not authenticate to the controller API")
