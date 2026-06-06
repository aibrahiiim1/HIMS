<#
.SYNOPSIS
  HIMS Windows Native Collector — legacy Windows fallback helper.

.DESCRIPTION
  Runs on a Windows / domain-joined machine and exposes a tiny HTTP endpoint that
  HIMS calls to collect deep OS inventory from LEGACY Windows hosts (Windows 7 /
  Server 2008 R2, WSMan Stack 2.0) that the pure-Go WinRM library cannot drive
  (it authenticates but the WSMan operation faults with w:InvalidSelectors).

  This helper uses the NATIVE Windows WSMan/PowerShell stack via Invoke-Command —
  the same path that already works manually — so legacy hosts collect correctly.

  Contract (what HIMS sends / expects):
    POST /                              (this listener)
    Authorization: Bearer <token>       (must match $env:HIMS_NATIVE_COLLECTOR_TOKEN)
    Body: { "host": "...", "username": "DOMAIN\\user", "password": "..." }
    200  -> JSON inventory in the HIMS osinv.Report shape
    4xx/5xx -> { "error": "<sanitized message>" }   (never echoes the password)

  Only READ-ONLY inventory commands are executed on the target. The password is
  used to build a PSCredential for Invoke-Command and is never logged.

.PARAMETER Prefix
  HTTP listener prefix. Default http://+:8092/  (run elevated, or netsh urlacl).

.EXAMPLE
  $env:HIMS_NATIVE_COLLECTOR_TOKEN = 'a-long-shared-secret'
  .\windows-native-collector.ps1 -Prefix 'http://+:8092/'

  Then on the HIMS host:
    HIMS_WINDOWS_NATIVE_COLLECTOR_URL=http://<this-host>:8092/
    HIMS_WINDOWS_NATIVE_COLLECTOR_TOKEN=a-long-shared-secret

.NOTES
  Deploy on a trusted management box. Prefer HTTPS (bind a cert via netsh) and a
  strong token. The target credential travels HIMS -> this helper -> target.
#>
param([string]$Prefix = 'http://+:8092/')

$ErrorActionPreference = 'Stop'
$Token = $env:HIMS_NATIVE_COLLECTOR_TOKEN

# Read-only inventory gathered ON the target via Invoke-Command. Returns a
# hashtable matching the HIMS osinv.Report JSON shape.
$InventoryBlock = {
  $os   = Get-WmiObject Win32_OperatingSystem
  $cs   = Get-WmiObject Win32_ComputerSystem
  $bios = Get-WmiObject Win32_BIOS
  $cpu  = @(Get-WmiObject Win32_Processor)
  $ramBytes = [int64]$cs.TotalPhysicalMemory
  $cores = ($cpu | Measure-Object NumberOfCores -Sum).Sum
  if (-not $cores) { $cores = ($cpu | Measure-Object -Property NumberOfLogicalProcessors -Sum).Sum }

  $disks = @(Get-WmiObject Win32_LogicalDisk -Filter "DriveType=3" | ForEach-Object {
    @{ name = $_.DeviceID; filesystem = $_.FileSystem;
       total_bytes = [int64]$_.Size; free_bytes = [int64]$_.FreeSpace; size_bytes = [int64]$_.Size }
  })

  $nics = @(Get-WmiObject Win32_NetworkAdapterConfiguration -Filter "IPEnabled=true" | ForEach-Object {
    @{ name = $_.Description; mac = $_.MACAddress;
       ip_addresses = (@($_.IPAddress) -join ','); gateway = (@($_.DefaultIPGateway) -join ',');
       dns_servers = (@($_.DNSServerSearchOrder) -join ','); dhcp_enabled = [bool]$_.DHCPEnabled }
  })

  $services = @(Get-WmiObject Win32_Service | ForEach-Object {
    @{ name = $_.Name; display_name = $_.DisplayName; status = $_.State;
       start_type = $_.StartMode; account = $_.StartName }
  })

  # Software from the uninstall registry (fast + safe; avoids slow Win32_Product).
  $paths = @(
    'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*',
    'HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*'
  )
  $software = @(Get-ItemProperty $paths -ErrorAction SilentlyContinue |
    Where-Object { $_.DisplayName } | ForEach-Object {
      @{ name = $_.DisplayName; version = "$($_.DisplayVersion)"; publisher = "$($_.Publisher)";
         install_date = "$($_.InstallDate)" }
    })

  # Roles (server features) — best-effort; empty on client SKUs.
  $roles = @()
  try {
    if (Get-Command Get-WindowsFeature -ErrorAction SilentlyContinue) {
      $roles = @(Get-WindowsFeature | Where-Object { $_.Installed } | ForEach-Object { $_.Name })
    }
  } catch {}

  # Event summary (last 24h) — best-effort.
  $events = $null
  try {
    $since = (Get-Date).AddDays(-1)
    $sys = @(Get-EventLog -LogName System -EntryType Error,Warning -After $since -ErrorAction SilentlyContinue)
    $events = @{ error_24h = @($sys | Where-Object { $_.EntryType -eq 'Error' }).Count;
                 warning_24h = @($sys | Where-Object { $_.EntryType -eq 'Warning' }).Count;
                 critical_24h = 0 }
  } catch {}

  @{
    method = 'winrm-native'
    identity = @{ hostname = $os.CSName; fqdn = ("{0}.{1}" -f $cs.Name, $cs.Domain).TrimEnd('.');
                  domain = $cs.Domain; workgroup = $cs.Workgroup; logged_on_user = $cs.UserName }
    os = @{ caption = $os.Caption; version = $os.Version; build = "$($os.BuildNumber)";
            arch = $os.OSArchitecture; install_date = "$($os.InstallDate)";
            last_boot = "$($os.LastBootUpTime)" }
    hardware = @{ manufacturer = $cs.Manufacturer; model = $cs.Model; serial = $bios.SerialNumber;
                  bios_version = (@($bios.SMBIOSBIOSVersion) -join ' '); cpu_model = $cpu[0].Name;
                  cpu_sockets = $cpu.Count; cpu_cores = [int]$cores; ram_total_bytes = $ramBytes }
    disks = $disks; nics = $nics; services = $services; software = $software;
    roles = $roles; events = $events
  }
}

