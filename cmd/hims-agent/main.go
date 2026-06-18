// Command hims-agent is the HIMS Relay Agent / Site Collector. It runs on a
// trusted machine inside a site (Windows/domain box, Windows Server, or Linux)
// and collects from devices the main HIMS API cannot reach directly. It PULLS
// jobs from HIMS (NAT-friendly — no inbound path to the agent), runs the
// collection locally, and posts structured results back. It authenticates with a
// per-agent bearer token and never logs secrets.
//
// Config (env):
//
//	HIMS_URL                 base URL of the HIMS API (e.g. https://hims.example:8090)
//	HIMS_AGENT_TOKEN         the per-agent token shown once in the HIMS Agents page
//	HIMS_AGENT_NAME          optional display name (defaults to hostname)
//	HIMS_AGENT_INSECURE_TLS  "1" to accept self-signed HIMS TLS (lab only)
//	HIMS_AGENT_POLL_SECONDS  job poll interval (default 8)
//
// Capabilities implemented in this build: winrm (modern Windows, pure-Go NTLM),
// wmi (legacy Windows via PowerShell Get-WmiObject over DCOM — Windows host only).
// ssh/snmp/onvif/vsphere are advertised as future and return an honest gate.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coralsearesorts/hims/internal/osinv"
)

const agentVersion = "1.2.6"

// agentMaxConcurrent bounds how many collection jobs the agent runs in parallel
// per poll. The HIMS server already caps how many jobs it dispatches to one agent
// (agentDispatchCap), so the batch here is small; running it concurrently instead
// of serially is what keeps a from-zero subnet scan draining in minutes rather
// than one-host-at-a-time. Windows WMI/WinRM is safe to parallelize (unlike
// lockout-prone appliances, which the server does not bulk-dispatch). Configurable
// via HIMS_AGENT_MAX_CONCURRENT (default 8, clamped 1..32) so a weaker agent host
// can be tuned down per environment.
var agentMaxConcurrent = func() int {
	if v := os.Getenv("HIMS_AGENT_MAX_CONCURRENT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 32 {
			return n
		}
	}
	return 8
}()

// serviceName is the Windows Service name the installer registers under and the
// agent answers to when launched by the Service Control Manager.
const serviceName = "HIMSRelayAgent"

type agent struct {
	base   string
	token  string
	name   string
	caps   []string
	client *http.Client
}

// out is where the agent writes its log lines. Console mode → stdout; Windows
// service mode → a log file under ProgramData (set in service_windows.go).
var out io.Writer = os.Stdout

func logf(format string, args ...any) { fmt.Fprintf(out, ts()+format+"\n", args...) }
func logln(args ...any)               { fmt.Fprint(out, ts()+fmt.Sprintln(args...)) }
func ts() string                      { return time.Now().Format("2006-01-02 15:04:05 ") }

func main() {
	showVersion := flag.Bool("version", false, "print the agent version and exit")
	runConsole := flag.Bool("console", false, "force interactive console mode (do not run as a Windows service)")
	flag.Parse()
	if *showVersion {
		fmt.Printf("HIMS Relay Agent %s (%s)\n", agentVersion, osLabel())
		return
	}
	// On Windows, when launched by the Service Control Manager, run as a service;
	// otherwise (and everywhere else) run in the foreground/console.
	if !*runConsole && runUnderServiceManager() {
		runAsService()
		return
	}
	if err := newAgentFromEnv().run(context.Background()); err != nil {
		logln("agent exited:", err)
		os.Exit(1)
	}
}

// newAgentFromEnv builds the agent from its environment (HIMS_URL,
// HIMS_AGENT_TOKEN, …). It exits early with a clear message if the token is
// missing — the one piece of config the operator must supply.
func newAgentFromEnv() *agent {
	a := &agent{
		base:  strings.TrimRight(getenv("HIMS_URL", "http://localhost:8090"), "/"),
		token: os.Getenv("HIMS_AGENT_TOKEN"),
		name:  getenv("HIMS_AGENT_NAME", hostname()),
		caps:  []string{"winrm", "wmi"},
	}
	if a.token == "" {
		fmt.Fprintln(os.Stderr, "HIMS_AGENT_TOKEN is required (register an agent in HIMS → Relay Agents and use the downloaded installer)")
		os.Exit(2)
	}
	tr := &http.Transport{}
	if os.Getenv("HIMS_AGENT_INSECURE_TLS") == "1" {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
	}
	a.client = &http.Client{Timeout: 3 * time.Minute, Transport: tr}
	return a
}

