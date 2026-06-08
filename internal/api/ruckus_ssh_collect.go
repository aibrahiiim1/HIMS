package api

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coralsearesorts/hims/internal/ssh"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	gossh "golang.org/x/crypto/ssh"
)

// ruckusSSHSource tags SSH-CLI diagnostic results for a Ruckus ZoneDirector,
// kept distinct from the Extreme path (extreme_xcc_ssh) and the primary
// Web-XML roster (ruckus_zd_xml).
const ruckusSSHSource = "ruckus_zd_ssh"

// ruckusZDCLICommands is the read-only command set the ZoneDirector CLI exposes
// (verified live on ZD 10.x). Nothing here changes configuration.
var ruckusZDCLICommands = []string{
	"show sysinfo",
	"show wlan all",
	"show ap all",
	"show current-active-clients all",
}

// reZDPassphrase scrubs a WLAN PSK from any stored CLI preview ("Passphrase = …",
// "Dynamic PSK …"). The PSK is NEVER persisted or logged.
var reZDPassphrase = regexp.MustCompile(`(?i)(passphrase|pre-shared-key|psk)(\s*=\s*).*`)

// collectRuckusSSHCLI is the Ruckus ZoneDirector SSH-CLI collector. Unlike the
// Extreme path, the ZD CLI requires an interactive shell with an in-band login
// (the SSH transport auth is lenient; the ZD then prompts "Please login:" /
// "Password:"). It runs the read-only command set, parses the rosters for COUNTS
// + controller identity, and records per-command diagnostics (redacted) with
// source=ruckus_zd_ssh.
//
// It deliberately does NOT overwrite the access_points/wireless_clients/
// wireless_ssids tables: those are owned by the richer Web-XML primary
// (ruckus_zd_xml), which exposes live status + per-AP client counts that the CLI
// does not. This is a connectivity + diagnostic source, honest about its role.
func (s *Server) collectRuckusSSHCLI(ctx context.Context, dev db.Device, overUser, overPass string, emit sshEmitFn) sshCLISummary {
	if emit == nil {
		emit = func(string, string, string, string, int, int, int) {}
	}
	emit("ssh_cli_collection_started", "started", "", "Ruckus ZD SSH CLI started", 0, 0, 0)
	sum := sshCLISummary{}
	if dev.PrimaryIp == nil || !dev.PrimaryIp.IsValid() {
		sum.Detail = "SSH CLI not run: this controller has no IP address"
		emit("ssh_cli_collection_failed", "failed", "", sum.Detail, 0, 0, 0)
		return sum
	}
	// The ZoneDirector CLI login uses the SAME admin account as the Web-XML profile,
	// so prefer the bound ruckus_zd profile credential. An explicit override (Test
	// SSH form) wins; a resolved SSH/CLI credential is the last resort.
	var creds ssh.Creds
	switch {
	case strings.TrimSpace(overUser) != "":
		creds = ssh.Creds{Username: strings.TrimSpace(overUser), Password: overPass}
	default:
		if prof, perr := s.queries.GetVendorProfileForDeviceVendor(ctx, db.GetVendorProfileForDeviceVendorParams{DeviceID: &dev.ID, VendorType: "ruckus_zd"}); perr == nil {
			if u, p, has := s.vendorProfileSecret(ctx, prof); has {
				creds = ssh.Creds{Username: u, Password: p}
			}
		}
		if creds.Username == "" {
			rc, _, _, rok, rdetail := s.resolveSSHCred(ctx, dev, "", "")
			if !rok {
				sum.Detail = "SSH CLI not run: " + rdetail
				emit("ssh_cli_collection_failed", "failed", "", sum.Detail, 0, 0, 0)
				return sum
			}
			creds = rc
		}
	}
	host := dev.PrimaryIp.String()

	// ZoneDirector firmware needs legacy SSH KEX/ciphers (appended to the modern set).
	outputs, transcript, err := runZDSSHSession(ctx, host, 22, creds, true, ruckusZDCLICommands, 90*time.Second)
	if err != nil && len(outputs) == 0 {
		sum.Detail = "Ruckus ZD SSH CLI failed: " + redactSecrets(err.Error(), 200)
		emit("ssh_cli_collection_failed", "failed", "", sum.Detail, 0, 0, 0)
		return sum
	}
	sum.Reachable = true

	_ = s.queries.DeleteSSHCliResultsForSource(ctx, db.DeleteSSHCliResultsForSourceParams{DeviceID: dev.ID, Source: ruckusSSHSource})

	// Controller identity + reported totals from `show sysinfo`.
	ident := parseZDSysinfo(outputs["show sysinfo"])
	sum.APTotal = ident.apCount
	sum.ClientsTotal = ident.clientCount

	for _, cmd := range ruckusZDCLICommands {
		emit("ssh_cli_command_started", "started", cmd, "", 0, 0, 0)
		out, present := outputs[cmd]
		res := sshCLIResult{Command: cmd}
		switch {
		case !present || strings.TrimSpace(out) == "":
			res.Status = "failed"
			res.Error = "no output (command not echoed / login or prompt issue)"
		default:
			rows := 0
			switch cmd {
			case "show ap all":
				rows = zdCountIndented(out, "MAC Address=")
				sum.APs = rows
			case "show current-active-clients all":
				rows = zdCountIndented(out, "Mac Address=")
				sum.Clients = rows
			case "show wlan all":
				rows = zdCountIndented(out, "NAME =")
				sum.SSIDs = rows
			case "show sysinfo":
				rows = 1
			}
			res.ParsedRows = rows
			res.LineCount = countNonEmptyLines(out)
			res.Status = map[bool]string{true: "parsed", false: "not_parsed"}[rows > 0]
			sum.ParsedRows += rows
		}
		// Scrub any PSK before storing the preview, then redact generic secrets.
		preview := reZDPassphrase.ReplaceAllString(out, "${1}${2}***")
		res.Preview = redactSecrets(strings.TrimSpace(preview), 2000)
		switch res.Status {
		case "parsed", "not_parsed":
			sum.Supported++
			emit("ssh_cli_command_parsed", "parsed", cmd, "", res.ParsedRows, 0, 0)
		default:
			emit("ssh_cli_command_failed", "failed", cmd, res.Error, 0, 0, 0)
		}
		_ = s.queries.UpsertSSHCliResult(ctx, db.UpsertSSHCliResultParams{
			DeviceID: dev.ID, Source: ruckusSSHSource, Command: cmd, Status: res.Status,
			OutputPreview: res.Preview, ParsedRows: int32(res.ParsedRows), ErrorMessage: res.Error,
			LineCount: int32(res.LineCount), Headers: "", SkippedRows: 0, Warnings: "",
		})
		sum.Results = append(sum.Results, res)
	}

	sum.RosterExposed = sum.APs > 0 || sum.Clients > 0 || sum.SSIDs > 0
	switch {
	case !sum.RosterExposed:
		sum.Status = "failed"
	case sum.APs >= sum.APTotal && sum.APTotal > 0:
		sum.Status = "complete"
	default:
		sum.Status = "partial"
	}
	sum.OK = sum.RosterExposed
	sum.Detail = fmt.Sprintf("Ruckus ZD SSH CLI (read-only diagnostic) — %d AP(s), %d client(s), %d WLAN(s) parsed; rosters are owned by the Web-XML primary.",
		sum.APs, sum.Clients, sum.SSIDs)
	if ident.name != "" {
		sum.Detail = ident.name + ": " + sum.Detail
	}
	// Persist the controller summary so the detail page's "Collection status" KPI
	// reflects THIS diagnostic run instead of a stale Extreme-SSH attempt. The
	// summary_source keeps it distinct, and it never touches the AP/client/SSID
	// roster tables (still owned by the Web-XML primary). active/non-active stay 0
	// because `show ap all` does not expose per-AP connection status.
	apTotal := maxInt(sum.APTotal, sum.APs)
	clientsTotal := maxInt(sum.ClientsTotal, sum.Clients)
	_ = s.queries.UpsertWirelessControllerSummary(ctx, db.UpsertWirelessControllerSummaryParams{
		DeviceID: dev.ID, SummarySource: ruckusSSHSource,
		NetworksCount: int32(sum.SSIDs), SwitchesCount: 0, ApTotal: int32(apTotal),
		AdoptionPrimary: 0, AdoptionBackup: 0,
		ActiveAps: 0, NonActiveAps: 0, ClientsTotal: int32(clientsTotal),
		ParsedApRows: int32(sum.APs), ParsedClientRows: int32(sum.Clients), ParsedSsidRows: int32(sum.SSIDs),
		CollectionStatus: sum.Status, Detail: capStr(sum.Detail, 480),
	})
	_ = transcript // retained for future field mapping; not stored (may contain a PSK)
	emit("ssh_cli_collection_finished", "finished", "", sum.Detail, sum.ParsedRows, 0, 0)
	return sum
}

