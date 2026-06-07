package extremexcc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// AP is one access point from the XCC API (flexibly parsed).
type AP struct {
	Name        string
	MAC         string
	IP          string
	Model       string
	Serial      string
	Firmware    string
	Status      string
	ClientCount int32
	Site        string
	Uptime      string
}

// SSID is one WLAN service.
type SSID struct {
	Name        string
	Status      string
	Security    string
	Band        string
	VLAN        string
	ClientCount int32
}

// Station is one associated client.
type Station struct {
	MAC      string
	IP       string
	Hostname string
	APName   string
	SSID     string
	RSSI     *int32 // signal in dBm (rss)
	SNR      *int32 // computed: rss − AP-radio noise floor (no direct SNR field)
	Band     string
	RxBytes  *int64
	TxBytes  *int64
	// ConnectedSince is Not Available on this firmware (station record carries
	// only lastSeen, never an association/connect time).
	ConnectedSince string
	// Channel drives the SSID in-use-band derivation (apply layer); not persisted.
	Channel *int32
}

// Event is one controller log event (platformmanager logging endpoint).
type Event struct {
	At       time.Time
	Severity string
	Category string
	Message  string
}

// EndpointOutcome records what happened for one roster endpoint (for honest UI).
type EndpointOutcome struct {
	Kind   string `json:"kind"`   // aps | ssids | clients
	Path   string `json:"path"`
	Status int    `json:"status"`
	Count  int    `json:"count"`
	Note   string `json:"note,omitempty"` // e.g. "not exposed by this firmware"
}

// CollectResult is the deep-collection outcome. Partial is true when some
// rosters were not exposed; the caller renders honest empty states for those.
type CollectResult struct {
	Authenticated bool
	AuthMethod    string
	Version       string
	Serial        string
	Model         string
	APs           []AP
	SSIDs         []SSID
	Stations      []Station
	Events        []Event
	Endpoints     []EndpointOutcome
	Partial       bool
}