// run registers the agent and then polls for jobs + heartbeats until ctx is
// cancelled (service stop / Ctrl-C). It is the single run loop shared by console
// and Windows-service modes.
func (a *agent) run(ctx context.Context) error {
	registered := a.register() == nil
	if registered {
		logf("HIMS Relay Agent %s registered as %q (caps=%v) → %s", agentVersion, a.name, a.caps, a.base)
	} else {
		// Don't exit hard (esp. in service mode) — a transient HIMS outage at boot
		// shouldn't leave the service dead. Polling still authenticates by token;
		// re-register opportunistically until identity/caps land.
		logln("register failed (will keep retrying); polling will still work once HIMS is reachable")
	}
	poll := time.Duration(getenvInt("HIMS_AGENT_POLL_SECONDS", 8)) * time.Second
	hb := time.NewTicker(30 * time.Second)
	defer hb.Stop()
	for {
		a.pollOnce()
		if !registered && a.register() == nil {
			registered = true
			logf("HIMS Relay Agent %s registered as %q (caps=%v) → %s", agentVersion, a.name, a.caps, a.base)
		}
		select {
		case <-ctx.Done():
			logln("shutting down")
			return nil
		case <-hb.C:
			a.heartbeat("")
		case <-time.After(poll):
		}
	}
}

// --- HIMS protocol -----------------------------------------------------------

func (a *agent) do(method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, a.base+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (a *agent) register() error {
	return a.do(http.MethodPost, "/api/v1/agent/register", map[string]any{
		"hostname": hostname(), "ip": localIP(), "os": osLabel(), "version": agentVersion, "capabilities": a.caps,
	}, nil)
}

func (a *agent) heartbeat(lastErr string) {
	_ = a.do(http.MethodPost, "/api/v1/agent/heartbeat", map[string]any{"version": agentVersion, "last_error": lastErr}, nil)
}

type job struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Protocol string `json:"protocol"`
	Target   string `json:"target"`
	Username string `json:"username"`
	Password string `json:"password"`
	// Credentials is the ordered candidate list to try (multi-credential collection,
	// mirroring the server's direct WinRM path). When present the agent tries each in
	// order and STOPS at the first success; when empty it falls back to the single
	// Username/Password (legacy). Secrets are never logged.
	Credentials []agentCred `json:"credentials,omitempty"`
}

type agentCred struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (a *agent) pollOnce() {
	var jobs []job
	if err := a.do(http.MethodGet, "/api/v1/agent/jobs", nil, &jobs); err != nil {
		logln("poll error:", err)
		return
	}
	// Run the received batch concurrently (bounded). Serial processing made a
	// from-zero subnet scan drain one host at a time over ~40 min; parallelizing the
	// small server-throttled batch settles it in minutes.
	sem := make(chan struct{}, agentMaxConcurrent)
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(j job) {
			defer wg.Done()
			defer func() { <-sem }()
			a.runJob(j)
		}(j)
	}
	wg.Wait()
}

func (a *agent) runJob(j job) {
	// NEVER log the password. Log only target/protocol/cred-count.
	logf("job %s: kind=%s protocol=%s target=%s creds=%d", j.ID, j.Kind, j.Protocol, j.Target, len(j.Credentials))
	if j.Kind == "test" {
		a.post(j.ID, map[string]any{"success": true, "category": "success"})
		return
	}

	// Build the ordered candidate list. Multi-credential when the server supplied one,
	// else the single legacy credential.
	creds := j.Credentials
	if len(creds) == 0 {
		creds = []agentCred{{Username: j.Username, Password: j.Password}}
	}

	// Try each candidate in order; STOP at the first success. Record every attempt's
	// outcome (success / auth / access-denied / transport / namespace / timeout) so the
	// server can surface full history and bind the winner. Never spray: the server has
	// already filtered to applicable Windows credentials and capped the count, and we
	// never retry the same credential within this cycle.
	attempts := make([]map[string]any, 0, len(creds))
	for _, c := range creds {
		jc := j
		jc.Username, jc.Password = c.Username, c.Password
		rep, cat, err := collect(jc)
		ok := err == nil
		att := map[string]any{"credential_id": c.ID, "success": ok}
		if ok {
			att["category"] = "success"
			attempts = append(attempts, att)
			a.post(j.ID, map[string]any{
				"success": true, "category": "success", "report": rep,
				"credential_id": c.ID, "attempts": attempts,
			})
			return
		}
		att["category"] = cat
		att["detail"] = sanitize(err.Error(), c.Password)
		attempts = append(attempts, att)
		// A transient WinRM transport/negotiation miss is NOT credential-specific (it
		// happens before auth completes). Stop trying the remaining candidates against a
		// momentarily-overloaded/unreachable WinRM listener — that only adds load and, for
		// a connect-timeout, N×120s of waiting. Report the transient so the server retries
		// the whole host with backoff (where the right credential then succeeds).
		if isTransientWinRM(cat) {
			break
		}
	}

	// All candidates failed — report the most-significant failure (last attempt) plus
	// the full per-credential history.
	res := map[string]any{"success": false, "category": "error", "error": "no candidate credential succeeded", "attempts": attempts}
	if n := len(attempts); n > 0 {
		last := attempts[n-1]
		if cat, ok := last["category"].(string); ok {
			res["category"] = cat
		}
		if d, ok := last["detail"].(string); ok {
			res["error"] = d
		}
		res["credential_id"] = last["credential_id"]
	}
	a.post(j.ID, res)
}

