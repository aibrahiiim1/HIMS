package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/ssh"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// decodeJSONOptional decodes a request body into dst, tolerating an empty body
// (used by endpoints whose body is entirely optional, e.g. SSH cred override).
func decodeJSONOptional(r *http.Request, dst any) error {
	if r.Body == nil {
		return nil
	}
	return json.NewDecoder(r.Body).Decode(dst)
}

// Extreme XCC SSH CLI collector. For a wireless controller with a working SSH
// credential, run a fixed READ-ONLY command allowlist, classify each command's
// support honestly, capture a size-capped + secret-redacted preview, and parse
// AP/SSID/client/version data WHERE the firmware exposes it over CLI. On the
// VE6120 / ExtremeCloud IQ Controller (fw 10.05) the CLI may not expose the
// AP/SSID/client roster — that is reported honestly, never faked.

const sshCLISource = "extreme_xcc_ssh"

// xccCLICommands is the read-only probe allowlist. Nothing here changes config.
// (The set is fixed in code — the API never executes an operator-supplied
// command, so write/destructive commands are impossible by construction.)
var xccCLICommands = []string{
	"show version",
	"show system",
	"show inventory",
	"show controller",
	"show ap",
	"show aps",
	"show wireless",
	"show wlan",
	"show ssid",
	"show clients",
	"show station",
	"show interface",
	"show license",
	"show log",
	"show running-config",
	"help",
	"show ?",
}

// cliParseKind picks which parser (if any) applies to a command's output.
func cliParseKind(cmd string) string {
	switch cmd {
	case "show ap", "show aps", "show wireless":
		return "aps"
	case "show wlan", "show ssid":
		return "ssids"
	case "show clients", "show station":
		return "clients"
	case "show version", "show system", "show inventory", "show controller":
		return "identity"
	case "show log":
		return "events"
	}
	return ""
}

var (
	reMAC    = regexp.MustCompile(`(?:[0-9A-Fa-f]{2}[:-]){5}[0-9A-Fa-f]{2}`)
	reIPv4   = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	reVer    = regexp.MustCompile(`(?i)(?:firmware|version|sw version|image|release)\s*[:=]?\s*([0-9][\w.\-]+)`)
	reModel  = regexp.MustCompile(`(?i)\b(VE\d{4}|VE-?\d{4}|ExtremeCloud IQ Controller|Summit WM\w*|C\d{4})\b`)
	reSerial = regexp.MustCompile(`(?i)serial(?: number| no\.?| #)?\s*[:=]?\s*([A-Z0-9\-]{6,})`)
	// Markers that mean "the controller rejected this command" (restricted CLI).
	reUnsupported = regexp.MustCompile(`(?i)(invalid command|unknown command|unrecognized command|command not found|% invalid|ambiguous command|syntax error|incomplete command|not allowed|permission denied|no such command|% error)`)
	// Secret-bearing tokens to redact from any captured output.
	reSecret = regexp.MustCompile(`(?i)\b(password|passwd|secret|psk|passphrase|community|pre-shared-key|preshared|wpa-?key|radius-?key|shared-?secret|token|apikey|api-key|credential)\b\s*[:=]?\s*\S+`)
)

// redactSecrets masks secret-bearing values and caps output length so nothing
// sensitive is persisted or returned, regardless of which command was run.
func redactSecrets(s string, max int) string {
	s = reSecret.ReplaceAllStringFunc(s, func(m string) string {
		if i := strings.IndexAny(m, ":= "); i >= 0 {
			return m[:i+1] + " ****"
		}
		return "****"
	})
	if len(s) > max {
		s = s[:max] + "…(truncated)"
	}
	return s
}

// sshCLIResult is the per-command outcome surfaced to the operator.
type sshCLIResult struct {
	Command    string `json:"command"`
	Status     string `json:"status"` // parsed|not_parsed|unsupported|failed|timeout
	ParsedRows int    `json:"parsed_rows"`
	Preview    string `json:"output_preview"`
	Error      string `json:"error_message,omitempty"`
}