// Collect authenticates and pulls the AP / SSID / client rosters from the
// (discovered) API base. Endpoints the firmware doesn't expose are recorded as
// outcomes with a note rather than failing the whole collection. Never fabricates.
func (c *Client) Collect(ctx context.Context) (CollectResult, error) {
	out := CollectResult{}
	out.AuthMethod, out.Authenticated = c.tryAuth(ctx)
	if !out.Authenticated {
		return out, errNoAuth
	}
	base := "/" + strings.Trim(c.APIBase, "/")
	if base == "/" {
		// No confirmed base — try the candidates and use the first JSON 200 root.
		for _, root := range candidateRoots {
			if p, ok := c.probe(ctx, http.MethodGet, root); ok && p.Status == 200 && p.JSON {
				base = root
				break
			}
		}
	}

	// APs — the runtime query view first: it carries the real operational `status`
	// (InService/critical/OutOfService …) + per-radio noise for client SNR, which
	// the plain /aps list omits. On BridgedAtAp deployments only …/query is rich.
	radioNoise := map[string]int32{} // "serial|channel" -> noise floor (dBm)
	apRows, apOut := c.fetch(ctx, "aps", base, []string{"/aps/query", "/aps", "/devices"})
	out.Endpoints = append(out.Endpoints, apOut)
	for _, m := range apRows {
		ap := AP{
			Name: pick(m, "apName", "name", "hostname", "deviceName"), MAC: pick(m, "macAddress", "mac", "baseMac", "apMac"),
			IP: pick(m, "ipAddress", "ipAddr", "ip", "mgmtIp"), Model: pick(m, "platformName", "hardwareType", "model", "productType"),
			Serial: pick(m, "serialNumber", "serial", "sn"), Firmware: pick(m, "softwareVersion", "swVersion", "firmware", "version"),
			Status:      prettyAPStatus(pick(m, "status", "apStatus", "operationalStatus")),
			ClientCount: pickInt(m, "clientCount", "numClients", "stationCount", "associatedClients"),
			Site:        pick(m, "hostSite", "siteName", "site", "zone", "location"),
			Uptime:      pick(m, "sysUptime", "uptime", "upTime"),
		}
		indexRadioNoise(m, ap.Serial, radioNoise)
		out.APs = append(out.APs, ap)
	}

	// SSIDs / WLAN services (config — no live client-count/band; derived in apply).
	ssidRows, ssidOut := c.fetch(ctx, "ssids", base, []string{"/services", "/wlans", "/ssids"})
	out.Endpoints = append(out.Endpoints, ssidOut)
	for _, m := range ssidRows {
		out.SSIDs = append(out.SSIDs, SSID{
			Name: pick(m, "ssid", "serviceName", "name", "ssidName"), Status: normEnabled(m, "enabled", "status", "adminState", "active"),
			Security: readPrivacy(m), Band: pick(m, "band", "radio", "allowedRadios"),
			VLAN: pick(m, "vlanId", "vlan", "defaultVlan", "dot1dPortNumber"), ClientCount: pickInt(m, "clientCount", "numClients", "stationCount"),
		})
	}

	// Clients / stations — …/query first (the plain /stations is empty under
	// BridgedAtAp forwarding). SNR is computed from the AP radio noise floor.
	stRows, stOut := c.fetch(ctx, "clients", base, []string{"/stations/query", "/stations", "/clients"})
	out.Endpoints = append(out.Endpoints, stOut)
	for _, m := range stRows {
		st := Station{
			MAC: pick(m, "macAddress", "mac", "stationMac"), IP: pick(m, "ipAddress", "ipAddr", "ip", "ipv4"),
			Hostname: pick(m, "dhcpHostName", "hostname", "userName", "deviceType", "deviceFamily", "name"),
			APName:   pick(m, "accessPointName", "accessPointSerialNumber", "apName", "apSerialNumber"),
			SSID:     pick(m, "serviceName", "ssid", "ssidName", "wlan"), Band: pick(m, "protocol", "channel", "band", "radioBand"),
		}
		if v := pickIntPtr(m, "rss", "rssi", "signal", "signalStrength"); v != nil {
			st.RSSI = v
		}
		st.Channel = pickIntPtr(m, "channel")
		st.RxBytes = pickInt64Ptr(m, "inBytes", "rxBytes", "bytesReceived")
		st.TxBytes = pickInt64Ptr(m, "outBytes", "txBytes", "bytesSent")
		if snr, ok := computeSNR(m, st.RSSI, radioNoise); ok {
			st.SNR = &snr
		}
		// ConnectedSince: Not Available (station record exposes only lastSeen).
		out.Stations = append(out.Stations, st)
	}

	// Events — separate root (platformmanager); requires an explicit epoch-ms range
	// (omitting it → 422). Best-effort: a failure here doesn't fail the collection.
	if evs, ok := c.fetchEvents(ctx); ok {
		out.Events = evs
	}

	// Identity: controller Version ← most-common AP firmware (no version endpoint);
	// Serial ← JWT `iss` suffix when present. Model/Uptime stay Not Available.
	out.Version = mostCommonFirmware(out.APs)
	if iss := decodeJWTIss(c.bearer); iss != "" {
		if dot := strings.IndexByte(iss, '.'); dot > 0 {
			out.Serial = iss[dot+1:]
		}
	}

	for _, e := range out.Endpoints {
		if e.Status != 200 {
			out.Partial = true
		}
	}
	return out, nil
}

// fetchEvents pulls the last 24h of controller events from the platformmanager
// logging endpoint (absolute path, not under the inventory API base).
func (c *Client) fetchEvents(ctx context.Context) ([]Event, bool) {
	endMs := time.Now().UTC().UnixMilli()
	startMs := time.Now().UTC().Add(-24 * time.Hour).UnixMilli()
	path := "/platformmanager/v1/logging/events?startTime=" + atoiStr(startMs) + "&endTime=" + atoiStr(endMs)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, false
	}
	req.Header.Set("Accept", "application/json")
	c.applyAuth(req)
	resp, err := c.Doer.Do(req)
	if err != nil {
		return nil, false
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, false
	}
	var evs []Event
	for _, m := range extractRows(raw) {
		evs = append(evs, Event{
			At:       parseEpochMillis(m["timestamp"]),
			Severity: pick(m, "severity", "level", "logLevel", "type"),
			Category: pick(m, "component", "category", "eventType", "module"),
			Message:  pick(m, "description", "message", "text", "event", "msg", "detail"),
		})
	}
	return evs, true
}

