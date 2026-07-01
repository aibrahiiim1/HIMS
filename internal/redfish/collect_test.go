package redfish

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// fakeDoer routes a request path to a canned JSON body (200), or 404.
type fakeDoer struct{ routes map[string]string }

func (f fakeDoer) Do(req *http.Request) (*http.Response, error) {
	body, ok := f.routes[req.URL.Path]
	code := http.StatusOK
	if !ok {
		body, code = `{"error":"not found"}`, http.StatusNotFound
	}
	return &http.Response{
		StatusCode: code,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}, nil
}

// An HPE iLO 5-shaped tree (trimmed to the fields the collector reads).
var hpeRoutes = map[string]string{
	"/redfish/v1/": `{"Vendor":"HPE","Product":"ProLiant","Oem":{"Hpe":{}},
		"Systems":{"@odata.id":"/redfish/v1/Systems"},
		"Chassis":{"@odata.id":"/redfish/v1/Chassis"},
		"Managers":{"@odata.id":"/redfish/v1/Managers"}}`,
	"/redfish/v1/Systems": `{"Members":[{"@odata.id":"/redfish/v1/Systems/1"}]}`,
	"/redfish/v1/Systems/1": `{"Manufacturer":"HPE","Model":"ProLiant DL360 Gen10","SKU":"868703-B21",
		"SerialNumber":"CZ200xxxxx","BiosVersion":"U32 v2.50","PowerState":"On",
		"Status":{"Health":"OK","State":"Enabled"},
		"ProcessorSummary":{"Count":2,"Model":"Intel Xeon Gold 6130"},
		"MemorySummary":{"TotalSystemMemoryGiB":256},
		"Processors":{"@odata.id":"/redfish/v1/Systems/1/Processors"},
		"Memory":{"@odata.id":"/redfish/v1/Systems/1/Memory"},
		"Storage":{"@odata.id":"/redfish/v1/Systems/1/Storage"}}`,
	"/redfish/v1/Systems/1/Processors":   `{"Members":[{"@odata.id":"/redfish/v1/Systems/1/Processors/1"}]}`,
	"/redfish/v1/Systems/1/Processors/1": `{"Socket":"Proc 1","Model":"Intel Xeon Gold 6130","TotalCores":16,"TotalThreads":32,"MaxSpeedMHz":2100,"Status":{"Health":"OK"}}`,
	"/redfish/v1/Systems/1/Memory":       `{"Members":[{"@odata.id":"/redfish/v1/Systems/1/Memory/1"},{"@odata.id":"/redfish/v1/Systems/1/Memory/2"}]}`,
	"/redfish/v1/Systems/1/Memory/1":     `{"DeviceLocator":"DIMM 1","CapacityMiB":16384,"MemoryDeviceType":"DDR4","OperatingSpeedMhz":2666,"Status":{"Health":"OK"}}`,
	"/redfish/v1/Systems/1/Memory/2":     `{"DeviceLocator":"DIMM 2","CapacityMiB":0}`,
	"/redfish/v1/Systems/1/Storage":      `{"Members":[{"@odata.id":"/redfish/v1/Systems/1/Storage/DA000"}]}`,
	"/redfish/v1/Systems/1/Storage/DA000": `{"Name":"Smart Array P408i",
		"StorageControllers":[{"Name":"Smart Array P408i-a","Model":"P408i-a SR Gen10","FirmwareVersion":"3.53","Status":{"Health":"OK"},"SupportedRAIDTypes":["RAID0","RAID1","RAID5"]}],
		"Volumes":{"@odata.id":"/redfish/v1/Systems/1/Storage/DA000/Volumes"},
		"Drives":[{"@odata.id":"/d/1"},{"@odata.id":"/d/2"}]}`,
	"/redfish/v1/Systems/1/Storage/DA000/Volumes": `{"Members":[{"@odata.id":"/v/1"}]}`,
	"/v/1":                  `{"Name":"OS Volume","RAIDType":"RAID1","CapacityBytes":960197124096,"Status":{"Health":"OK"}}`,
	"/d/1":                  `{"Name":"Drive 1","Model":"EG000600JWFUV","SerialNumber":"S1","MediaType":"HDD","Protocol":"SAS","CapacityBytes":600127266816,"RotationSpeedRPM":10000,"Status":{"Health":"OK"}}`,
	"/d/2":                  `{"Name":"Drive 2","Model":"VK000480GWTHA","SerialNumber":"S2","MediaType":"SSD","Protocol":"SATA","CapacityBytes":480103981056,"Status":{"Health":"OK"}}`,
	"/redfish/v1/Chassis":   `{"Members":[{"@odata.id":"/redfish/v1/Chassis/1"}]}`,
	"/redfish/v1/Chassis/1": `{"Thermal":{"@odata.id":"/redfish/v1/Chassis/1/Thermal"},"Power":{"@odata.id":"/redfish/v1/Chassis/1/Power"}}`,
	"/redfish/v1/Chassis/1/Thermal": `{"Temperatures":[{"Name":"01-Inlet","ReadingCelsius":21,"Status":{"Health":"OK"}}],
		"Fans":[{"Name":"Fan 1","Reading":34,"ReadingUnits":"Percent","Status":{"Health":"OK"}},
		        {"Name":"Fan 2","Reading":0,"ReadingUnits":"Percent","Status":{"Health":"Critical"}}]}`,
	"/redfish/v1/Chassis/1/Power": `{"PowerSupplies":[{"Name":"PSU 1","LastPowerOutputWatts":120,"Status":{"Health":"OK"}}]}`,
	"/redfish/v1/Managers":        `{"Members":[{"@odata.id":"/redfish/v1/Managers/1"}]}`,
	"/redfish/v1/Managers/1":      `{"Model":"iLO 5","FirmwareVersion":"2.78 Mar 23 2023"}`,
}

