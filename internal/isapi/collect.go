package isapi

import (
	"context"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// NVR is the full read-only inventory collected from a Hikvision recorder/camera
// over ISAPI: identity, camera channels, HDDs, plus a recording/health summary and
// a per-endpoint probe log so the UI can show exactly what each endpoint returned
// (and honestly mark unsupported ones as "not exposed by device").
type NVR struct {
	Info      DeviceInfo
	Channels  []Channel
	Storage   []HDD
	Recording string  // human summary; "" = not exposed by this firmware
	Health    string  // working-status summary; "" = not exposed
	Net       NetInfo // NIC config (IP/mask/gateway/DNS/MAC) — cameras + recorders
	TimeCfg   TimeInfo
	Probes    []Probe // every endpoint tried + its HTTP status/outcome
}

// NetInfo is the device's primary network interface config (ISAPI System/Network).
type NetInfo struct {
	IP, Mask, Gateway, DNS, MAC string
}

// TimeInfo is the device's clock config (ISAPI System/time).
type TimeInfo struct {
	TimeZone, NTPServer string
}

// Channel is one camera/input on the recorder.
type Channel struct {
	No           int
	Name         string
	IP           string
	Online       *bool  // nil = status not reported
	DetectResult string // chanDetectResult: connect | netUnreachable | errorUserNameOrPasswd | offline | …
	Enabled      bool   // configured/enabled in the recorder
	Resolution   string // analog inputs: resDesc, e.g. "1080P25" ("" = no signal)
	Recording    *bool  // nil = recording state not reported; from record/tracks
}

// OfflineReason maps the recorder's raw chanDetectResult to a clear, operator-facing reason.
// Empty string = online / no issue.
func (c Channel) OfflineReason() string {
	if c.Online != nil && *c.Online {
		return ""
	}
	switch strings.TrimSpace(c.DetectResult) {
	case "", "connect":
		if c.Online != nil && !*c.Online {
			return "offline"
		}
		return ""
	case "netUnreachable", "networkAbnormal", "offline", "timeout":
		return "network unreachable"
	case "errorUserNameOrPasswd", "userOrPasswordError", "authError":
		return "credential error"
	case "IPConflict":
		return "IP conflict"
	case "notSupport", "videoLoss":
		return strings.TrimSpace(c.DetectResult)
	default:
		return strings.TrimSpace(c.DetectResult)
	}
}

// HDD is one storage device on the recorder.
type HDD struct {
	ID         int    // parse-order slot (the device's <id> can collide across array + members)
	Name       string // hddName, or the disk type when unnamed
	Type       string // "Virtual Disk" (RAID volume) | "SATA" | …
	Status     string
	CapacityMB int64
	FreeMB     int64
	Property   string
}

// Probe records the outcome of one ISAPI endpoint (for the Collection-health view).
type Probe struct {
	Path       string `json:"path"`
	Status     int    `json:"status"` // HTTP status (0 = transport error)
	OK         bool   `json:"ok"`
	Note       string `json:"note"`
	Sample     string `json:"sample,omitempty"`      // truncated response body (OK probes) — diagnostics + schema discovery
	DurationMs int64  `json:"duration_ms,omitempty"` // wall-clock of this probe — latency diagnostics
}

// IsRecorder reports whether the device is an NVR/DVR (so channel/HDD/recording
// collection should run). deviceType is the primary signal, but Hikvision Turbo HD
// DVRs report a generic deviceType ("IPC") over ISAPI, so we also recognise the
// recorder by its model code — …HGHI/…HQHI/…HUHI/…HVR = DVR; …NI…/…NXI…/NVR = NVR.
// Plain IP cameras (DS-2CD…/DS-2DE…) match none of these, so they correctly stay
// non-recorders.
func (n NVR) IsRecorder() bool {
	dt := strings.ToLower(n.Info.DeviceType)
	if strings.Contains(dt, "nvr") || strings.Contains(dt, "dvr") || strings.Contains(dt, "hvr") || strings.Contains(dt, "hybrid") {
		return true
	}
	m := strings.ToUpper(n.Info.Model)
	for _, code := range []string{"HGHI", "HQHI", "HUHI", "HVR", "DVR", "NVR", "NXI"} {
		if strings.Contains(m, code) {
			return true
		}
	}
	return false
}

// Collect runs the full read-only ISAPI inventory: DeviceInfo first (also
// establishes the working endpoint), then — only for recorders — the camera
// channels, per-channel online status, HDD/storage, recording tracks and working
// status. A nil doer uses PermissiveClient. Optional endpoints that 4xx are
// recorded as probes (not failures). Only DeviceInfo is required.
// prefer is an ordered list of base URLs (scheme://ip[:port]) to try BEFORE the
// default scheme/port ladder — the device's last-OK endpoint, operator override,
// scanned-open web ports, then the configured candidate ports. Pass nil to use
// only the default ladder.
func Collect(ctx context.Context, ip, user, pass string, doer Doer, prefer []string) (NVR, error) {
	if doer == nil {
		doer = PermissiveClient(15 * time.Second)
	}
	var out NVR
	info, err := CollectDeviceInfo(ctx, ip, user, pass, doer, prefer)
	if err != nil {
		return out, err
	}
	out.Info = info
	out.Probes = append(out.Probes, Probe{Path: "/ISAPI/System/deviceInfo", Status: 200, OK: true, Note: "identity " + strings.TrimSpace(info.Model+" "+info.DeviceType)})

	cl := NewClient(info.Endpoint, user, pass, doer)
	probe := func(path string) ([]byte, bool) {
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		t0 := time.Now()
		body, status, err := cl.GetStatus(pctx, path)
		cancel()
		p, ok := makeProbe(path, body, status, err)
		p.DurationMs = time.Since(t0).Milliseconds()
		out.Probes = append(out.Probes, p)
		if !ok {
			return nil, false
		}
		return body, true
	}

	// Network + time config — collected for cameras AND recorders (the useful
	// "other data" a camera exposes over ISAPI beyond bare identity).
	if b, ok := probe("/ISAPI/System/Network/interfaces"); ok {
		out.Net = parseNetworkInterfaces(b)
	}
	if b, ok := probe("/ISAPI/System/time"); ok {
		out.TimeCfg.TimeZone = parseTimeZone(b)
	}
	if b, ok := probe("/ISAPI/System/time/ntpServers"); ok {
		out.TimeCfg.NTPServer = parseNTPServer(b)
	}

	// Only recorders have channels/HDDs; a plain camera stops here.
	if !out.IsRecorder() {
		return out, nil
	}

	// Camera channels: NVRs proxy IP cameras (InputProxy); DVRs/encoders expose
	// local video inputs. Try both; merge names from streaming channels.
	chByNo := map[int]*Channel{}
	videoInputNos := []int{} // local analog inputs (DVRs/encoders) — status fetched separately from IP-camera proxy
	if b, ok := probe("/ISAPI/ContentMgmt/InputProxy/channels"); ok {
		for _, c := range parseInputProxyChannels(b) {
			cc := c
			chByNo[c.No] = &cc
		}
	}
	if b, ok := probe("/ISAPI/System/Video/inputs/channels"); ok {
		for _, c := range parseVideoInputChannels(b) {
			videoInputNos = append(videoInputNos, c.No)
			if _, seen := chByNo[c.No]; !seen {
				cc := c
				chByNo[c.No] = &cc
			}
		}
	}
	// IP-camera channel online status (NVRs): the camera-behind-the-channel link state.
	if b, ok := probe("/ISAPI/ContentMgmt/InputProxy/channels/status"); ok {
		for no, st := range parseInputProxyStatus(b) {
			if ch := chByNo[no]; ch != nil {
				o := st.Online
				ch.Online = &o
				ch.DetectResult = st.Detect
				if ch.IP == "" && st.IP != "" {
					ch.IP = st.IP
				}
			}
		}
	}
	// Analog-input signal status (DVRs/Turbo-HD/encoders): whether a camera is wired
	// to each local BNC input. The IP-camera proxy status above never covers these,
	// so without this every analog channel shows "unknown". Try the aggregate list
	// first; per-channel fills any input the aggregate didn't (firmware schemas vary).
	out.Probes = append(out.Probes, collectVideoInputStatus(ctx, cl, chByNo, videoInputNos)...)
	if b, ok := probe("/ISAPI/Streaming/channels"); ok {
		for no, name := range parseStreamingChannelNames(b) {
			if ch := chByNo[no]; ch != nil && ch.Name == "" {
				ch.Name = name
			}
		}
	}
	// Recording (read-only): the record-tracks list gives a per-channel enabled
	// flag (which inputs are actually being recorded) plus the overall summary.
	// Done before the channel copy so per-channel Recording lands in out.Channels.
	if b, ok := probe("/ISAPI/ContentMgmt/record/tracks"); ok {
		for no, on := range parseRecordTracks(b) {
			if ch := chByNo[no]; ch != nil {
				o := on
				ch.Recording = &o
			}
		}
		if n := countTag(b, "Track"); n > 0 {
			out.Recording = fmt.Sprintf("%d recording track(s) configured", n)
		} else {
			out.Recording = "no recording tracks configured"
		}
	}
	for _, ch := range chByNo {
		out.Channels = append(out.Channels, *ch)
	}

	// Storage / HDDs.
	if b, ok := probe("/ISAPI/ContentMgmt/Storage/hdd"); ok {
		out.Storage = parseHDDs(b)
	}

	// System status (best-effort health; firmware-dependent).
	if b, ok := probe("/ISAPI/System/status"); ok {
		out.Health = summarizeWorkingStatus(b)
	}

	return out, nil
}

// ---- parsers (defensive: namespace-agnostic, tolerant of missing fields) -------

func parseInputProxyChannels(b []byte) []Channel {
	type src struct {
		IP   string `xml:"ipAddress"`
		Port string `xml:"managePortNo"`
	}
	type ch struct {
		ID   string `xml:"id"`
		Name string `xml:"name"`
		Src  src    `xml:"sourceInputPortDescriptor"`
	}
	var doc struct {
		Ch []ch `xml:"InputProxyChannel"`
	}
	if xml.Unmarshal(b, &doc) != nil {
		return nil
	}
	out := make([]Channel, 0, len(doc.Ch))
	for _, c := range doc.Ch {
		out = append(out, Channel{No: atoi(c.ID), Name: strings.TrimSpace(c.Name), IP: strings.TrimSpace(c.Src.IP), Enabled: true})
	}
	return out
}

func parseVideoInputChannels(b []byte) []Channel {
	type ch struct {
		ID      string `xml:"id"`
		Name    string `xml:"name"`
		Enabled string `xml:"videoInputEnabled"`
		ResDesc string `xml:"resDesc"` // analog signal: "1080P25" when a camera is wired, "" otherwise
	}
	var doc struct {
		Ch []ch `xml:"VideoInputChannel"`
	}
	if xml.Unmarshal(b, &doc) != nil {
		return nil
	}
	out := make([]Channel, 0, len(doc.Ch))
	for _, c := range doc.Ch {
		res := strings.TrimSpace(c.ResDesc)
		enabled := !strings.EqualFold(strings.TrimSpace(c.Enabled), "false")
		cc := Channel{No: atoi(c.ID), Name: strings.TrimSpace(c.Name), Enabled: enabled, Resolution: res}
		// Analog-input signal status straight from the channel list: a connected
		// camera reports a resolution (resDesc); a BNC with no camera reports none.
		// These DVRs 403 the per-channel /status endpoint, so this is THE reliable
		// signal — and it costs no extra request.
		switch {
		case res != "" && !strings.Contains(strings.ToLower(res), "no video"):
			online := true
			cc.Online = &online
		case enabled:
			offline := false // enabled input, no resolution ⇒ no signal
			cc.Online = &offline
		}
		out = append(out, cc)
	}
	return out
}

// parseRecordTracks maps channel number → whether recording is enabled, from the
// ISAPI record-tracks list. Each channel has main/sub tracks (id/Channel encode
// channel*100+stream, e.g. 101 = ch1 main); a channel counts as recording if ANY
// of its tracks is enabled.
func parseRecordTracks(b []byte) map[int]bool {
	type tr struct {
		Channel string `xml:"Channel"`
		Enable  string `xml:"Enable"`
	}
	var doc struct {
		Tr []tr `xml:"Track"`
	}
	out := map[int]bool{}
	if xml.Unmarshal(b, &doc) != nil {
		return out
	}
	for _, t := range doc.Tr {
		no := atoi(t.Channel)
		if no >= 100 {
			no /= 100 // 101 → ch1
		}
		if no == 0 {
			continue
		}
		on := strings.EqualFold(strings.TrimSpace(t.Enable), "true")
		out[no] = out[no] || on // sticky-true: any enabled track ⇒ recording
	}
	return out
}

// chStatus is one channel's live link state from the InputProxy status walk.
type chStatus struct {
	Online bool
	Detect string // chanDetectResult diagnostic code
	IP     string // camera IP behind the channel
}

func parseInputProxyStatus(b []byte) map[int]chStatus {
	type st struct {
		ID     string `xml:"id"`
		Online string `xml:"online"`
		Detect string `xml:"chanDetectResult"`
		Src    struct {
			IP string `xml:"ipAddress"`
		} `xml:"sourceInputPortDescriptor"`
	}
	var doc struct {
		St []st `xml:"InputProxyChannelStatus"`
	}
	out := map[int]chStatus{}
	if xml.Unmarshal(b, &doc) != nil {
		return out
	}
	for _, s := range doc.St {
		out[atoi(s.ID)] = chStatus{
			Online: strings.EqualFold(strings.TrimSpace(s.Online), "true"),
			Detect: strings.TrimSpace(s.Detect),
			IP:     strings.TrimSpace(s.Src.IP),
		}
	}
	return out
}

// makeProbe builds a Probe record from a single GET outcome, capturing a short
// body sample for 2xx responses (diagnostics + analog-DVR schema discovery).
func makeProbe(path string, body []byte, status int, err error) (Probe, bool) {
	if err != nil {
		return Probe{Path: path, Status: 0, OK: false, Note: err.Error()}, false
	}
	ok := status >= 200 && status < 300
	p := Probe{Path: path, Status: status, OK: ok}
	if ok {
		p.Sample = bodySample(body)
	} else {
		switch {
		case status == 401:
			p.Note = "authentication rejected"
		case status == 400 || status == 403 || status == 404 || status == 501:
			p.Note = "not exposed by device"
		default:
			p.Note = fmt.Sprintf("HTTP %d", status)
		}
	}
	return p, ok
}

// videoStatusTimeout bounds each analog-input status probe. It's short because
// these are best-effort enrichment, and on firmwares that don't expose the
// endpoint the request often hangs until timeout rather than returning a clean
// 404 — a long timeout there would stall the whole collection.
const videoStatusTimeout = 5 * time.Second

// videoStatusConcurrency caps simultaneous per-channel status probes. DVRs answer
// each request slowly, so a sequential walk of 16–32 inputs would dominate the
// collection's wall-clock; bounded concurrency keeps it to a couple of round-trips.
const videoStatusConcurrency = 8

// collectVideoInputStatus fills Channel.Online for local analog video inputs
// (DVRs/Turbo-HD/encoders) — whether a camera is actually wired to each BNC input.
// The IP-camera proxy status never covers these, so without this every analog
// channel reads "unknown". It returns the probe records it made (for the caller
// to append to the collection-health view).
//
// It tries the aggregate list first (one request covers every input). Per-channel
// is only used to fill inputs the aggregate left blank, and is capability-gated: a
// single probe on the first pending input decides whether to fan out, so a firmware
// that doesn't expose the per-channel endpoint costs 2 probes, not one per channel.
// The fan-out runs concurrently (bounded) because DVRs are slow per request. Inputs
// whose status can't be positively read stay nil ("unknown"), never guessed offline.
func collectVideoInputStatus(ctx context.Context, cl *Client, chByNo map[int]*Channel, videoInputNos []int) []Probe {
	if len(videoInputNos) == 0 {
		return nil // pure NVR (IP-camera proxy only) — no local analog inputs
	}
	var probes []Probe
	pathOf := func(no int) string { return fmt.Sprintf("/ISAPI/System/Video/inputs/channels/%d/status", no) }
	get := func(path string) ([]byte, bool) {
		pctx, cancel := context.WithTimeout(ctx, videoStatusTimeout)
		body, status, err := cl.GetStatus(pctx, path)
		cancel()
		p, ok := makeProbe(path, body, status, err)
		probes = append(probes, p)
		if !ok {
			return nil, false
		}
		return body, true
	}

	// Aggregate first — one request covers every input.
	if b, ok := get("/ISAPI/System/Video/inputs/channels/status"); ok {
		for no, online := range parseVideoInputStatus(b) {
			if ch := chByNo[no]; ch != nil && ch.Online == nil {
				o := online
				ch.Online = &o
			}
		}
	}

	// Inputs still without a status after the aggregate.
	var pending []int
	for _, no := range videoInputNos {
		if ch := chByNo[no]; ch != nil && ch.Online == nil {
			pending = append(pending, no)
		}
	}
	if len(pending) == 0 {
		return probes
	}

	// Capability check on the first pending input (sequential): gate fan-out on
	// whether the per-channel endpoint responds (2xx), NOT on whether that one
	// input parsed — a first input with no signal must not skip the rest.
	b, ok := get(pathOf(pending[0]))
	if !ok {
		return probes // endpoint unsupported — leave the rest "unknown", no per-channel storm
	}
	if online, known := parseVideoInputStatusOne(b); known {
		o := online
		chByNo[pending[0]].Online = &o
	}

	// Fan out the remaining inputs concurrently (bounded).
	rest := pending[1:]
	if len(rest) == 0 {
		return probes
	}
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, videoStatusConcurrency)
	)
	for _, no := range rest {
		no := no
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			pctx, cancel := context.WithTimeout(ctx, videoStatusTimeout)
			body, status, err := cl.GetStatus(pctx, pathOf(no))
			cancel()
			p, pok := makeProbe(pathOf(no), body, status, err)
			online, known := false, false
			if pok {
				online, known = parseVideoInputStatusOne(body)
			}
			mu.Lock()
			probes = append(probes, p)
			if known {
				o := online
				chByNo[no].Online = &o // unique no per goroutine; map write guarded by mu
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	return probes
}

// parseVideoInputStatus reads analog-input signal status from the aggregate
// endpoint. Only inputs reporting a recognizable status field are returned;
// callers keep the rest "unknown".
func parseVideoInputStatus(b []byte) map[int]bool {
	type st struct {
		ID     string `xml:"id"`
		Online string `xml:"online"`
		Signal string `xml:"signalStatus"`     // some firmwares: "normal"/"noVideo"
		Video  string `xml:"videoInputStatus"` // some firmwares
	}
	var doc struct {
		St []st `xml:"VideoInputChannelStatus"`
	}
	out := map[int]bool{}
	if xml.Unmarshal(b, &doc) != nil {
		return out
	}
	for _, s := range doc.St {
		if online, known := videoOnline(s.Online, s.Signal, s.Video); known {
			out[atoi(s.ID)] = online
		}
	}
	return out
}

// parseVideoInputStatusOne reads the per-channel video-input status object.
func parseVideoInputStatusOne(b []byte) (online, known bool) {
	var doc struct {
		Online string `xml:"online"`
		Signal string `xml:"signalStatus"`
		Video  string `xml:"videoInputStatus"`
	}
	if xml.Unmarshal(b, &doc) != nil {
		return false, false
	}
	return videoOnline(doc.Online, doc.Signal, doc.Video)
}

// videoOnline interprets the assorted fields Hikvision firmwares use to report
// whether an analog input has a live camera signal. known=false means none was
// recognizable, so the caller leaves the channel "unknown".
func videoOnline(online, signal, video string) (val, known bool) {
	if v := strings.TrimSpace(online); v != "" {
		return strings.EqualFold(v, "true"), true
	}
	for _, s := range []string{signal, video} {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "normal", "online", "connected", "connect", "true":
			return true, true
		case "novideo", "no video", "videoloss", "video loss", "offline", "disconnect", "disconnected", "false":
			return false, true
		}
	}
	return false, false
}

