package api

import (
	"bytes"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/xuri/excelize/v2"
)

// The generated template must use ONE consistent example device_key across the
// Devices row and every child sheet, so the unmodified template imports as a
// single complete device (the bug that left Ports orphaned was Devices="device1"
// vs Ports="sw1").
func TestVirtualTemplateConsistentKey(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.virtualTemplateXLSX(rec, httptest.NewRequest("GET", "/devices/virtual/template.xlsx?type=switch", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	xl, err := excelize.OpenReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer xl.Close()
	dev, _ := xl.GetRows("Devices")
	if len(dev) < 2 {
		t.Fatal("Devices sheet missing example row")
	}
	devKey := dev[1][0]
	for _, sh := range []string{"Ports", "VLANs", "Neighbors", "LearnedMACs"} {
		rows, _ := xl.GetRows(sh)
		if len(rows) < 2 {
			continue
		}
		if rows[1][0] != devKey {
			t.Fatalf("%s example device_key=%q but Devices device_key=%q — child rows would orphan", sh, rows[1][0], devKey)
		}
	}
}

// vdAtoiList parses the trunk_vlans cell ("20,30,40" in any common separator).
func TestVdAtoiList(t *testing.T) {
	cases := []struct {
		in   string
		want []int
	}{
		{"20,30,40", []int{20, 30, 40}},
		{"20; 30  40", []int{20, 30, 40}},
		{"", []int{}},
		{"x, 5, 0, 7", []int{5, 7}}, // non-numeric + zero dropped
	}
	for _, c := range cases {
		if got := vdAtoiList(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("vdAtoiList(%q) = %v; want %v", c.in, got, c.want)
		}
	}
}

// headerIndex lowercases headers and strips the "(yes/no)" hint so the importer
// matches "up (yes/no)" → "up".
func TestHeaderIndexNormalizes(t *testing.T) {
	idx := headerIndex([]string{"device_key", "If_Index", "up (yes/no)", "admin_down (yes/no)", "MAC"})
	want := map[string]int{"device_key": 0, "if_index": 1, "up": 2, "admin_down": 3, "mac": 4}
	if !reflect.DeepEqual(idx, want) {
		t.Fatalf("headerIndex = %v; want %v", idx, want)
	}
}

// forEachRow must skip the header + blank rows and report the true 1-based sheet
// row number (so import errors point the operator at the right line).
func TestForEachRowSkipsBlankAndNumbers(t *testing.T) {
	rows := [][]string{
		{"device_key", "name"},
		{"sw1", "Core"},
		{"", ""}, // blank → skipped
		{"sw2", "Edge"},
	}
	type hit struct {
		row  int
		key  string
		name string
	}
	var got []hit
	forEachRow(rows, func(rowNum int, g func(string) string) {
		got = append(got, hit{rowNum, g("device_key"), g("name")})
	})
	want := []hit{{2, "sw1", "Core"}, {4, "sw2", "Edge"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("forEachRow = %+v; want %+v", got, want)
	}
}

// Category-aware template: each rich category's sheet set must include its own
// sections and exclude irrelevant ones (a switch has no Disks/APs sheet).
func TestCategorySheetSelection(t *testing.T) {
	has := func(list []string, s string) bool {
		for _, x := range list {
			if x == s {
				return true
			}
		}
		return false
	}
	sw := vdCategorySheets["switch"]
	for _, want := range []string{"Ports", "VLANs", "Neighbors", "LearnedMACs"} {
		if !has(sw, want) {
			t.Errorf("switch template missing %q sheet", want)
		}
	}
	for _, bad := range []string{"Disks", "APs", "UPS"} {
		if has(sw, bad) {
			t.Errorf("switch template should not include %q sheet", bad)
		}
	}
	for _, want := range []string{"VpnTunnels", "HAMembers", "Licenses"} {
		if !has(vdCategorySheets["firewall"], want) {
			t.Errorf("firewall template missing %q sheet", want)
		}
	}
	if !has(vdCategorySheets["ups"], "UPS") {
		t.Error("ups template missing UPS sheet")
	}
}

// End-to-end parse of a 2-device, 2-category workbook: child rows must attach to
// the correct device by device_key, exercising the multi-device grouping.
func TestParseVirtualWorkbookMultiDevice(t *testing.T) {
	xl := excelize.NewFile()
	defer xl.Close()
	set := func(sheet string, rows [][]any) {
		if sheet != "Sheet1" {
			_, _ = xl.NewSheet(sheet)
		}
		for i, r := range rows {
			_ = xl.SetSheetRow(sheet, "A"+itoaTest(i+1), &r)
		}
	}
	set("Sheet1", [][]any{{"device_key", "name", "category", "status"}, {"sw1", "Core-SW", "switch", "up"}, {"srv1", "App-Srv", "server", "up"}})
	_ = xl.SetSheetName("Sheet1", "Devices")
	set("Ports", [][]any{{"device_key", "if_index", "name", "up (yes/no)", "vlan", "trunk_vlans"}, {"sw1", 1, "Gi1/0/1", "yes", 10, "20,30"}})
	set("Disks", [][]any{{"device_key", "name", "total_bytes"}, {"srv1", "C:", 512000000000}})

	var buf bytes.Buffer
	if err := xl.Write(&buf); err != nil {
		t.Fatal(err)
	}
	rd, err := excelize.OpenReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()

	reqs, rows, errs, fatal := parseVirtualWorkbook(rd)
	if fatal != "" {
		t.Fatalf("fatal: %s", fatal)
	}
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %+v", errs)
	}
	if len(reqs) != 2 || len(rows) != 2 {
		t.Fatalf("want 2 devices, got %d (rows %v)", len(reqs), rows)
	}
	// Device 1: switch with 1 port (trunk 20,30), no disks.
	sw := reqs[0]
	if sw.Name != "Core-SW" || sw.Category != "switch" {
		t.Fatalf("device0 = %s/%s", sw.Name, sw.Category)
	}
	if len(sw.Ports) != 1 || sw.Ports[0].IfIndex != 1 || !reflect.DeepEqual(sw.Ports[0].TrunkVLANs, []int{20, 30}) {
		t.Fatalf("switch ports wrong: %+v", sw.Ports)
	}
	if len(sw.Disks) != 0 {
		t.Fatalf("switch should have no disks, got %d", len(sw.Disks))
	}
	// Device 2: server with 1 disk, no ports.
	srv := reqs[1]
	if srv.Category != "server" || len(srv.Disks) != 1 || srv.Disks[0].TotalBytes != 512000000000 {
		t.Fatalf("server disks wrong: %+v", srv.Disks)
	}
	if len(srv.Ports) != 0 {
		t.Fatalf("server should have no ports, got %d", len(srv.Ports))
	}
}

// A child row whose device_key matches no Devices row must be reported (not
// silently dropped) when the workbook has multiple devices.
func TestParseVirtualWorkbookOrphanChildRow(t *testing.T) {
	xl := excelize.NewFile()
	defer xl.Close()
	set := func(sheet string, rows [][]any) {
		if sheet != "Sheet1" {
			_, _ = xl.NewSheet(sheet)
		}
		for i, r := range rows {
			_ = xl.SetSheetRow(sheet, "A"+itoaTest(i+1), &r)
		}
	}
	set("Sheet1", [][]any{{"device_key", "name", "category", "status"}, {"sw1", "SW-A", "switch", "up"}, {"sw2", "SW-B", "switch", "up"}})
	_ = xl.SetSheetName("Sheet1", "Devices")
	// "typo" matches neither sw1 nor sw2.
	set("Ports", [][]any{{"device_key", "if_index", "name"}, {"typo", 1, "Gi1/0/1"}})

	var buf bytes.Buffer
	if err := xl.Write(&buf); err != nil {
		t.Fatal(err)
	}
	rd, err := excelize.OpenReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()

	reqs, _, errs, fatal := parseVirtualWorkbook(rd)
	if fatal != "" {
		t.Fatalf("fatal: %s", fatal)
	}
	if len(reqs) != 2 {
		t.Fatalf("want 2 devices, got %d", len(reqs))
	}
	for _, r := range reqs {
		if len(r.Ports) != 0 {
			t.Fatalf("%s should have no ports (orphaned row), got %d", r.Name, len(r.Ports))
		}
	}
	found := false
	for _, e := range errs {
		if e.Sheet == "Ports" && e.Field == "device_key" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a Ports/device_key orphan error, got %+v", errs)
	}
}

func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