func countKind(s []Sensor, kind string) int {
	n := 0
	for _, x := range s {
		if x.Kind == kind {
			n++
		}
	}
	return n
}

func TestCollect_HPEiLO(t *testing.T) {
	c := NewClient("https://10.0.0.50", "admin", "secret", fakeDoer{routes: hpeRoutes})
	f, err := Collect(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if f.Vendor != "HPE" || f.ControllerKind != "iLO" {
		t.Fatalf("vendor/kind = %s/%s; want HPE/iLO", f.Vendor, f.ControllerKind)
	}
	if f.Model != "ProLiant DL360 Gen10" || f.Serial != "CZ200xxxxx" {
		t.Fatalf("model/serial wrong: %q / %q", f.Model, f.Serial)
	}
	if f.PowerState != "On" || f.Health != "OK" {
		t.Fatalf("power/health = %s/%s", f.PowerState, f.Health)
	}
	if f.FirmwareVersion != "2.78 Mar 23 2023" {
		t.Fatalf("firmware = %q", f.FirmwareVersion)
	}
	if f.ProcessorCount != 2 || f.MemoryGiB != 256 {
		t.Fatalf("cpu/mem = %d / %v", f.ProcessorCount, f.MemoryGiB)
	}
	if countKind(f.Sensors, "fan") != 2 || countKind(f.Sensors, "psu") != 1 ||
		countKind(f.Sensors, "temperature") != 1 {
		t.Fatalf("sensor mix wrong: %+v", f.Sensors)
	}
	// Detailed inventory: 1 CPU (16 cores), 1 populated DIMM (empty slot skipped),
	// 1 RAID controller, 1 volume (RAID1), 2 physical drives (HDD + SSD).
	kinds := map[string]int{}
	for _, c := range f.Components {
		kinds[c.Kind]++
	}
	if kinds["cpu"] != 1 || kinds["memory"] != 1 || kinds["controller"] != 1 || kinds["volume"] != 1 || kinds["drive"] != 2 {
		t.Fatalf("component mix wrong: %+v", f.Components)
	}
	if f.ProcessorCores != 16 {
		t.Fatalf("cpu cores = %d, want 16", f.ProcessorCores)
	}
	var raid1, ssd bool
	for _, c := range f.Components {
		if c.Kind == "volume" && c.Detail["raid"] == "RAID1" {
			raid1 = true
		}
		if c.Kind == "drive" && c.Detail["media"] == "SSD" && c.Detail["protocol"] == "SATA" {
			ssd = true
		}
	}
	if !raid1 || !ssd {
		t.Fatalf("RAID/media detail not captured: raid1=%v ssd=%v; %+v", raid1, ssd, f.Components)
	}
	// The critical fan must carry its status through.
	var critical bool
	for _, s := range f.Sensors {
		if s.Kind == "fan" && s.Name == "Fan 2" && s.Status == "Critical" {
			critical = true
		}
	}
	if !critical {
		t.Fatalf("critical fan status not preserved: %+v", f.Sensors)
	}
}

func TestCollect_DellOemDetect(t *testing.T) {
	routes := map[string]string{
		"/redfish/v1/": `{"Oem":{"Dell":{}},"Systems":{"@odata.id":"/redfish/v1/Systems"},
			"Chassis":{"@odata.id":"/x"},"Managers":{"@odata.id":"/y"}}`,
		"/redfish/v1/Systems":                   `{"Members":[{"@odata.id":"/redfish/v1/Systems/System.Embedded.1"}]}`,
		"/redfish/v1/Systems/System.Embedded.1": `{"Manufacturer":"Dell Inc.","Model":"PowerEdge R740","SerialNumber":"ABC123","PowerState":"On","Status":{"Health":"OK"}}`,
	}
	c := NewClient("https://10.0.0.51", "root", "calvin", fakeDoer{routes: routes})
	f, err := Collect(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if f.Vendor != "Dell" || f.ControllerKind != "iDRAC" {
		t.Fatalf("Dell OEM detect failed: %s/%s", f.Vendor, f.ControllerKind)
	}
	if f.Model != "PowerEdge R740" {
		t.Fatalf("model = %q", f.Model)
	}
}

func TestGetJSON_Non2xxErrors(t *testing.T) {
	c := NewClient("https://10.0.0.99", "u", "p", fakeDoer{routes: map[string]string{}})
	var v map[string]any
	if err := c.GetJSON(context.Background(), "/redfish/v1/", &v); err == nil {
		t.Fatal("expected error on 404")
	}
}
