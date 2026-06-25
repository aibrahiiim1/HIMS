package api

import (
	"context"
	"strings"
	"time"

	"github.com/coralsearesorts/hims/internal/aruba"
	"github.com/coralsearesorts/hims/internal/arubacentral"
	"github.com/coralsearesorts/hims/internal/omada"
	"github.com/coralsearesorts/hims/internal/ruckus"
	"github.com/coralsearesorts/hims/internal/unifi"
)

// Per-vendor REST collection source labels — one per driver so each roster row
// records exactly which integration produced it (honest provenance + staleness
// pruning, mirroring the ZD/XCC sources).
const (
	unifiSource        = "unifi_rest"
	omadaSource        = "omada_rest"
	smartzoneSource    = "smartzone_rest"
	arubaSource        = "aruba_rest"
	arubaCentralSource = "aruba_central_rest"
)

// wlanSSID / wlanClient / wlanRadio are the normalized rosters every vendor REST
// collector maps into, so persistence + health are vendor-agnostic.
type wlanSSID struct {
	name     string
	enabled  bool
	security string
	band     string
	vlan     string
}

type wlanClient struct {
	mac, ip, hostname, ssid, apName, band string
	rssi, snr                             *int32
	rx, tx                                *int64
}

type wlanRadio struct {
	apName, radio, band, width string
	channel, power             *int32
	clients                    int32
}

type wlanEvent struct {
	at                          time.Time
	severity, category, message string
}

// wlanResult is the normalized output of a vendor REST collection. version is the
// controller firmware (when an endpoint exposes it) and healthOK records whether
// a controller/API health endpoint answered, so per-capability health is honest.
type wlanResult struct {
	vendor   string
	source   string
	version  string
	healthOK bool
	aps      []wlanAP
	ssids    []wlanSSID
	clients  []wlanClient
	radios   []wlanRadio
	events   []wlanEvent
}

