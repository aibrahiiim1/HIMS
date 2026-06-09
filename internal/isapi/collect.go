package isapi

import (
	"context"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
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
	Probes    []Probe // every endpoint tried + its HTTP status/outcome
}

// Channel is one camera/input on the recorder.
type Channel struct {
	No      int
	Name    string
	IP      string
	Online  *bool // nil = status not reported
	Enabled bool
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
	Path   string `json:"path"`
	Status int    `json:"status"` // HTTP status (0 = transport error)
	OK     bool   `json:"ok"`
	Note   string `json:"note"`
}

// IsRecorder reports whether the identified deviceType is an NVR/DVR.
func (n NVR) IsRecorder() bool {
	dt := strings.ToLower(n.Info.DeviceType)
	return strings.Contains(dt, "nvr") || strings.Contains(dt, "dvr")
}

// Collect runs the full read-only ISAPI inventory: DeviceInfo first (also
// establishes the working endpoint), then — only for recorders — the camera
// channels, per-channel online status, HDD/storage, recording tracks and working
// status. A nil doer uses PermissiveClient. Optional endpoints that 4xx are
// recorded as probes (not failures). Only DeviceInfo is required.
func Collect(ctx context.Context, ip, user, pass string, doer Doer) (NVR, error) {
	if doer == nil {
		doer = PermissiveClient(15 * time.Second)
	}
	var out NVR
	info, err := CollectDeviceInfo(ctx, ip, user, pass, doer)
	if err != nil {
		return out, err
	}
	out.Info = info
	out.Probes = append(out.Probes, Probe{Path: "/ISAPI/System/deviceInfo", Status: 200, OK: true, Note: "identity " + strings.TrimSpace(info.Model+" "+info.DeviceType)})

	cl := NewClient(info.Endpoint, user, pass, doer)
	probe := func(path string) ([]byte, bool) {
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		body, status, err := cl.GetStatus(pctx, path)
		cancel()
		if err != nil {
			out.Probes = append(out.Probes, Probe{Path: path, Status: 0, OK: false, Note: err.Error()})
			return nil, false
		}
		ok := status >= 200 && status < 300
		note := ""
		if !ok {
			switch {
			case status == 401:
				note = "authentication rejected"
			case status == 400 || status == 403 || status == 404 || status == 501:
				note = "not exposed by device"
			default:
				note = fmt.Sprintf("HTTP %d", status)
			}
		}
		out.Probes = append(out.Probes, Probe{Path: path, Status: status, OK: ok, Note: note})
		if !ok {
			return nil, false
		}
		return body, true
	}

	// Only recorders have channels/HDDs; a plain camera stops at identity.
	if !out.IsRecorder() {
		return out, nil
	}

	// Camera channels: NVRs proxy IP cameras (InputProxy); DVRs/encoders expose
	// local video inputs. Try both; merge names from streaming channels.
	chByNo := map[int]*Channel{}
	if b, ok := probe("/ISAPI/ContentMgmt/InputProxy/channels"); ok {
		for _, c := range parseInputProxyChannels(b) {
			cc := c
			chByNo[c.No] = &cc
		}
	}
	if b, ok := probe("/ISAPI/System/Video/inputs/channels"); ok {
		for _, c := range parseVideoInputChannels(b) {
			if _, seen := chByNo[c.No]; !seen {
				cc := c
				chByNo[c.No] = &cc
			}
		}
	}
	if b, ok := probe("/ISAPI/ContentMgmt/InputProxy/channels/status"); ok {
		for no, online := range parseInputProxyStatus(b) {
			if ch := chByNo[no]; ch != nil {
				o := online
				ch.Online = &o
			}
		}
	}
	if b, ok := probe("/ISAPI/Streaming/channels"); ok {
		for no, name := range parseStreamingChannelNames(b) {
			if ch := chByNo[no]; ch != nil && ch.Name == "" {
				ch.Name = name
			}
		}
	}
	for _, ch := range chByNo {
		out.Channels = append(out.Channels, *ch)
	}

	// Storage / HDDs.
	if b, ok := probe("/ISAPI/ContentMgmt/Storage/hdd"); ok {
		out.Storage = parseHDDs(b)
	}

	// Recording (read-only): the record tracks list. Presence ⇒ recording set up.
	if b, ok := probe("/ISAPI/ContentMgmt/record/tracks"); ok {
		if n := countTag(b, "Track"); n > 0 {
			out.Recording = fmt.Sprintf("%d recording track(s) configured", n)
		} else {
			out.Recording = "no recording tracks configured"
		}
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
		ID   string `xml:"id"`
		Name string `xml:"name"`
	}
	var doc struct {
		Ch []ch `xml:"VideoInputChannel"`
	}
	if xml.Unmarshal(b, &doc) != nil {
		return nil
	}
	out := make([]Channel, 0, len(doc.Ch))
	for _, c := range doc.Ch {
		out = append(out, Channel{No: atoi(c.ID), Name: strings.TrimSpace(c.Name), Enabled: true})
	}
	return out
}

func parseInputProxyStatus(b []byte) map[int]bool {
	type st struct {
		ID     string `xml:"id"`
		Online string `xml:"online"`
	}
	var doc struct {
		St []st `xml:"InputProxyChannelStatus"`
	}
	out := map[int]bool{}
	if xml.Unmarshal(b, &doc) != nil {
		return out
	}
	for _, s := range doc.St {
		out[atoi(s.ID)] = strings.EqualFold(strings.TrimSpace(s.Online), "true")
	}
	return out
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

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
func atoi64(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}
