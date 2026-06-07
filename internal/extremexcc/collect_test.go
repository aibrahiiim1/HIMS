package extremexcc

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// fakeDoer returns a canned response per path substring.
type fakeDoer struct{ routes map[string]string }

func (f fakeDoer) Do(req *http.Request) (*http.Response, error) {
	for k, body := range f.routes {
		if strings.Contains(req.URL.Path, k) {
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": {"application/json"}},
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		}
	}
	return &http.Response{StatusCode: 404, Header: http.Header{"Content-Type": {"text/html"}}, Body: io.NopCloser(strings.NewReader("<html>"))}, nil
}

func TestExtractRowsShapes(t *testing.T) {
	// bare array
	if rows := extractRows([]byte(`[{"name":"ap1"},{"name":"ap2"}]`)); len(rows) != 2 {
		t.Fatalf("bare array: got %d", len(rows))
	}
	// wrapped in data
	if rows := extractRows([]byte(`{"data":[{"name":"ap1"}]}`)); len(rows) != 1 {
		t.Fatalf("data wrapper: got %d", len(rows))
	}
	// wrapped in aps
	if rows := extractRows([]byte(`{"aps":[{"name":"x"},{"name":"y"},{"name":"z"}]}`)); len(rows) != 3 {
		t.Fatalf("aps wrapper: got %d", len(rows))
	}
}

func TestCollectParsesRosters(t *testing.T) {
	doer := fakeDoer{routes: map[string]string{
		"/aps":      `[{"name":"AP-Lobby","macAddress":"aa:bb:cc:00:11:22","ipAddress":"172.21.96.150","model":"AP410C","serialNumber":"SN123","softwareVersion":"10.5","status":"online","clientCount":7}]`,
		"/services": `{"data":[{"ssid":"CoralGuest","enabled":true,"security":"wpa2-psk","band":"5GHz","clientCount":4}]}`,
		"/stations": `[{"macAddress":"de:ad:be:ef:00:01","ipAddress":"10.0.0.5","hostname":"phone","apName":"AP-Lobby","ssid":"CoralGuest","rss":-55,"band":"5GHz"}]`,
	}}
	c := NewClient("https://ctrl:8443", "/management/v1", "admin", "pw", "", doer)
	res, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(res.APs) != 1 || res.APs[0].Name != "AP-Lobby" || res.APs[0].Model != "AP410C" || res.APs[0].ClientCount != 7 || res.APs[0].Status != "online" {
		t.Fatalf("AP parse: %+v", res.APs)
	}
	if len(res.SSIDs) != 1 || res.SSIDs[0].Name != "CoralGuest" || res.SSIDs[0].Status != "enabled" {
		t.Fatalf("SSID parse: %+v", res.SSIDs)
	}
	if len(res.Stations) != 1 || res.Stations[0].MAC != "de:ad:be:ef:00:01" || res.Stations[0].RSSI == nil || *res.Stations[0].RSSI != -55 {
		t.Fatalf("station parse: %+v", res.Stations)
	}
}

// TestCollectXIQCRichFields exercises the proven XIQC field mappings: aps/query
// real status, per-radio noise → computed client SNR, stations/query byte counters,
// privacy-type security (no PSK), and version backfill from AP firmware.
func TestCollectXIQCRichFields(t *testing.T) {
	doer := fakeDoer{routes: map[string]string{
		"/aps/query":      `[{"apName":"AP-1","serialNumber":"SN1","platformName":"AP410C","macAddress":"aa:bb:cc:00:11:22","ipAddress":"172.21.96.150","softwareVersion":"10.5.4.0-002R","status":"InService","hostSite":"Aqua Club","radios":[{"opChannel":36,"noise":-95}]}]`,
		"/stations/query": `[{"macAddress":"de:ad:be:ef:00:01","ipAddress":"10.0.0.5","dhcpHostName":"phone","accessPointSerialNumber":"SN1","accessPointName":"AP-1","serviceName":"Corp","protocol":"802.11ac","rss":-60,"channel":36,"inBytes":1000,"outBytes":2000}]`,
		"/services":       `[{"ssid":"Corp","status":"enabled","privacy":{"WpaPsk2Element":{"mode":"aesOnly"}},"dot1dPortNumber":10}]`,
	}}
	c := NewClient("https://ctrl:5825", "/management/v1", "admin", "pw", "", doer)
	res, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(res.APs) != 1 {
		t.Fatalf("APs: %+v", res.APs)
	}
	ap := res.APs[0]
	if ap.Status != "In Service" {
		t.Errorf("AP status = %q, want In Service", ap.Status)
	}
	if ap.Site != "Aqua Club" {
		t.Errorf("AP site = %q", ap.Site)
	}
	if res.Version != "10.5.4.0-002R" {
		t.Errorf("controller version backfill = %q", res.Version)
	}
	if len(res.Stations) != 1 {
		t.Fatalf("stations: %+v", res.Stations)
	}
	st := res.Stations[0]
	if st.RSSI == nil || *st.RSSI != -60 {
		t.Errorf("RSSI = %v, want -60", st.RSSI)
	}
	if st.SNR == nil || *st.SNR != 35 { // -60 − (−95) = 35
		t.Errorf("SNR = %v, want 35", st.SNR)
	}
	if st.RxBytes == nil || *st.RxBytes != 1000 || st.TxBytes == nil || *st.TxBytes != 2000 {
		t.Errorf("bytes rx=%v tx=%v", st.RxBytes, st.TxBytes)
	}
	if st.ConnectedSince != "" {
		t.Errorf("ConnectedSince should be N/A for XIQC, got %q", st.ConnectedSince)
	}
	if len(res.SSIDs) != 1 || res.SSIDs[0].Security != "WPA/WPA2 PSK (aesOnly)" {
		t.Errorf("SSID security = %+v (PSK must never leak)", res.SSIDs)
	}
}

func TestNormStatus(t *testing.T) {
	for in, want := range map[string]string{"up": "online", "Connected": "online", "down": "offline", "": "unknown", "weird": "unknown"} {
		if got := normStatus(in); got != want {
			t.Errorf("normStatus(%q)=%q want %q", in, got, want)
		}
	}
}
