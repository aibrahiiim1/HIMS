// Package ruckuszd is a dependency-light client for Ruckus ZoneDirector (e.g.
// ZD3050, firmware 10.x) via its internal Web-XML AJAX interface on :443 — the
// management surface ZoneDirector exposes (it has no REST API; internal/ruckus is
// a SmartZone/vSZ REST client, a different product).
//
// It is a faithful Go port of the live-verified desktop connector
// (NetworkTool/Connectors/Ruckus/RuckusAjaxSession.cs +
// RuckusZoneDirectorConnector.cs): admin-path discovery, form login, the
// misspelled `csfrToken` CSRF token, the _cmdstat.jsp/_conf.jsp AJAX flow, and
// expired-session re-auth. Secrets are never logged or returned.
package ruckuszd

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Doer performs an HTTP request. The injected client MUST carry a cookie jar (for
// the -ejs-session- cookie) and MUST NOT auto-follow redirects (so we can read the
// admin-path 302 and detect expired-session 3xx). For self-signed mgmt certs it
// should skip TLS verification.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client speaks the ZoneDirector internal AJAX interface.
type Client struct {
	BaseURL  string // https://<ip>:443
	Username string
	Password string
	Doer     Doer

	adminBase string // discovered, e.g. "admin10"
	csrf      string
	loggedIn  bool
}

// New builds a Client. baseURL should include scheme + host(:port).
func New(baseURL, user, pass string, doer Doer) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"), Username: user, Password: pass,
		Doer: doer, adminBase: "admin10",
	}
}

// csrfRe matches both the ZD spelling (csfrToken) and csrfToken; the token is
// ~10 chars on ZD 10.x (don't require 12+).
var csrfRe = regexp.MustCompile(`cs[fr]{2}Token\s*=\s*['"]([^'"]+)['"]`)

func (c *Client) url(page string) string { return c.BaseURL + "/" + c.adminBase + "/" + page }

// AdminBase returns the discovered admin path segment (e.g. "admin10").
func (c *Client) AdminBase() string { return c.adminBase }

// Login discovers the admin path, posts the web login form, and obtains the CSRF
// token. Returns a non-secret error describing auth/transport failures.
func (c *Client) Login(ctx context.Context) error {
	c.csrf, c.loggedIn = "", false
	c.discoverAdminBase(ctx)

	// Prime any pre-session cookie the login page sets (best-effort).
	if req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url("login.jsp"), nil); err == nil {
		if resp, err := c.Doer.Do(req); err == nil {
			io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16)) //nolint:errcheck
			resp.Body.Close()
		}
	}

	form := url.Values{"username": {c.Username}, "password": {c.Password}, "ok": {"Log In"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url("login.jsp"), strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.Doer.Do(req)
	if err != nil {
		return fmt.Errorf("ruckuszd: login transport error: %w", err)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()

	if resp.StatusCode == http.StatusInternalServerError {
		return fmt.Errorf("ruckuszd: server error on login (the admin path may be wrong for this firmware)")
	}
	loc := resp.Header.Get("Location")
	redirectedToDashboard := resp.StatusCode >= 300 && resp.StatusCode < 400 &&
		!strings.Contains(strings.ToLower(loc), "login")
	if !redirectedToDashboard && looksLikeLoginPage(body) {
		return fmt.Errorf("ruckuszd: login rejected — check the username and password")
	}
	if v := resp.Header.Get("X-CSRF-Token"); v != "" {
		c.csrf = v
	}
	if c.csrf == "" {
		c.fetchCSRF(ctx)
	}
	c.loggedIn = true
	return nil
}

// discoverAdminBase reads the root redirect (GET / with redirects disabled) and
// takes the first path segment of the Location (admin10 / admin) as the base.
func (c *Client) discoverAdminBase(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/", nil)
	if err != nil {
		return
	}
	resp, err := c.Doer.Do(req)
	if err != nil {
		return
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16)) //nolint:errcheck
	resp.Body.Close()
	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		return
	}
	loc := resp.Header.Get("Location")
	if loc == "" {
		return
	}
	if u, err := url.Parse(loc); err == nil && u.Path != "" {
		loc = u.Path
	}
	seg := ""
	for _, p := range strings.Split(strings.Trim(loc, "/"), "/") {
		if p != "" {
			seg = p
			break
		}
	}
	if strings.HasPrefix(strings.ToLower(seg), "admin") {
		c.adminBase = seg
	}
}

