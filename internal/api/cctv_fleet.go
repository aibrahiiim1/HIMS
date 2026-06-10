package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// Fleet-wide CCTV collection (CCTV Phase 2). Applies the strict bound-credential-
// only collector across every camera/NVR/DVR device, classifying each device and
// fetching recorder inventory where applicable. Because the per-device collector
// never sprays credentials, running it against the whole fleet cannot trigger a
// Hikvision lockout (at most one bound-credential attempt per device).
//
// The run is tracked in memory (ephemeral) and exposed as a start (POST) +
// progress/summary (GET) pair so the operator can launch it and watch results.

// cctvFleetItem is one device's outcome in a fleet-wide CCTV collection.
type cctvFleetItem struct {
	DeviceID    string `json:"device_id"`
	Name        string `json:"name"`
	IP          string `json:"ip"`
	WasCategory string `json:"was_category"`
	NowCategory string `json:"now_category"`
	Status      string `json:"status"`  // collected | failed
	Outcome     string `json:"outcome"` // collected | auth | lockout | unsupported | unreachable | no_credential | error
	Detail      string `json:"detail"`
	Channels    int    `json:"channels"`
	Storage     int    `json:"storage"`
}

// cctvFleetRun is the summary of one fleet-wide collection. Each progress update
// stores a fresh immutable copy via atomic.Pointer, so the GET handler reads a
// consistent snapshot without locking.
type cctvFleetRun struct {
	Running    bool            `json:"running"`
	StartedAt  string          `json:"started_at"`
	FinishedAt string          `json:"finished_at,omitempty"`
	Total      int             `json:"total"`
	Done       int             `json:"done"`
	Collected  int             `json:"collected"`
	Failed     int             `json:"failed"`
	Skipped    int             `json:"skipped"`  // skipped to avoid re-hammering a recently auth-failed device
	NVRs       int             `json:"nvrs"`     // recorders classified nvr this run
	DVRs       int             `json:"dvrs"`     // recorders classified dvr this run
	Cameras    int             `json:"cameras"`  // standalone cameras collected this run
	Channels   int             `json:"channels"` // total camera channels across collected recorders
	Items      []cctvFleetItem `json:"items"`
}

func fleetNow() string { return time.Now().UTC().Format(time.RFC3339) }

// cctvAuthSkipWindow is how long the FLEET collector leaves a device alone after
// it auth-failed. Long enough that repeated fleet runs within one operating
// session never re-hammer a device whose bound credential is wrong (the path to
// a Hikvision IP lockout); short enough that a daily run still re-checks. The
// single-device manual Collect ignores this — operator intent overrides the
// guard (e.g. immediately after rebinding the credential).
const cctvAuthSkipWindow = 6 * time.Hour

// shouldSkipCCTV reports whether a device's most recent ONVIF/ISAPI credential
// test was an auth rejection within the window, meaning a fresh fleet attempt
// would only add another failed login. Pure, for testability.
func shouldSkipCCTV(category string, success bool, testedAt, now time.Time, window time.Duration) bool {
	return category == "auth_failed" && !success && now.Sub(testedAt) < window
}

// fleetSkipItem returns a "skipped" row (and true) when the device recently
// auth-failed over ONVIF/ISAPI, so the fleet run reports it honestly without
// re-attempting. A device that was never tested (no row) is always attempted.
func (s *Server) fleetSkipItem(ctx context.Context, d db.Device) (cctvFleetItem, bool) {
	last, err := s.queries.LatestCCTVCredTest(ctx, d.ID)
	if err != nil {
		return cctvFleetItem{}, false // never tested (or lookup failed) → safe to attempt
	}
	if !shouldSkipCCTV(last.Category, last.Success, last.TestedAt, time.Now(), cctvAuthSkipWindow) {
		return cctvFleetItem{}, false
	}
	ip := ""
	if d.PrimaryIp != nil && d.PrimaryIp.IsValid() {
		ip = d.PrimaryIp.String()
	}
	return cctvFleetItem{
		DeviceID: d.ID.String(), Name: d.Name, IP: ip,
		WasCategory: d.Category, NowCategory: d.Category,
		Status: "skipped", Outcome: "skipped",
		Detail: "skipped — credential was rejected within the last 6h; rebind the device's web login, then collect (avoids a Hikvision IP lockout)",
	}, true
}

