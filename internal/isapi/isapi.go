// Package isapi is a thin Hikvision ISAPI client used to identify and classify
// cameras / NVRs / DVRs when ONVIF is unavailable (many Hikvision NVRs ship with
// ONVIF disabled but always expose ISAPI over HTTPS). It performs an HTTP GET of
// /ISAPI/System/deviceInfo with HTTP Digest auth (Basic fallback) over an
// injectable Doer, and parses the XML so the digest + parsing are unit-testable
// against sample responses with no real device.
//
// The <deviceType> it returns is the definitive camera-vs-NVR signal consumed by
// classify.ISAPIDeviceInfo.
package isapi

import (
	"context"
	"crypto/md5" //nolint:gosec // HTTP Digest (RFC 2617) mandates MD5
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Doer performs an HTTP request (so tests inject a fake). *http.Client fits.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client targets one device's ISAPI service at a base URL (scheme + host[:port]).
type Client struct {
	BaseURL  string
	Username string
	Password string
	Doer     Doer
}

// NewClient builds a Client. A nil doer uses a short-timeout http.Client.
func NewClient(baseURL, user, pass string, doer Doer) *Client {
	if doer == nil {
		doer = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Username: user, Password: pass, Doer: doer}
}

// DeviceInfo is the identity parsed from /ISAPI/System/deviceInfo.
type DeviceInfo struct {
	Manufacturer string // always "Hikvision" for this endpoint
	DeviceName   string
	Model        string
	Serial       string
	Firmware     string
	MAC          string
	DeviceType   string // "NVR" | "DVR" | "IPCamera" | … (deviceDescription as fallback)
	Endpoint     string // base URL that answered
}

type deviceInfoXML struct {
	XMLName           xml.Name `xml:"DeviceInfo"`
	DeviceName        string   `xml:"deviceName"`
	Model             string   `xml:"model"`
	SerialNumber      string   `xml:"serialNumber"`
	FirmwareVersion   string   `xml:"firmwareVersion"`
	MACAddress        string   `xml:"macAddress"`
	DeviceType        string   `xml:"deviceType"`
	DeviceDescription string   `xml:"deviceDescription"`
}

// parseDeviceInfo decodes a Hikvision deviceInfo XML body. DeviceType prefers the
// explicit <deviceType>, falling back to <deviceDescription> (older firmware).
func parseDeviceInfo(body []byte) (DeviceInfo, error) {
	var x deviceInfoXML
	if err := xml.Unmarshal(body, &x); err != nil {
		return DeviceInfo{}, err
	}
	dt := strings.TrimSpace(x.DeviceType)
	if dt == "" {
		dt = strings.TrimSpace(x.DeviceDescription)
	}
	if x.Model == "" && dt == "" && x.SerialNumber == "" {
		return DeviceInfo{}, fmt.Errorf("isapi: not a deviceInfo document")
	}
	return DeviceInfo{
		Manufacturer: "Hikvision",
		DeviceName:   strings.TrimSpace(x.DeviceName),
		Model:        strings.TrimSpace(x.Model),
		Serial:       strings.TrimSpace(x.SerialNumber),
		Firmware:     strings.TrimSpace(x.FirmwareVersion),
		MAC:          strings.TrimSpace(x.MACAddress),
		DeviceType:   dt,
	}, nil
}

// DeviceInfo GETs /ISAPI/System/deviceInfo, authenticating with Digest (Basic
// fallback) when challenged.
func (c *Client) DeviceInfo(ctx context.Context) (DeviceInfo, error) {
	const path = "/ISAPI/System/deviceInfo"
	body, err := c.get(ctx, path)
	if err != nil {
		return DeviceInfo{}, err
	}
	info, err := parseDeviceInfo(body)
	if err != nil {
		return DeviceInfo{}, err
	}
	info.Endpoint = c.BaseURL
	return info, nil
}

// get issues an authenticated GET: it sends once unauthenticated, and on a 401
// re-issues with Digest (or Basic) per the WWW-Authenticate challenge.
func (c *Client) get(ctx context.Context, path string) ([]byte, error) {
	resp, body, err := c.do(ctx, path, "")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized && c.Username != "" {
		ch := resp.Header.Get("WWW-Authenticate")
		var auth string
		switch {
		case strings.HasPrefix(strings.ToLower(ch), "digest"):
			auth = c.digestHeader(http.MethodGet, path, ch)
		case strings.HasPrefix(strings.ToLower(ch), "basic"):
			auth = basicHeader(c.Username, c.Password)
		default:
			return nil, fmt.Errorf("isapi: unsupported auth challenge %q", firstWord(ch))
		}
		resp, body, err = c.do(ctx, path, auth)
		if err != nil {
			return nil, err
		}
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("isapi: authentication rejected")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("isapi: %s → %d", path, resp.StatusCode)
	}
	return body, nil
}

func (c *Client) do(ctx context.Context, path, authHeader string) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Accept", "application/xml, text/xml, */*")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	resp, err := c.Doer.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, nil, err
	}
	return resp, out, nil
}

func basicHeader(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

// digestHeader builds an RFC 2617 Digest Authorization header for the challenge.
func (c *Client) digestHeader(method, uri, challenge string) string {
	ch := parseChallenge(challenge)
	cnonce := randHex(8)
	const nc = "00000001"
	resp := digestResponse(c.Username, ch["realm"], c.Password, method, uri, ch, cnonce, nc)
	var b strings.Builder
	fmt.Fprintf(&b, `Digest username=%q, realm=%q, nonce=%q, uri=%q, response=%q`,
		c.Username, ch["realm"], ch["nonce"], uri, resp)
	if a := ch["algorithm"]; a != "" {
		fmt.Fprintf(&b, `, algorithm=%s`, a)
	}
	if qopHasAuth(ch["qop"]) {
		fmt.Fprintf(&b, `, qop=auth, nc=%s, cnonce=%q`, nc, cnonce)
	}
	if o := ch["opaque"]; o != "" {
		fmt.Fprintf(&b, `, opaque=%q`, o)
	}
	return b.String()
}

// digestResponse computes the RFC 2617 response hash (MD5, qop=auth when offered).
func digestResponse(user, realm, pass, method, uri string, ch map[string]string, cnonce, nc string) string {
	ha1 := md5hex(user + ":" + realm + ":" + pass)
	ha2 := md5hex(method + ":" + uri)
	if qopHasAuth(ch["qop"]) {
		return md5hex(ha1 + ":" + ch["nonce"] + ":" + nc + ":" + cnonce + ":auth:" + ha2)
	}
	return md5hex(ha1 + ":" + ch["nonce"] + ":" + ha2)
}

func qopHasAuth(qop string) bool {
	for _, q := range strings.Split(qop, ",") {
		if strings.TrimSpace(q) == "auth" {
			return true
		}
	}
	return false
}

var challengeRE = regexp.MustCompile(`([a-zA-Z]+)=(?:"([^"]*)"|([^,]*))`)

// parseChallenge parses the params of a WWW-Authenticate header (after the scheme).
func parseChallenge(h string) map[string]string {
	if i := strings.IndexByte(h, ' '); i >= 0 {
		h = h[i+1:]
	}
	out := map[string]string{}
	for _, m := range challengeRE.FindAllStringSubmatch(h, -1) {
		v := m[2]
		if v == "" {
			v = strings.TrimSpace(m[3])
		}
		out[strings.ToLower(m[1])] = v
	}
	return out
}

func firstWord(s string) string {
	if i := strings.IndexByte(s, ' '); i >= 0 {
		return s[:i]
	}
	return s
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s)) //nolint:gosec
	return hex.EncodeToString(sum[:])
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// PermissiveClient is an http.Client tuned for embedded devices: it skips cert
// verification (NVRs/cameras ship self-signed certs) and offers the full cipher
// set incl. legacy suites, since Go's modern defaults won't negotiate with the old
// TLS stacks these devices run (e.g. RSA-kex GCM). Without this, HTTPS to a
// Hikvision NVR fails with "tls: handshake failure".
func PermissiveClient(timeout time.Duration) *http.Client {
	var ids []uint16
	for _, s := range tls.CipherSuites() {
		ids = append(ids, s.ID)
	}
	for _, s := range tls.InsecureCipherSuites() {
		ids = append(ids, s.ID)
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{ //nolint:gosec // embedded devices use self-signed certs + legacy ciphers
				InsecureSkipVerify: true,
				MinVersion:         tls.VersionTLS10,
				CipherSuites:       ids,
			},
		},
	}
}

// CollectDeviceInfo identifies a Hikvision device by trying ISAPI deviceInfo over a
// scheme/port ladder (HTTPS first, since NVRs commonly disable plain HTTP), and
// returns the first endpoint that answers. A nil doer uses PermissiveClient so the
// HTTPS rungs negotiate with the device's legacy TLS. Each attempt is bounded so a
// closed/hung port doesn't stall the ladder; an authentication rejection short-
// circuits (we found ISAPI — another port won't fix a wrong password).
func CollectDeviceInfo(ctx context.Context, ip, user, pass string, doer Doer) (DeviceInfo, error) {
	if doer == nil {
		doer = PermissiveClient(15 * time.Second)
	}
	ladder := []string{
		"https://" + ip,
		"https://" + ip + ":8443",
		"http://" + ip,
		"http://" + ip + ":8000",
		"http://" + ip + ":8080",
		"http://" + ip + ":8010",
	}
	var lastErr error
	for _, base := range ladder {
		actx, cancel := context.WithTimeout(ctx, 8*time.Second)
		info, err := NewClient(base, user, pass, doer).DeviceInfo(actx)
		cancel()
		if err == nil {
			return info, nil
		}
		if strings.Contains(err.Error(), "authentication rejected") {
			return DeviceInfo{}, err // ISAPI reachable; the credential is wrong
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("isapi: no endpoint answered")
	}
	return DeviceInfo{}, lastErr
}