// fetch tries the given paths under base until one returns JSON 200, returning
// the parsed rows + an outcome. A 404/401 on all paths yields an honest note.
func (c *Client) fetch(ctx context.Context, kind, base string, paths []string) ([]map[string]any, EndpointOutcome) {
	last := EndpointOutcome{Kind: kind, Note: "not exposed by this firmware/API"}
	for _, sp := range paths {
		full := base + sp
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+full, nil)
		if err != nil {
			continue
		}
		req.Header.Set("Accept", "application/json")
		switch c.loginMethod {
		case "bearer", "token":
			if c.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+c.bearer)
			}
		case "basic":
			req.SetBasicAuth(c.Username, c.Password)
		}
		resp, err := c.Doer.Do(req)
		if err != nil {
			last = EndpointOutcome{Kind: kind, Path: sp, Note: shortErr(err)}
			continue
		}
		ct := resp.Header.Get("Content-Type")
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		oc := EndpointOutcome{Kind: kind, Path: full, Status: resp.StatusCode}
		if resp.StatusCode == 200 && strings.Contains(strings.ToLower(ct), "json") {
			rows := extractRows(raw)
			oc.Count = len(rows)
			return rows, oc
		}
		if resp.StatusCode == 404 {
			oc.Note = "not exposed by this firmware/API"
		} else if resp.StatusCode == 401 || resp.StatusCode == 403 {
			oc.Note = "authorization required (credential lacks API access)"
		}
		last = oc
	}
	return nil, last
}

// extractRows pulls an array of objects out of a response that may be a bare
// array or a wrapper object ({data:[…]}, {items:[…]}, {<kind>:[…]}).
func extractRows(raw []byte) []map[string]any {
	var arr []map[string]any
	if json.Unmarshal(raw, &arr) == nil && len(arr) > 0 {
		return arr
	}
	var obj map[string]any
	if json.Unmarshal(raw, &obj) != nil {
		return nil
	}
	for _, key := range []string{"data", "items", "result", "results", "aps", "devices", "stations", "clients", "services", "wlans"} {
		if v, ok := obj[key]; ok {
			if rows := toRows(v); len(rows) > 0 {
				return rows
			}
		}
	}
	return nil
}

func toRows(v any) []map[string]any {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(arr))
	for _, e := range arr {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// pick returns the first non-empty string value among the given keys.
func pick(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case string:
				if t != "" {
					return t
				}
			case float64:
				return trimFloat(t)
			case bool:
				if t {
					return "true"
				}
			}
		}
	}
	return ""
}

func pickInt(m map[string]any, keys ...string) int32 {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case float64:
				return int32(t)
			case string:
				return int32(atoiSafe(t))
			}
		}
	}
	return 0
}

// pickIntPtr returns a pointer to the first present numeric value (nil if absent),
// so a legitimate 0/negative (e.g. RSSI dBm) is distinguishable from "not reported".
func pickIntPtr(m map[string]any, keys ...string) *int32 {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case float64:
				n := int32(t)
				return &n
			case string:
				if t != "" {
					n := int32(atoiSafe(t))
					return &n
				}
			}
		}
	}
	return nil
}

func pickInt64Ptr(m map[string]any, keys ...string) *int64 {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case float64:
				n := int64(t)
				return &n
			case string:
				if t != "" {
					n := atoiSafe(t)
					return &n
				}
			}
		}
	}
	return nil
}

// applyAuth sets the bearer/basic header per the established login method.
func (c *Client) applyAuth(req *http.Request) {
	switch c.loginMethod {
	case "bearer", "token":
		if c.bearer != "" {
			req.Header.Set("Authorization", "Bearer "+c.bearer)
		}
	case "basic":
		if c.Username != "" {
			req.SetBasicAuth(c.Username, c.Password)
		}
	}
}

// prettyAPStatus maps the controller's operational AP status to a stable label,
// keeping critical/major/minor verbatim (never collapsing to "unknown") and never
// deriving status from proxied/adoptedBy. Mirrors the desktop PrettyApStatus.
func prettyAPStatus(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return "unknown"
	case "inservice", "in-service", "in service":
		return "In Service"
	case "outofservice", "out-of-service", "out of service":
		return "Out of Service"
	case "critical":
		return "Critical"
	case "major":
		return "Major"
	case "minor":
		return "Minor"
	case "disconnected":
		return "Disconnected"
	case "connected":
		return "Connected"
	default:
		return raw
	}
}

// indexRadioNoise records each radio's noise floor keyed by "serial|channel" for
// later client-SNR computation (the station record carries no SNR field).
func indexRadioNoise(m map[string]any, serial string, dst map[string]int32) {
	if serial == "" {
		return
	}
	radios, ok := m["radios"].([]any)
	if !ok {
		return
	}
	for _, r := range radios {
		rm, ok := r.(map[string]any)
		if !ok {
			continue
		}
		ch := pickIntPtr(rm, "opChannel", "channel")
		noise := pickIntPtr(rm, "noise", "noiseFloor")
		if ch != nil && noise != nil {
			dst[serial+"|"+atoiStr(int64(*ch))] = *noise
		}
	}
}

