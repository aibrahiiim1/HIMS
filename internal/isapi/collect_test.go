package isapi

import "testing"

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
