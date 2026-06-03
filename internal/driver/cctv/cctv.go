// Package cctv is the HIMS driver for IP cameras and NVR/DVR recorders. It
// fingerprints by HTTP banner (Hikvision/Dahua/Axis), open RTSP (554), and
// ONVIF discovery ports — the cheap evidence available at probe time.
//
// Deep collection (channel inventory, codec/resolution, recording state) is
// an ONVIF (SOAP) + vendor-REST transport that's pure-Go-feasible but not yet
// wired; it is deferred with a trigger (see PROGRESS Phase 7). Reachability
// (TCP 554/80) is already monitored by the monitoring engine today.
package cctv

import (
	"strings"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
)

// Driver identifies CCTV devices (cameras + recorders).
type Driver struct{}

// New returns the driver.
func New() *Driver { return &Driver{} }

// Name implements driver.Driver.
func (*Driver) Name() string { return "cctv" }

// Template implements driver.Driver. Cameras and NVRs share the cctv
// template family; the concrete category is set per-probe in Fingerprint.
func (*Driver) Template() string { return "camera" }

// recorderHints mark a device as an NVR/DVR rather than a single camera.
var recorderHints = []string{"nvr", "dvr", "recorder", "ivms"}

// cameraVendors are HTTP banner / hint substrings that identify CCTV gear.
var cameraVendors = []string{"hikvision", "dahua", "dvrdvs", "axis", "uniview", "webs", "hipcam", "go-ahead"}

// Fingerprint scores CCTV evidence:
//
//	75 — a known camera-vendor HTTP banner (authoritative-ish for the class)
//	60 — RTSP (554) open, optionally with an ONVIF port (80/8000)
//
// A recorder hint in the banner flips the category to NVR.
func (*Driver) Fingerprint(p driver.Probe) driver.Match {
	banner := strings.ToLower(p.HTTPServer + " " + hint(p, "http_title") + " " + hint(p, "onvif"))

	category := domain.CatCamera
	for _, h := range recorderHints {
		if strings.Contains(banner, h) {
			category = domain.CatNVR
			break
		}
	}

	for _, v := range cameraVendors {
		if strings.Contains(banner, v) {
			return driver.Match{Confidence: 75, Category: category}
		}
	}
	// RTSP open is a strong-but-not-vendor signal of a camera/recorder.
	if p.HasTCPPort(554) {
		return driver.Match{Confidence: 60, Category: category}
	}
	return driver.NoMatch
}

func hint(p driver.Probe, k string) string {
	if p.Hints == nil {
		return ""
	}
	return p.Hints[k]
}
