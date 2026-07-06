package isapi

import (
	"context"
	"strings"
	"time"
)

// gatherChannels collects the per-channel inventory (name/IP/enabled) plus the
// per-channel online status for a recorder. It is shared by Collect (the full
// deviceInfo/HDD/recording walk) and CollectChannelStatus (the cheap status-only
// poll the NVR channel-health monitor runs on a cadence). probe issues one ISAPI
// GET and records it; probes accumulates the endpoint outcomes. The returned map
// is keyed by channel number so the caller can enrich (record/tracks) before
// flattening.
func gatherChannels(ctx context.Context, cl *Client, probe func(string) ([]byte, bool), probes *[]Probe) map[int]*Channel {
	chByNo := map[int]*Channel{}
	videoInputNos := []int{} // local analog inputs (DVRs/encoders) — status fetched separately from IP-camera proxy
	// NVRs proxy IP cameras (InputProxy); DVRs/encoders expose local video inputs.
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
	// so without this every analog channel shows "unknown". Aggregate list first;
	// per-channel fills any input the aggregate didn't (firmware schemas vary).
	*probes = append(*probes, collectVideoInputStatus(ctx, cl, chByNo, videoInputNos)...)
	if b, ok := probe("/ISAPI/Streaming/channels"); ok {
		for no, name := range parseStreamingChannelNames(b) {
			if ch := chByNo[no]; ch != nil && ch.Name == "" {
				ch.Name = name
			}
		}
	}
	return chByNo
}

// CollectChannelStatus is a LIGHTWEIGHT poll that returns only the recorder's
// per-channel camera list + online status — no HDD, recording tracks, network,
// time, or system-status endpoints. It is what the NVR channel-health monitor
// runs every few minutes so a camera dropping off the recorder is detected
// quickly without the cost of a full CCTV collection. deviceInfo is still fetched
// (it names the endpoint and confirms the device is a recorder). A non-recorder
// returns an NVR with no channels and no error.
func CollectChannelStatus(ctx context.Context, ip, user, pass string, doer Doer, prefer []string) (NVR, error) {
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
	if !out.IsRecorder() {
		return out, nil // a plain camera has no channels to poll
	}

	cl := NewClient(info.Endpoint, user, pass, doer)
	probe := func(path string) ([]byte, bool) {
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		t0 := time.Now()
		body, status, gerr := cl.GetStatus(pctx, path)
		cancel()
		p, ok := makeProbe(path, body, status, gerr)
		p.DurationMs = time.Since(t0).Milliseconds()
		out.Probes = append(out.Probes, p)
		if !ok {
			return nil, false
		}
		return body, true
	}
	chByNo := gatherChannels(ctx, cl, probe, &out.Probes)
	for _, ch := range chByNo {
		out.Channels = append(out.Channels, *ch)
	}
	return out, nil
}
