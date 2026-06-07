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
	"show summary",
	"show status",
	"show inventory",
	"show controller",
	"show adoption",
	"show network",
	"show networks",
	"show ap",
	"show aps",
	"show wireless",
	"show wlan",
	"show ssid",
	"show clients",
	"show stations",
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
	case "show wlan", "show ssid", "show network", "show networks":
		return "ssids"
	case "show clients", "show stations", "show station":
		return "clients"
	case "show summary", "show status", "show controller", "show system", "show adoption":
		return "summary"
	case "show version", "show inventory":
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

// redactOnly masks secret-bearing values WITHOUT length-capping — used for
// parsing, which must see the FULL command output (capping the parse input was
// the bug that truncated 123 APs to ~80 and 358 clients to a handful).
func redactOnly(s string) string {
	return reSecret.ReplaceAllStringFunc(s, func(m string) string {
		if i := strings.IndexAny(m, ":= "); i >= 0 {
			return m[:i+1] + " ****"
		}
		return "****"
	})
}

// redactSecrets masks secret-bearing values and caps output length — used only
// for the STORED preview, never for parsing.
func redactSecrets(s string, max int) string {
	return capStr(redactOnly(s), max)
}

func capStr(s string, max int) string {
	if max > 0 && len(s) > max {
		return s[:max] + "…(truncated)"
	}
	return s
}

// sshCLIResult is the per-command outcome surfaced to the operator.
type sshCLIResult struct {
	Command     string `json:"command"`
	Status      string `json:"status"` // parsed|not_parsed|unsupported|failed|timeout
	ParsedRows  int    `json:"parsed_rows"`
	SkippedRows int    `json:"skipped_rows"`
	LineCount   int    `json:"line_count"`
	Headers     string `json:"headers,omitempty"`
	Warnings    string `json:"warnings,omitempty"`
	Preview     string `json:"output_preview"`
	Error       string `json:"error_message,omitempty"`
}

// parseDiag carries why a parser kept/skipped rows (surfaced to the operator).
type parseDiag struct {
	header   string
	skipped  int
	warnings []string
}

