<#
.SYNOPSIS
  HIMS WMI/DCOM Collector — legacy Windows fallback helper (WinRM-independent).

.DESCRIPTION
  Runs on a trusted Windows / domain box and exposes an HTTP endpoint HIMS calls
  to collect deep inventory from LEGACY Windows hosts where WinRM is DISABLED or
  the Go WinRM library is incompatible (Windows 7 / Server 2008 R2). Unlike the
  Native Collector (which uses Invoke-Command / WinRM), this helper uses
  Get-WmiObject -ComputerName over DCOM/RPC (TCP 135 + dynamic range), so it works
  on hosts with WinRM turned off entirely.

  Contract (identical to the Native Collector; HIMS sends mode="wmi"):
    POST /
    Authorization: Bearer <token>      (must match $env:HIMS_WMI_COLLECTOR_TOKEN)
    Body: { "host":"...", "username":"DOMAIN\\user", "password":"...", "mode":"wmi" }
    200  -> osinv.Report JSON   |   4xx/5xx -> { "error":"<sanitized>" }

  READ-ONLY WMI queries only. The password builds a PSCredential for the remote
  WMI calls and is never logged.

.EXAMPLE
  $env:HIMS_WMI_COLLECTOR_TOKEN = 'a-long-shared-secret'
  .\wmi-collector.ps1 -Prefix 'http://+:8093/'
  # On the HIMS host:  HIMS_WMI_COLLECTOR_URL=http://<host>:8093/  HIMS_WMI_COLLECTOR_TOKEN=...
#>
param([string]$Prefix = 'http://+:8093/')

$ErrorActionPreference = 'Stop'
$Token = $env:HIMS_WMI_COLLECTOR_TOKEN

function Get-WmiInventory($target, $cred) {
  $g = { param($cls) Get-WmiObject -ComputerName $target -Credential $cred -Class $cls -ErrorAction Stop }
  $os   = & $g Win32_OperatingSystem
  $cs   = & $g Win32_ComputerSystem
  $bios = & $g Win32_BIOS
  $cpu  = @(& $g Win32_Processor)
  $cores = ($cpu | Measure-Object NumberOfCores -Sum).Sum
  if (-not $cores) { $cores = ($cpu | Measure-Object NumberOfLogicalProcessors -Sum).Sum }
  $disks = @(& $g Win32_LogicalDisk | Where-Object { $_.DriveType -eq 3 } | ForEach-Object {
    @{ name=$_.DeviceID; filesystem=$_.FileSystem; total_bytes=[int64]$_.Size; free_bytes=[int64]$_.FreeSpace; size_bytes=[int64]$_.Size } })
  $nics = @(& $g Win32_NetworkAdapterConfiguration | Where-Object { $_.IPEnabled } | ForEach-Object {
    @{ name=$_.Description; mac=$_.MACAddress; ip_addresses=(@($_.IPAddress) -join ','); gateway=(@($_.DefaultIPGateway) -join ',');
       dns_servers=(@($_.DNSServerSearchOrder) -join ','); dhcp_enabled=[bool]$_.DHCPEnabled } })
  $services = @(& $g Win32_Service | ForEach-Object {
    @{ name=$_.Name; display_name=$_.DisplayName; status=$_.State; start_type=$_.StartMode; account=$_.StartName } })
  @{
    method='wmi'
    identity=@{ hostname=$os.CSName; fqdn=("{0}.{1}" -f $cs.Name,$cs.Domain).TrimEnd('.'); domain=$cs.Domain; workgroup=$cs.Workgroup; logged_on_user=$cs.UserName }
    os=@{ caption=$os.Caption; version=$os.Version; build="$($os.BuildNumber)"; arch=$os.OSArchitecture; install_date="$($os.InstallDate)"; last_boot="$($os.LastBootUpTime)" }
    hardware=@{ manufacturer=$cs.Manufacturer; model=$cs.Model; serial=$bios.SerialNumber; bios_version=(@($bios.SMBIOSBIOSVersion) -join ' '); cpu_model=$cpu[0].Name; cpu_sockets=$cpu.Count; cpu_cores=[int]$cores; ram_total_bytes=[int64]$cs.TotalPhysicalMemory }
    disks=$disks; nics=$nics; services=$services; software=@(); roles=@(); events=$null
  }
}

function Send-Json($ctx,$code,$obj){ $b=[Text.Encoding]::UTF8.GetBytes(($obj|ConvertTo-Json -Depth 8 -Compress)); $ctx.Response.StatusCode=$code; $ctx.Response.ContentType='application/json'; $ctx.Response.OutputStream.Write($b,0,$b.Length); $ctx.Response.Close() }

$listener=[System.Net.HttpListener]::new(); $listener.Prefixes.Add($Prefix); $listener.Start()
Write-Host "HIMS WMI/DCOM Collector listening on $Prefix"
while ($listener.IsListening) {
  $ctx=$listener.GetContext()
  try {
    if ($ctx.Request.HttpMethod -ne 'POST') { Send-Json $ctx 405 @{ error='POST only' }; continue }
    if ($Token -and $ctx.Request.Headers['Authorization'] -ne "Bearer $Token") { Send-Json $ctx 401 @{ error='unauthorized' }; continue }
    $req=((New-Object IO.StreamReader($ctx.Request.InputStream)).ReadToEnd() | ConvertFrom-Json)
    if (-not $req.host -or -not $req.username) { Send-Json $ctx 400 @{ error='host and username required' }; continue }
    $target=[string]$req.host
    $cred=New-Object System.Management.Automation.PSCredential($req.username,(ConvertTo-SecureString $req.password -AsPlainText -Force)); $req=$null
    try { Send-Json $ctx 200 (Get-WmiInventory $target $cred) }
    catch { Send-Json $ctx 502 @{ error="$($_.Exception.Message)" } }   # sanitized; never echoes the password
  } catch { try { Send-Json $ctx 500 @{ error="$($_.Exception.Message)" } } catch {} }
}