// post sends a job result back to HIMS (never logs secrets — res carries none).
func (a *agent) post(jobID string, res map[string]any) {
	if err := a.do(http.MethodPost, "/api/v1/agent/jobs/"+jobID+"/result", res, nil); err != nil {
		logln("post result error:", err)
	}
}

// collect runs one device collection locally and returns an osinv.Report.
func collect(j job) (*osinv.Report, string, error) {
	// 6 min: a healthy host collects fast on the first rung, but a fully-failing host may
	// run all three Windows rungs sequentially (native PSRP → Go WinRM shell → WMI/DCOM),
	// and the installed-software registry walk is many small round-trips. Kept under the
	// server's 8-min stale-dispatch reaper so a slow-but-progressing job is never reaped.
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	switch j.Protocol {
	case "winrm", "wmi":
		// Both labels run the SAME Windows ladder so the agent path is equivalent to the
		// server's direct collector regardless of which protocol the router picked.
		return collectWindows(ctx, j)
	default:
		return nil, "unsupported", fmt.Errorf("protocol %q not implemented in this agent build", j.Protocol)
	}
}

// collectWindows runs the Windows collection ladder, in robustness order:
//
//	Rung 0 — NATIVE PowerShell Remoting (collectWinRMNative): Windows' own WinRM/PSRP
//	         client, inventory run locally in-session. Most robust negotiation (no Go-lib
//	         401 under load) AND bypasses remote UAC filtering (full token in-session).
//	         This is the primary path and what makes from-zero discovery reliable for the
//	         hard hosts (.106/.119) under production scan-storm load.
//	Rung 1 — Go WinRM command shell (collectWinRM): the original pure-Go path; kept as a
//	         fallback (e.g. if PowerShell/PSRP is unavailable on the agent host).
//	Rung 2 — WMI/DCOM (collectWMI): for hosts with WinRM disabled but RPC/DCOM open.
//
// A clean WinRM auth rejection short-circuits the WMI rung (the same credential would be
// rejected over DCOM too — avoids lockout); every other failure falls through, because
// WMI/DCOM is a different transport (RPC/135) and a WinRM problem says nothing about it.
func collectWindows(ctx context.Context, j job) (*osinv.Report, string, error) {
	// Rung 0: native PowerShell Remoting — the robust primary path.
	var nerr error
	if runtime.GOOS == "windows" {
		rep, _, err := collectWinRMNative(ctx, j)
		if err == nil {
			return rep, "success", nil
		}
		nerr = err // fold into the combined error if every rung fails
	}
	// Rung 1: Go WinRM command shell.
	rep, wcat, werr := collectWinRM(ctx, j)
	if werr == nil {
		return rep, "success", nil
	}
	// Short-circuit the WMI/DCOM rung ONLY for a clean WinRM auth rejection (the same
	// credential will be rejected over DCOM too, so a second logon adds nothing but
	// account-lockout pressure) or when the agent isn't on Windows. Everything else —
	// INCLUDING a WinRM transport/negotiation failure (connect-timeout, or a persistent
	// "401 invalid content type" when the listener requires message encryption) — MUST
	// fall through to WMI/DCOM: it is a different transport (RPC/135), so a WinRM
	// transport problem says nothing about DCOM reachability. On this very subnet the
	// single most successful method is WMI/DCOM, and the .106/.119/.161/.194 acceptance
	// failures were exactly hosts whose WinRM was unusable (401/timeout) while RPC/135
	// was open — they can ONLY be collected over WMI, and the old short-circuit on the
	// transient category meant that path was never tried (retry + self-heal just re-ran
	// the same dead WinRM rung forever). pickWindowsFailCat keeps the retryable WinRM
	// transient as the headline when WMI ALSO can't reach the host, so a genuine
	// double-transport miss still retries rather than settling a terminal verdict.
	if winRMShortCircuitsWMI(wcat) || runtime.GOOS != "windows" {
		if nerr != nil {
			return nil, wcat, fmt.Errorf("native: %v | winrm: %v", nerr, werr)
		}
		return nil, wcat, werr
	}
	rep, mcat, merr := collectWMI(ctx, j)
	if merr == nil {
		return rep, "success", nil
	}
	// Every rung failed: surface the WMI category (the rung that may have reached the host
	// over DCOM) as the headline — falling back to the WinRM category if WMI produced none
	// — so the server classifies credential_failed vs collection_failed honestly. The
	// combined error keeps each rung's precise reason for diagnostics (native PSRP first).
	if nerr != nil {
		return nil, pickWindowsFailCat(wcat, mcat), fmt.Errorf("native: %v | winrm: %v | wmi: %v", nerr, werr, merr)
	}
	return nil, pickWindowsFailCat(wcat, mcat), fmt.Errorf("winrm: %v | wmi: %v", werr, merr)
}

