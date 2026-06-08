package api

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/coralsearesorts/hims/internal/ruckuszd"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

const ruckusZDSource = "ruckus_zd_xml"

// ruckusZDDoer builds the HTTP client for a ZoneDirector profile: a cookie jar
// (for the -ejs-session- cookie), NO auto-redirect (so the client can read the
// admin-path 302 and detect expired-session 3xx), and TLS-insecure for the
// self-signed management certificate (verifying when the profile opts in).
func ruckusZDDoer(cfg vpConfig, timeout time.Duration) *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Timeout:       timeout,
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: !cfg.SSLVerify, MinVersion: tls.VersionTLS10}}, //nolint:gosec
	}
}

// collectRuckusZDProfile runs the ZoneDirector Web-XML deep collection and persists
// the rosters with source=ruckus_zd_xml + the profile id. Honest detail (events are
// not exposed by the AJAX interface); never fabricated. Mirrors collectXCCProfile.
func (s *Server) collectRuckusZDProfile(ctx context.Context, p db.VendorConnectionProfile, dev db.Device) (bool, string) {
	user, pass, hasCred := s.vendorProfileSecret(ctx, p)
	cfg := parseVPConfig(p.Config)
	if !hasCred {
		return false, "no usable credential bound to this profile (add the ZoneDirector admin credentials)"
	}
	base := strings.TrimRight(p.TargetUrl, "/")
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	c := ruckuszd.New(base, user, pass, ruckusZDDoer(cfg, 30*time.Second))

	res, err := c.Collect(cctx)
	if err != nil {
		return false, "Ruckus ZoneDirector Web-XML collection failed: " + shortErr(err) + " — Test Connection to re-verify the admin login + CSRF."
	}

	poll := time.Now().UTC()
	pid := p.ID
	ven := "Ruckus Wireless"
	verPtr := nzPtr(res.Version)
	_, _ = s.queries.UpsertWLANControllerInfo(ctx, db.UpsertWLANControllerInfoParams{
		DeviceID: dev.ID, Vendor: &ven, Version: verPtr, ApCount: int32(len(res.APs)), ClientCount: int32(len(res.Stations)),
		Source: ruckusZDSource, ProfileID: &pid, ControllerName: nz(res.Hostname, dev.Name), Model: derefStr(dev.Model),
		Serial: res.Serial, SsidCount: int32(len(res.SSIDs)),
	})
	for _, a := range res.APs {
		name := a.Name
		if name == "" {
			name = a.MAC
		}
		if name == "" {
			continue
		}
		var ip *netip.Addr
		if addr, perr := netip.ParseAddr(a.IP); perr == nil {
			ip = &addr
		}
		_, _ = s.queries.UpsertAccessPoint(ctx, db.UpsertAccessPointParams{
			ControllerDeviceID: dev.ID, Name: name, Mac: nzPtr(a.MAC), Model: nzPtr(a.Model),
			Ip: ip, Status: nz(a.Status, "unknown"), ClientCount: a.ClientCount,
			Serial: a.Serial, Firmware: a.Firmware, Site: a.Site, Source: ruckusZDSource,
		})
	}
	// Derive each SSID's live client count + in-use band from the stations.
	ssidClientN := map[string]int32{}
	ssidBands := map[string]map[string]bool{}
	for _, st := range res.Stations {
		key := strings.ToLower(strings.TrimSpace(st.SSID))
		if key == "" {
			continue
		}
		ssidClientN[key]++
		if b := wirelessBandFromChannel(st.Channel); b != "" {
			if ssidBands[key] == nil {
				ssidBands[key] = map[string]bool{}
			}
			ssidBands[key][b] = true
		}
	}
	for _, ss := range res.SSIDs {
		if ss.Name == "" {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(ss.Name))
		cc, band := ss.ClientCount, ss.Band
		if cc == 0 {
			cc = ssidClientN[key]
		}
		if band == "" {
			band = joinBandSet(ssidBands[key])
		}
		_, _ = s.queries.UpsertWirelessSSID(ctx, db.UpsertWirelessSSIDParams{
			ControllerDeviceID: dev.ID, Name: ss.Name, Status: nz(ss.Status, "unknown"),
			Security: ss.Security, Band: band, Vlan: ss.VLAN, ClientCount: cc, Source: ruckusZDSource,
		})
	}
	for _, st := range res.Stations {
		if st.MAC == "" {
			continue
		}
		_, _ = s.queries.UpsertWirelessClient(ctx, db.UpsertWirelessClientParams{
			ControllerDeviceID: dev.ID, Mac: st.MAC, Ip: st.IP, Hostname: st.Hostname,
			ApName: st.APName, Ssid: st.SSID, Rssi: st.RSSI, Snr: st.SNR,
			RxBytes: st.RxBytes, TxBytes: st.TxBytes, ConnectedSince: st.ConnectedSince,
			Band: st.Band, Source: ruckusZDSource,
		})
	}
	// Prune rows from this source not refreshed this run.
	_ = s.queries.DeleteStaleAccessPoints(ctx, db.DeleteStaleAccessPointsParams{ControllerDeviceID: dev.ID, Source: ruckusZDSource, CollectedAt: poll})
	_ = s.queries.DeleteStaleWirelessSSIDs(ctx, db.DeleteStaleWirelessSSIDsParams{ControllerDeviceID: dev.ID, Source: ruckusZDSource, CollectedAt: poll})
	_ = s.queries.DeleteStaleWirelessClients(ctx, db.DeleteStaleWirelessClientsParams{ControllerDeviceID: dev.ID, Source: ruckusZDSource, CollectedAt: poll})

	if p.CredentialID != nil {
		_ = s.queries.SetDeviceCredential(ctx, db.SetDeviceCredentialParams{ID: dev.ID, CredentialID: p.CredentialID})
	}

	parts := []string{strconv.Itoa(len(res.APs)) + " AP(s)", strconv.Itoa(len(res.SSIDs)) + " SSID(s)", strconv.Itoa(len(res.Stations)) + " client(s)"}
	detail := "Ruckus ZoneDirector collected via Web-XML — " + strings.Join(parts, ", ") +
		"; events not exposed by this ZoneDirector firmware (AJAX) — available via SNMP traps."
	ok := len(res.APs) > 0 || len(res.SSIDs) > 0 || len(res.Stations) > 0
	if !ok {
		detail = "Ruckus ZoneDirector authenticated but the AJAX interface returned no AP/SSID/client rows. " + detail
	}
	return ok, detail
}