func (c *Client) fetchCSRF(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url("_csrfTokenVar.jsp"), nil)
	if err != nil {
		return
	}
	resp, err := c.Doer.Do(req)
	if err != nil {
		return
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return
	}
	if m := csrfRe.FindSubmatch(body); m != nil {
		c.csrf = string(m[1])
	}
}

type endpoint int

const (
	cmdStat endpoint = iota // _cmdstat.jsp (getstat)
	conf                    // _conf.jsp    (getconf)
)

// AJAX request bodies (constants — the schema drifts between firmware generations).
const (
	apStatsXML  = `<ajax-request action='getstat' comp='stamgr' enable-gzip='0'><ap LEVEL='1'/></ajax-request>`
	clientXML   = `<ajax-request action='getstat' comp='stamgr' enable-gzip='0'><client LEVEL='2'/></ajax-request>`
	wlanListXML = `<ajax-request action='getconf' comp='wlansvc-list' updater='wlansvc-list.0.5' />`
	systemXML   = `<ajax-request action='getconf' comp='system' updater='system.0.5' />`
	eventsXML   = `<ajax-request action='getstat' comp='eventd'><pieceStat pid='1' start='0' number='300' requestId='evt.1' cleanupId='0'/></ajax-request>`
)

// postAjax POSTs an XML body and returns the response bytes, re-logging-in once on
// a 3xx (expired session). A 1-byte body with no CSRF token means the token was
// required but missing.
func (c *Client) postAjax(ctx context.Context, ep endpoint, xmlBody string, retry bool) ([]byte, error) {
	if !c.loggedIn {
		if err := c.Login(ctx); err != nil {
			return nil, err
		}
	}
	page := "_conf.jsp"
	if ep == cmdStat {
		page = "_cmdstat.jsp"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url(page), strings.NewReader(xmlBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "text/xml")
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	resp, err := c.Doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ruckuszd: ajax transport error: %w", err)
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 { // session expired
		resp.Body.Close()
		if !retry {
			return nil, fmt.Errorf("ruckuszd: session expired and re-authentication did not help")
		}
		c.loggedIn = false
		if err := c.Login(ctx); err != nil {
			return nil, err
		}
		return c.postAjax(ctx, ep, xmlBody, false)
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	resp.Body.Close()
	if len(b) < 3 && c.csrf == "" {
		return nil, fmt.Errorf("ruckuszd: empty response — a CSRF token is required but none was obtained")
	}
	if looksLikeHTML(b) {
		return nil, fmt.Errorf("ruckuszd: received an HTML/login page instead of XML — the session is invalid")
	}
	return b, nil
}

func looksLikeLoginPage(body []byte) bool {
	s := strings.ToLower(string(body))
	return strings.Contains(s, `type="password"`) || strings.Contains(s, "loginfailed") || strings.Contains(s, `name="password"`)
}

func looksLikeHTML(body []byte) bool {
	s := strings.ToLower(strings.TrimSpace(string(body)))
	return strings.HasPrefix(s, "<!doctype html") || strings.HasPrefix(s, "<html")
}

// --- XML attribute-soup parsing ---

func attrMap(attrs []xml.Attr) map[string]string {
	m := make(map[string]string, len(attrs))
	for _, a := range attrs {
		m[strings.ToLower(a.Name.Local)] = a.Value
	}
	return m
}

// attrRows returns every element named `local` as its attribute map.
func attrRows(b []byte, local string) []map[string]string {
	dec := xml.NewDecoder(bytes.NewReader(b))
	var out []map[string]string
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok && strings.EqualFold(se.Name.Local, local) {
			out = append(out, attrMap(se.Attr))
		}
	}
	return out
}

// apRows walks each <ap> element, capturing its attributes and summing the
// per-radio <radio num-sta> client counts into "num-sta-total".
func apRows(b []byte) []map[string]string {
	dec := xml.NewDecoder(bytes.NewReader(b))
	var out []map[string]string
	var cur map[string]string
	staTotal := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch se := tok.(type) {
		case xml.StartElement:
			switch strings.ToLower(se.Name.Local) {
			case "ap":
				cur, staTotal = attrMap(se.Attr), 0
			case "radio":
				if cur != nil {
					if n, ok := atoi(attrMap(se.Attr)["num-sta"]); ok {
						staTotal += n
					}
				}
			}
		case xml.EndElement:
			if strings.EqualFold(se.Name.Local, "ap") && cur != nil {
				if staTotal > 0 {
					cur["num-sta-total"] = strconv.Itoa(staTotal)
				}
				out = append(out, cur)
				cur = nil
			}
		}
	}
	return out
}
