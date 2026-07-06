package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/coralsearesorts/hims/internal/credtest"
	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/isapi"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

// nvrChannelRefreshInterval is how often each managed NVR/DVR is re-polled for
// per-channel camera online status.
const nvrChannelRefreshInterval = 10 * time.Minute

// StartNVRChannelMonitor runs the NVR-side camera-health poll on a cadence, in
// the API process (alongside StartMonitoring). Each cycle re-polls every managed
// NVR/DVR for per-channel camera status and raises/resolves a per-camera alert on
// online->offline transitions — so a camera dropping off a recorder is surfaced
// even when the camera was never added to HIMS as its own device. Inert until an
// encryption key is unlocked (the poll needs the recorder's decrypted credential).
func (s *Server) StartNVRChannelMonitor(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = nvrChannelRefreshInterval
	}
	go func() {
		// Delay the first run so it doesn't pile onto startup seeding/collect.
		select {
		case <-ctx.Done():
			return
		case <-time.After(45 * time.Second):
		}
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			if n, err := s.refreshNVRChannels(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("nvr channel monitor: refresh failed", "error", err)
			} else if n > 0 {
				slog.Info("nvr channel monitor: refreshed recorders", "count", n)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

// refreshNVRChannelsNow (POST /cctv/refresh-channels) runs the NVR-side camera
// health poll on demand (the same work the background monitor does on a cadence),
// so an operator can force a refresh + transition-alert evaluation immediately.
func (s *Server) refreshNVRChannelsNow(w http.ResponseWriter, r *http.Request) {
	n, err := s.refreshNVRChannels(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "monitoring", "cctv.refresh_channels", "cctv", "",
		itoa(n)+" recorder(s) re-polled for camera channel status", map[string]any{"recorders": n})
	writeJSON(w, http.StatusOK, map[string]any{"recorders_polled": n})
}

// refreshOneNVRNow (POST /devices/{id}/refresh-nvr-channels) re-polls a single
// recorder for per-channel camera status on demand (the "Refresh from NVR" button
// on the recorder's Camera Health tab), including transition-alert evaluation.
func (s *Server) refreshOneNVRNow(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	d, err := s.queries.GetDevice(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if d.Category != "nvr" && d.Category != "dvr" {
		http.Error(w, "device is not an NVR/DVR recorder", http.StatusBadRequest)
		return
	}
	if _, _, ok := s.cctvCredential(ctx, d); !ok {
		writeJSON(w, http.StatusOK, map[string]any{"polled": false, "reason": "no usable CCTV/ONVIF/HTTP-Basic credential bound to this recorder"})
		return
	}
	polled := s.refreshOneRecorder(ctx, d, s.cameraOfflineRuleID(ctx))
	if polled {
		s.audit(r, "monitoring", "cctv.refresh_nvr_channels", "device", id.String(),
			"Re-polled camera channel status for "+d.Name, nil)
	}
	writeJSON(w, http.StatusOK, map[string]any{"polled": polled})
}

// refreshNVRChannels re-polls every managed NVR/DVR for per-channel camera status.
// Returns the number of recorders successfully polled. Honest no-op when the
// encryption key is locked (can't decrypt recorder credentials).
func (s *Server) refreshNVRChannels(ctx context.Context) (int, error) {
	if s.cipher() == nil {
		return 0, nil // key locked — nothing we can poll
	}
	recorders, err := s.queries.ListRecorderDevices(ctx)
	if err != nil {
		return 0, err
	}
	ruleID := s.cameraOfflineRuleID(ctx) // nil ⇒ rule not seeded yet; still refresh status, just don't alert
	polled := 0
	for _, d := range recorders {
		if ctx.Err() != nil {
			break
		}
		if s.refreshOneRecorder(ctx, d, ruleID) {
			polled++
		}
	}
	return polled, nil
}

// cameraOfflineRuleID returns the id of the seeded "Camera offline (via NVR)"
// alert rule, or nil if it isn't present/enabled yet.
func (s *Server) cameraOfflineRuleID(ctx context.Context) *uuid.UUID {
	rules, err := s.queries.ListAlertRules(ctx)
	if err != nil {
		return nil
	}
	for _, r := range rules {
		if r.Name == "Camera offline (via NVR)" && r.Enabled {
			id := r.ID
			return &id
		}
	}
	return nil
}

// refreshOneRecorder polls one NVR/DVR, updates each channel's stored status +
// freshness, and drives per-camera offline alerts on status transitions. Returns
// true when the recorder answered. Best-effort: a recorder we can't reach/auth is
// left untouched (its channels keep their last-known status + stale last_seen_at).
func (s *Server) refreshOneRecorder(ctx context.Context, d db.Device, ruleID *uuid.UUID) bool {
	user, pass, ok := s.cctvCredential(ctx, d)
	if !ok || d.PrimaryIp == nil || !d.PrimaryIp.IsValid() {
		return false // needs a usable web/ONVIF credential — honest gate, not a failure
	}
	// Snapshot prior per-channel status BEFORE the poll, so we can detect transitions.
	prior := map[int32]string{}
	if existing, lerr := s.queries.ListNVRChannels(ctx, d.ID); lerr == nil {
		for _, c := range existing {
			prior[c.ChannelNo] = c.Status
		}
	}

	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	// nil doer ⇒ isapi.PermissiveClient (insecure-TLS, self-signed recorders).
	nvr, err := isapi.CollectChannelStatus(cctx, d.PrimaryIp.String(), user, pass, nil, s.webCandidateBases(ctx, d))
	if err != nil || !nvr.IsRecorder() {
		return false
	}

	for _, ch := range nvr.Channels {
		status := channelStatus(ch)
		var ipp *netip.Addr
		var camDevID *uuid.UUID
		if a, perr := netip.ParseAddr(strings.TrimSpace(ch.IP)); perr == nil {
			ipp = &a
			if dev, derr := s.queries.LiveDeviceByIP(ctx, &a); derr == nil && dev.ID != d.ID {
				id := dev.ID
				camDevID = &id
			}
		}
		_, _ = s.queries.UpsertNVRChannel(ctx, db.UpsertNVRChannelParams{
			NvrDeviceID: d.ID, ChannelNo: int32(ch.No), CameraName: strPtrOrNil(ch.Name),
			CameraIp: ipp, CameraDeviceID: camDevID, Status: status, Enabled: ch.Enabled,
			Recording: ch.Recording, Resolution: strings.TrimSpace(ch.Resolution),
			DetectReason: ch.OfflineReason(),
		})
		if ruleID != nil {
			s.evaluateChannelAlert(ctx, *ruleID, d, ch, status, prior[int32(ch.No)])
		}
	}
	return true
}

// evaluateChannelAlert raises or resolves the per-camera offline alert for one
// channel, TRANSITION-driven: it opens an alert only when a channel goes from
// online -> offline (so the pre-existing offline channels don't flood on first
// run), and resolves it when the camera comes back online. Unknown status never
// opens or resolves (indeterminate). Disabled channels are ignored (a channel an
// operator turned off is not a fault).
func (s *Server) evaluateChannelAlert(ctx context.Context, ruleID uuid.UUID, nvr db.Device, ch isapi.Channel, status, prior string) {
	fp := fmt.Sprintf("nvr:%s:ch:%d", nvr.ID, ch.No)
	switch channelAlertAction(status, prior, ch.Enabled) {
	case "resolve":
		// Recovered (or steady online) — clear any open alert.
		_ = s.queries.ResolveStateAlertByFingerprint(ctx, db.ResolveStateAlertByFingerprintParams{RuleID: ruleID, Fingerprint: fp})
	case "open":
		// online -> offline transition on an enabled channel: raise once (idempotent).
		_ = s.queries.OpenStateAlertIfAbsent(ctx, db.OpenStateAlertIfAbsentParams{
			RuleID: ruleID, DeviceID: &nvr.ID, Severity: "warning",
			Message:     channelOfflineMessage(nvr, ch),
			Fingerprint: fp,
		})
	}
}

// channelAlertAction is the pure transition decision for the per-camera NVR-side
// alert: "open" only on an online->offline transition of an ENABLED channel (so the
// pre-existing offline fleet doesn't flood on first poll), "resolve" whenever a
// channel is online again, "none" otherwise (steady offline, unknown, disabled).
func channelAlertAction(status, prior string, enabled bool) string {
	switch {
	case status == "online":
		return "resolve"
	case status == "offline" && prior == "online" && enabled:
		return "open"
	default:
		return "none"
	}
}

// channelOfflineMessage is the operator-facing alert headline for an offline camera.
func channelOfflineMessage(nvr db.Device, ch isapi.Channel) string {
	name := strings.TrimSpace(ch.Name)
	if name == "" {
		name = fmt.Sprintf("channel %d", ch.No)
	}
	loc := ""
	if ip := strings.TrimSpace(ch.IP); ip != "" {
		loc = " (" + ip + ")"
	}
	reason := ch.OfflineReason()
	if reason == "" {
		reason = "offline"
	}
	return fmt.Sprintf("Camera %s%s offline on %s (channel %d) — %s", name, loc, nvr.Name, ch.No, reason)
}

// channelStatus maps an ISAPI channel's online flag to the stored status string.
func channelStatus(ch isapi.Channel) string {
	if ch.Online == nil {
		return "unknown"
	}
	if *ch.Online {
		return "online"
	}
	return "offline"
}

// cctvCredential returns the decrypted user/pass of the recorder's usable CCTV web
// credential (the durable cctv_credential_id, else the generic bound credential),
// restricted to ONVIF / HTTP-Basic kinds. ok=false when none is usable — an honest
// gate (no credential ⇒ no NVR-side poll), never a spray.
func (s *Server) cctvCredential(ctx context.Context, d db.Device) (user, pass string, ok bool) {
	cph := s.cipher()
	if cph == nil {
		return "", "", false
	}
	pref := d.CctvCredentialID
	if pref == nil {
		pref = d.CredentialID
	}
	if pref == nil {
		return "", "", false
	}
	c, err := s.queries.GetCredential(ctx, *pref)
	if err != nil {
		return "", "", false
	}
	if c.Kind != string(domain.CredONVIF) && c.Kind != string(domain.CredHTTPBasic) {
		return "", "", false // e.g. drifted to an SNMP bind — can't authenticate a recorder's web API
	}
	plain, err := cph.Open(c.EncryptedBlob, c.KeyID)
	if err != nil {
		return "", "", false
	}
	u, p := credtest.SplitUserPass(string(plain))
	return u, p, true
}