func (d parseDiag) warnStr() string { return strings.Join(d.warnings, "; ") }

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
	// Controller-reported totals + collection classification.
	Status       string         `json:"status"` // complete|partial|summary_only|failed
	APTotal      int            `json:"ap_total"`
	ClientsTotal int            `json:"clients_total"`
	Networks     int            `json:"networks"`
	Switches     int            `json:"switches"`
	ActiveAPs    int            `json:"active_aps"`
	NonActiveAPs int            `json:"non_active_aps"`
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

	var apN, ssidN, cliN, activeAPs, nonActiveAPs int
	var ctrl ctrlSummary
	for _, cmd := range xccCLICommands {
		out, err := ssh.Run(ctx, host, 22, creds, legacy, cmd, 15*time.Second)
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
			// Ran OK. Parse the FULL (redacted, NOT length-capped) output — the
			// 4000-char cap was what truncated the AP/client rosters.
			full := redactOnly(out)
			res.LineCount = countNonEmptyLines(full)
			rows, diag := 0, parseDiag{}
			switch cliParseKind(cmd) {
			case "aps":
				aps, d := parseCLIAPRows(full)
				diag = d
				rows = len(aps)
				if persistWireless {
					apN += s.persistAPs(ctx, dev, aps)
				}
				for _, a := range aps {
					switch a.Status {
					case "online":
						activeAPs++
					case "offline":
						nonActiveAPs++
					}
				}
			case "clients":
				clients, ssids, d := parseCLIClientRows(full)
				diag = d
				rows = len(clients)
				if persistWireless {
					c, sd := s.persistClients(ctx, dev, clients, ssids)
					cliN += c
					ssidN += sd
				} else {
					ssidN += len(ssids)
				}
			case "ssids":
				names, d := parseSSIDNamesDiag(full)
				diag = d
				rows = len(names)
				ctrl.networks = maxInt(ctrl.networks, len(names))
				if persistWireless {
					ssidN += s.persistSSIDNames(ctx, dev, names)
				}
			case "summary":
				parseControllerSummary(full, &ctrl)
				if ctrl.found {
					rows = 1
				}
			case "identity":
				if persistWireless {
					rows = s.persistCLIIdentity(ctx, dev, full)
				} else if reVer.MatchString(full) || reModel.MatchString(full) {
					rows = 1
				}
			case "events":
				if persistWireless {
					rows = s.persistCLIEvents(ctx, dev, full, poll)
				} else {
					rows = countNonEmptyLines(full)
				}
			}
			res.ParsedRows = rows
			res.SkippedRows = diag.skipped
			res.Headers = capStr(diag.header, 300)
			res.Warnings = capStr(diag.warnStr(), 300)
			res.Status = map[bool]string{true: "parsed", false: "not_parsed"}[rows > 0]
			sum.ParsedRows += rows
		}
		if res.Status != "failed" && res.Status != "timeout" {
			res.Preview = redactSecrets(strings.TrimSpace(out), 2000)
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
			LineCount: int32(res.LineCount), Headers: res.Headers, SkippedRows: int32(res.SkippedRows), Warnings: res.Warnings,
		})
		sum.Results = append(sum.Results, res)
	}

	sum.APs, sum.SSIDs, sum.Clients = apN, ssidN, cliN
	sum.RosterExposed = apN > 0 || ssidN > 0 || cliN > 0
	if persistWireless {
		// Prune stale rows of THIS source only — never touch SNMP/API-sourced rows,
		// and only prune SSH rows when we actually parsed some (an empty/failed SSH
		// run must not erase a previously-collected SSH roster).
		if sum.RosterExposed {
			_ = s.queries.DeleteStaleAccessPoints(ctx, db.DeleteStaleAccessPointsParams{ControllerDeviceID: dev.ID, Source: sshCLISource, CollectedAt: poll})
			_ = s.queries.DeleteStaleWirelessSSIDs(ctx, db.DeleteStaleWirelessSSIDsParams{ControllerDeviceID: dev.ID, Source: sshCLISource, CollectedAt: poll})
			_ = s.queries.DeleteStaleWirelessClients(ctx, db.DeleteStaleWirelessClientsParams{ControllerDeviceID: dev.ID, Source: sshCLISource, CollectedAt: poll})
			ven := derefStr(dev.Vendor)
			_, _ = s.queries.UpsertWLANControllerInfo(ctx, db.UpsertWLANControllerInfoParams{
				DeviceID: dev.ID, Vendor: nzPtr(ven), Version: dev.OsVersion, ApCount: int32(apN), ClientCount: int32(cliN),
				Source: sshCLISource, ControllerName: dev.Name, Model: derefStr(dev.Model), SsidCount: int32(ssidN),
			})
		}
	}

	// Controller-reported totals: prefer a summary command's numbers; fall back to
	// the parsed row counts (after the truncation fix these reflect the full roster).
	apTotal := maxInt(ctrl.apTotal, apN)
	clientsTotal := maxInt(ctrl.clients, cliN)
	netCount := maxInt(ctrl.networks, ssidN)
	if activeAPs == 0 && ctrl.activeAPs > 0 {
		activeAPs = ctrl.activeAPs
	}
	if nonActiveAPs == 0 && ctrl.nonActiveAPs > 0 {
		nonActiveAPs = ctrl.nonActiveAPs
	}
	status := collectionStatusFor(sum.Supported, apTotal, clientsTotal, apN, cliN)
	sum.OK = sum.Supported > 0
	sum.Status = status
	sum.APTotal, sum.ClientsTotal, sum.Networks = apTotal, clientsTotal, netCount
	sum.ActiveAPs, sum.NonActiveAPs, sum.Switches = activeAPs, nonActiveAPs, ctrl.switches
	sum.Detail = sshDetail(status, apN, apTotal, cliN, clientsTotal, sum.Supported)
	_ = s.queries.UpsertWirelessControllerSummary(ctx, db.UpsertWirelessControllerSummaryParams{
		DeviceID: dev.ID, SummarySource: sshCLISource,
		NetworksCount: int32(netCount), SwitchesCount: int32(ctrl.switches), ApTotal: int32(apTotal),
		AdoptionPrimary: int32(ctrl.adoptionPrimary), AdoptionBackup: int32(ctrl.adoptionBackup),
		ActiveAps: int32(activeAPs), NonActiveAps: int32(nonActiveAPs), ClientsTotal: int32(clientsTotal),
		ParsedApRows: int32(apN), ParsedClientRows: int32(cliN), ParsedSsidRows: int32(ssidN),
		CollectionStatus: status, Detail: capStr(sum.Detail, 480),
	})
	return sum
}