// pickWindowsFailCat chooses the headline category when BOTH Windows rungs fail. A
// retryable WinRM transport/negotiation transient stays the headline (so the server
// retries the host with backoff and self-heal stays eligible — never masked into a
// terminal verdict by a UAC-blocked wmi_access_denied; this is the .49/.50 storm guard).
// Otherwise the WMI rung's category is the more informative signal (e.g. wmi_access_denied
// means the host WAS reached over DCOM and the credential was rejected), so it wins when
// present.
//
// IMPORTANT (from-zero #6): no single WinRM 401/"invalid content type" pattern is
// terminal by itself. .106/.119 looked like a permanent "host refuses all transports"
// wall (WinRM-401 + WMI-access-denied) yet collected via winrm-agent on a later run with
// NO host-side change — the failures were load/timing-sensitive negotiation instability
// during the subnet storm. So that combo stays a RETRYABLE transient here; governed
// retry + WMI fallback + self-heal decide the outcome, and host-policy-blocked is an
// operator-facing diagnosis ONLY after the automatic budget is exhausted (never a
// premature terminal short-circuit, which the self-heal round budget already bounds).
func pickWindowsFailCat(winrmCat, wmiCat string) string {
	if isTransientWinRM(winrmCat) {
		return winrmCat
	}
	if wmiCat != "" {
		return wmiCat
	}
	return winrmCat
}

// isTransientWinRM reports whether a WinRM failure category is a transient TRANSPORT /
// negotiation miss (the host is reachable; WinRM did not answer or the NTLM handshake
// glitched, typically under scan-storm load) rather than a definitive auth/closed-port
// verdict. Such failures are not credential-specific, so the per-credential loop stops
// at the first one instead of hammering the host with every remaining candidate over a
// momentarily-overloaded WinRM listener — and the server retries the whole host later.
func isTransientWinRM(cat string) bool {
	return cat == osinv.WinRMConnectTimeout || cat == osinv.WinRMNegotiateError
}

// winRMShortCircuitsWMI reports whether a FAILED WinRM rung should skip the WMI/DCOM
// fallback. ONLY a clean auth rejection short-circuits: the same credential will be
// rejected over DCOM too, and a second logon adds nothing but account-lockout pressure.
// Every other WinRM failure — connect-timeout, the persistent "401 invalid content
// type" negotiation error, refused/closed — MUST fall through to WMI/DCOM, which is a
// different transport (RPC/135) and is frequently the ONLY path that reaches such a
// host. The 172.21.60.0/24 acceptance failures (.106/.119: WinRM 401; .161/.194: WinRM
// 5985 closed) all had RPC/135 open and were collectable only over WMI — the old
// short-circuit on the transient category meant that path was never tried.
func winRMShortCircuitsWMI(cat string) bool {
	return cat == "auth_failed"
}