// Streaming channel ids encode channel*100+stream (e.g. 101 = ch1 main). Map the
// first stream's channelName to the channel number.
func parseStreamingChannelNames(b []byte) map[int]string {
	type sc struct {
		ID   string `xml:"id"`
		Name string `xml:"channelName"`
	}
	var doc struct {
		Sc []sc `xml:"StreamingChannel"`
	}
	out := map[int]string{}
	if xml.Unmarshal(b, &doc) != nil {
		return out
	}
	for _, s := range doc.Sc {
		id := atoi(s.ID)
		no := id
		if id >= 100 {
			no = id / 100
		}
		if _, seen := out[no]; !seen && strings.TrimSpace(s.Name) != "" {
			out[no] = strings.TrimSpace(s.Name)
		}
	}
	return out
}

func parseHDDs(b []byte) []HDD {
	type hdd struct {
		ID       string `xml:"id"`
		Name     string `xml:"hddName"`
		Type     string `xml:"hddType"`
		Status   string `xml:"status"`
		Capacity string `xml:"capacity"`
		Free     string `xml:"freeSpace"`
		Property string `xml:"property"`
	}
	var doc struct {
		HDD []hdd `xml:"hdd"`
	}
	if xml.Unmarshal(b, &doc) != nil {
		return nil
	}
	// Use parse-order slots as the key: the device's <id> repeats across the RAID
	// volume (id 1) and its physical members (id 1..N), which would collide.
	out := make([]HDD, 0, len(doc.HDD))
	for i, h := range doc.HDD {
		name := strings.TrimSpace(h.Name)
		if name == "" {
			name = strings.TrimSpace(h.Type)
		}
		out = append(out, HDD{
			ID: i + 1, Name: name, Type: strings.TrimSpace(h.Type), Status: strings.TrimSpace(h.Status),
			CapacityMB: atoi64(h.Capacity), FreeMB: atoi64(h.Free), Property: strings.TrimSpace(h.Property),
		})
	}
	return out
}

