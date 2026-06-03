package redfish

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
	rf "github.com/coralsearesorts/hims/internal/redfish"
)

func TestFingerprint_iLOBanner(t *testing.T) {
	d := New()
	m := d.Fingerprint(driver.Probe{HTTPServer: "HPE iLO 5", OpenTCPPorts: []int{443}})
	if m.Confidence != 72 || m.Category != domain.CatServer {
		t.Fatalf("iLO banner = %+v; want 72 server", m)
	}
}

func TestFingerprint_NoMatch(t *testing.T) {
	d := New()
	if m := d.Fingerprint(driver.Probe{HTTPServer: "nginx"}); m.Confidence != 0 {
		t.Fatalf("plain web server should not match; got %+v", m)
	}
}

type fakeDoer struct{ routes map[string]string }

func (f fakeDoer) Do(req *http.Request) (*http.Response, error) {
	body, ok := f.routes[req.URL.Path]
	code := http.StatusOK
	if !ok {
		body, code = `{}`, http.StatusNotFound
	}
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
}

func TestCollect_MapsBMCFactsToSnap(t *testing.T) {
	routes := map[string]string{
		"/redfish/v1/":          `{"Oem":{"Dell":{}},"Systems":{"@odata.id":"/redfish/v1/Systems"},"Chassis":{"@odata.id":"/c"},"Managers":{"@odata.id":"/m"}}`,
		"/redfish/v1/Systems":   `{"Members":[{"@odata.id":"/redfish/v1/Systems/1"}]}`,
		"/redfish/v1/Systems/1": `{"Model":"PowerEdge R740","SerialNumber":"SVC123","PowerState":"On","Status":{"Health":"OK"},"MemorySummary":{"TotalSystemMemoryGiB":128},"ProcessorSummary":{"Count":2}}`,
	}
	client := rf.NewClient("https://10.0.0.51", "root", "calvin", fakeDoer{routes: routes})
	d := New()
	f, err := d.Collect(&Session{Client: client, Ctx: context.Background()}, driver.Probe{})
	if err != nil {
		t.Fatal(err)
	}
	if f.BMC == nil || f.BMC.Vendor != "Dell" || f.BMC.ControllerKind != "iDRAC" {
		t.Fatalf("BMC snap not mapped: %+v", f.BMC)
	}
	if f.Model != "PowerEdge R740" || f.Serial != "SVC123" {
		t.Fatalf("identity not mapped: %q / %q", f.Model, f.Serial)
	}
	if f.KV["cpu.count"] != "2" || f.KV["memory.total_bytes"] == "" {
		t.Fatalf("KV facts not mapped: %+v", f.KV)
	}
}

func TestCollect_WrongSessionType(t *testing.T) {
	d := New()
	if _, err := d.Collect(&driver.SessionBase{}, driver.Probe{}); err == nil {
		t.Fatal("expected error for non-redfish session")
	}
}