// collectWinRMNative gathers inventory using WINDOWS' OWN PowerShell Remoting client
// (New-PSSession + Invoke-Command over WinRM/5985, Negotiate auth), NOT the third-party
// Go winrm library. This is the most robust Windows rung and the primary path:
//
//   - Microsoft's WinRM/PSRP client does NTLM/Kerberos (SPNEGO) negotiation + WSMan
//     message encryption CORRECTLY. The Go masterzen/winrm library, by contrast, emits
//     "http response error: 401 - invalid content type" on some hosts under scan-storm
//     load (it receives an HTML error page mid-negotiation and cannot parse it). Hosts
//     .106/.119 hit exactly that and could only ever be collected in a lucky low-load
//     window — native PSRP removes that fragility.
//   - The inventory script runs LOCALLY inside the remote runspace via Invoke-Command,
//     so it executes with the caller's FULL token on the target — bypassing the UAC
//     remote-token filter (LocalAccountTokenFilterPolicy) that makes remote WMI/CIM
//     return "Access is denied" for a local-admin account. Same advantage as the WinRM
//     command shell, but over Microsoft's robust client.
//
// Writes collection_method = "winrm-native". Requires the agent to run on Windows.
func collectWinRMNative(ctx context.Context, j job) (*osinv.Report, string, error) {
	if runtime.GOOS != "windows" {
		return nil, "unsupported", fmt.Errorf("native PowerShell remoting requires the agent to run on Windows")
	}
	// The script opens a PSSession with Microsoft's WinRM client and runs the inventory
	// gather LOCALLY in the remote runspace (full-token; bypasses remote UAC filtering).
	script := `$ErrorActionPreference='Stop'
$u=$env:HIMS_J_USER; $p=ConvertTo-SecureString $env:HIMS_J_PASS -AsPlainText -Force
$c=New-Object System.Management.Automation.PSCredential($u,$p); $t=$env:HIMS_J_TARGET
$so=New-PSSessionOption -OpenTimeout 30000 -OperationTimeout 180000 -CancelTimeout 8000 -IdleTimeout 120000
$s=New-PSSession -ComputerName $t -Credential $c -Authentication Negotiate -SessionOption $so -ErrorAction Stop
try {
  $out=Invoke-Command -Session $s -ScriptBlock {
    # Runs LOCALLY on the target with the full admin token (bypasses remote UAC filtering).
    $os=Get-CimInstance Win32_OperatingSystem; $cs=Get-CimInstance Win32_ComputerSystem; $bios=Get-CimInstance Win32_BIOS
    $cpu=@(Get-CimInstance Win32_Processor)
    $cores=($cpu|Measure-Object NumberOfCores -Sum).Sum; if(-not $cores){$cores=($cpu|Measure-Object NumberOfLogicalProcessors -Sum).Sum}
    $disks=@(Get-CimInstance Win32_LogicalDisk|?{$_.DriveType -eq 3}|%{@{name=$_.DeviceID;filesystem=$_.FileSystem;total_bytes=[int64]$_.Size;free_bytes=[int64]$_.FreeSpace;size_bytes=[int64]$_.Size}})
    $nics=@(Get-CimInstance Win32_NetworkAdapterConfiguration|?{$_.IPEnabled}|%{@{name=$_.Description;mac=$_.MACAddress;ip_addresses=(@($_.IPAddress)-join',');gateway=(@($_.DefaultIPGateway)-join',');dns_servers=(@($_.DNSServerSearchOrder)-join',');dhcp_enabled=[bool]$_.DHCPEnabled}})
    $svc=@(Get-CimInstance Win32_Service|%{@{name=$_.Name;display_name=$_.DisplayName;status=$_.State;start_type=$_.StartMode;account=$_.StartName}})
    $procs=@(); try { $procs=@(Get-Process|Sort-Object WS -Descending|Select-Object -First 50|%{@{name=$_.ProcessName;pid=[int]$_.Id;mem_bytes=[int64]$_.WS}}) } catch {}
    $sw=@(); $swnote=''
    try {
      $items=Get-ItemProperty 'HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*','HKLM:\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*' -ErrorAction SilentlyContinue|Where-Object{$_.DisplayName}
      $sw=@($items|%{@{name=[string]$_.DisplayName;version=[string]$_.DisplayVersion;publisher=[string]$_.Publisher;install_date=[string]$_.InstallDate}})
      if($sw.Count -gt 0){$swnote='collected via winrm_native_local'} else {$swnote='no_software_found'}
    } catch { $swnote=('software_failed: '+$_.Exception.Message) }
    @{ method='winrm-native';
       identity=@{hostname=$os.CSName;fqdn=("{0}.{1}" -f $cs.Name,$cs.Domain).TrimEnd('.');domain=$cs.Domain;workgroup=$cs.Workgroup;logged_on_user=$cs.UserName};
       os=@{caption=$os.Caption;version=$os.Version;build="$($os.BuildNumber)";arch=$os.OSArchitecture;install_date="$($os.InstallDate)";last_boot="$($os.LastBootUpTime)"};
       hardware=@{manufacturer=$cs.Manufacturer;model=$cs.Model;serial=$bios.SerialNumber;bios_version=(@($bios.SMBIOSBIOSVersion)-join' ');cpu_model=$cpu[0].Name;cpu_sockets=$cpu.Count;cpu_cores=[int]$cores;ram_total_bytes=[int64]$cs.TotalPhysicalMemory};
       disks=$disks; nics=$nics; services=$svc; software=$sw; processes=$procs; roles=@(); events=$null; software_note=$swnote }
  }
  $out|ConvertTo-Json -Depth 8 -Compress
} finally { if($s){ Remove-PSSession $s -ErrorAction SilentlyContinue } }`
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	base := os.Environ()
	clean := base[:0]
	for _, kv := range base {
		if strings.HasPrefix(strings.ToUpper(kv), "PSMODULEPATH=") {
			continue
		}
		clean = append(clean, kv)
	}
	cmd.Env = append(clean, "HIMS_J_USER="+j.Username, "HIMS_J_PASS="+j.Password, "HIMS_J_TARGET="+j.Target)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, classifyNativeWinRMErr(stderr.String()), fmt.Errorf("%s", strings.TrimSpace(stderr.String()))
	}
	var rep osinv.Report
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &rep); err != nil {
		return nil, "winrm_native_error", fmt.Errorf("could not parse native WinRM inventory JSON")
	}
	rep.Method = "winrm-native"
	return &rep, "success", nil
}