// zdIdentity holds the fields parsed from `show sysinfo`.
type zdIdentity struct {
	name, model, serial, version, ip, mac string
	apCount, clientCount                  int
}

// parseZDSysinfo reads the `key= value` lines of `show sysinfo`.
func parseZDSysinfo(out string) zdIdentity {
	var id zdIdentity
	for _, ln := range strings.Split(out, "\n") {
		k, v, ok := zdKV(ln)
		if !ok {
			continue
		}
		switch strings.ToLower(k) {
		case "name":
			id.name = v
		case "model":
			id.model = v
		case "serial number":
			id.serial = v
		case "version":
			id.version = v
		case "ip address":
			if id.ip == "" {
				id.ip = v
			}
		case "mac address":
			if id.mac == "" {
				id.mac = v
			}
		case "number of aps":
			id.apCount = atoiSafe(v)
		case "number of client devices":
			id.clientCount = atoiSafe(v)
		}
	}
	return id
}

// zdKV splits a ZD CLI "key= value" / "key = value" line on the first '='.
func zdKV(line string) (key, val string, ok bool) {
	i := strings.IndexByte(line, '=')
	if i < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:i])
	val = strings.TrimSpace(line[i+1:])
	if key == "" {
		return "", "", false
	}
	return key, val, true
}

