package osinv

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// windowsScript gathers deep inventory via Get-CimInstance (WMI over WinRM — no
// DCOM) plus registry + Get-WinEvent, and emits ONE compact JSON object whose
// keys match the Report json tags. Collections are wrapped in @() so a single
// row still serialises as an array. Dates are ISO-8601 strings. No backticks
// (this is embedded in a Go raw string).
const windowsScript = `
$ErrorActionPreference='SilentlyContinue'
$os=Get-CimInstance Win32_OperatingSystem
$cs=Get-CimInstance Win32_ComputerSystem
$bios=Get-CimInstance Win32_BIOS
$cpu=@(Get-CimInstance Win32_Processor)
$tz=(Get-CimInstance Win32_TimeZone).Caption
$fqdn=$cs.DNSHostName
if($cs.PartOfDomain){$fqdn="$($cs.DNSHostName).$($cs.Domain)"}
$uptime=0
if($os.LastBootUpTime){$uptime=[int64]((Get-Date)-$os.LastBootUpTime).TotalSeconds}
$disks=@(Get-CimInstance Win32_LogicalDisk -Filter "DriveType=3" | ForEach-Object {
  [ordered]@{name=$_.DeviceID;filesystem=$_.FileSystem;total_bytes=[int64]$_.Size;free_bytes=[int64]$_.FreeSpace;size_bytes=[int64]$_.Size}})
$nics=@(Get-CimInstance Win32_NetworkAdapterConfiguration -Filter "IPEnabled=True" | ForEach-Object {
  [ordered]@{name=$_.Description;mac=$_.MACAddress;ip_addresses=($_.IPAddress -join ',');gateway=(@($_.DefaultIPGateway)[0]);dns_servers=($_.DNSServerSearchOrder -join ',');dhcp_enabled=[bool]$_.DHCPEnabled}})
$services=@(Get-CimInstance Win32_Service | ForEach-Object {
  [ordered]@{name=$_.Name;display_name=$_.DisplayName;status=$_.State;start_type=$_.StartMode;account=$_.StartName}})
$processes=@(Get-Process | Sort-Object WS -Descending | Select-Object -First 50 | ForEach-Object {
  $st='';if($_.StartTime){$st=$_.StartTime.ToString('o')};[ordered]@{name=$_.ProcessName;pid=$_.Id;mem_bytes=[int64]$_.WS;start_time=$st}})
$uk='HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*','HKLM:\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*'
$software=@(Get-ItemProperty $uk | Where-Object {$_.DisplayName} | ForEach-Object {
  [ordered]@{name=$_.DisplayName;version=[string]$_.DisplayVersion;publisher=[string]$_.Publisher;install_date=[string]$_.InstallDate}})
$since=(Get-Date).AddHours(-24)
$crit=@(Get-WinEvent -FilterHashtable @{LogName='System','Application';Level=1;StartTime=$since}).Count
$err=@(Get-WinEvent -FilterHashtable @{LogName='System','Application';Level=2;StartTime=$since}).Count
$warn=@(Get-WinEvent -FilterHashtable @{LogName='System','Application';Level=3;StartTime=$since}).Count
$lastcrit=''
$lc=Get-WinEvent -FilterHashtable @{LogName='System','Application';Level=1;StartTime=$since} -MaxEvents 1
if($lc){$lastcrit="$($lc.ProviderName): $($lc.Id)"}
$out=[ordered]@{
 method='winrm';
 identity=[ordered]@{hostname=$cs.Name;fqdn=$fqdn;domain=$cs.Domain;workgroup=$cs.Workgroup;logged_on_user=$cs.UserName};
 os=[ordered]@{caption=$os.Caption;version=$os.Version;build=$os.BuildNumber;edition=$os.OperatingSystemSKU.ToString();arch=$os.OSArchitecture;install_date=($os.InstallDate.ToString('o'));last_boot=($os.LastBootUpTime.ToString('o'));uptime_seconds=$uptime;timezone=$tz};
 hardware=[ordered]@{manufacturer=$cs.Manufacturer;model=$cs.Model;serial=$bios.SerialNumber;bios_version=(@($bios.BIOSVersion)-join ' ');bios_date=($bios.ReleaseDate.ToString('o'));cpu_model=$cpu[0].Name;cpu_sockets=$cpu.Count;cpu_cores=(($cpu|Measure-Object NumberOfCores -Sum).Sum);ram_total_bytes=[int64]$cs.TotalPhysicalMemory};
 disks=$disks;nics=$nics;services=$services;processes=$processes;software=$software;
 events=[ordered]@{critical_24h=[int]$crit;error_24h=[int]$err;warning_24h=[int]$warn;last_critical=$lastcrit}
}
$out|ConvertTo-Json -Depth 5 -Compress
`

// CollectWindows runs the Windows inventory script through the runner and parses
// the JSON result. The runner is the WinRM PowerShell runner in production.
func CollectWindows(ctx context.Context, r Runner) (Report, error) {
	out, err := r.Run(ctx, windowsScript)
	if err != nil {
		return Report{}, fmt.Errorf("winrm collect: %w", err)
	}
	return ParseWindows([]byte(out))
}

// ParseWindows parses the windowsScript JSON output into a Report (pure).
func ParseWindows(b []byte) (Report, error) {
	b = []byte(strings.TrimSpace(string(b)))
	if len(b) == 0 {
		return Report{}, fmt.Errorf("empty inventory output")
	}
	var rep Report
	if err := json.Unmarshal(b, &rep); err != nil {
		return Report{}, fmt.Errorf("parse windows inventory: %w", err)
	}
	rep.Method = "winrm"
	return rep, nil
}

// roleSignals maps a lowercased Windows service name to a detected role. Roles
// are inferred from running/installed SERVICES (real evidence), never from open
// ports alone.
var winServiceRoles = map[string]string{
	"ntds":         "Domain Controller",
	"dns":          "DNS Server",
	"dhcpserver":   "DHCP Server",
	"w3svc":        "IIS / Web Server",
	"mssqlserver":  "SQL Server",
	"sqlserveragent": "SQL Server",
	"vmms":         "Hyper-V",
	"termservice":  "Remote Desktop Services",
	"spooler":      "", // every Windows host has the spooler — not a role signal
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
		// A file server is signalled by the Server service PLUS shares; we treat
		// LanmanServer running as a weak signal only when shares exist — not
		// inferrable from the service alone, so left out (honest).
	}
	return roles
}