// classifyNativeWinRMErr maps a native PowerShell-Remoting (New-PSSession/Invoke-Command)
// stderr to a credential-test category. "Access is denied" from native PSRP is an
// AUTHORIZATION refusal (the credential authenticated but the host policy/UAC denied the
// session) — operator-fixable host config, NEVER a wrong password — so it is its own
// non-auth category and the ladder falls through to the other rungs. A clean logon
// failure is auth_failed; connect/negotiate problems are retryable transients.
func classifyNativeWinRMErr(stderr string) string {
	e := strings.ToLower(stderr)
	switch {
	case strings.Contains(e, "logon failure") || strings.Contains(e, "user name or password") ||
		strings.Contains(e, "bad username or password"):
		return "auth_failed"
	case strings.Contains(e, "access is denied") || strings.Contains(e, "access denied"):
		return "winrm_native_access_denied"
	case strings.Contains(e, "cannot connect") || strings.Contains(e, "refused") ||
		strings.Contains(e, "no such host") || strings.Contains(e, "unreachable"):
		return "unreachable"
	case strings.Contains(e, "timed out") || strings.Contains(e, "timeout") ||
		strings.Contains(e, "cannot complete the operation") || strings.Contains(e, "operation timed out"):
		return osinv.WinRMConnectTimeout
	default:
		// Unknown WinRM/WSMan negotiation problem — treat as a retryable transient.
		return osinv.WinRMNegotiateError
	}
}

// collectWinRM gathers inventory over a WinRM command shell (Go-winrm) — the same
// collector the server's direct path uses. Its Get-CimInstance and Uninstall-registry
// reads execute locally on the target inside the shell, so they succeed for a
// local-admin account where remote WMI/CIM is UAC-blocked.
func collectWinRM(ctx context.Context, j job) (*osinv.Report, string, error) {
	cl, err := osinv.NewWinRMClient(j.Target, j.Username, j.Password, 120*time.Second)
	if err != nil {
		return nil, "error", err
	}
	rep, err := osinv.CollectWindows(ctx, osinv.WinRMRunner{C: cl})
	if err != nil {
		cat, _, _ := osinv.ClassifyWinRMError(err)
		return nil, cat, err
	}
	rep.Method = "winrm-agent"
	return &rep, "success", nil
}

