package isapi

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// pathDoer answers GETs by URL path: a known path returns 200 + its body, an
// unknown path returns 404. Safe for concurrent use (collectVideoInputStatus fans
// out per-channel probes).
type pathDoer struct {
	mu     sync.Mutex
	bodies map[string]string
	calls  map[string]int
}

func (d *pathDoer) Do(req *http.Request) (*http.Response, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.calls == nil {
		d.calls = map[string]int{}
	}
	d.calls[req.URL.Path]++
	if body, ok := d.bodies[req.URL.Path]; ok {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	}
	return &http.Response{StatusCode: 404, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
}

func TestParseInputProxyChannels(t *testing.T) {
	xml := `<InputProxyChannelList xmlns="http://www.hikvision.com/ver20/XMLSchema">
	<InputProxyChannel><id>1</id><name>Lobby Cam</name>
	  <sourceInputPortDescriptor><ipAddress>172.21.210.50</ipAddress><managePortNo>8000</managePortNo></sourceInputPortDescriptor>
	</InputProxyChannel>
	<InputProxyChannel><id>2</id><name>Garage</name>
	  <sourceInputPortDescriptor><ipAddress>172.21.210.51</ipAddress></sourceInputPortDescriptor>
	</InputProxyChannel></InputProxyChannelList>`
	ch := parseInputProxyChannels([]byte(xml))
	if len(ch) != 2 || ch[0].No != 1 || ch[0].Name != "Lobby Cam" || ch[0].IP != "172.21.210.50" {
		t.Fatalf("channels = %+v", ch)
	}
	if ch[1].IP != "172.21.210.51" {
		t.Errorf("ch2 ip = %q", ch[1].IP)
	}
}

func TestParseInputProxyStatus(t *testing.T) {
	xml := `<InputProxyChannelStatusList>
	  <InputProxyChannelStatus><id>1</id><online>true</online></InputProxyChannelStatus>
	  <InputProxyChannelStatus><id>2</id><online>false</online></InputProxyChannelStatus>
	</InputProxyChannelStatusList>`
	st := parseInputProxyStatus([]byte(xml))
	if st[1] != true || st[2] != false {
		t.Fatalf("status = %+v", st)
	}
}

func TestParseHDDs(t *testing.T) {
	xml := `<hddList xmlns="http://www.hikvision.com/ver20/XMLSchema">
	  <hdd><id>1</id><hddName>HDD1</hddName><status>ok</status><capacity>3815447</capacity><freeSpace>120832</freeSpace><property>RW</property></hdd>
	</hddList>`
	h := parseHDDs([]byte(xml))
	if len(h) != 1 || h[0].ID != 1 || h[0].Status != "ok" || h[0].CapacityMB != 3815447 || h[0].FreeMB != 120832 || h[0].Property != "RW" {
		t.Fatalf("hdd = %+v", h)
	}
}

func TestParseStreamingChannelNames(t *testing.T) {
	xml := `<StreamingChannelList>
	  <StreamingChannel><id>101</id><channelName>Lobby</channelName></StreamingChannel>
	  <StreamingChannel><id>102</id><channelName>Lobby-sub</channelName></StreamingChannel>
	  <StreamingChannel><id>201</id><channelName>Garage</channelName></StreamingChannel>
	</StreamingChannelList>`
	n := parseStreamingChannelNames([]byte(xml))
	if n[1] != "Lobby" || n[2] != "Garage" {
		t.Fatalf("names = %+v", n)
	}
}

func TestCountTag(t *testing.T) {
	xml := `<TrackList><Track><id>1</id></Track><Track><id>2</id></Track></TrackList>`
	if countTag([]byte(xml), "Track") != 2 {
		t.Fatalf("count = %d", countTag([]byte(xml), "Track"))
	}
}

func TestParseVideoInputStatus(t *testing.T) {
	// Aggregate analog-input status: ch1 has a live signal, ch2 lost video,
	// ch3 reports no recognizable field (must stay "unknown", not assumed offline).
	xml := `<VideoInputChannelStatusList xmlns="http://www.hikvision.com/ver20/XMLSchema">
	  <VideoInputChannelStatus><id>1</id><online>true</online></VideoInputChannelStatus>
	  <VideoInputChannelStatus><id>2</id><signalStatus>noVideo</signalStatus></VideoInputChannelStatus>
	  <VideoInputChannelStatus><id>3</id></VideoInputChannelStatus>
	</VideoInputChannelStatusList>`
	st := parseVideoInputStatus([]byte(xml))
	if v, ok := st[1]; !ok || !v {
		t.Errorf("ch1 = (%v,%v), want online", v, ok)
	}
	if v, ok := st[2]; !ok || v {
		t.Errorf("ch2 = (%v,%v), want offline", v, ok)
	}
	if _, ok := st[3]; ok {
		t.Errorf("ch3 should stay unknown (absent from map), got present")
	}
}

func TestParseVideoInputStatusOne(t *testing.T) {
	cases := []struct {
		xml         string
		want, known bool
	}{
		{`<VideoInputChannelStatus><id>1</id><online>true</online></VideoInputChannelStatus>`, true, true},
		{`<VideoInputChannelStatus><id>1</id><online>false</online></VideoInputChannelStatus>`, false, true},
		{`<VideoInputChannelStatus><id>1</id><videoInputStatus>normal</videoInputStatus></VideoInputChannelStatus>`, true, true},
		{`<VideoInputChannelStatus><id>1</id><signalStatus>noVideo</signalStatus></VideoInputChannelStatus>`, false, true},
		{`<VideoInputChannelStatus><id>1</id></VideoInputChannelStatus>`, false, false}, // unknown stays unknown
	}
	for i, c := range cases {
		got, known := parseVideoInputStatusOne([]byte(c.xml))
		if got != c.want || known != c.known {
			t.Errorf("case %d: got (%v,%v), want (%v,%v)", i, got, known, c.want, c.known)
		}
	}
}

func TestParseVideoInputChannels_SignalFromResDesc(t *testing.T) {
	// ch1 has a live signal (resDesc set), ch2 is enabled but no signal, ch3 disabled.
	xml := `<VideoInputChannelList xmlns="http://www.hikvision.com/ver20/XMLSchema">
	  <VideoInputChannel><id>1</id><name>Reception_Desk</name><videoInputEnabled>true</videoInputEnabled><resDesc>1080P25</resDesc></VideoInputChannel>
	  <VideoInputChannel><id>2</id><name>Garage</name><videoInputEnabled>true</videoInputEnabled><resDesc></resDesc></VideoInputChannel>
	  <VideoInputChannel><id>3</id><name>Spare</name><videoInputEnabled>false</videoInputEnabled></VideoInputChannel>
	</VideoInputChannelList>`
	ch := parseVideoInputChannels([]byte(xml))
	if len(ch) != 3 {
		t.Fatalf("channels = %d", len(ch))
	}
	if ch[0].Name != "Reception_Desk" || ch[0].Resolution != "1080P25" || ch[0].Online == nil || !*ch[0].Online {
		t.Errorf("ch1 = %+v (want online, 1080P25)", ch[0])
	}
	if ch[1].Online == nil || *ch[1].Online { // enabled + no resDesc ⇒ offline
		t.Errorf("ch2 should be offline (no signal), got %+v", ch[1])
	}
	if ch[2].Online != nil { // disabled ⇒ unknown
		t.Errorf("ch3 (disabled) should stay unknown, got %v", *ch[2].Online)
	}
}

func TestParseRecordTracks(t *testing.T) {
	// ch1: main on, sub off ⇒ recording. ch2: both off ⇒ not recording. ch3: main on.
	xml := `<TrackList xmlns="http://www.hikvision.com/ver20/XMLSchema">
	  <Track><id>101</id><Channel>101</Channel><Enable>true</Enable></Track>
	  <Track><id>102</id><Channel>102</Channel><Enable>false</Enable></Track>
	  <Track><id>201</id><Channel>201</Channel><Enable>false</Enable></Track>
	  <Track><id>202</id><Channel>202</Channel><Enable>false</Enable></Track>
	  <Track><id>301</id><Channel>301</Channel><Enable>true</Enable></Track>
	</TrackList>`
	rec := parseRecordTracks([]byte(xml))
	if !rec[1] {
		t.Errorf("ch1 should be recording")
	}
	if rec[2] {
		t.Errorf("ch2 should not be recording")
	}
	if !rec[3] {
		t.Errorf("ch3 should be recording")
	}
}

func TestCollectVideoInputStatus(t *testing.T) {
	// DVR: aggregate endpoint 404s, so per-channel fills each analog input.
	// ch2 is already known (merged from a proxy channel) and must not be re-probed.
	known := false
	chByNo := map[int]*Channel{
		1: {No: 1},
		2: {No: 2, Online: &known}, // pre-set ⇒ skip
		3: {No: 3},
	}
	doer := &pathDoer{bodies: map[string]string{
		// "/ISAPI/System/Video/inputs/channels/status" absent ⇒ aggregate 404s.
		"/ISAPI/System/Video/inputs/channels/1/status": `<VideoInputChannelStatus><online>true</online></VideoInputChannelStatus>`,
		"/ISAPI/System/Video/inputs/channels/3/status": `<VideoInputChannelStatus><signalStatus>noVideo</signalStatus></VideoInputChannelStatus>`,
	}}
	cl := NewClient("http://10.0.0.10", "u", "p", doer)
	probes := collectVideoInputStatus(context.Background(), cl, chByNo, []int{1, 2, 3})

	if chByNo[1].Online == nil || !*chByNo[1].Online {
		t.Errorf("ch1 should be online")
	}
	if chByNo[3].Online == nil || *chByNo[3].Online {
		t.Errorf("ch3 should be offline (no video)")
	}
	if doer.calls["/ISAPI/System/Video/inputs/channels/2/status"] != 0 {
		t.Errorf("ch2 was already known and must not be re-probed")
	}
	if len(probes) == 0 {
		t.Errorf("expected probe records to be returned")
	}
}

// A DVR whose per-channel status endpoint isn't supported (everything 404s) must
// not be guessed offline, and must stop after the aggregate + one capability probe.
func TestCollectVideoInputStatus_Unsupported(t *testing.T) {
	chByNo := map[int]*Channel{1: {No: 1}, 2: {No: 2}, 3: {No: 3}}
	doer := &pathDoer{bodies: map[string]string{}} // every path 404s
	cl := NewClient("http://10.0.0.10", "u", "p", doer)
	collectVideoInputStatus(context.Background(), cl, chByNo, []int{1, 2, 3})

	for no, ch := range chByNo {
		if ch.Online != nil {
			t.Errorf("ch%d should stay unknown when endpoint unsupported, got %v", no, *ch.Online)
		}
	}
	// aggregate (1) + capability probe on first pending (1) = 2 total; no storm.
	total := 0
	for _, n := range doer.calls {
		total += n
	}
	if total != 2 {
		t.Errorf("expected 2 probes (aggregate + capability), got %d (%v)", total, doer.calls)
	}
}

func TestBodySample(t *testing.T) {
	if got := bodySample([]byte("  <a>\n  <b>x</b>\t</a>  ")); got != "<a> <b>x</b> </a>" {
		t.Errorf("whitespace collapse = %q", got)
	}
	long := make([]byte, 0, 1000)
	for i := 0; i < 1000; i++ {
		long = append(long, 'x')
	}
	got := bodySample(long)
	if r := []rune(got); len(r) > 282 { // 280 + " …"
		t.Errorf("not truncated: %d runes", len(r))
	}
}
