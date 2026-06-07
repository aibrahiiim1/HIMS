package extremexcc

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
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
	RSSI     *int32
	Band     string
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

	// APs.
	apRows, apOut := c.fetch(ctx, "aps", base, []string{"/aps", "/devices"})
	out.Endpoints = append(out.Endpoints, apOut)
	for _, m := range apRows {
		out.APs = append(out.APs, AP{
			Name: pick(m, "name", "hostname", "apName"), MAC: pick(m, "macAddress", "mac", "baseMac"),
			IP: pick(m, "ipAddress", "ip", "mgtIp"), Model: pick(m, "model", "hardwareType", "productType"),
			Serial: pick(m, "serialNumber", "serial"), Firmware: pick(m, "softwareVersion", "firmware", "version"),
			Status: normStatus(pick(m, "status", "state", "connectionState", "adminState")),
			ClientCount: pickInt(m, "clientCount", "numClients", "stationCount"),
		})
	}

	// SSIDs / WLAN services.
	ssidRows, ssidOut := c.fetch(ctx, "ssids", base, []string{"/services", "/wlans", "/ssids"})
	out.Endpoints = append(out.Endpoints, ssidOut)
	for _, m := range ssidRows {
		out.SSIDs = append(out.SSIDs, SSID{
			Name: pick(m, "ssid", "name", "serviceName"), Status: normEnabled(m, "enabled", "status", "state"),
			Security: pick(m, "security", "privacy", "authType"), Band: pick(m, "band", "radioBand"),
			VLAN: pick(m, "vlan", "vlanId"), ClientCount: pickInt(m, "clientCount", "numClients", "stationCount"),
		})
	}

	// Clients / stations.
	stRows, stOut := c.fetch(ctx, "clients", base, []string{"/stations", "/clients"})
	out.Endpoints = append(out.Endpoints, stOut)
	for _, m := range stRows {
		st := Station{
			MAC: pick(m, "macAddress", "mac"), IP: pick(m, "ipAddress", "ip"),
			Hostname: pick(m, "hostname", "deviceName", "name"), APName: pick(m, "apName", "ap", "apSerialNumber"),
			SSID: pick(m, "ssid", "serviceName"), Band: pick(m, "band", "radioBand"),
		}
		if v := pickInt(m, "rss", "rssi", "signal"); v != 0 {
			st.RSSI = &v
		}
		out.Stations = append(out.Stations, st)
	}

	for _, e := range out.Endpoints {
		if e.Status != 200 {
			out.Partial = true
		}
	}
	return out, nil
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
