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
	"strings"
	"time"

	"github.com/coralsearesorts/hims/internal/osinv"
)

const agentVersion = "1.0.0"

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
}

func (a *agent) pollOnce() {
	var jobs []job
	if err := a.do(http.MethodGet, "/api/v1/agent/jobs", nil, &jobs); err != nil {
		logln("poll error:", err)
		return
	}
	for _, j := range jobs {
		a.runJob(j)
	}
}

func (a *agent) runJob(j job) {
	// NEVER log the password. Log only target/protocol.
	logf("job %s: kind=%s protocol=%s target=%s", j.ID, j.Kind, j.Protocol, j.Target)
	res := map[string]any{}
	if j.Kind == "test" {
		res = map[string]any{"success": true, "category": "success"}
	} else {
		rep, cat, err := collect(j)
		if err != nil {
			res = map[string]any{"success": false, "category": cat, "error": sanitize(err.Error(), j.Password)}
		} else {
			res = map[string]any{"success": true, "category": "success", "report": rep}
		}
	}
	if err := a.do(http.MethodPost, "/api/v1/agent/jobs/"+j.ID+"/result", res, nil); err != nil {
		logln("post result error:", err)
	}
}

// collect runs one device collection locally and returns an osinv.Report.
func collect(j job) (*osinv.Report, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	switch j.Protocol {
	case "winrm":
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
	case "wmi":
		return collectWMI(ctx, j)
	default:
		return nil, "unsupported", fmt.Errorf("protocol %q not implemented in this agent build", j.Protocol)
	}
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
$g={param($cls) Get-WmiObject -ComputerName $t -Credential $c -Class $cls -ErrorAction Stop}
$os=&$g Win32_OperatingSystem; $cs=&$g Win32_ComputerSystem; $bios=&$g Win32_BIOS; $cpu=@(&$g Win32_Processor)
$cores=($cpu|Measure-Object NumberOfCores -Sum).Sum; if(-not $cores){$cores=($cpu|Measure-Object NumberOfLogicalProcessors -Sum).Sum}
$disks=@(&$g Win32_LogicalDisk|?{$_.DriveType -eq 3}|%{@{name=$_.DeviceID;filesystem=$_.FileSystem;total_bytes=[int64]$_.Size;free_bytes=[int64]$_.FreeSpace;size_bytes=[int64]$_.Size}})
$nics=@(&$g Win32_NetworkAdapterConfiguration|?{$_.IPEnabled}|%{@{name=$_.Description;mac=$_.MACAddress;ip_addresses=(@($_.IPAddress)-join',');gateway=(@($_.DefaultIPGateway)-join',');dns_servers=(@($_.DNSServerSearchOrder)-join',');dhcp_enabled=[bool]$_.DHCPEnabled}})
$svc=@(&$g Win32_Service|%{@{name=$_.Name;display_name=$_.DisplayName;status=$_.State;start_type=$_.StartMode;account=$_.StartName}})
@{ method='wmi'; identity=@{hostname=$os.CSName;fqdn=("{0}.{1}" -f $cs.Name,$cs.Domain).TrimEnd('.');domain=$cs.Domain;workgroup=$cs.Workgroup;logged_on_user=$cs.UserName};
   os=@{caption=$os.Caption;version=$os.Version;build="$($os.BuildNumber)";arch=$os.OSArchitecture;install_date="$($os.InstallDate)";last_boot="$($os.LastBootUpTime)"};
   hardware=@{manufacturer=$cs.Manufacturer;model=$cs.Model;serial=$bios.SerialNumber;bios_version=(@($bios.SMBIOSBIOSVersion)-join' ');cpu_model=$cpu[0].Name;cpu_sockets=$cpu.Count;cpu_cores=[int]$cores;ram_total_bytes=[int64]$cs.TotalPhysicalMemory};
   disks=$disks; nics=$nics; services=$svc; software=@(); roles=@(); events=$null } | ConvertTo-Json -Depth 8 -Compress`
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