// sshCLISummary is the run summary returned by the collect/test endpoints.
type sshCLISummary struct {
	OK           bool           `json:"ok"`
	Reachable    bool           `json:"reachable"`
	Detail       string         `json:"detail"`
	Supported    int            `json:"supported"`
	Unsupported  int            `json:"unsupported"`
	ParsedRows   int            `json:"parsed_rows"`
	APs          int            `json:"aps"`
	SSIDs        int            `json:"ssids"`
	Clients      int            `json:"clients"`
	RosterExposed bool          `json:"roster_exposed"`
	Results      []sshCLIResult `json:"results"`
}

// authSSH tries modern then legacy KEX; returns (ok, legacyNeeded).
func authSSH(ctx context.Context, host string, c ssh.Creds) (bool, bool) {
	if err := ssh.CheckAuth(ctx, host, 22, c, false, 8*time.Second); err == nil {
		return true, false
	}
	if err := ssh.CheckAuth(ctx, host, 22, c, true, 8*time.Second); err == nil {
		return true, true
	}
	return false, false
}

// resolveSSHCred finds a working SSH credential for a device: an explicit
// override (username/password from the request), else the bound credential if
// it is 'ssh', else any enabled 'ssh' credential that authenticates (the
// "system tries reasonable credentials" model). Never logs the password.
func (s *Server) resolveSSHCred(ctx context.Context, dev db.Device, overUser, overPass string) (creds ssh.Creds, credID *uuid.UUID, legacy bool, ok bool, detail string) {
	if dev.PrimaryIp == nil || !dev.PrimaryIp.IsValid() {
		return creds, nil, false, false, "device has no IP"
	}
	host := dev.PrimaryIp.String()

	if overUser != "" {
		c := ssh.Creds{Username: overUser, Password: overPass}
		if good, lg := authSSH(ctx, host, c); good {
			return c, nil, lg, true, "supplied SSH credential"
		}
		return creds, nil, false, false, "supplied SSH credential failed to authenticate"
	}

	cph := s.cipher()
	if cph == nil {
		return creds, nil, false, false, "encryption key not configured"
	}
	open := func(id uuid.UUID) (ssh.Creds, bool) {
		c, err := s.queries.GetCredential(ctx, id)
		if err != nil || c.Kind != string(domain.CredSSH) {
			return ssh.Creds{}, false
		}
		plain, err := cph.Open(c.EncryptedBlob, c.KeyID)
		if err != nil {
			return ssh.Creds{}, false
		}
		u, p := splitUserPass(string(plain))
		return ssh.Creds{Username: u, Password: p}, true
	}
	if dev.CredentialID != nil {
		if c, good := open(*dev.CredentialID); good {
			if auth, lg := authSSH(ctx, host, c); auth {
				id := *dev.CredentialID
				return c, &id, lg, true, "bound SSH credential"
			}
		}
	}
	all, _ := s.queries.ListCredentials(ctx)
	for _, cr := range all {
		if cr.Kind != string(domain.CredSSH) {
			continue
		}
		if c, good := open(cr.ID); good {
			if auth, lg := authSSH(ctx, host, c); auth {
				id := cr.ID
				return c, &id, lg, true, "auto-resolved SSH credential"
			}
		}
	}
	return creds, nil, false, false, "no working SSH credential (bound or enabled 'ssh' credentials)"
}