// zdCountIndented counts indented lines whose trimmed text starts with prefix —
// the per-record key used to count AP/client/WLAN blocks in ZD CLI output.
func zdCountIndented(out, prefix string) int {
	n := 0
	for _, ln := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), prefix) {
			n++
		}
	}
	return n
}

func atoiSafe(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// runZDSSHSession opens an interactive SSH shell to a Ruckus ZoneDirector, drives
// the in-band CLI login (admin/password), enters privileged mode, and runs each
// command, returning per-command output keyed by command plus the full
// transcript. The password is never logged. Commands are read-only.
func runZDSSHSession(ctx context.Context, host string, port int, c ssh.Creds, legacyKEX bool, commands []string, overall time.Duration) (map[string]string, string, error) {
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	dialCtx, cancel := context.WithTimeout(ctx, overall)
	defer cancel()

	d := net.Dialer{Timeout: 20 * time.Second}
	conn, err := d.DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return nil, "", fmt.Errorf("dial %s: %w", addr, err)
	}
	ki := gossh.KeyboardInteractive(func(_, _ string, qs []string, _ []bool) ([]string, error) {
		a := make([]string, len(qs))
		for i := range qs {
			a[i] = c.Password
		}
		return a, nil
	})
	base := gossh.Config{}
	base.SetDefaults()
	cfg := &gossh.ClientConfig{
		User:            c.Username,
		Auth:            []gossh.AuthMethod{gossh.Password(c.Password), ki},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(), //nolint:gosec // device keys not pinned
		Timeout:         20 * time.Second,
	}
	// ZoneDirector firmware speaks legacy KEX/ciphers; appending keeps modern too.
	cfg.KeyExchanges = append(base.KeyExchanges, "diffie-hellman-group14-sha1", "diffie-hellman-group1-sha1")
	cfg.Ciphers = append(base.Ciphers, "aes128-cbc", "aes256-cbc", "3des-cbc")

	sshConn, chans, reqs, err := gossh.NewClientConn(conn, addr, cfg)
	if err != nil {
		conn.Close()
		return nil, "", fmt.Errorf("ssh handshake: %w", err)
	}
	client := gossh.NewClient(sshConn, chans, reqs)
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		return nil, "", fmt.Errorf("ssh session: %w", err)
	}
	defer sess.Close()

	modes := gossh.TerminalModes{gossh.ECHO: 1, gossh.TTY_OP_ISPEED: 38400, gossh.TTY_OP_OSPEED: 38400}
	_ = sess.RequestPty("vt100", 100000, 512, modes)
	stdin, err := sess.StdinPipe()
	if err != nil {
		return nil, "", fmt.Errorf("stdin: %w", err)
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		return nil, "", fmt.Errorf("stdout: %w", err)
	}
	stderr, _ := sess.StderrPipe()

	var mu sync.Mutex
	var buf bytes.Buffer
	reader := func(r interface{ Read([]byte) (int, error) }) {
		b := make([]byte, 8192)
		for {
			n, e := r.Read(b)
			if n > 0 {
				mu.Lock()
				buf.Write(b[:n])
				mu.Unlock()
			}
			if e != nil {
				return
			}
		}
	}
	go reader(stdout)
	if stderr != nil {
		go reader(stderr)
	}
	if err := sess.Shell(); err != nil {
		return nil, "", fmt.Errorf("ssh shell: %w", err)
	}

	snapshot := func() string { mu.Lock(); defer mu.Unlock(); return buf.String() }
	// expect waits until the tail of the transcript contains any marker, advancing
	// a "--More--" pager with a space. Returns false on timeout.
	expect := func(timeout time.Duration, markers ...string) bool {
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			if dialCtx.Err() != nil {
				return false
			}
			cur := snapshot()
			tail := cur
			if len(tail) > 80 {
				tail = tail[len(tail)-80:]
			}
			if strings.Contains(strings.ToLower(tail), "more") {
				_, _ = stdin.Write([]byte(" "))
			}
			for _, m := range markers {
				if strings.Contains(tail, m) {
					return true
				}
			}
			time.Sleep(200 * time.Millisecond)
		}
		return false
	}

	// In-band CLI login.
	expect(8*time.Second, "login:", "Login:")
	_, _ = stdin.Write([]byte(c.Username + "\n"))
	expect(8*time.Second, "Password:", "password:")
	_, _ = stdin.Write([]byte(c.Password + "\n"))
	if !expect(12*time.Second, "ruckus>", "ruckus#", ">", "#") {
		return nil, snapshot(), fmt.Errorf("ZD CLI login did not reach a prompt (check SSH credentials)")
	}
	// Privileged mode.
	_, _ = stdin.Write([]byte("enable\n"))
	expect(5*time.Second, "ruckus#", "#")

	outputs := map[string]string{}
	prompt := "ruckus#"
	for _, cmd := range commands {
		if dialCtx.Err() != nil {
			break
		}
		start := len(snapshot())
		_, _ = stdin.Write([]byte(cmd + "\n"))
		// Wait for the command echo + a returning prompt after it.
		deadline := time.Now().Add(25 * time.Second)
		for time.Now().Before(deadline) {
			cur := snapshot()
			seg := cur[start:]
			// A new prompt after the echoed command signals completion.
			if idx := strings.Index(seg, cmd); idx >= 0 {
				after := seg[idx+len(cmd):]
				if strings.Contains(after, prompt) {
					break
				}
				if strings.Contains(strings.ToLower(after[max0(len(after)-80):]), "more") {
					_, _ = stdin.Write([]byte(" "))
				}
			}
			time.Sleep(200 * time.Millisecond)
		}
		cur := snapshot()
		seg := cur[start:]
		outputs[cmd] = trimZDCommandOutput(seg, cmd, prompt)
	}
	_, _ = stdin.Write([]byte("quit\n"))
	time.Sleep(500 * time.Millisecond)
	return outputs, snapshot(), nil
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

// trimZDCommandOutput extracts the body between the echoed command and the next
// prompt, dropping the echo line and the trailing prompt.
func trimZDCommandOutput(seg, cmd, prompt string) string {
	if i := strings.Index(seg, cmd); i >= 0 {
		seg = seg[i+len(cmd):]
	}
	if j := strings.LastIndex(seg, prompt); j >= 0 {
		seg = seg[:j]
	}
	return strings.TrimSpace(seg)
}
