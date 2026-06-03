package cctv

import (
	"testing"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
)

func TestFingerprint_HikvisionCamera(t *testing.T) {
	d := New()
	m := d.Fingerprint(driver.Probe{HTTPServer: "App-webs/ Hikvision", OpenTCPPorts: []int{80, 554}})
	if m.Confidence != 75 || m.Category != domain.CatCamera {
		t.Fatalf("hikvision = %+v; want 75 camera", m)
	}
}

func TestFingerprint_RecorderHintIsNVR(t *testing.T) {
	d := New()
	m := d.Fingerprint(driver.Probe{HTTPServer: "Dahua NVR", OpenTCPPorts: []int{80}})
	if m.Category != domain.CatNVR {
		t.Fatalf("recorder hint → %v; want nvr", m.Category)
	}
}

func TestFingerprint_RTSPOnly(t *testing.T) {
	d := New()
	m := d.Fingerprint(driver.Probe{OpenTCPPorts: []int{554}})
	if m.Confidence != 60 || m.Category != domain.CatCamera {
		t.Fatalf("rtsp-only = %+v; want 60 camera", m)
	}
}

func TestFingerprint_NoMatch(t *testing.T) {
	d := New()
	if m := d.Fingerprint(driver.Probe{HTTPServer: "nginx", OpenTCPPorts: []int{443}}); m.Confidence != 0 {
		t.Fatalf("plain web server should not match cctv; got %+v", m)
	}
}
