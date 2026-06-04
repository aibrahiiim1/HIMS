package reports

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

func sampleReport() Report {
	return Report{
		Title:     "Inventory",
		Generated: time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC),
		Sheets: []Sheet{
			{Name: "Devices", Headers: []string{"Name", "IP", "Category"}, Rows: [][]string{
				{"sw1", "10.0.0.1", "switch"},
				{"fw1", "10.0.0.2", "firewall"},
			}},
			{Name: "By Vendor", Headers: []string{"Vendor", "Count"}, Rows: [][]string{{"Cisco", "5"}}},
		},
	}
}

func TestCSVMultiSheet(t *testing.T) {
	b, err := sampleReport().CSV()
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "# Devices") || !strings.Contains(s, "# By Vendor") {
		t.Errorf("multi-sheet CSV missing sheet banners:\n%s", s)
	}
	if !strings.Contains(s, "sw1,10.0.0.1,switch") {
		t.Errorf("CSV missing data row:\n%s", s)
	}
}

func TestXLSXRoundTrip(t *testing.T) {
	b, err := sampleReport().XLSX()
	if err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("output is not a valid xlsx: %v", err)
	}
	defer f.Close()
	sheets := f.GetSheetList()
	if len(sheets) != 2 || sheets[0] != "Devices" || sheets[1] != "By Vendor" {
		t.Fatalf("sheets = %v, want [Devices, By Vendor]", sheets)
	}
	v, _ := f.GetCellValue("Devices", "A1")
	if v != "Name" {
		t.Errorf("A1 = %q, want header Name", v)
	}
	v, _ = f.GetCellValue("Devices", "B3")
	if v != "10.0.0.2" {
		t.Errorf("B3 = %q, want 10.0.0.2", v)
	}
}

func TestSanitizeSheetName(t *testing.T) {
	if got := sanitizeSheetName("A/B:C", 0); strings.ContainsAny(got, `[]:*?/\`) {
		t.Errorf("sanitized name still has illegal chars: %q", got)
	}
	if got := sanitizeSheetName("", 2); got != "Sheet3" {
		t.Errorf("empty name = %q, want Sheet3", got)
	}
	long := strings.Repeat("x", 40)
	if got := sanitizeSheetName(long, 0); len(got) != 31 {
		t.Errorf("long name not truncated to 31, got %d", len(got))
	}
}

func TestSummary(t *testing.T) {
	s := sampleReport().Summary()
	if !strings.Contains(s, "Inventory") || !strings.Contains(s, "Devices: 2 row") {
		t.Errorf("summary missing expected content:\n%s", s)
	}
}