// collectSSHCLI runs the read-only allowlist over SSH, classifies + persists
// command results, and (when persistWireless) maps exposed AP/SSID/client data
// into the wireless_* tables with source=extreme_xcc_ssh.
func (s *Server) collectSSHCLI(ctx context.Context, dev db.Device, overUser, overPass string, persistWireless bool) sshCLISummary {
	sum := sshCLISummary{}
	creds, credID, legacy, ok, detail := s.resolveSSHCred(ctx, dev, overUser, overPass)
	if !ok {
		sum.Detail = "SSH CLI not run: " + detail
		return sum
	}
	sum.Reachable = true
	host := dev.PrimaryIp.String()
	poll := time.Now().UTC()

	// Bind the working SSH credential only if the device has none yet (keep the
	// SNMP-identity binding otherwise — both sources stay valid).
	if credID != nil && dev.CredentialID == nil {
		_ = s.queries.SetDeviceCredential(ctx, db.SetDeviceCredentialParams{ID: dev.ID, CredentialID: credID})
	}

	_ = s.queries.DeleteSSHCliResultsForSource(ctx, db.DeleteSSHCliResultsForSourceParams{DeviceID: dev.ID, Source: sshCLISource})

	var apN, ssidN, cliN int
	for _, cmd := range xccCLICommands {
		out, err := ssh.Run(ctx, host, 22, creds, legacy, cmd, 12*time.Second)
		res := sshCLIResult{Command: cmd}
		switch {
		case err != nil:
			low := strings.ToLower(err.Error())
			if strings.Contains(low, "timeout") || strings.Contains(low, "deadline") {
				res.Status = "timeout"
			} else {
				res.Status = "failed"
			}
			res.Error = redactSecrets(err.Error(), 240)
		case reUnsupported.MatchString(out):
			res.Status = "unsupported"
		default:
			// Ran OK. Parse if a parser applies + this is a persisting run.
			red := redactSecrets(out, 4000)
			rows := 0
			if persistWireless {
				switch cliParseKind(cmd) {
				case "aps":
					rows = s.persistCLIAPs(ctx, dev, red)
					apN += rows
				case "ssids":
					rows = s.persistCLISSIDs(ctx, dev, red)
					ssidN += rows
				case "clients":
					// The client table also exposes the SSIDs actively serving
					// clients — derive distinct SSIDs from it (show wlan/ssid are
					// rejected by this CLI), honestly sourced from real associations.
					cN, sN := s.persistCLIClients(ctx, dev, red)
					cliN += cN
					ssidN += sN
					rows = cN
				case "identity":
					rows = s.persistCLIIdentity(ctx, dev, red)
				case "events":
					rows = s.persistCLIEvents(ctx, dev, red, poll)
				}
			} else {
				rows = cliDryParseCount(cliParseKind(cmd), red)
			}
			res.ParsedRows = rows
			if rows > 0 {
				res.Status = "parsed"
			} else {
				res.Status = "not_parsed"
			}
			sum.ParsedRows += rows
		}
		// Preview = redacted, capped output (only when a command actually ran).
		if res.Status != "failed" && res.Status != "timeout" {
			res.Preview = redactSecrets(strings.TrimSpace(out), 1500)
		}
		switch res.Status {
		case "parsed", "not_parsed":
			sum.Supported++
		case "unsupported":
			sum.Unsupported++
		}
		_ = s.queries.UpsertSSHCliResult(ctx, db.UpsertSSHCliResultParams{
			DeviceID: dev.ID, Source: sshCLISource, Command: cmd, Status: res.Status,
			OutputPreview: res.Preview, ParsedRows: int32(res.ParsedRows), ErrorMessage: res.Error,
		})
		sum.Results = append(sum.Results, res)
	}

	sum.APs, sum.SSIDs, sum.Clients = apN, ssidN, cliN
	sum.RosterExposed = apN > 0 || ssidN > 0 || cliN > 0
	if persistWireless {
		_ = s.queries.DeleteStaleAccessPoints(ctx, db.DeleteStaleAccessPointsParams{ControllerDeviceID: dev.ID, Source: sshCLISource, CollectedAt: poll})
		_ = s.queries.DeleteStaleWirelessSSIDs(ctx, db.DeleteStaleWirelessSSIDsParams{ControllerDeviceID: dev.ID, Source: sshCLISource, CollectedAt: poll})
		_ = s.queries.DeleteStaleWirelessClients(ctx, db.DeleteStaleWirelessClientsParams{ControllerDeviceID: dev.ID, Source: sshCLISource, CollectedAt: poll})
		if sum.RosterExposed {
			ven := derefStr(dev.Vendor)
			_, _ = s.queries.UpsertWLANControllerInfo(ctx, db.UpsertWLANControllerInfoParams{
				DeviceID: dev.ID, Vendor: nzPtr(ven), Version: dev.OsVersion, ApCount: int32(apN), ClientCount: int32(cliN),
				Source: sshCLISource, ControllerName: dev.Name, Model: derefStr(dev.Model), SsidCount: int32(ssidN),
			})
		}
	}

	sum.OK = sum.Supported > 0
	switch {
	case sum.Supported == 0:
		sum.Detail = "SSH connected but none of the probed read-only commands were supported by this controller's CLI."
	case sum.RosterExposed:
		sum.Detail = "SSH CLI collected: " + strconv.Itoa(apN) + " APs, " + strconv.Itoa(ssidN) + " SSIDs, " + strconv.Itoa(cliN) + " clients (source=extreme_xcc_ssh)."
	default:
		sum.Detail = "SSH CLI ran (" + strconv.Itoa(sum.Supported) + " supported commands) but does not expose AP/SSID/client roster using the supported commands. Identity/operational/log output captured for troubleshooting."
	}
	return sum
}

