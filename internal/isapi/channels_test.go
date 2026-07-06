package isapi

import (
	"context"
	"testing"
	"time"
)

// TestCollectChannelStatus_RecorderOnlineOffline verifies the lightweight poll
// returns per-channel online/offline WITHOUT touching HDD/recording endpoints,
// and that a plain camera yields no channels.
func TestCollectChannelStatus_Recorder(t *testing.T) {
	doer := &pathDoer{bodies: map[string]string{
		"/ISAPI/System/deviceInfo": `<DeviceInfo><deviceName>NVR</deviceName><model>DS-9632NI-I8</model><deviceType>NVR</deviceType></DeviceInfo>`,
		"/ISAPI/ContentMgmt/InputProxy/channels": `<InputProxyChannelList>
			<InputProxyChannel><id>1</id><name>B1-1101</name><sourceInputPortDescriptor><ipAddress>172.21.210.48</ipAddress></sourceInputPortDescriptor></InputProxyChannel>
			<InputProxyChannel><id>2</id><name>B1-1102</name><sourceInputPortDescriptor><ipAddress>172.21.210.49</ipAddress></sourceInputPortDescriptor></InputProxyChannel></InputProxyChannelList>`,
		"/ISAPI/ContentMgmt/InputProxy/channels/status": `<InputProxyChannelStatusList>
			<InputProxyChannelStatus><id>1</id><online>false</online><chanDetectResult>offline</chanDetectResult></InputProxyChannelStatus>
			<InputProxyChannelStatus><id>2</id><online>true</online><chanDetectResult>connect</chanDetectResult></InputProxyChannelStatus></InputProxyChannelStatusList>`,
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	nvr, err := CollectChannelStatus(ctx, "172.21.210.1", "admin", "pass", doer, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !nvr.IsRecorder() || len(nvr.Channels) != 2 {
		t.Fatalf("want 2 channels on a recorder; got recorder=%v channels=%d", nvr.IsRecorder(), len(nvr.Channels))
	}
	byNo := map[int]Channel{}
	for _, c := range nvr.Channels {
		byNo[c.No] = c
	}
	if byNo[1].Online == nil || *byNo[1].Online {
		t.Errorf("channel 1 (210.48) must be OFFLINE; got %+v", byNo[1].Online)
	}
	if byNo[2].Online == nil || !*byNo[2].Online {
		t.Errorf("channel 2 must be ONLINE; got %+v", byNo[2].Online)
	}
	// Lightweight: HDD/recording endpoints must NOT be probed.
	if doer.calls["/ISAPI/ContentMgmt/Storage/hdd"] != 0 || doer.calls["/ISAPI/ContentMgmt/record/tracks"] != 0 {
		t.Errorf("status-only poll must not hit HDD/recording endpoints; calls=%v", doer.calls)
	}
}

func TestCollectChannelStatus_PlainCameraNoChannels(t *testing.T) {
	doer := &pathDoer{bodies: map[string]string{
		"/ISAPI/System/deviceInfo": `<DeviceInfo><model>DS-2CD1123G0E-I</model><deviceType>IPCamera</deviceType></DeviceInfo>`,
	}}
	nvr, err := CollectChannelStatus(context.Background(), "172.21.210.48", "admin", "pass", doer, nil)
	if err != nil {
		t.Fatal(err)
	}
	if nvr.IsRecorder() || len(nvr.Channels) != 0 {
		t.Fatalf("plain camera must yield no channels; got recorder=%v channels=%d", nvr.IsRecorder(), len(nvr.Channels))
	}
}
