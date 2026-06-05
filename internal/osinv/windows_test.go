package osinv

import (
	"context"
	"strings"
	"testing"
)

// winMock returns canned per-section JSON based on which snippet was requested
// (matched by a distinctive substring) — mirroring what the real PowerShell
// emits, so CollectWindows assembly is tested without a Windows host.
type winMock struct{}

func (winMock) Run(_ context.Context, script string) (string, error) {
	switch {
	case strings.Contains(script, "Win32_OperatingSystem"):
		return `{"hostname":"DC01","fqdn":"DC01.corp.local","domain":"corp.local","workgroup":"","logged_on_user":"CORP\\svc","caption":"Microsoft Windows Server 2019 Standard","version":"10.0.17763","build":"17763","arch":"64-bit","install_date":"2021-03-01T10:00:00.000+00:00","last_boot":"2026-06-01T02:00:00.000+00:00","uptime_seconds":345600,"timezone":"(UTC+03:00) Istanbul","manufacturer":"Dell Inc.","model":"PowerEdge R740","serial":"ABC1234","bios_version":"2.10.2","bios_date":"2022-05-01T00:00:00.000+00:00","cpu_model":"Intel Xeon Silver 4210","cpu_sockets":2,"cpu_cores":20,"ram_total_bytes":68719476736}`, nil
	case strings.Contains(script, "Win32_LogicalDisk"):
		// single row → ConvertTo-Json emits an OBJECT, not an array (jsonArray must cope)
		return `{"name":"C:","filesystem":"NTFS","total_bytes":107374182400,"free_bytes":42949672960,"size_bytes":107374182400}`, nil
	case strings.Contains(script, "Win32_NetworkAdapterConfiguration"):
		return `[{"name":"Intel I350","mac":"AA:BB:CC:DD:EE:FF","ip_addresses":"10.0.0.10","gateway":"10.0.0.1","dns_servers":"10.0.0.10,10.0.0.11","dhcp_enabled":false}]`, nil
	case strings.Contains(script, "Win32_Service"):
		return `[{"name":"NTDS","display_name":"AD DS","status":"Running","start_type":"Auto","account":"LocalSystem"},{"name":"DNS","display_name":"DNS Server","status":"Running","start_type":"Auto"},{"name":"MSSQL$SQLEXPRESS","display_name":"SQL (SQLEXPRESS)","status":"Running"},{"name":"Spooler","display_name":"Print Spooler","status":"Running"}]`, nil
	case strings.Contains(script, "Get-Process"):
		return `[{"name":"sqlservr","pid":1234,"mem_bytes":2147483648}]`, nil
	case strings.Contains(script, "Uninstall"):
		return `[{"name":"Microsoft SQL Server 2019","version":"15.0.2000.5","publisher":"Microsoft Corporation","install_date":"20210301"}]`, nil
	case strings.Contains(script, "Get-WinEvent"):
		return `{"critical_24h":0,"error_24h":3,"warning_24h":12}`, nil
	}
	return "", nil
}

func TestCollectWindows_Assembles(t *testing.T) {
	rep, err := CollectWindows(context.Background(), winMock{})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if rep.Method != "winrm" || rep.Identity.Hostname != "DC01" || rep.OS.Build != "17763" {
		t.Errorf("summary wrong: %+v / %+v", rep.Identity, rep.OS)
	}
	if rep.Hardware.RAMTotalBytes != 68719476736 || rep.Hardware.CPUCores != 20 || rep.Hardware.Serial != "ABC1234" {
		t.Errorf("hardware wrong: %+v", rep.Hardware)
	}
	// single-object disk JSON must still yield one Disk (unary-array trick)
	if len(rep.Disks) != 1 || rep.Disks[0].Name != "C:" || rep.Disks[0].FreeBytes != 42949672960 {
		t.Errorf("disks wrong: %+v", rep.Disks)
	}
	if len(rep.Nics) != 1 || len(rep.Services) != 4 || len(rep.Processes) != 1 || len(rep.Software) != 1 {
		t.Errorf("collection counts wrong: nics=%d svc=%d proc=%d sw=%d", len(rep.Nics), len(rep.Services), len(rep.Processes), len(rep.Software))
	}
	if rep.Events == nil || rep.Events.Error24h != 3 || rep.Events.Warning24h != 12 {
		t.Errorf("events wrong: %+v", rep.Events)
	}
}

// summaryErr fails the first (summary) call → CollectWindows must surface it.
type summaryErr struct{}

func (summaryErr) Run(context.Context, string) (string, error) { return "", errFail }

var errFail = &simpleErr{"ssh: unable to authenticate"}

type simpleErr struct{ s string }

func (e *simpleErr) Error() string { return e.s }

func TestCollectWindows_SummaryFailureAborts(t *testing.T) {
	if _, err := CollectWindows(context.Background(), summaryErr{}); err == nil {
		t.Error("summary failure must return an error (so the caller reports it)")
	}
}

func TestDetectWindowsRoles(t *testing.T) {
	rep, _ := CollectWindows(context.Background(), winMock{})
	roles := DetectWindowsRoles(rep)
	want := map[string]bool{"Domain Controller": true, "DNS Server": true, "SQL Server": true}
	for _, r := range roles {
		if r == "Print Spooler" || r == "" {
			t.Errorf("spooler must not be a role; got %q", r)
		}
		delete(want, r)
	}
	if len(want) != 0 {
		t.Errorf("missing roles: %v (got %v)", want, roles)
	}
}

func TestJSONArray_ObjectAndArray(t *testing.T) {
	one, _ := jsonArray[Disk]([]byte(`{"name":"C:"}`))
	if len(one) != 1 || one[0].Name != "C:" {
		t.Errorf("single object should yield 1 element: %+v", one)
	}
	many, _ := jsonArray[Disk]([]byte(`[{"name":"C:"},{"name":"D:"}]`))
	if len(many) != 2 {
		t.Errorf("array should yield 2: %+v", many)
	}
	if empty, _ := jsonArray[Disk]([]byte(`  `)); empty != nil {
		t.Errorf("empty should be nil, got %+v", empty)
	}
}