// --- parsers (tolerant; only persist confidently-identified rows) -----------

func cliDryParseCount(kind, out string) int {
	switch kind {
	case "aps":
		return len(parseCLIAPRows(out))
	case "clients":
		c, _ := parseCLIClientRows(out)
		return len(c)
	case "ssids":
		return len(parseSSIDNames(out))
	case "identity":
		if reVer.MatchString(out) || reModel.MatchString(out) || reSerial.MatchString(out) {
			return 1
		}
	case "events":
		return countNonEmptyLines(out)
	}
	return 0
}

// cliAP / cliClient are the pure-parse results (unit-testable, no DB).
type cliAP struct {
	Serial, Name, Model, MAC, IP string
}
type cliClient struct {
	MAC, IP, SSID, AP, Band, Hostname string
	RSSI                              *int32
}

var (
	reCols    = regexp.MustCompile(`\s{2,}`)
	reAPModel = regexp.MustCompile(`(?i)\bAP\d{3,4}[A-Za-z0-9]*\b`)
	reSerial15 = regexp.MustCompile(`^\d{10,}$`)
)

// parseCLIAPRows parses Extreme XCC `show ap` output. The observed format is
// "serial <serial> <serial> <ap-name>"; a MAC-table fallback covers other CLIs.
func parseCLIAPRows(out string) []cliAP {
	var aps []cliAP
	for _, ln := range strings.Split(out, "\n") {
		f := strings.Fields(ln)
		if len(f) >= 2 && strings.EqualFold(f[0], "serial") && reSerial15.MatchString(f[1]) {
			ap := cliAP{Serial: f[1]}
			ap.Name = f[len(f)-1]
			if ap.Name == ap.Serial && len(f) >= 3 {
				ap.Name = f[2]
			}
			ap.Model = reAPModel.FindString(ln)
			aps = append(aps, ap)
			continue
		}
		// Fallback: a MAC-led AP row (other vendors/firmware).
		if mac := reMAC.FindString(ln); mac != "" {
			name := firstField(ln)
			if isMACish(name) {
				name = mac
			}
			aps = append(aps, cliAP{Name: name, MAC: mac, IP: reIPv4.FindString(ln), Model: reAPModel.FindString(ln)})
		}
	}
	return aps
}

// parseCLIClientRows parses the `show clients` column table (header-driven), and
// returns the clients plus the distinct SSIDs they are associated with.
func parseCLIClientRows(out string) ([]cliClient, []string) {
	lines := strings.Split(out, "\n")
	// Locate the header row + map column name -> index.
	colIdx := map[string]int{}
	headerAt := -1
	for i, ln := range lines {
		low := strings.ToLower(ln)
		if strings.Contains(low, "client mac") && strings.Contains(low, "ssid") {
			for j, name := range reCols.Split(strings.TrimSpace(ln), -1) {
				colIdx[strings.ToLower(strings.TrimSpace(name))] = j
			}
			headerAt = i
			break
		}
	}
	get := func(vals []string, key string) string {
		if j, ok := colIdx[key]; ok && j < len(vals) {
			return strings.TrimSpace(vals[j])
		}
		return ""
	}
	var clients []cliClient
	ssidSeen := map[string]bool{}
	var ssids []string
	currentAP := ""
	for i, ln := range lines {
		if i <= headerAt {
			// Capture AP grouping headers that may appear before the table.
			if m := regexp.MustCompile(`(?i)AP Serial:\s*(\S+)`).FindStringSubmatch(ln); m != nil {
				currentAP = m[1]
			}
			continue
		}
		if m := regexp.MustCompile(`(?i)AP Serial:\s*(\S+)`).FindStringSubmatch(ln); m != nil {
			currentAP = m[1]
			continue
		}
		mac := reMAC.FindString(ln)
		if mac == "" {
			continue
		}
		vals := reCols.Split(strings.TrimSpace(ln), -1)
		c := cliClient{MAC: mac, AP: currentAP}
		if headerAt >= 0 {
			c.IP = reIPv4.FindString(get(vals, "client ip"))
			c.SSID = get(vals, "ssid")
			c.Hostname = get(vals, "user")
			c.Band = bandFromProto(get(vals, "protocol"))
			if r, err := strconv.Atoi(strings.TrimSpace(get(vals, "rss(dbm)"))); err == nil {
				rr := int32(r)
				c.RSSI = &rr
			}
		}
		if c.IP == "" {
			c.IP = reIPv4.FindString(ln)
		}
		clients = append(clients, c)
		if c.SSID != "" && !ssidSeen[c.SSID] && !strings.EqualFold(c.SSID, "ssid") {
			ssidSeen[c.SSID] = true
			ssids = append(ssids, c.SSID)
		}
	}
	return clients, ssids
}