// ctrlSummary accumulates controller-reported counts across summary commands.
type ctrlSummary struct {
	networks, switches, apTotal, adoptionPrimary, adoptionBackup, activeAPs, nonActiveAPs, clients int
	found                                                                                          bool
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// collectionStatusFor classifies the run: complete (rows ≥ reported), partial
// (rows < reported), summary_only (reported counts but no rows), failed.
func collectionStatusFor(supported, apTotal, clientsTotal, apRows, clientRows int) string {
	if supported == 0 {
		return "failed"
	}
	reported := apTotal > 0 || clientsTotal > 0
	rows := apRows > 0 || clientRows > 0
	switch {
	case reported && !rows:
		return "summary_only"
	case apRows >= apTotal && clientRows >= clientsTotal:
		return "complete"
	default:
		return "partial"
	}
}

func sshDetail(status string, apRows, apTotal, clientRows, clientsTotal, supported int) string {
	switch status {
	case "failed":
		return "SSH connected but none of the probed read-only commands were supported by this controller's CLI."
	case "summary_only":
		return "SSH CLI exposed controller summary counts (APs " + strconv.Itoa(apTotal) + ", clients " + strconv.Itoa(clientsTotal) + ") but no detailed AP/client rows."
	case "complete":
		return "SSH CLI collection complete: " + strconv.Itoa(apRows) + " APs, " + strconv.Itoa(clientRows) + " clients (matches controller summary)."
	default:
		return "SSH CLI PARTIAL: parsed " + strconv.Itoa(apRows) + "/" + strconv.Itoa(apTotal) + " APs and " + strconv.Itoa(clientRows) + "/" + strconv.Itoa(clientsTotal) + " clients. Some rows not exposed/parsed via the supported commands."
	}
}

// --- parsers (tolerant; only persist confidently-identified rows) -----------

// cliAP / cliClient are the pure-parse results (unit-testable, no DB).
type cliAP struct {
	Serial, Name, Model, MAC, IP, Status, Network, Adoption string
	Warn                                                    string
}
type cliClient struct {
	MAC, IP, SSID, AP, Band, Hostname, Username, VLAN string
	RSSI                                              *int32
}

var (
	reCols     = regexp.MustCompile(`\s{2,}`)
	reAPModel  = regexp.MustCompile(`(?i)\bAP\d{3,4}[A-Za-z0-9]*\b`)
	reSerial15 = regexp.MustCompile(`^\d{10,}$`)
	reAPSerial = regexp.MustCompile(`(?i)AP Serial:\s*(\S+)`)
)

func apStatusWord(ln string) string {
	low := strings.ToLower(ln)
	switch {
	case strings.Contains(low, "non-active"), strings.Contains(low, "nonactive"), strings.Contains(low, "inactive"), strings.Contains(low, "offline"), strings.Contains(low, " down"):
		return "offline"
	case strings.Contains(low, "active"), strings.Contains(low, "online"), strings.Contains(low, " up "), strings.Contains(low, "approved"), strings.Contains(low, "registered"):
		return "online"
	}
	return ""
}

// parseCLIAPRows parses Extreme XCC `show ap` output. The observed format is
// "serial <serial> <serial> <model>"; a MAC-table fallback covers other CLIs.
// Returns parse diagnostics (header line, skipped count, warnings).
func parseCLIAPRows(out string) ([]cliAP, parseDiag) {
	var aps []cliAP
	d := parseDiag{}
	for _, ln := range strings.Split(out, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		f := strings.Fields(ln)
		if len(f) >= 2 && strings.EqualFold(f[0], "serial") && reSerial15.MatchString(f[1]) {
			// serial is the unique AP identity (clients reference their AP by
			// serial); the trailing token is the hardware MODEL (e.g. AP305C-1-EG),
			// NOT a per-AP name — naming by it would collapse every AP into one row.
			ap := cliAP{Serial: f[1], Name: f[1], Status: apStatusWord(ln)}
			ap.Model = f[len(f)-1]
			if ap.Model == ap.Serial {
				ap.Model = reAPModel.FindString(ln)
			}
			aps = append(aps, ap)
			continue
		}
		if mac := reMAC.FindString(ln); mac != "" { // MAC-led fallback row
			name := firstField(ln)
			if isMACish(name) {
				name = mac
			}
			aps = append(aps, cliAP{Name: name, MAC: mac, IP: reIPv4.FindString(ln), Model: reAPModel.FindString(ln), Status: apStatusWord(ln)})
			continue
		}
		// A non-empty, non-header line we couldn't turn into an AP row.
		if !looksLikeHeaderOrNoise(t) {
			d.skipped++
		} else if d.header == "" && looksLikeHeaderOrNoise(t) && strings.ContainsAny(t, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
			d.header = t
		}
	}
	if d.skipped > 0 {
		d.warnings = append(d.warnings, strconv.Itoa(d.skipped)+" line(s) had no serial/MAC and were skipped")
	}
	return aps, d
}

func looksLikeHeaderOrNoise(t string) bool {
	low := strings.ToLower(t)
	return strings.Contains(low, "serial") || strings.Contains(low, "name") || strings.Contains(low, "model") ||
		strings.HasPrefix(t, "---") || strings.HasPrefix(t, "==") || strings.HasPrefix(t, "#")
}

// parseCLIClientRows parses the `show clients` column table (header-driven) and
// returns clients + distinct SSIDs + parse diagnostics. The client MAC is the
// FIRST MAC on a row (the BSS MAC appears later) so BSS MACs are not counted as
// clients.
func parseCLIClientRows(out string) ([]cliClient, []string, parseDiag) {
	lines := strings.Split(out, "\n")
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
	d := parseDiag{}
	if headerAt >= 0 {
		d.header = strings.TrimSpace(lines[headerAt])
	}
	get := func(vals []string, keys ...string) string {
		for _, key := range keys {
			if j, ok := colIdx[key]; ok && j < len(vals) {
				if v := strings.TrimSpace(vals[j]); v != "" {
					return v
				}
			}
		}
		return ""
	}
	var clients []cliClient
	ssidSeen := map[string]bool{}
	var ssids []string
	currentAP := ""
	for i, ln := range lines {
		if m := reAPSerial.FindStringSubmatch(ln); m != nil {
			currentAP = m[1]
			continue
		}
		if i <= headerAt {
			continue
		}
		mac := reMAC.FindString(ln)
		if mac == "" {
			continue
		}
		vals := reCols.Split(strings.TrimSpace(ln), -1)
		c := cliClient{MAC: mac, AP: currentAP}
		if headerAt >= 0 {
			c.IP = reIPv4.FindString(get(vals, "client ip", "ip address", "ip"))
			c.SSID = get(vals, "ssid")
			c.Hostname = get(vals, "host name", "hostname", "device name")
			c.Username = get(vals, "user", "user name", "username")
			c.Band = bandFromProto(get(vals, "protocol", "radio", "band"))
			c.VLAN = get(vals, "vlan", "pvid", "network", "topology")
			if r, err := strconv.Atoi(get(vals, "rss(dbm)", "rss", "rssi", "signal")); err == nil {
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
	if headerAt < 0 {
		d.warnings = append(d.warnings, "no client table header detected (Client MAC + SSID); rows parsed by MAC only")
	}
	return clients, ssids, d
}

func parseSSIDNamesDiag(out string) ([]string, parseDiag) {
	names := parseSSIDNames(out)
	return names, parseDiag{}
}

// parseControllerSummary extracts controller-reported counts from a summary
// command's output (labels like "APs: 123", "Active APs 121", "Clients 358").
// Best-effort + tolerant of layout; sets c.found when any count is recognized.
func parseControllerSummary(out string, c *ctrlSummary) {
	set := func(dst *int, n int, ok bool) {
		if ok && n > *dst {
			*dst = n
			c.found = true
		}
	}
	n, ok := grabPair(out, `active\s+aps?`)
	set(&c.activeAPs, n, ok)
	n, ok = grabPairAny(out, `non[\s-]*active\s+aps?`, `in\s*active\s+aps?`)
	set(&c.nonActiveAPs, n, ok)
	n, ok = grabPair(out, `adoption\s+primary`)
	set(&c.adoptionPrimary, n, ok)
	n, ok = grabPair(out, `adoption\s+backup`)
	set(&c.adoptionBackup, n, ok)
	n, ok = grabPair(out, `networks?`)
	set(&c.networks, n, ok)
	n, ok = grabPair(out, `switches?`)
	set(&c.switches, n, ok)
	n, ok = grabPairAny(out, `clients?`, `stations?`)
	set(&c.clients, n, ok)
	// AP total: require a label that is just "APs"/"Access Points"/"Total APs"
	// (so "active APs"/"non active APs" don't shadow it).
	if m := regexp.MustCompile(`(?im)^\s*(?:total\s+)?(?:access\s+points?|aps?)\s*[:=]?\s*(\d+)\b`).FindStringSubmatch(out); m != nil {
		v, _ := strconv.Atoi(m[1])
		set(&c.apTotal, v, true)
	}
}

// grabPair finds "<label> ... <number>" or "<number> ... <label>" (label first
// preferred), case-insensitive, on a single line.
func grabPair(out, label string) (int, bool) {
	if m := regexp.MustCompile(`(?im)` + label + `\s*[:=]?\s*(\d+)\b`).FindStringSubmatch(out); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n, true
	}
	if m := regexp.MustCompile(`(?im)(\d+)\s+` + label).FindStringSubmatch(out); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n, true
	}
	return 0, false
}

func grabPairAny(out string, labels ...string) (int, bool) {
	for _, l := range labels {
		if n, ok := grabPair(out, l); ok {
			return n, true
		}
	}
	return 0, false
}

func (s *Server) persistAPs(ctx context.Context, dev db.Device, aps []cliAP) int {
	n := 0
	for _, ap := range aps {
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
			Status: nz(ap.Status, "unknown"), ClientCount: 0, Serial: ap.Serial, Source: sshCLISource,
		})
		n++
	}
	return n
}

// persistClients persists clients + derived SSIDs; returns (clients, ssids).
func (s *Server) persistClients(ctx context.Context, dev db.Device, clients []cliClient, ssids []string) (int, int) {
	for _, c := range clients {
		_, _ = s.queries.UpsertWirelessClient(ctx, db.UpsertWirelessClientParams{
			ControllerDeviceID: dev.ID, Mac: c.MAC, Ip: c.IP, Hostname: nz(c.Hostname, c.Username),
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

func (s *Server) persistSSIDNames(ctx context.Context, dev db.Device, names []string) int {
	for _, name := range names {
		_, _ = s.queries.UpsertWirelessSSID(ctx, db.UpsertWirelessSSIDParams{
			ControllerDeviceID: dev.ID, Name: name, Status: "unknown", Source: sshCLISource,
		})
	}
	return len(names)
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