// collectCCTVFleet handles POST /cctv/collect-fleet — start a fleet-wide CCTV
// collection across all camera/NVR/DVR devices (bound-credential-only, no spray).
func (s *Server) collectCCTVFleet(w http.ResponseWriter, r *http.Request) {
	if cur := s.cctvFleet.Load(); cur != nil && cur.Running {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "a fleet CCTV collection is already running", "summary": cur})
		return
	}
	ctx := r.Context()
	var devices []db.Device
	for _, c := range []string{string(domain.CatNVR), string(domain.CatDVR), string(domain.CatCamera)} {
		if ds, err := s.queries.ListDevicesByCategory(ctx, c); err == nil {
			devices = append(devices, ds...)
		}
	}
	s.cctvFleet.Store(&cctvFleetRun{Running: true, StartedAt: fleetNow(), Total: len(devices), Items: []cctvFleetItem{}})
	s.audit(r, "inventory", "device.collect_cctv_fleet", "fleet", "",
		fmt.Sprintf("Started fleet CCTV collection across %d device(s)", len(devices)), map[string]any{"total": len(devices)})
	go s.runCCTVFleet(context.Background(), devices)
	writeJSON(w, http.StatusAccepted, map[string]any{"started": true, "total": len(devices)})
}

// cctvSummary handles GET /cctv/summary — the CCTV inventory breakdown the
// dashboard/inventory uses: NVRs vs DVRs vs standalone cameras (device counts),
// plus camera channels collected from recorders. Channels are reported SEPARATELY
// and never folded into the device count (they are recorder-owned rows, not
// inventory devices).
func (s *Server) cctvSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	count := func(cat string) int {
		if ds, err := s.queries.ListDevicesByCategory(ctx, cat); err == nil {
			return len(ds)
		}
		return 0
	}
	nvrs := count(string(domain.CatNVR))
	dvrs := count(string(domain.CatDVR))
	cams := count(string(domain.CatCamera))
	channels, _ := s.queries.CountNVRChannels(ctx)
	linked, _ := s.queries.CountLinkedNVRChannels(ctx)
	writeJSON(w, http.StatusOK, map[string]any{
		"nvrs":            nvrs,
		"dvrs":            dvrs,
		"cameras":         cams,
		"recorders":       nvrs + dvrs,
		"devices_total":   nvrs + dvrs + cams, // NVRs + DVRs + standalone cameras (channels excluded)
		"channels":        channels,           // camera channels inside recorders (not devices)
		"channels_linked": linked,             // channels matched to a standalone camera device
	})
}

// relinkCCTVChannels handles POST /cctv/relink-channels — link every NVR/DVR
// channel to the standalone camera device at its IP (and vice-versa). The
// per-channel link is set once at NVR-collect time, so a camera discovered after
// its NVR was collected stays unlinked until this runs. Idempotent; an operator
// can trigger it on demand, and a scan does it automatically on completion.
func (s *Server) relinkCCTVChannels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	n, err := s.queries.ReconcileNVRChannelLinks(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	if n > 0 {
		s.audit(r, "inventory", "cctv.relink_channels", "cctv", "",
			itoa(int(n))+" NVR/DVR channel(s) linked to camera devices", map[string]any{"linked": n})
	}
	writeJSON(w, http.StatusOK, map[string]any{"linked": n})
}

// getCCTVFleet handles GET /cctv/collect-fleet — the current/last run summary.
func (s *Server) getCCTVFleet(w http.ResponseWriter, r *http.Request) {
	if cur := s.cctvFleet.Load(); cur != nil {
		writeJSON(w, http.StatusOK, cur)
		return
	}
	writeJSON(w, http.StatusOK, &cctvFleetRun{Running: false, Items: []cctvFleetItem{}})
}

