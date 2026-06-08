package ruckuszd

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// AP is one access point from the ZoneDirector stamgr stats.
type AP struct {
	Name        string
	MAC         string
	IP          string
	Model       string
	Serial      string
	Firmware    string
	Status      string // mapped from the numeric `state` code
	ClientCount int32
	Site        string
}

// Station is one associated client (LEVEL=2 — includes traffic counters).
type Station struct {
	MAC      string
	IP       string
	Hostname string
	APName   string
	SSID     string
	Band     string
	RSSI     *int32 // true signal in dBm (received-signal-strength)
	SNR      *int32 // ZD `rssi` field IS the SNR (verified rssi == signal − noise)
	RxBytes  *int64
	TxBytes  *int64
	// ConnectedSince is the association time (first-assoc), pre-formatted local.
	ConnectedSince string
	// Channel drives the SSID in-use-band derivation; not persisted.
	Channel *int32
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

// CollectResult is the deep-collection outcome.
type CollectResult struct {
	Authenticated bool
	AdminBase     string
	Hostname      string
	ManagementIP  string
	Version       string // backfilled from AP firmware (system config exposes none)
	Model         string // Not Available on ZD system config
	Serial        string // Not Available
	APs           []AP
	SSIDs         []SSID
	Stations      []Station
	// EventsExposed is false: the ZD AJAX interface returns zero event rows on
	// this firmware (honest gate — events would require SNMP traps).
	EventsExposed bool
}

// Collect logs in and pulls the AP / client / SSID / system rosters via the
// internal AJAX interface, mapping each per the live-verified desktop connector.
func (c *Client) Collect(ctx context.Context) (CollectResult, error) {
	out := CollectResult{}
	if err := c.Login(ctx); err != nil {
		return out, err
	}
	out.Authenticated = true
	out.AdminBase = c.adminBase

	// APs (live stats: status + per-radio client counts).
	if b, err := c.postAjax(ctx, cmdStat, apStatsXML, true); err == nil {
		for _, m := range apRows(b) {
			out.APs = append(out.APs, AP{
				Name: get(m, "ap-name", "devname", "name", "description"), MAC: get(m, "mac", "ap-mac", "bssid"),
				IP: get(m, "ip", "ipaddr", "ip-addr"), Model: get(m, "model", "ap-model", "hwtype"),
				Serial:      get(m, "serial-number", "serial", "sn"),
				Firmware:    get(m, "firmware-version", "build-version", "version", "ap-fw-version"),
				Status:      apState(get(m, "state", "status", "connection-status")),
				ClientCount: int32(atoiOr(get(m, "num-sta-total", "num-sta", "client-count"))),
				Site:        get(m, "location", "group-id", "ap-group", "zone"),
			})
		}
	} else {
		return out, err
	}

	// Clients (LEVEL=2 traffic counters + dBm signal + SNR + first-assoc).
	if b, err := c.postAjax(ctx, cmdStat, clientXML, true); err == nil {
		for _, m := range attrRows(b, "client") {
			st := Station{
				MAC: get(m, "mac", "sta-mac", "client-mac"), IP: get(m, "ip", "ipaddr", "ip-addr"),
				Hostname: get(m, "hostname", "user", "username", "name"), SSID: get(m, "ssid", "wlan", "wlan-ssid"),
				APName: get(m, "ap-name", "apname", "ap", "devname"), Band: get(m, "radio-type-text", "radio-type", "channel"),
				RSSI:           getIntPtr(m, "received-signal-strength", "signal-strength", "signal"),
				SNR:            ruckusSNR(m),
				RxBytes:        getInt64Ptr(m, "total-rx-bytes", "rx-bytes", "recv-bytes"),
				TxBytes:        getInt64Ptr(m, "total-tx-bytes", "tx-bytes", "sent-bytes"),
				ConnectedSince: formatEpoch(get(m, "first-assoc", "assoc-time", "conn-since")),
				Channel:        getIntPtr(m, "channel"),
			}
			out.Stations = append(out.Stations, st)
		}
	}

	// SSIDs (config — listed = active; no enabled flag).
	if b, err := c.postAjax(ctx, conf, wlanListXML, true); err == nil {
		for _, m := range attrRows(b, "wlansvc") {
			out.SSIDs = append(out.SSIDs, SSID{
				Name: get(m, "name", "ssid", "description"), Status: ssidStatus(m),
				Security: get(m, "authentication", "auth"), Band: get(m, "radio", "wlan-radio"),
				VLAN: get(m, "vlan-id", "vlan", "x-vlan", "vlanid"),
			})
		}
	}

	// System identity (hostname + mgmt IP; version backfilled from AP firmware).
	if b, err := c.postAjax(ctx, conf, systemXML, true); err == nil {
		if rows := attrRows(b, "system"); len(rows) > 0 {
			m := rows[0]
			out.Hostname = get(m, "name", "system-name", "identity", "hostname")
			out.ManagementIP = get(m, "mgmt-ip", "ip", "ipaddr")
		}
		// <identity name=...> / <mgmt-ip ip=...> may be nested child elements.
		if out.Hostname == "" {
			for _, m := range attrRows(b, "identity") {
				if v := get(m, "name"); v != "" {
					out.Hostname = v
					break
				}
			}
		}
		if out.ManagementIP == "" {
			for _, m := range attrRows(b, "mgmt-ip") {
				if v := get(m, "ip"); v != "" {
					out.ManagementIP = v
					break
				}
			}
		}
	}
	out.Version = mostCommonFirmware(out.APs)
	return out, nil
}

// Ping logs in and fetches only the AP roster — a fast connectivity/auth check
// (exercises the full login + CSRF + AJAX path) for the profile Test action.
func (c *Client) Ping(ctx context.Context) (int, error) {
	if err := c.Login(ctx); err != nil {
		return 0, err
	}
	b, err := c.postAjax(ctx, cmdStat, apStatsXML, true)
	if err != nil {
		return 0, err
	}
	return len(apRows(b)), nil
}

// ssidStatus maps a ZD WLAN's admin state. The ZoneDirector wlansvc-list returns
// the CONFIGURED WLANs without a reliable per-WLAN enable flag on this firmware —
// a listed WLAN is an active one — so we report "enabled" and only downgrade when
// an explicit disable flag is present (honest: listed = active, never fabricated).
func ssidStatus(m map[string]string) string {
	switch strings.ToLower(strings.TrimSpace(get(m, "enable", "enabled", "status", "state", "admin-state"))) {
	case "false", "0", "no", "disabled", "disable", "down", "inactive":
		return "disabled"
	default:
		return "enabled"
	}
}

// apState maps the ZD AP `state` code to text (RUCKUS-ZD-WLAN-MIB
// ruckusZDWLANAPStatus). 0/1 confirmed live on ZD 10.x.
func apState(code string) string {
	switch strings.TrimSpace(code) {
	case "0":
		return "Disconnected"
	case "1":
		return "Connected"
	case "2":
		return "Approval Pending"
	case "3":
		return "Upgrading Firmware"
	case "4":
		return "Provisioning"
	case "":
		return ""
	default:
		return "Unknown (" + strings.TrimSpace(code) + ")"
	}
}

// ruckusSNR resolves a client's SNR (dB): a direct snr field, else the ZD `rssi`
// field (which IS the SNR — verified rssi == signal − noise), else signal − noise.
func ruckusSNR(m map[string]string) *int32 {
	if v := getIntPtr(m, "snr"); v != nil {
		return v
	}
	if v := getIntPtr(m, "rssi"); v != nil {
		return v
	}
	sig := getIntPtr(m, "received-signal-strength", "signal-strength")
	noise := getIntPtr(m, "noise-floor")
	if sig != nil && noise != nil {
		n := *sig - *noise
		return &n
	}
	return nil
}

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

// formatEpoch renders an epoch (seconds or ms, possibly float) as local time;
// returns the raw string unchanged when it isn't a positive epoch.
func formatEpoch(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	n, ok := atoi64(raw)
	if !ok {
		if f, err := strconv.ParseFloat(raw, 64); err == nil && f > 0 {
			n = int64(f)
		} else {
			return raw
		}
	}
	if n <= 0 {
		return raw
	}
	var t time.Time
	if n >= 1_000_000_000_000 { // ms
		t = time.UnixMilli(n)
	} else {
		t = time.Unix(n, 0)
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

// --- map[string]string accessors ---

func get(m map[string]string, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func getIntPtr(m map[string]string, keys ...string) *int32 {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if n, ok := atoi(v); ok {
				x := int32(n)
				return &x
			}
		}
	}
	return nil
}

func getInt64Ptr(m map[string]string, keys ...string) *int64 {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if n, ok := atoi64(v); ok {
				return &n
			}
		}
	}
	return nil
}

func atoi(s string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	return n, err == nil
}

func atoi64(s string) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n, err == nil
}

func atoiOr(s string) int {
	n, _ := atoi(s)
	return n
}
