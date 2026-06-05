package osinv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Windows deep inventory over WinRM/PowerShell (Get-CimInstance — WMI without
// DCOM). The data is gathered as a SET OF SMALL per-section commands rather than
// one big script: WinRM runs PowerShell via `-EncodedCommand`, and a single
// combined script exceeds the Windows command-line length limit ("The command
// line is too long"). Each small section also keeps a failing section (e.g.
// event-log access denied) from sinking the whole collection — it just stays
// "Not collected". Auth/transport (NTLM + message encryption) is the caller's
// Runner concern.

// winSummary is the flat shape of the summary snippet's JSON.
type winSummary struct {
	Hostname     string `json:"hostname"`
	FQDN         string `json:"fqdn"`
	Domain       string `json:"domain"`
	Workgroup    string `json:"workgroup"`
	LoggedOnUser string `json:"logged_on_user"`
	Caption      string `json:"caption"`
	Version      string `json:"version"`
	Build        string `json:"build"`
	Arch         string `json:"arch"`
	InstallDate  string `json:"install_date"`
	LastBoot     string `json:"last_boot"`
	Uptime       int64  `json:"uptime_seconds"`
	Timezone     string `json:"timezone"`
	Manufacturer string `json:"manufacturer"`
	Model        string `json:"model"`
	Serial       string `json:"serial"`
	BIOSVersion  string `json:"bios_version"`
	BIOSDate     string `json:"bios_date"`
	CPUModel     string `json:"cpu_model"`
	CPUSockets   int    `json:"cpu_sockets"`
	CPUCores     int    `json:"cpu_cores"`
	RAMTotal     int64  `json:"ram_total_bytes"`
}

// PowerShell snippets — each small enough for -EncodedCommand. Collections force
// an array with the unary-array trick handled by jsonArray (single object OR
// array both parse). No backticks (Go raw strings).
const (
	winSummaryPS = `$os=Get-CimInstance Win32_OperatingSystem;$cs=Get-CimInstance Win32_ComputerSystem;$b=Get-CimInstance Win32_BIOS;$c=@(Get-CimInstance Win32_Processor);$f=$cs.DNSHostName;if($cs.PartOfDomain){$f="$($cs.DNSHostName).$($cs.Domain)"};$u=0;if($os.LastBootUpTime){$u=[int64]((Get-Date)-$os.LastBootUpTime).TotalSeconds};[pscustomobject]@{hostname=$cs.Name;fqdn=$f;domain=$cs.Domain;workgroup=$cs.Workgroup;logged_on_user=$cs.UserName;caption=$os.Caption;version=$os.Version;build=$os.BuildNumber;arch=$os.OSArchitecture;install_date=($os.InstallDate.ToString('o'));last_boot=($os.LastBootUpTime.ToString('o'));uptime_seconds=$u;timezone=(Get-CimInstance Win32_TimeZone).Caption;manufacturer=$cs.Manufacturer;model=$cs.Model;serial=$b.SerialNumber;bios_version=(@($b.BIOSVersion)-join ' ');bios_date=($b.ReleaseDate.ToString('o'));cpu_model=$c[0].Name;cpu_sockets=$c.Count;cpu_cores=(($c|Measure-Object NumberOfCores -Sum).Sum);ram_total_bytes=[int64]$cs.TotalPhysicalMemory}|ConvertTo-Json -Compress`

	winDisksPS = `@(Get-CimInstance Win32_LogicalDisk -Filter "DriveType=3"|ForEach-Object{[pscustomobject]@{name=$_.DeviceID;filesystem=$_.FileSystem;total_bytes=[int64]$_.Size;free_bytes=[int64]$_.FreeSpace;size_bytes=[int64]$_.Size}})|ConvertTo-Json -Compress`

	winNicsPS = `@(Get-CimInstance Win32_NetworkAdapterConfiguration -Filter "IPEnabled=True"|ForEach-Object{[pscustomobject]@{name=$_.Description;mac=$_.MACAddress;ip_addresses=($_.IPAddress -join ',');gateway=(@($_.DefaultIPGateway)[0]);dns_servers=($_.DNSServerSearchOrder -join ',');dhcp_enabled=[bool]$_.DHCPEnabled}})|ConvertTo-Json -Compress`

	winServicesPS = `@(Get-CimInstance Win32_Service|ForEach-Object{[pscustomobject]@{name=$_.Name;display_name=$_.DisplayName;status=$_.State;start_type=$_.StartMode;account=$_.StartName}})|ConvertTo-Json -Compress`

	winProcessesPS = `@(Get-Process|Sort-Object WS -Descending|Select-Object -First 50|ForEach-Object{[pscustomobject]@{name=$_.ProcessName;pid=$_.Id;mem_bytes=[int64]$_.WS}})|ConvertTo-Json -Compress`

	winSoftwarePS = `@(Get-ItemProperty 'HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*','HKLM:\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*'|Where-Object{$_.DisplayName}|ForEach-Object{[pscustomobject]@{name=$_.DisplayName;version=[string]$_.DisplayVersion;publisher=[string]$_.Publisher;install_date=[string]$_.InstallDate}})|ConvertTo-Json -Compress`

	winEventsPS = `$s=(Get-Date).AddHours(-24);$h=@{LogName='System','Application';StartTime=$s};[pscustomobject]@{critical_24h=@(Get-WinEvent -FilterHashtable ($h+@{Level=1}) -EA SilentlyContinue).Count;error_24h=@(Get-WinEvent -FilterHashtable ($h+@{Level=2}) -EA SilentlyContinue).Count;warning_24h=@(Get-WinEvent -FilterHashtable ($h+@{Level=3}) -EA SilentlyContinue).Count}|ConvertTo-Json -Compress`
)

// CollectWindows runs the per-section snippets through the runner and assembles
// a Report. The summary section is required (its failure — usually auth — aborts
// with the error); every other section is best-effort (a failure leaves that
// section empty = "Not collected").
func CollectWindows(ctx context.Context, r Runner) (Report, error) {
	rep := Report{Method: "winrm"}

	out, err := r.Run(ctx, winSummaryPS)
	if err != nil {
		return Report{}, fmt.Errorf("winrm collect: %w", err)
	}
	var sum winSummary
	if e := json.Unmarshal(bytes.TrimSpace([]byte(out)), &sum); e != nil {
		return Report{}, fmt.Errorf("parse summary: %w", e)
	}
	rep.Identity = Identity{Hostname: sum.Hostname, FQDN: sum.FQDN, Domain: sum.Domain, Workgroup: sum.Workgroup, LoggedOnUser: sum.LoggedOnUser}
	rep.OS = OSInfo{Caption: sum.Caption, Version: sum.Version, Build: sum.Build, Arch: sum.Arch, InstallDate: sum.InstallDate, LastBoot: sum.LastBoot, UptimeSeconds: sum.Uptime, Timezone: sum.Timezone}
	rep.Hardware = Hardware{Manufacturer: sum.Manufacturer, Model: sum.Model, Serial: sum.Serial, BIOSVersion: sum.BIOSVersion, BIOSDate: sum.BIOSDate, CPUModel: sum.CPUModel, CPUSockets: sum.CPUSockets, CPUCores: sum.CPUCores, RAMTotalBytes: sum.RAMTotal}

	if out, err := r.Run(ctx, winDisksPS); err == nil {
		rep.Disks, _ = jsonArray[Disk]([]byte(out))
	}
	if out, err := r.Run(ctx, winNicsPS); err == nil {
		rep.Nics, _ = jsonArray[Nic]([]byte(out))
	}
	if out, err := r.Run(ctx, winServicesPS); err == nil {
		rep.Services, _ = jsonArray[Service]([]byte(out))
	}
	if out, err := r.Run(ctx, winProcessesPS); err == nil {
		rep.Processes, _ = jsonArray[Process]([]byte(out))
	}
	if out, err := r.Run(ctx, winSoftwarePS); err == nil {
		rep.Software, _ = jsonArray[Software]([]byte(out))
	}
	if out, err := r.Run(ctx, winEventsPS); err == nil {
		var ev EventSummary
		if json.Unmarshal(bytes.TrimSpace([]byte(out)), &ev) == nil {
			rep.Events = &ev
		}
	}
	return rep, nil
}

// jsonArray parses JSON that PowerShell's ConvertTo-Json may emit as either a
// single object (one row) or an array (many rows), returning a slice either way.
// Empty/whitespace input → nil slice.
func jsonArray[T any](raw []byte) ([]T, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if raw[0] == '[' {
		var s []T
		err := json.Unmarshal(raw, &s)
		return s, err
	}
	var one T
	if err := json.Unmarshal(raw, &one); err != nil {
		return nil, err
	}
	return []T{one}, nil
}

// winServiceRoles maps a lowercased Windows service name to a detected role.
var winServiceRoles = map[string]string{
	"ntds":           "Domain Controller",
	"dns":            "DNS Server",
	"dhcpserver":     "DHCP Server",
	"w3svc":          "IIS / Web Server",
	"mssqlserver":    "SQL Server",
	"sqlserveragent": "SQL Server",
	"vmms":           "Hyper-V",
	"termservice":    "Remote Desktop Services",
	"msexchangeis":   "Exchange Server",
	"spooler":        "", // every Windows host has the spooler — not a role signal
}

// DetectWindowsRoles returns the distinct roles implied by the host's services
// (and SQL named instances like MSSQL$INSTANCE). Pure → unit-tested.
func DetectWindowsRoles(rep Report) []string {
	seen := map[string]bool{}
	var roles []string
	add := func(role string) {
		if role == "" || seen[role] {
			return
		}
		seen[role] = true
		roles = append(roles, role)
	}
	for _, s := range rep.Services {
		n := strings.ToLower(s.Name)
		if role, ok := winServiceRoles[n]; ok {
			add(role)
		}
		if strings.HasPrefix(n, "mssql$") {
			add("SQL Server")
		}
	}
	return roles
}