// runCCTVFleet collects every device with bounded concurrency, recording a
// per-device outcome and rolling counts. Each device gets its own deadline so one
// unreachable host cannot stall the run.
func (s *Server) runCCTVFleet(ctx context.Context, devices []db.Device) {
	const workers = 8
	sem := make(chan struct{}, workers)
	var mu sync.Mutex // serializes read-modify-store of the summary snapshot
	var wg sync.WaitGroup

	update := func(fn func(*cctvFleetRun)) {
		mu.Lock()
		cur := s.cctvFleet.Load()
		cp := *cur
		cp.Items = append([]cctvFleetItem(nil), cur.Items...)
		fn(&cp)
		s.cctvFleet.Store(&cp)
		mu.Unlock()
	}

	for _, d := range devices {
		wg.Add(1)
		sem <- struct{}{}
		go func(d db.Device) {
			defer wg.Done()
			defer func() { <-sem }()
			// Skip a device that recently auth-failed — a fresh attempt would only
			// add another failed login (lockout risk) with no chance of success
			// until the operator rebinds its credential.
			if it, skip := s.fleetSkipItem(ctx, d); skip {
				update(func(run *cctvFleetRun) {
					run.Items = append(run.Items, it)
					run.Done++
					run.Skipped++
				})
				return
			}
			dctx, cancel := context.WithTimeout(ctx, 60*time.Second)
			res := s.runCCTVCollection(dctx, d, nil) // fleet = bound-credential-only (never sprays)
			cancel()
			it := s.fleetItem(ctx, d, res)
			update(func(run *cctvFleetRun) {
				run.Items = append(run.Items, it)
				run.Done++
				if it.Status == "collected" {
					run.Collected++
					run.Channels += it.Channels
					switch it.NowCategory {
					case string(domain.CatNVR):
						run.NVRs++
					case string(domain.CatDVR):
						run.DVRs++
					case string(domain.CatCamera):
						run.Cameras++
					}
				} else {
					run.Failed++
				}
			})
		}(d)
	}
	wg.Wait()
	update(func(run *cctvFleetRun) {
		run.Running = false
		run.FinishedAt = fleetNow()
	})
}

// fleetItem turns a per-device collect result into a fleet row, filling channel/
// storage counts for collected recorders from nvr_info.
func (s *Server) fleetItem(ctx context.Context, d db.Device, res cctvResult) cctvFleetItem {
	ip := ""
	if d.PrimaryIp != nil && d.PrimaryIp.IsValid() {
		ip = d.PrimaryIp.String()
	}
	it := cctvFleetItem{
		DeviceID: d.ID.String(), Name: d.Name, IP: ip,
		WasCategory: d.Category, NowCategory: d.Category, Detail: res.Detail,
	}
	if res.ok() {
		it.Status = "collected"
		it.Outcome = "collected"
		it.NowCategory = res.Category
		if res.Category == string(domain.CatNVR) || res.Category == string(domain.CatDVR) {
			if info, err := s.queries.GetNVRInfo(ctx, d.ID); err == nil {
				it.Channels = int(info.ChannelCount)
				it.Storage = int(info.HddCount)
			}
		}
	} else {
		it.Status = "failed"
		it.Outcome = fleetOutcome(res.Reason, res.Detail)
	}
	return it
}

// fleetOutcome buckets a failure into the operator-facing reason classes the
// report distinguishes: auth, lockout, unsupported, unreachable, no_credential.
//
// Lockout is only claimed on a GENUINE device-reported lock phrase ("illegal
// login", "ip is locked"), never on the generic lockout-avoidance guidance that
// every auth-failure detail carries — otherwise every auth rejection would be
// mislabelled a lockout. We cannot reliably distinguish a wrong password from an
// active lockout over a 401, so an ordinary auth rejection is honestly "auth".
func fleetOutcome(reason, detail string) string {
	d := strings.ToLower(detail)
	if strings.Contains(d, "illegal login") || strings.Contains(d, "ip is locked") ||
		strings.Contains(d, "user locked") || strings.Contains(d, "account locked") {
		return "lockout"
	}
	switch reason {
	case "no_credential":
		return "no_credential"
	case "auth_failed", "wmi_auth_failed":
		return "auth"
	case "isapi_timeout", "onvif_timeout", "timeout", "connection_refused", "no_ip", "ssh_unreachable":
		return "unreachable"
	case "encryption_unavailable":
		return "error"
	}
	switch {
	case strings.Contains(d, "not exposed"):
		return "unsupported"
	case strings.Contains(d, "authentication") || strings.Contains(d, "unauthorized") || strings.Contains(d, "401") || strings.Contains(d, "403"):
		return "auth"
	case strings.Contains(d, "timeout") || strings.Contains(d, "refused") || strings.Contains(d, "unreachable"):
		return "unreachable"
	}
	if reason == "" {
		return "error"
	}
	return reason
}
