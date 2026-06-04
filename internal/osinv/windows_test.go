package osinv

import "testing"

// winFixture mirrors the compact JSON the windowsScript emits (the contract
// between the PowerShell and ParseWindows). A DC running DNS + a SQL instance.
const winFixture = `{"method":"winrm",
"identity":{"hostname":"DC01","fqdn":"DC01.corp.local","domain":"corp.local","workgroup":"","logged_on_user":"CORP\\svc"},
"os":{"caption":"Microsoft Windows Server 2019 Standard","version":"10.0.17763","build":"17763","edition":"7","arch":"64-bit","install_date":"2021-03-01T10:00:00.000+00:00","last_boot":"2026-06-01T02:00:00.000+00:00","uptime_seconds":345600,"timezone":"(UTC+03:00) Istanbul"},
"hardware":{"manufacturer":"Dell Inc.","model":"PowerEdge R740","serial":"ABC1234","bios_version":"2.10.2","bios_date":"2022-05-01T00:00:00.000+00:00","cpu_model":"Intel(R) Xeon(R) Silver 4210","cpu_sockets":2,"cpu_cores":20,"ram_total_bytes":68719476736},
"disks":[{"name":"C:","filesystem":"NTFS","total_bytes":107374182400,"free_bytes":42949672960,"size_bytes":107374182400}],
"nics":[{"name":"Intel I350","mac":"AA:BB:CC:DD:EE:FF","ip_addresses":"10.0.0.10","gateway":"10.0.0.1","dns_servers":"10.0.0.10,10.0.0.11","dhcp_enabled":false}],
"services":[{"name":"NTDS","display_name":"Active Directory Domain Services","status":"Running","start_type":"Auto","account":"LocalSystem"},
{"name":"DNS","display_name":"DNS Server","status":"Running","start_type":"Auto","account":"LocalSystem"},
{"name":"MSSQL$SQLEXPRESS","display_name":"SQL Server (SQLEXPRESS)","status":"Running","start_type":"Auto","account":"NT Service"},
{"name":"Spooler","display_name":"Print Spooler","status":"Running","start_type":"Auto","account":"LocalSystem"}],
"processes":[{"name":"sqlservr","pid":1234,"mem_bytes":2147483648,"start_time":"2026-06-01T02:01:00.000+00:00"}],
"software":[{"name":"Microsoft SQL Server 2019","version":"15.0.2000.5","publisher":"Microsoft Corporation","install_date":"20210301"}],
"events":{"critical_24h":0,"error_24h":3,"warning_24h":12,"last_critical":""}}`

func TestParseWindows(t *testing.T) {
	rep, err := ParseWindows([]byte(winFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if rep.Method != "winrm" {
		t.Errorf("method = %q", rep.Method)
	}
	if rep.Identity.Hostname != "DC01" || rep.Identity.Domain != "corp.local" {
		t.Errorf("identity wrong: %+v", rep.Identity)
	}
	if rep.OS.Caption != "Microsoft Windows Server 2019 Standard" || rep.OS.Build != "17763" {
		t.Errorf("os wrong: %+v", rep.OS)
	}
	if rep.Hardware.RAMTotalBytes != 68719476736 || rep.Hardware.CPUCores != 20 || rep.Hardware.Serial != "ABC1234" {
		t.Errorf("hardware wrong: %+v", rep.Hardware)
	}
	if len(rep.Disks) != 1 || rep.Disks[0].Name != "C:" || rep.Disks[0].FreeBytes != 42949672960 {
		t.Errorf("disks wrong: %+v", rep.Disks)
	}
	if len(rep.Nics) != 1 || rep.Nics[0].MAC != "AA:BB:CC:DD:EE:FF" || rep.Nics[0].DHCPEnabled {
		t.Errorf("nics wrong: %+v", rep.Nics)
	}
	if len(rep.Services) != 4 || len(rep.Processes) != 1 || len(rep.Software) != 1 {
		t.Errorf("collection counts wrong: svc=%d proc=%d sw=%d", len(rep.Services), len(rep.Processes), len(rep.Software))
	}
	if rep.Events == nil || rep.Events.Error24h != 3 || rep.Events.Warning24h != 12 {
		t.Errorf("events wrong: %+v", rep.Events)
	}
}

func TestParseWindows_Empty(t *testing.T) {
	if _, err := ParseWindows([]byte("   ")); err == nil {
		t.Error("empty output should error")
	}
}

func TestDetectWindowsRoles(t *testing.T) {
	rep, _ := ParseWindows([]byte(winFixture))
	roles := DetectWindowsRoles(rep)
	want := map[string]bool{"Domain Controller": true, "DNS Server": true, "SQL Server": true}
	for _, r := range roles {
		delete(want, r)
		if r == "Print Spooler" || r == "" {
			t.Errorf("spooler must not be a role; got %q", r)
		}
	}
	if len(want) != 0 {
		t.Errorf("missing roles: %v (got %v)", want, roles)
	}
	// SQL detected once despite both the named-instance service AND no dup.
	count := 0
	for _, r := range roles {
		if r == "SQL Server" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("SQL Server should appear once, got %d", count)
	}
}
