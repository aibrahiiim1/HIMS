// Package osdiscovery does lightweight, mostly-unauthenticated OS/NVR discovery
// probing from the HIMS host: a TCP port profile, an SSH banner grab, an HTTP
// Server/title read, and a Hikvision ISAPI deviceInfo fetch. It gathers raw
// observations and turns them into classification evidence via internal/classify.
//
// Scope boundary (deliberate): this is *fingerprinting*, not deep inventory.
// Pure-Go WMI/DCOM from Linux is not viable, so deep Windows inventory
// (services, patches, installed software) stays with the future Windows agent;
// here we only need enough signal to classify os_family + category + subtype.
package osdiscovery

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/xml"
	"io"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
	"time"

	"github.com/coralsearesorts/hims/internal/classify"
	"github.com/coralsearesorts/hims/internal/domain"
)

// DefaultPorts is the OS/role-revealing TCP profile probed by default.
var DefaultPorts = []int{22, 80, 135, 389, 443, 445, 554, 3389, 5985, 5986, 8000, 9100}

// Options tunes a probe. Zero value is usable (sensible defaults applied).
type Options struct {
	TCPTimeout  time.Duration
	HTTPTimeout time.Duration
	Ports       []int
	// ISAPIUser/Pass are optional Hikvision credentials. Without them the ISAPI
	// endpoint's mere presence (a 401) is still recorded as a weak signal.
	ISAPIUser string
	ISAPIPass string
}

func (o *Options) applyDefaults() {
	if o.TCPTimeout <= 0 {
		o.TCPTimeout = 1500 * time.Millisecond
	}
	if o.HTTPTimeout <= 0 {
		o.HTTPTimeout = 3 * time.Second
	}
	if len(o.Ports) == 0 {
		o.Ports = DefaultPorts
	}
}

// Observation is everything one probe learned about a host. Plain data so the
// evidence-building path is unit-testable without a network.
type Observation struct {
	IP              netip.Addr
	OpenTCP         []int
	SSHBanner       string
	HTTPServer      string
	HTTPTitle       string
	ISAPIPresent    bool
	ISAPIDeviceType string
	ISAPIModel      string
	SNMPSysDescr    string // optional, supplied by the caller from existing SNMP collection
}

// Probe runs the network probes against ip and returns the observation.
func Probe(ctx context.Context, ip netip.Addr, opts Options) Observation {
	opts.applyDefaults()
	obs := Observation{IP: ip}

	for _, p := range opts.Ports {
		if ctx.Err() != nil {
			break
		}
		if dialOpen(ctx, ip, p, opts.TCPTimeout) {
			obs.OpenTCP = append(obs.OpenTCP, p)
		}
	}
	open := func(p int) bool {
		for _, x := range obs.OpenTCP {
			if x == p {
				return true
			}
		}
		return false
	}

	if open(22) {
		obs.SSHBanner = grabSSHBanner(ctx, ip, opts.TCPTimeout)
	}
	if open(80) || open(443) || open(8000) {
		scheme, port := "http", 80
		if open(443) {
			scheme, port = "https", 443
		} else if open(8000) {
			port = 8000
		}
		obs.HTTPServer, obs.HTTPTitle = grabHTTP(ctx, scheme, ip, port, opts.HTTPTimeout)
		present, dt, model := fetchISAPI(ctx, scheme, ip, port, opts.ISAPIUser, opts.ISAPIPass, opts.HTTPTimeout)
		obs.ISAPIPresent, obs.ISAPIDeviceType, obs.ISAPIModel = present, dt, model
	}
	return obs
}

// Evidence converts an observation into classification evidence (pure).
func (o Observation) Evidence() []domain.ClassificationEvidence {
	var ev []domain.ClassificationEvidence
	if o.ISAPIDeviceType != "" || o.ISAPIModel != "" {
		ev = append(ev, classify.ISAPIDeviceInfo(o.ISAPIDeviceType, o.ISAPIModel)...)
	} else if o.ISAPIPresent {
		// Endpoint exists but we couldn't read deviceType (no creds) — weak hint.
		ev = append(ev, domain.ClassificationEvidence{
			Source: domain.EvidenceSourceISAPI, Signal: "ISAPI endpoint present (401)",
			Category: string(domain.CatCamera), OSFamily: domain.OSFamilyEmbedded, Confidence: 35,
		})
	}
	if o.SSHBanner != "" {
		ev = append(ev, classify.SSHBanner(o.SSHBanner)...)
	}
	if o.SNMPSysDescr != "" {
		ev = append(ev, classify.SNMPSysDescr(o.SNMPSysDescr)...)
	}
	if o.HTTPServer != "" || o.HTTPTitle != "" {
		ev = append(ev, classify.HTTPServer(o.HTTPServer, o.HTTPTitle)...)
	}
	if len(o.OpenTCP) > 0 {
		ev = append(ev, classify.OpenPorts(o.OpenTCP)...)
	}
	return ev
}