// parseNetworkInterfaces reads the device's primary NIC config (first interface).
func parseNetworkInterfaces(b []byte) NetInfo {
	type gw struct {
		IP string `xml:"ipAddress"`
	}
	type addr struct {
		IP   string `xml:"ipAddress"`
		Mask string `xml:"subnetMask"`
		GW   gw     `xml:"DefaultGateway"`
		DNS  gw     `xml:"PrimaryDNS"`
	}
	var doc struct {
		Iface []struct {
			Addr addr   `xml:"IPAddress"`
			MAC  string `xml:"Link>MACAddress"`
		} `xml:"NetworkInterface"`
	}
	if xml.Unmarshal(b, &doc) != nil || len(doc.Iface) == 0 {
		return NetInfo{}
	}
	i := doc.Iface[0]
	return NetInfo{
		IP: strings.TrimSpace(i.Addr.IP), Mask: strings.TrimSpace(i.Addr.Mask),
		Gateway: strings.TrimSpace(i.Addr.GW.IP), DNS: strings.TrimSpace(i.Addr.DNS.IP),
		MAC: strings.TrimSpace(i.MAC),
	}
}

func parseTimeZone(b []byte) string {
	var doc struct {
		TZ string `xml:"timeZone"`
	}
	_ = xml.Unmarshal(b, &doc)
	return strings.TrimSpace(doc.TZ)
}