// gatherWirelessRosters logs into the controller and pulls every roster its
// driver implements — APs always, plus SSIDs/clients/radios where the vendor
// client supports them. It NEVER fabricates: a capability the client cannot
// collect simply isn't populated, and recordWirelessHealth marks it honestly.
// Returns (result, ok, detail) — ok=false only on a hard login/AP failure.
func (s *Server) gatherWirelessRosters(ctx context.Context, vendorType, base string, cfg vpConfig, user, pass string) (wlanResult, bool, string) {
	doer := insecureDoer(20 * time.Second)
	switch vendorType {
	case "wireless_unifi":
		res := wlanResult{vendor: "Ubiquiti UniFi", source: unifiSource}
		c := unifi.NewClient(base, nz(cfg.Site, "default"), user, pass, doer)
		if err := c.Login(ctx); err != nil {
			return res, false, "UniFi login failed: " + shortErr(err)
		}
		raw, err := c.ListAPs(ctx)
		if err != nil {
			return res, false, "UniFi AP list failed: " + shortErr(err)
		}
		macName := map[string]string{}
		for _, a := range raw {
			res.aps = append(res.aps, wlanAP{a.Name, a.MAC, a.Model, a.IP, a.Status, a.ClientCount})
			macName[strings.ToLower(a.MAC)] = a.Name
		}
		if ss, e := c.ListSSIDs(ctx); e == nil {
			for _, x := range ss {
				res.ssids = append(res.ssids, wlanSSID{x.Name, x.Enabled, x.Security, x.Band, x.VLAN})
			}
		}
		if cl, e := c.ListClients(ctx); e == nil {
			for _, x := range cl {
				res.clients = append(res.clients, wlanClient{
					mac: x.MAC, ip: x.IP, hostname: x.Hostname, ssid: x.SSID,
					apName: macName[strings.ToLower(x.APMac)], band: x.Band,
					rssi: x.RSSI, snr: x.SNR, rx: x.RxBytes, tx: x.TxBytes,
				})
			}
		}
		if rd, e := c.ListRadios(ctx); e == nil {
			for _, x := range rd {
				res.radios = append(res.radios, wlanRadio{apName: x.APName, radio: x.Radio, band: x.Band, channel: x.Channel, power: x.PowerDbm, clients: x.ClientCount})
			}
		}
		if v, e := c.Version(ctx); e == nil {
			res.version = v
		}
		if hs, e := c.Health(ctx); e == nil && len(hs) > 0 {
			res.healthOK = true
		}
		if ev, e := c.ListEvents(ctx); e == nil {
			for _, x := range ev {
				res.events = append(res.events, wlanEvent{at: time.UnixMilli(x.AtMillis).UTC(), category: x.Key, message: x.Message, severity: x.Subsystem})
			}
		}
		return res, true, ""
	case "wireless_omada":
		res := wlanResult{vendor: "TP-Link Omada", source: omadaSource}
		c := omada.NewClient(base, cfg.ControllerID, nz(cfg.Site, "Default"), user, pass, doer)
		if err := c.Login(ctx); err != nil {
			return res, false, "Omada login failed: " + shortErr(err)
		}
		raw, err := c.ListAPs(ctx)
		if err != nil {
			return res, false, "Omada AP list failed: " + shortErr(err)
		}
		macToName := map[string]string{}
		for _, a := range raw {
			res.aps = append(res.aps, wlanAP{a.Name, a.MAC, a.Model, a.IP, a.Status, a.ClientCount})
			if a.MAC != "" {
				macToName[a.MAC] = a.Name
			}
		}
		if ss, e := c.ListSSIDs(ctx); e == nil {
			for _, x := range ss {
				res.ssids = append(res.ssids, wlanSSID{x.Name, x.Enabled, x.Security, x.Band, x.VLAN})
			}
		}
		if cl, e := c.ListClients(ctx); e == nil {
			for _, x := range cl {
				res.clients = append(res.clients, wlanClient{
					mac: x.MAC, ip: x.IP, hostname: x.Hostname, ssid: x.SSID, apName: x.APName,
					band: x.Band, rssi: x.RSSI, snr: x.SNR, rx: x.RxBytes, tx: x.TxBytes,
				})
			}
		}
		if rd, e := c.ListRadios(ctx, macToName, 200); e == nil {
			for _, x := range rd {
				res.radios = append(res.radios, wlanRadio{apName: x.APName, band: x.Band, width: x.ChannelWidth, channel: x.Channel, power: x.Power, clients: x.Clients})
			}
		}
		if v, e := c.ControllerVersion(ctx); e == nil && v != "" {
			res.version, res.healthOK = v, true // /api/info answering proves controller/API health
		}
		if ev, e := c.ListEvents(ctx); e == nil {
			for _, x := range ev {
				res.events = append(res.events, wlanEvent{at: time.UnixMilli(x.AtMillis).UTC(), severity: x.Level, message: x.Message})
			}
		}
		return res, true, ""
	case "wireless_ruckus":
		res := wlanResult{vendor: "Ruckus", source: smartzoneSource}
		c := ruckus.NewClient(base, cfg.APIBase, user, pass, doer)
		// Not pinned to one API version: ask the version-independent apiInfo for the
		// newest public-API base and use it (operator's api_base stays the fallback).
		if detected, e := c.DetectAPIBase(ctx); e == nil && detected != "" {
			c = ruckus.NewClient(base, detected, user, pass, doer)
		}
		if err := c.Login(ctx); err != nil {
			return res, false, "Ruckus login failed: " + shortErr(err)
		}
		raw, err := c.ListAPs(ctx)
		if err != nil {
			return res, false, "Ruckus AP list failed: " + shortErr(err)
		}
		macName := map[string]string{}
		for _, a := range raw {
			res.aps = append(res.aps, wlanAP{a.Name, a.MAC, a.Model, a.IP, a.Status, a.ClientCount})
			macName[strings.ToLower(a.MAC)] = a.Name
		}
		if ss, e := c.ListSSIDs(ctx); e == nil {
			for _, x := range ss {
				res.ssids = append(res.ssids, wlanSSID{x.Name, x.Enabled, x.Security, "", x.VLAN})
			}
		}
		if cl, e := c.ListClients(ctx); e == nil {
			for _, x := range cl {
				res.clients = append(res.clients, wlanClient{
					mac: x.MAC, ip: x.IP, hostname: x.Hostname, ssid: x.SSID,
					apName: macName[strings.ToLower(x.APMac)], band: x.Band, rssi: x.RSSI,
				})
			}
		}
		if rd, e := c.ListRadios(ctx); e == nil {
			for _, x := range rd {
				res.radios = append(res.radios, wlanRadio{apName: x.APName, band: x.Band, width: x.ChannelWidth, channel: x.Channel, power: x.PowerDbm, clients: x.ClientCount})
			}
		}
		if h, okk, e := c.GetControllerHealth(ctx); e == nil && okk {
			res.healthOK, res.version = true, h.Version
		}
		if ev, e := c.ListEvents(ctx); e == nil {
			for _, x := range ev {
				res.events = append(res.events, wlanEvent{at: time.UnixMilli(x.AtMillis).UTC(), category: x.Category, severity: x.Severity, message: x.Message})
			}
		}
		return res, true, ""
	case "wireless_aruba", "wireless_aruba_os10":
		// ArubaOS 8 on-prem and ArubaOS 10 on-prem (Conductor-led) both expose the
		// AOS8-compatible REST showcommand API, so they share this collector. Cloud-
		// managed AOS10 uses the separate Aruba Central path below.
		res := wlanResult{vendor: "Aruba", source: arubaSource}
		c := aruba.NewClient(base, user, pass, doer)
		if err := c.Login(ctx); err != nil {
			return res, false, "Aruba login failed: " + shortErr(err)
		}
		raw, err := c.ListAPs(ctx)
		if err != nil {
			return res, false, "Aruba AP list failed: " + shortErr(err)
		}
		for _, a := range raw {
			// ArubaOS "show ap database long" exposes no radio MAC; key on name/IP.
			res.aps = append(res.aps, wlanAP{a.Name, "", a.Model, a.IP, a.Status, 0})
		}
		// ArubaOS exposes no SSID roster directly; derive the live SSID set from the
		// associated clients (honest — only SSIDs actually in use are reported).
		ssidSeen := map[string]bool{}
		if cl, e := c.ListClients(ctx); e == nil {
			for _, x := range cl {
				res.clients = append(res.clients, wlanClient{
					mac: x.MAC, ip: x.IP, hostname: x.Hostname, ssid: x.SSID, apName: x.APName, band: x.Band,
				})
				if x.SSID != "" && !ssidSeen[x.SSID] {
					ssidSeen[x.SSID] = true
					res.ssids = append(res.ssids, wlanSSID{name: x.SSID, enabled: true})
				}
			}
		}
		if rd, e := c.ListRadios(ctx); e == nil {
			for _, x := range rd {
				res.radios = append(res.radios, wlanRadio{apName: x.APName, band: x.Band, channel: x.Channel})
			}
		}
		if v, e := c.Version(ctx); e == nil && v != "" {
			res.version, res.healthOK = v, true // controller answered show version → healthy
		}
		return res, true, ""
	case "wireless_aruba_central":
		// Aruba Central cloud (ArubaOS 10 cloud-managed). The OAuth2 bearer token is
		// the credential (carried in the password; username ignored). The regional
		// API gateway URL is the api_base config when set, else the target URL.
		res := wlanResult{vendor: "Aruba Central", source: arubaCentralSource}
		token := pass
		if token == "" {
			token = user
		}
		gw := centralGateway(cfg.APIBase, base)
		c := arubacentral.NewClient(gw, token, doer)
		raw, err := c.ListAPs(ctx)
		if err != nil {
			return res, false, "Aruba Central AP list failed: " + shortErr(err)
		}
		for _, a := range raw {
			res.aps = append(res.aps, wlanAP{a.Name, a.MAC, a.Model, a.IP, a.Status, 0})
		}
		// Firmware = the fleet's most-common AP version; APs answering proves the
		// token + gateway are healthy.
		if v := arubacentral.MostCommonFirmware(raw); v != "" {
			res.version = v
		}
		res.healthOK = len(raw) > 0
		if ss, e := c.ListSSIDs(ctx); e == nil {
			for _, x := range ss {
				res.ssids = append(res.ssids, wlanSSID{x.Name, x.Enabled, x.Security, x.Band, ""})
			}
		}
		if cl, e := c.ListClients(ctx); e == nil {
			for _, x := range cl {
				res.clients = append(res.clients, wlanClient{
					mac: x.MAC, ip: x.IP, hostname: x.Hostname, ssid: x.SSID, apName: x.APName, band: x.Band, rssi: x.RSSI,
				})
			}
		}
		if rd, e := c.ListRadios(ctx); e == nil {
			for _, x := range rd {
				res.radios = append(res.radios, wlanRadio{apName: x.APName, band: x.Band, width: x.ChannelWidth, channel: x.Channel, power: x.Power, clients: x.Clients})
			}
		}
		if ev, e := c.ListEvents(ctx); e == nil {
			for _, x := range ev {
				res.events = append(res.events, wlanEvent{at: time.UnixMilli(x.AtMillis).UTC(), severity: x.Severity, message: x.Message})
			}
		}
		return res, true, ""
	}
	return wlanResult{}, false, "unsupported wireless vendor_type: " + vendorType
}

// centralGateway resolves the Aruba Central regional API-gateway base URL: the
// operator-supplied api_base when set (normalized to https://…), else the
// profile target URL.
func centralGateway(apiBase, target string) string {
	g := strings.TrimSpace(apiBase)
	if g == "" {
		return target
	}
	if !strings.HasPrefix(g, "http://") && !strings.HasPrefix(g, "https://") {
		g = "https://" + g
	}
	return strings.TrimRight(g, "/")
}
