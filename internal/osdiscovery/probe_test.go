package osdiscovery

import (
	"testing"

	"github.com/coralsearesorts/hims/internal/domain"
)

func TestParseISAPIDeviceInfo_Namespaced(t *testing.T) {
	// Real Hikvision firmware wraps the doc in a vendor namespace.
	body := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<DeviceInfo version="2.0" xmlns="http://www.hikvision.com/ver20/XMLSchema">
  <deviceName>HK-NVR</deviceName>
  <deviceType>NVR</deviceType>
  <model>DS-7608NI-K2/8P</model>
  <serialNumber>DS-7608NI...</serialNumber>
</DeviceInfo>`)
	dt, model := parseISAPIDeviceInfo(body)
	if dt != "NVR" {
		t.Errorf("deviceType = %q, want NVR", dt)
	}
	if model != "DS-7608NI-K2/8P" {
		t.Errorf("model = %q", model)
	}
}

func TestParseISAPIDeviceInfo_Garbage(t *testing.T) {
	dt, model := parseISAPIDeviceInfo([]byte("<html>login</html>"))
	if dt != "" || model != "" {
		t.Errorf("non-ISAPI body should yield empty, got %q/%q", dt, model)
	}
}

func TestObservation_NVRClassification(t *testing.T) {
	// A Hikvision NVR: RTSP + HTTPS open, ISAPI says NVR. Must classify as nvr,
	// not camera — the headline 172.21.210.x fix.
	obs := Observation{
		OpenTCP:         []int{80, 443, 554, 8000},
		HTTPServer:      "App-webs/",
		ISAPIPresent:    true,
		ISAPIDeviceType: "NVR",
		ISAPIModel:      "DS-7608NI-K2",
	}
	r := obs.Result()
	if r.Category != string(domain.CatNVR) {
		t.Errorf("NVR observation → %q, want nvr", r.Category)
	}
	if r.OSFamily != domain.OSFamilyEmbedded {
		t.Errorf("os_family = %q, want embedded", r.OSFamily)
	}
	if r.Confidence < 90 {
		t.Errorf("confidence %d, want >=90", r.Confidence)
	}
}

func TestObservation_ISAPIPresentNoCreds(t *testing.T) {
	// 401 on ISAPI without creds: still a (weak) camera/embedded signal.
	obs := Observation{OpenTCP: []int{443, 554}, ISAPIPresent: true}
	r := obs.Result()
	if r.Category != string(domain.CatCamera) {
		t.Errorf("ISAPI-present → %q, want camera", r.Category)
	}
	if r.OSFamily != domain.OSFamilyEmbedded {
		t.Errorf("os_family = %q, want embedded", r.OSFamily)
	}
}

func TestObservation_LinuxServer(t *testing.T) {
	obs := Observation{OpenTCP: []int{22, 443}, SSHBanner: "SSH-2.0-OpenSSH_8.0p1 Ubuntu-6"}
	r := obs.Result()
	if r.OSFamily != domain.OSFamilyLinux {
		t.Errorf("os_family = %q, want linux", r.OSFamily)
	}
	if r.Subtype != "linux_server" {
		t.Errorf("subtype = %q, want linux_server", r.Subtype)
	}
}

func TestObservation_EmptyIsUnknown(t *testing.T) {
	if r := (Observation{}).Result(); r.Category != string(domain.CatUnknown) {
		t.Errorf("empty observation → %q, want unknown", r.Category)
	}
}