function Send-Json($ctx, $code, $obj) {
  $json = $obj | ConvertTo-Json -Depth 8 -Compress
  $buf  = [Text.Encoding]::UTF8.GetBytes($json)
  $ctx.Response.StatusCode = $code
  $ctx.Response.ContentType = 'application/json'
  $ctx.Response.OutputStream.Write($buf, 0, $buf.Length)
  $ctx.Response.Close()
}

$listener = [System.Net.HttpListener]::new()
$listener.Prefixes.Add($Prefix)
$listener.Start()
Write-Host "HIMS Windows Native Collector listening on $Prefix (token $([bool]$Token ? 'required' : 'NOT SET — set HIMS_NATIVE_COLLECTOR_TOKEN!'))"

while ($listener.IsListening) {
  $ctx = $listener.GetContext()
  try {
    if ($ctx.Request.HttpMethod -ne 'POST') { Send-Json $ctx 405 @{ error = 'POST only' }; continue }
    if ($Token) {
      $auth = $ctx.Request.Headers['Authorization']
      if ($auth -ne "Bearer $Token") { Send-Json $ctx 401 @{ error = 'unauthorized' }; continue }
    }
    $body = (New-Object IO.StreamReader($ctx.Request.InputStream)).ReadToEnd()
    $req  = $body | ConvertFrom-Json
    if (-not $req.host -or -not $req.username) { Send-Json $ctx 400 @{ error = 'host and username required' }; continue }

    $targetHost = [string]$req.host
    $sec  = ConvertTo-SecureString $req.password -AsPlainText -Force
    $cred = New-Object System.Management.Automation.PSCredential($req.username, $sec)
    $req  = $null  # drop the plaintext request as soon as the credential is built

    try {
      $inv = Invoke-Command -ComputerName $targetHost -Credential $cred -ScriptBlock $InventoryBlock -ErrorAction Stop
    } catch {
      # Sanitize: never echo the password (it is not in the error, but be safe).
      $msg = "$($_.Exception.Message)"
      Send-Json $ctx 502 @{ error = $msg }
      continue
    }
    Send-Json $ctx 200 $inv
  } catch {
    try { Send-Json $ctx 500 @{ error = "$($_.Exception.Message)" } } catch {}
  }
}