func parseNTPServer(b []byte) string {
	var doc struct {
		Servers []struct {
			IP   string `xml:"ipAddress"`
			Host string `xml:"hostName"`
		} `xml:"NTPServer"`
	}
	if xml.Unmarshal(b, &doc) != nil || len(doc.Servers) == 0 {
		return ""
	}
	if v := strings.TrimSpace(doc.Servers[0].IP); v != "" {
		return v
	}
	return strings.TrimSpace(doc.Servers[0].Host)
}

func summarizeWorkingStatus(b []byte) string {
	// Firmware varies widely here; just report that it answered + any deviceStatus.
	type ws struct {
		DeviceStatus string `xml:"deviceStatus"`
	}
	var doc ws
	_ = xml.Unmarshal(b, &doc)
	if s := strings.TrimSpace(doc.DeviceStatus); s != "" {
		return "device status: " + s
	}
	return "working status reported"
}

func countTag(b []byte, local string) int {
	n := 0
	dec := xml.NewDecoder(strings.NewReader(string(b)))
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == local {
			n++
		}
	}
	return n
}

// bodySample returns a whitespace-collapsed, rune-safe, truncated copy of an ISAPI
// response body for the Collection-health view (and analog-input schema discovery).
// Capped so probe logs stay small even for long channel/HDD lists.
func bodySample(b []byte) string {
	s := strings.Join(strings.Fields(string(b)), " ") // trims + collapses whitespace runs
	const max = 280
	if r := []rune(s); len(r) > max {
		return strings.TrimSpace(string(r[:max])) + " …"
	}
	return s
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
func atoi64(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}