func bandFromProto(p string) string {
	switch {
	case strings.Contains(p, "5.0"), strings.Contains(p, "5G"), strings.HasPrefix(p, "5"):
		return "5GHz"
	case strings.Contains(p, "2.4"), strings.Contains(p, "2G"):
		return "2.4GHz"
	case strings.Contains(strings.ToLower(p), "6e"), strings.HasPrefix(p, "6"):
		return "6GHz"
	}
	return ""
}

func (s *Server) persistCLIAPs(ctx context.Context, dev db.Device, out string) int {
	n := 0
	for _, ap := range parseCLIAPRows(out) {
		name := ap.Name
		if name == "" {
			name = ap.Serial
		}
		if name == "" {
			continue
		}
		var macPtr *string
		if ap.MAC != "" {
			m := ap.MAC
			macPtr = &m
		}
		var ip *netip.Addr
		if a, e := netip.ParseAddr(ap.IP); e == nil {
			ip = &a
		}
		_, _ = s.queries.UpsertAccessPoint(ctx, db.UpsertAccessPointParams{
			ControllerDeviceID: dev.ID, Name: name, Mac: macPtr, Model: nzPtr(ap.Model), Ip: ip,
			Status: "unknown", ClientCount: 0, Serial: ap.Serial, Source: sshCLISource,
		})
		n++
	}
	return n
}

// persistCLIClients persists clients and returns (clients, derivedSSIDs).
func (s *Server) persistCLIClients(ctx context.Context, dev db.Device, out string) (int, int) {
	clients, ssids := parseCLIClientRows(out)
	for _, c := range clients {
		_, _ = s.queries.UpsertWirelessClient(ctx, db.UpsertWirelessClientParams{
			ControllerDeviceID: dev.ID, Mac: c.MAC, Ip: c.IP, Hostname: c.Hostname,
			ApName: c.AP, Ssid: c.SSID, Rssi: c.RSSI, Band: c.Band, Source: sshCLISource,
		})
	}
	for _, name := range ssids {
		_, _ = s.queries.UpsertWirelessSSID(ctx, db.UpsertWirelessSSIDParams{
			ControllerDeviceID: dev.ID, Name: name, Status: "active", Source: sshCLISource,
		})
	}
	return len(clients), len(ssids)
}

func (s *Server) persistCLISSIDs(ctx context.Context, dev db.Device, out string) int {
	n := 0
	for _, name := range parseSSIDNames(out) {
		_, _ = s.queries.UpsertWirelessSSID(ctx, db.UpsertWirelessSSIDParams{
			ControllerDeviceID: dev.ID, Name: name, Status: "unknown", Source: sshCLISource,
		})
		n++
	}
	return n
}

