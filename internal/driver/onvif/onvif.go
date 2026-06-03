// Package onvif is the HIMS driver boundary for ONVIF IP cameras. Camera
// classification is done by the cctv driver (banner/RTSP fingerprint); this
// driver does the deep ONVIF collection over a SOAP session, so its
// Fingerprint is NoMatch (collection-only) to avoid duplicate classification.
package onvif

import (
	"context"
	"fmt"

	"github.com/coralsearesorts/hims/internal/driver"
	ov "github.com/coralsearesorts/hims/internal/onvif"
)

// Driver collects ONVIF camera inventory.
type Driver struct{}

// New returns the driver.
func New() *Driver { return &Driver{} }

// Name implements driver.Driver.
func (*Driver) Name() string { return "onvif_camera" }

// Template implements driver.Driver.
func (*Driver) Template() string { return "camera" }

// Fingerprint is NoMatch — the cctv driver classifies cameras; this is the
// credentialed deep-collection path.
func (*Driver) Fingerprint(driver.Probe) driver.Match { return driver.NoMatch }

// Session carries the ONVIF SOAP client.
type Session struct {
	driver.SessionBase
	Client *ov.Client
	Ctx    context.Context //nolint:containedctx
}

// Collect fetches device info + profiles and maps them into driver.Facts.
func (d *Driver) Collect(sess driver.Session, _ driver.Probe) (driver.Facts, error) {
	os, ok := sess.(*Session)
	if !ok {
		return driver.Facts{}, fmt.Errorf("onvif_camera: expected *Session, got %T", sess)
	}
	info, err := ov.Collect(os.Ctx, os.Client)
	if err != nil {
		return driver.Facts{}, err
	}
	f := driver.Facts{KV: map[string]string{}, Raw: map[string]any{}}
	f.Vendor = info.Manufacturer
	f.Model = info.Model
	f.OSVersion = info.Firmware
	f.Serial = info.Serial
	f.Camera = &driver.CameraSnap{
		Manufacturer: info.Manufacturer, Model: info.Model,
		Resolution: info.Resolution(), ONVIFUrl: os.Client.BaseURL,
	}
	return f, nil
}