// computeSNR derives client SNR (dB) = RSS (dBm) − the AP radio's noise floor. Uses
// a direct snr field when present, else the per-radio noise indexed by serial|channel.
func computeSNR(m map[string]any, rss *int32, radioNoise map[string]int32) (int32, bool) {
	if v := pickIntPtr(m, "snr"); v != nil {
		return *v, true
	}
	serial := pick(m, "accessPointSerialNumber", "accessPointName")
	ch := pickIntPtr(m, "channel")
	if rss != nil && serial != "" && ch != nil {
		if noise, ok := radioNoise[serial+"|"+atoiStr(int64(*ch))]; ok {
			return *rss - noise, true
		}
	}
	return 0, false
}

// readPrivacy renders the SSID security TYPE from the nested `privacy` object's key
// (+ its mode) WITHOUT surfacing the preshared key; falls back to flat fields.
func readPrivacy(m map[string]any) string {
	if priv, ok := m["privacy"].(map[string]any); ok {
		for name, val := range priv {
			sec := prettySecurity(name)
			if obj, ok := val.(map[string]any); ok {
				if mode, ok := obj["mode"].(string); ok && mode != "" {
					return sec + " (" + mode + ")"
				}
			}
			return sec
		}
	}
	return pick(m, "security", "authType", "authentication", "securityMode")
}

func prettySecurity(raw string) string {
	key := strings.ReplaceAll(raw, "Element", "")
	switch key {
	case "WpaPsk", "WpaPsk2", "Wpa2Psk":
		return "WPA/WPA2 PSK"
	case "Wpa3Psk", "Wpa3":
		return "WPA3"
	case "Wpa2", "Wpa2Enterprise", "Dot1x":
		return "WPA2 Enterprise (802.1X)"
	case "Wep":
		return "WEP"
	case "None", "Open", "":
		return "Open"
	default:
		return key
	}
}

// mostCommonFirmware returns the predominant AP softwareVersion (the controller
// shares the AP release; no controller version endpoint exists on this firmware).
func mostCommonFirmware(aps []AP) string {
	counts := map[string]int{}
	best, bestN := "", 0
	for _, a := range aps {
		if a.Firmware == "" {
			continue
		}
		counts[a.Firmware]++
		if counts[a.Firmware] > bestN {
			best, bestN = a.Firmware, counts[a.Firmware]
		}
	}
	return best
}

// decodeJWTIss returns the `iss` claim of a JWT bearer (base64url payload), or "".
func decodeJWTIss(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		s := strings.NewReplacer("-", "+", "_", "/").Replace(parts[1])
		for len(s)%4 != 0 {
			s += "="
		}
		if payload, err = base64.StdEncoding.DecodeString(s); err != nil {
			return ""
		}
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	if iss, ok := claims["iss"].(string); ok {
		return iss
	}
	return ""
}

// parseEpochMillis converts an epoch-ms (or -s) timestamp to time; zero on failure.
func parseEpochMillis(v any) time.Time {
	var n int64
	switch t := v.(type) {
	case float64:
		n = int64(t)
	case string:
		n = atoiSafe(t)
	}
	if n <= 0 {
		return time.Time{}
	}
	if n < 1_000_000_000_000 { // epoch seconds, not ms
		return time.Unix(n, 0).UTC()
	}
	return time.UnixMilli(n).UTC()
}

func normStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "up", "online", "connected", "active", "inservice", "in-service", "true":
		return "online"
	case "down", "offline", "disconnected", "inactive", "false":
		return "offline"
	case "":
		return "unknown"
	default:
		return "unknown"
	}
}

func normEnabled(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case bool:
				if t {
					return "enabled"
				}
				return "disabled"
			case string:
				switch strings.ToLower(t) {
				case "enabled", "true", "up", "active":
					return "enabled"
				case "disabled", "false", "down", "inactive":
					return "disabled"
				}
			}
		}
	}
	return "unknown"
}

func trimFloat(f float64) string {
	if f == float64(int64(f)) {
		return atoiStr(int64(f))
	}
	return ""
}

func atoiStr(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [24]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func atoiSafe(s string) int64 {
	var n int64
	neg := false
	s = strings.TrimSpace(s)
	for i, ch := range s {
		if i == 0 && ch == '-' {
			neg = true
			continue
		}
		if ch < '0' || ch > '9' {
			break
		}
		n = n*10 + int64(ch-'0')
	}
	if neg {
		return -n
	}
	return n
}
