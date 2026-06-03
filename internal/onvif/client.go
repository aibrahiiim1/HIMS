// Package onvif is a thin, dependency-light ONVIF client: it POSTs SOAP
// envelopes with WS-Security UsernameToken (PasswordDigest) auth over an
// injectable Doer and parses the device-info / media-profile responses. Doing
// our own SOAP rather than leaning on a heavyweight ONVIF library keeps the
// WS-Security digest and the XML parsing unit-testable against sample
// responses with no real camera.
//
// Live-validation trigger: the SOAP shapes follow the ONVIF Core/Media specs
// + common camera responses; validate against a real Hikvision/Dahua/Axis
// camera once an ONVIF credential is bound (cameras vary in namespace prefixes
// and optional fields).
package onvif

import (
	"context"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // WS-Security UsernameToken mandates SHA1
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Doer performs an HTTP request (so tests inject a fake). *http.Client fits.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client targets one camera's ONVIF services.
type Client struct {
	BaseURL  string // e.g. "http://10.0.0.60"
	Username string
	Password string
	Doer     Doer
	// now is overridable in tests for a deterministic Created timestamp.
	now func() time.Time
}

// NewClient builds a Client. A nil doer uses a short-timeout http.Client.
func NewClient(baseURL, user, pass string, doer Doer) *Client {
	if doer == nil {
		doer = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"), Username: user, Password: pass,
		Doer: doer, now: func() time.Time { return time.Now().UTC() },
	}
}

// passwordDigest computes the WS-Security PasswordDigest:
// Base64( SHA1( nonceBytes + created + password ) ).
func passwordDigest(nonceBytes []byte, created, password string) string {
	h := sha1.New() //nolint:gosec
	h.Write(nonceBytes)
	h.Write([]byte(created))
	h.Write([]byte(password))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// securityHeader builds the WS-Security SOAP header (empty when no username).
func (c *Client) securityHeader() string {
	if c.Username == "" {
		return ""
	}
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	created := c.now().Format("2006-01-02T15:04:05.000Z")
	digest := passwordDigest(nonce, created, c.Password)
	return `<s:Header><Security s:mustUnderstand="1" xmlns="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd">` +
		`<UsernameToken><Username>` + xmlEscape(c.Username) + `</Username>` +
		`<Password Type="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest">` + digest + `</Password>` +
		`<Nonce>` + base64.StdEncoding.EncodeToString(nonce) + `</Nonce>` +
		`<Created xmlns="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd">` + created + `</Created>` +
		`</UsernameToken></Security></s:Header>`
}

// call POSTs a SOAP body to a service path and returns the response XML.
func (c *Client) call(ctx context.Context, servicePath, body string) ([]byte, error) {
	env := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">` +
		c.securityHeader() + `<s:Body>` + body + `</s:Body></s:Envelope>`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+servicePath, strings.NewReader(env))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/soap+xml; charset=utf-8")
	resp, err := c.Doer.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("onvif: %s → %d", servicePath, resp.StatusCode)
	}
	return out, nil
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}