func (s *Server) persistCLIIdentity(ctx context.Context, dev db.Device, out string) int {
	ver, model := "", ""
	if m := reVer.FindStringSubmatch(out); m != nil {
		ver = m[1]
	}
	if m := reModel.FindString(out); m != "" {
		model = m
	}
	if ver == "" && model == "" {
		return 0
	}
	verPtr := dev.OsVersion
	if ver != "" {
		verPtr = &ver
	}
	if model == "" {
		model = derefStr(dev.Model)
	}
	_, _ = s.queries.UpsertWLANControllerInfo(ctx, db.UpsertWLANControllerInfoParams{
		DeviceID: dev.ID, Vendor: nzPtr(derefStr(dev.Vendor)), Version: verPtr, ApCount: 0, ClientCount: 0,
		Source: sshCLISource, ControllerName: dev.Name, Model: model, SsidCount: 0,
	})
	return 1
}

func (s *Server) persistCLIEvents(ctx context.Context, dev db.Device, out string, poll time.Time) int {
	lines := []string{}
	for _, ln := range strings.Split(out, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			lines = append(lines, t)
		}
	}
	if len(lines) == 0 {
		return 0
	}
	_ = s.queries.DeleteWirelessEventsForSource(ctx, db.DeleteWirelessEventsForSourceParams{ControllerDeviceID: dev.ID, Source: sshCLISource})
	n := 0
	for i, ln := range lines {
		if i >= 100 {
			break
		}
		_ = s.queries.InsertWirelessEvent(ctx, db.InsertWirelessEventParams{
			ControllerDeviceID: dev.ID, At: poll, Severity: "info", Category: "cli_log",
			Message: truncStr(ln, 480), Source: sshCLISource,
		})
		n++
	}
	return n
}

// --- small text helpers -----------------------------------------------------

func parseSSIDNames(out string) []string {
	var names []string
	seen := map[string]bool{}
	re := regexp.MustCompile(`(?i)\bssid\b\s*[:=]?\s*"?([\w .\-]{1,32})"?`)
	for _, m := range re.FindAllStringSubmatch(out, -1) {
		name := strings.TrimSpace(m[1])
		if name != "" && !seen[name] && !strings.EqualFold(name, "name") {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

func firstField(s string) string {
	for _, f := range strings.Fields(s) {
		return f
	}
	return ""
}

func isMACish(s string) bool { return reMAC.MatchString(s) }

func cliStatusWord(ln string) string {
	low := strings.ToLower(ln)
	switch {
	case strings.Contains(low, "online"), strings.Contains(low, " up"), strings.Contains(low, "active"), strings.Contains(low, "registered"):
		return "online"
	case strings.Contains(low, "offline"), strings.Contains(low, " down"), strings.Contains(low, "inactive"):
		return "offline"
	}
	return "unknown"
}

func countNonEmptyLines(s string) int {
	n := 0
	for _, ln := range strings.Split(s, "\n") {
		if strings.TrimSpace(ln) != "" {
			n++
		}
	}
	return n
}

// --- handlers ----------------------------------------------------------------

type sshCLIReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// runSSHCLICollection handles POST /devices/{id}/collect-ssh-cli (persists).
func (s *Server) runSSHCLICollection(w http.ResponseWriter, r *http.Request) { s.sshCLIHandler(w, r, true) }

// testSSHCLICommands handles POST /devices/{id}/test-ssh-cli (no wireless persistence).
func (s *Server) testSSHCLICommands(w http.ResponseWriter, r *http.Request) { s.sshCLIHandler(w, r, false) }

func (s *Server) sshCLIHandler(w http.ResponseWriter, r *http.Request, persist bool) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	dev, err := s.queries.GetDevice(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	var body sshCLIReq
	_ = decodeJSONOptional(r, &body)
	sum := s.collectSSHCLI(ctx, dev, strings.TrimSpace(body.Username), body.Password, persist)
	action := "wireless.ssh_test"
	if persist {
		action = "wireless.ssh_collect"
	}
	s.audit(r, "inventory", action, "device", id.String(), "Extreme XCC SSH CLI on "+dev.Name,
		map[string]any{"supported": sum.Supported, "unsupported": sum.Unsupported, "roster_exposed": sum.RosterExposed})
	writeJSON(w, http.StatusOK, sum)
}

// listSSHCliResults handles GET /devices/{id}/ssh-cli-results.
func (s *Server) listSSHCliResults(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	rows, err := s.queries.ListSSHCliResults(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}