// collectWMI gathers inventory via PowerShell Get-WmiObject over DCOM (Windows
// host only). The credential is passed to the child PowerShell via env vars, not
// the command line, so it never appears in the process table.
func collectWMI(ctx context.Context, j job) (*osinv.Report, string, error) {
	if runtime.GOOS != "windows" {
		return nil, "unsupported", fmt.Errorf("WMI/DCOM collection requires the agent to run on Windows")
	}
	script := `$ErrorActionPreference='Stop'
$u=$env:HIMS_J_USER; $p=ConvertTo-SecureString $env:HIMS_J_PASS -AsPlainText -Force
$c=New-Object System.Management.Automation.PSCredential($u,$p); $t=$env:HIMS_J_TARGET
# Primary: WMI/DCOM (Get-WmiObject over RPC/135). Fallback: when DCOM is dead
# ("RPC server unavailable") but WinRM (5985) is up, collect the SAME CIM classes
# over a WSMan CIM session — different transport, identical property names — so a
# host with WMI/DCOM blocked but WinRM open still inventories. CIM classes match
# Win32_* property names, so the rest of the script is unchanged.
$sess=$null
$probe=$null
$useCim=$false
try { $probe=Get-WmiObject -ComputerName $t -Credential $c -Class Win32_OperatingSystem -ErrorAction Stop } catch { $sess='cim' }
if($sess -eq 'cim'){
  $opt=New-CimSessionOption -Protocol Wsman
  $sess=New-CimSession -ComputerName $t -Credential $c -SessionOption $opt -OperationTimeoutSec 60 -ErrorAction Stop
  $g={param($cls) Get-CimInstance -CimSession $sess -ClassName $cls -ErrorAction Stop}
  $useCim=$true
} else {
  $g={param($cls) Get-WmiObject -ComputerName $t -Credential $c -Class $cls -ErrorAction Stop}
}
$os=&$g Win32_OperatingSystem; $cs=&$g Win32_ComputerSystem; $bios=&$g Win32_BIOS; $cpu=@(&$g Win32_Processor)
$cores=($cpu|Measure-Object NumberOfCores -Sum).Sum; if(-not $cores){$cores=($cpu|Measure-Object NumberOfLogicalProcessors -Sum).Sum}
$disks=@(&$g Win32_LogicalDisk|?{$_.DriveType -eq 3}|%{@{name=$_.DeviceID;filesystem=$_.FileSystem;total_bytes=[int64]$_.Size;free_bytes=[int64]$_.FreeSpace;size_bytes=[int64]$_.Size}})
$nics=@(&$g Win32_NetworkAdapterConfiguration|?{$_.IPEnabled}|%{@{name=$_.Description;mac=$_.MACAddress;ip_addresses=(@($_.IPAddress)-join',');gateway=(@($_.DefaultIPGateway)-join',');dns_servers=(@($_.DNSServerSearchOrder)-join',');dhcp_enabled=[bool]$_.DHCPEnabled}})
$svc=@(&$g Win32_Service|%{@{name=$_.Name;display_name=$_.DisplayName;status=$_.State;start_type=$_.StartMode;account=$_.StartName}})
# Top processes by working set. Win32_Process works over BOTH transports (WMI/DCOM
# and the WSMan CIM session), unlike Get-Process which is local-only — so this
# populates for agent-collected hosts the same as the direct-WinRM path does.
$procs=@(); try { $procs=@(&$g Win32_Process | Sort-Object WorkingSetSize -Descending | Select-Object -First 50 | %{@{name=$_.Name;pid=[int]$_.ProcessId;mem_bytes=[int64]$_.WorkingSetSize}}) } catch {}
# Installed software from the HKLM Uninstall registry. Win32_Product is deliberately
# NOT used — it is slow and triggers MSI self-repair. A fallback chain is walked and
# the method that worked (or the precise blocker) is recorded in $swnote, surfaced in
# the device's Software section instead of a silent empty list:
#   1/2. In-band per transport — Invoke-Command Get-ItemProperty over WinRM (CIM/WSMan
#        hosts), or StdRegProv via Invoke-WmiMethod over DCOM (WMI hosts).
#   3.   Remote Registry over SMB — when the in-band read yields nothing, authenticate
#        an SMB session and read the Uninstall hive over the winreg pipe. If the
#        RemoteRegistry service is stopped/disabled it is temporarily enabled+started
#        and then restored to its prior state (never left changed silently).
$sw=@()
$swnote=''
try {
  if($useCim){
    # WSMan path: the host speaks WinRM, so read the Uninstall registry with native
    # PS remoting (Get-ItemProperty runs locally on the target) — more reliable than
    # StdRegProv method-invocation over WSMan on legacy stacks.
    $items=Invoke-Command -ComputerName $t -Credential $c -ScriptBlock {
      Get-ItemProperty 'HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*','HKLM:\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*' -ErrorAction SilentlyContinue |
      Where-Object{$_.DisplayName} | ForEach-Object{[pscustomobject]@{n=[string]$_.DisplayName;v=[string]$_.DisplayVersion;p=[string]$_.Publisher;d=[string]$_.InstallDate}} } -ErrorAction Stop
    $sw=@($items|%{@{name=$_.n;version=$_.v;publisher=$_.p;install_date=$_.d}})
    if($sw.Count -gt 0){$swnote='collected via winrm_invoke'}
  } else {
    # DCOM path: no WinRM, so read the registry remotely via StdRegProv over WMI.
    $HKLM=[uint32]2147483650
    $ek={param($k) (Invoke-WmiMethod -ComputerName $t -Credential $c -Namespace 'root\default' -Class StdRegProv -Name EnumKey -ArgumentList $HKLM,$k).sNames}
    $gv={param($k,$v) (Invoke-WmiMethod -ComputerName $t -Credential $c -Namespace 'root\default' -Class StdRegProv -Name GetStringValue -ArgumentList $HKLM,$k,$v).sValue}
    foreach($base in @('SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall','SOFTWARE\Wow6432Node\Microsoft\Windows\CurrentVersion\Uninstall')){
      $subs=&$ek $base
      if($subs){ foreach($s in $subs){ $kp="$base\$s"; $dn=&$gv $kp 'DisplayName'
        if($dn){ $sw+=@{name=[string]$dn;version=[string](&$gv $kp 'DisplayVersion');publisher=[string](&$gv $kp 'Publisher');install_date=[string](&$gv $kp 'InstallDate')} } } }
    }
    if($sw.Count -gt 0){$swnote='collected via wmi_stdregprov'}
  }
} catch { $swnote=('inband_failed: '+$_.Exception.Message) }
# Remote Registry over SMB fallback when the in-band method returned no software.
if($sw.Count -eq 0){
  $rrStarted=$false; $rrWasDisabled=$false; $mapped=$false
  try {
    New-SmbMapping -RemotePath ("\\"+$t+"\IPC$") -UserName $u -Password $env:HIMS_J_PASS -ErrorAction Stop | Out-Null
    $mapped=$true
    $qc=(& sc.exe ("\\"+$t) qc RemoteRegistry) 2>&1 | Out-String
    if($qc -match 'FAILED 1060' -or $qc -match 'does not exist'){ throw 'remote_registry_absent' }
    if($qc -match 'START_TYPE\s*:\s*4'){ $rrWasDisabled=$true }
    $qs=(& sc.exe ("\\"+$t) query RemoteRegistry) 2>&1 | Out-String
    if($qs -notmatch 'STATE\s*:\s*4'){
      if($rrWasDisabled){ (& sc.exe ("\\"+$t) config RemoteRegistry start= demand) | Out-Null }
      (& sc.exe ("\\"+$t) start RemoteRegistry) | Out-Null
      $rrStarted=$true
      Start-Sleep -Milliseconds 1500
      $qs2=(& sc.exe ("\\"+$t) query RemoteRegistry) 2>&1 | Out-String
      if($qs2 -notmatch 'STATE\s*:\s*4'){ throw 'service_start_failed' }
    }
    $rk=[Microsoft.Win32.RegistryKey]::OpenRemoteBaseKey('LocalMachine',$t)
    foreach($bp in @('SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall','SOFTWARE\Wow6432Node\Microsoft\Windows\CurrentVersion\Uninstall')){
      $uk=$rk.OpenSubKey($bp)
      if($uk){ foreach($sn in $uk.GetSubKeyNames()){ $k2=$uk.OpenSubKey($sn)
        if($k2){ $dn=$k2.GetValue('DisplayName')
          if($dn){ $sw+=@{name=[string]$dn;version=[string]$k2.GetValue('DisplayVersion');publisher=[string]$k2.GetValue('Publisher');install_date=[string]$k2.GetValue('InstallDate')} }
          $k2.Close() } }
        $uk.Close() }
    }
    $rk.Close()
    $restored=''
    if($rrStarted){ (& sc.exe ("\\"+$t) stop RemoteRegistry) | Out-Null; $restored=' (RemoteRegistry temporarily started, then stopped'
      if($rrWasDisabled){ (& sc.exe ("\\"+$t) config RemoteRegistry start= disabled) | Out-Null; $restored+=' and re-disabled' }
      $restored+=')' }
    if($sw.Count -gt 0){ $swnote=('collected via remote_registry'+$restored) }
    else { $swnote='registry_access_denied' }
  } catch {
    $m=[string]$_; if($_.Exception){ $m=$_.Exception.Message }
    try { if($rrStarted){ (& sc.exe ("\\"+$t) stop RemoteRegistry) | Out-Null; if($rrWasDisabled){ (& sc.exe ("\\"+$t) config RemoteRegistry start= disabled) | Out-Null } } } catch {}
    if($m -match 'remote_registry_absent'){ $swnote='remote_registry_disabled' }
    elseif($m -match 'service_start_failed'){ $swnote='service_start_failed' }
    elseif($m -match 'Access is denied|denied|1219|1326'){ $swnote=('access_denied: '+$m) }
    elseif($m -match '53|54|64|unreachable|RPC|network path'){ $swnote=('rpc_unreachable: '+$m) }
    else { $swnote=('remote_registry_failed: '+$m) }
  } finally {
    if($mapped){ try { Remove-SmbMapping -RemotePath ("\\"+$t+"\IPC$") -Force -ErrorAction SilentlyContinue | Out-Null } catch {} }
  }
}
if($swnote -eq '' -and $sw.Count -eq 0){ $swnote='no_software_method_succeeded' }
@{ method='wmi'; identity=@{hostname=$os.CSName;fqdn=("{0}.{1}" -f $cs.Name,$cs.Domain).TrimEnd('.');domain=$cs.Domain;workgroup=$cs.Workgroup;logged_on_user=$cs.UserName};
   os=@{caption=$os.Caption;version=$os.Version;build="$($os.BuildNumber)";arch=$os.OSArchitecture;install_date="$($os.InstallDate)";last_boot="$($os.LastBootUpTime)"};
   hardware=@{manufacturer=$cs.Manufacturer;model=$cs.Model;serial=$bios.SerialNumber;bios_version=(@($bios.SMBIOSBIOSVersion)-join' ');cpu_model=$cpu[0].Name;cpu_sockets=$cpu.Count;cpu_cores=[int]$cores;ram_total_bytes=[int64]$cs.TotalPhysicalMemory};
   disks=$disks; nics=$nics; services=$svc; software=$sw; processes=$procs; roles=@(); events=$null; software_note=$swnote } | ConvertTo-Json -Depth 8 -Compress`
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	// Strip any inherited PSModulePath so Windows PowerShell 5.1 uses its own
	// default module locations. A PSModulePath pointing at PowerShell 7 modules
	// (e.g. when the agent is launched from a pwsh session) makes the 5.1 host
	// fail to load Microsoft.PowerShell.Security, breaking ConvertTo-SecureString.
	base := os.Environ()
	clean := base[:0]
	for _, kv := range base {
		if strings.HasPrefix(strings.ToUpper(kv), "PSMODULEPATH=") {
			continue
		}
		clean = append(clean, kv)
	}
	cmd.Env = append(clean, "HIMS_J_USER="+j.Username, "HIMS_J_PASS="+j.Password, "HIMS_J_TARGET="+j.Target)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		cat, _ := osinv.ClassifyWMIError(fmt.Errorf("%s", stderr.String()))
		return nil, cat, fmt.Errorf("%s", strings.TrimSpace(stderr.String()))
	}
	var rep osinv.Report
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &rep); err != nil {
		return nil, "wmi_error", fmt.Errorf("could not parse WMI inventory JSON")
	}
	rep.Method = "wmi"
	return &rep, "success", nil
}

// --- helpers -----------------------------------------------------------------

func sanitize(msg, pass string) string {
	if pass != "" {
		msg = strings.ReplaceAll(msg, pass, "***")
	}
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	return strings.TrimSpace(msg)
}

func hostname() string { h, _ := os.Hostname(); return h }

func osLabel() string { return runtime.GOOS + "/" + runtime.GOARCH }

func localIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

func getenv(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func getenvInt(k string, def int) int {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			return n
		}
	}
	return def
}
