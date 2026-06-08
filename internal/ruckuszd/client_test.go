package ruckuszd

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// fakeDoer routes by path substring and serves canned responses; it records the
// last X-CSRF-Token header seen so tests can assert it was sent.
type fakeDoer struct {
	routes   map[string]resp
	lastCSRF string
}

type resp struct {
	status  int
	body    string
	headers map[string]string
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	if v := req.Header.Get("X-CSRF-Token"); v != "" {
		f.lastCSRF = v
	}
	// Pick the most-specific (longest) matching route; "/" matches only the exact
	// root path (admin-discovery GET), never as a substring of every path.
	best, bestLen := "", -1
	for k := range f.routes {
		matched := (k == "/" && req.URL.Path == "/") || (k != "/" && strings.Contains(req.URL.Path, k))
		if matched && len(k) > bestLen {
			best, bestLen = k, len(k)
		}
	}
	if bestLen >= 0 {
		r := f.routes[best]
		h := http.Header{}
		for hk, hv := range r.headers {
			h.Set(hk, hv)
		}
		return &http.Response{StatusCode: r.status, Header: h, Body: io.NopCloser(strings.NewReader(r.body))}, nil
	}
	return &http.Response{StatusCode: 404, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
}

const (
	apXMLFixture = `<ajax-response><response type='getstat'>
<ap mac='aa:bb:cc:00:11:22' ap-name='Lobby' devname='Lobby' serial-number='SN-1' model='r720' ip='192.168.2.50' firmware-version='10.1.2.0.306' state='1' location='Floor 1'>
  <radio radio-type='11ng' num-sta='3'/>
  <radio radio-type='11ac' num-sta='5'/>
</ap>
<ap mac='aa:bb:cc:00:33:44' ap-name='Pool' serial-number='SN-2' model='t310' ip='192.168.2.51' firmware-version='10.1.2.0.306' state='0'>
  <radio radio-type='11ng' num-sta='0'/>
</ap>
</response></ajax-response>`

	clientXMLFixture = `<ajax-response><response type='getstat'>
<client mac='de:ad:be:ef:00:01' ip='10.0.0.5' hostname='iPhone' ssid='CoralGuest' ap-name='Lobby' radio-type-text='11ac' channel='36' received-signal-strength='-84' rssi='28' total-rx-bytes='123456' total-tx-bytes='654321' first-assoc='1700000000'/>
</response></ajax-response>`

	wlanXMLFixture = `<ajax-response><response type='getconf'>
<wlansvc name='CoralGuest' authentication='open' encryption='none' vlan-id='10'/>
<wlansvc name='CoralCorp' authentication='wpa2' encryption='aes' vlan-id='20'/>
</response></ajax-response>`

	systemXMLFixture = `<ajax-response><response type='getconf'><system><identity name='CSHV-ZD'/><mgmt-ip ip='192.168.2.2'/></system></response></ajax-response>`
)

func newTestClient() (*Client, *fakeDoer) {
	d := &fakeDoer{routes: map[string]resp{
		"/": {status: 302, headers: map[string]string{"Location": "/admin10/login.jsp"}}, // root redirect (admin discovery)
		"/admin10/login.jsp":         {status: 302, headers: map[string]string{"Location": "/admin10/dashboard.jsp"}},
		"/admin10/_csrfTokenVar.jsp": {status: 200, body: `<script>var csfrToken = 'ABC1234567';</script>`},
		"/admin10/_cmdstat.jsp":      {status: 200, body: apXMLFixture},
		"/admin10/_conf.jsp":         {status: 200, body: wlanXMLFixture},
	}}
	return New("https://192.168.2.2:443", "admin", "pw", d), d
}

func TestLoginDiscoversAdminAndCSRF(t *testing.T) {
	c, d := newTestClient()
	if err := c.Login(context.Background()); err != nil {
		t.Fatalf("login: %v", err)
	}
	if c.AdminBase() != "admin10" {
		t.Errorf("adminBase = %q, want admin10", c.AdminBase())
	}
	if c.csrf != "ABC1234567" {
		t.Errorf("csrf = %q (10-char csfrToken expected)", c.csrf)
	}
	// drive one AJAX call and confirm the CSRF header was attached
	if _, err := c.postAjax(context.Background(), cmdStat, apStatsXML, true); err != nil {
		t.Fatalf("postAjax: %v", err)
	}
	if d.lastCSRF != "ABC1234567" {
		t.Errorf("X-CSRF-Token not sent on AJAX (got %q)", d.lastCSRF)
	}
}

func TestLoginRejectedShowsLoginPage(t *testing.T) {
	d := &fakeDoer{routes: map[string]resp{
		"/":                  {status: 302, headers: map[string]string{"Location": "/admin10/login.jsp"}},
		"/admin10/login.jsp": {status: 200, body: `<form><input type="password" name="password"></form>`},
	}}
	c := New("https://192.168.2.2", "admin", "wrong", d)
	if err := c.Login(context.Background()); err == nil {
		t.Fatal("expected login rejection on a re-rendered login page")
	}
}

func TestParseAPs(t *testing.T) {
	rows := apRows([]byte(apXMLFixture))
	if len(rows) != 2 {
		t.Fatalf("ap rows = %d, want 2", len(rows))
	}
	// Lobby: state=1 → Connected, client count = 3+5 = 8
	if got := apState(rows[0]["state"]); got != "Connected" {
		t.Errorf("state(1) = %q, want Connected", got)
	}
	if rows[0]["num-sta-total"] != "8" {
		t.Errorf("num-sta-total = %q, want 8 (sum of radios)", rows[0]["num-sta-total"])
	}
	if got := apState(rows[1]["state"]); got != "Disconnected" {
		t.Errorf("state(0) = %q, want Disconnected", got)
	}
}

func TestParseClients(t *testing.T) {
	rows := attrRows([]byte(clientXMLFixture), "client")
	if len(rows) != 1 {
		t.Fatalf("client rows = %d", len(rows))
	}
	m := rows[0]
	if v := getIntPtr(m, "received-signal-strength"); v == nil || *v != -84 {
		t.Errorf("RSSI dBm = %v, want -84", v)
	}
	if v := ruckusSNR(m); v == nil || *v != 28 { // ZD rssi field IS the SNR
		t.Errorf("SNR = %v, want 28 (the rssi field)", v)
	}
	if v := getInt64Ptr(m, "total-rx-bytes"); v == nil || *v != 123456 {
		t.Errorf("rx bytes = %v", v)
	}
	if cs := formatEpoch(m["first-assoc"]); cs == "" || cs == m["first-assoc"] {
		t.Errorf("first-assoc not formatted: %q", cs)
	}
}

func TestCollectEndToEnd(t *testing.T) {
	d := &fakeDoer{routes: map[string]resp{
		"/":                          {status: 302, headers: map[string]string{"Location": "/admin10/login.jsp"}},
		"/admin10/login.jsp":         {status: 302, headers: map[string]string{"Location": "/admin10/dashboard.jsp"}},
		"/admin10/_csrfTokenVar.jsp": {status: 200, body: `<script>var csfrToken = 'TOK1234567';</script>`},
	}}
	// _cmdstat serves AP stats then client stats; _conf serves wlan then system.
	// Our fakeDoer is stateless per-path, so route both comp types to a combined doc.
	d.routes["/admin10/_cmdstat.jsp"] = resp{status: 200, body: apXMLFixture}
	d.routes["/admin10/_conf.jsp"] = resp{status: 200, body: wlanXMLFixture}
	c := New("https://192.168.2.2", "admin", "pw", d)
	res, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if !res.Authenticated || res.AdminBase != "admin10" {
		t.Fatalf("auth=%v adminBase=%q", res.Authenticated, res.AdminBase)
	}
	if len(res.APs) != 2 {
		t.Errorf("APs = %d, want 2", len(res.APs))
	}
	if res.APs[0].Status != "Connected" || res.APs[0].ClientCount != 8 {
		t.Errorf("AP[0] = %+v", res.APs[0])
	}
	if res.Version != "10.1.2.0.306" {
		t.Errorf("version backfill = %q", res.Version)
	}
	if len(res.SSIDs) != 2 {
		t.Errorf("SSIDs = %d, want 2", len(res.SSIDs))
	}
	if res.EventsExposed {
		t.Error("EventsExposed must be false (honest gate — ZD AJAX exposes no events)")
	}
}