// Result classifies the observation end-to-end.
func (o Observation) Result() classify.Result { return classify.FromEvidence(o.Evidence()) }

// --- network helpers ---

func dialOpen(ctx context.Context, ip netip.Addr, port int, timeout time.Duration) bool {
	d := net.Dialer{Timeout: timeout}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := d.DialContext(cctx, "tcp", net.JoinHostPort(ip.String(), itoa(port)))
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func grabSSHBanner(ctx context.Context, ip netip.Addr, timeout time.Duration) string {
	d := net.Dialer{Timeout: timeout}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := d.DialContext(cctx, "tcp", net.JoinHostPort(ip.String(), "22"))
	if err != nil {
		return ""
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	line, _ := bufio.NewReader(conn).ReadString('\n')
	return strings.TrimSpace(line)
}

var titleRe = regexp.MustCompile(`(?is)<title>(.*?)</title>`)

// legacyCipherSuites is every cipher suite the Go runtime can speak, including
// the ones it considers insecure (CBC-SHA, 3DES) — NVRs/cameras frequently only
// offer these, so the default secure-only list fails the handshake. Built once.
var legacyCipherSuites = func() []uint16 {
	var ids []uint16
	for _, s := range tls.CipherSuites() {
		ids = append(ids, s.ID)
	}
	for _, s := range tls.InsecureCipherSuites() {
		ids = append(ids, s.ID)
	}
	return ids
}()

func newHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,             // appliances ship self-signed certs
				MinVersion:         tls.VersionTLS10, // NVRs/cameras often only speak TLS 1.0/1.1
				CipherSuites:       legacyCipherSuites,
			},
			ForceAttemptHTTP2: false, // these appliances are HTTP/1.1 only
			DisableKeepAlives: true,
		},
		// Don't follow redirects to login portals; the Server header is what we want.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func grabHTTP(ctx context.Context, scheme string, ip netip.Addr, port int, timeout time.Duration) (server, title string) {
	url := scheme + "://" + net.JoinHostPort(ip.String(), itoa(port)) + "/"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", ""
	}
	resp, err := newHTTPClient(timeout).Do(req)
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()
	server = resp.Header.Get("Server")
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if m := titleRe.FindSubmatch(body); m != nil {
		title = strings.TrimSpace(string(m[1]))
	}
	return server, title
}

// isapiDeviceInfo is the subset of Hikvision /ISAPI/System/deviceInfo we read.
type isapiDeviceInfo struct {
	DeviceType string `xml:"deviceType"`
	Model      string `xml:"model"`
}

func fetchISAPI(ctx context.Context, scheme string, ip netip.Addr, port int, user, pass string, timeout time.Duration) (present bool, deviceType, model string) {
	url := scheme + "://" + net.JoinHostPort(ip.String(), itoa(port)) + "/ISAPI/System/deviceInfo"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, "", ""
	}
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	resp, err := newHTTPClient(timeout).Do(req)
	if err != nil {
		return false, "", ""
	}
	defer resp.Body.Close()
	// 401 on this exact path is itself a strong "this is a Hikvision/ISAPI device".
	if resp.StatusCode == http.StatusUnauthorized {
		return true, "", ""
	}
	if resp.StatusCode != http.StatusOK {
		return false, "", ""
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
	dt, model := parseISAPIDeviceInfo(body)
	return true, dt, model
}

// parseISAPIDeviceInfo extracts deviceType + model from an ISAPI deviceInfo body
// (pure — unit-tested). Namespaces vary across firmware, so we match local names.
func parseISAPIDeviceInfo(body []byte) (deviceType, model string) {
	var di isapiDeviceInfo
	if err := xml.Unmarshal(body, &di); err == nil {
		return strings.TrimSpace(di.DeviceType), strings.TrimSpace(di.Model)
	}
	return "", ""
}

// itoa avoids importing strconv just for ports.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [6]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
