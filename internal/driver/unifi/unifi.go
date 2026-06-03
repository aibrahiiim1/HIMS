// Package unifi is the HIMS driver boundary for Ubiquiti UniFi wireless
// controllers. Controller classification is done by the wlan_controller driver
// (banner/port fingerprint); this driver does the deep REST collection (login
// + AP list) over a session, so Fingerprint is NoMatch (collection-only).
package unifi

import (
	"context"
	"fmt"

	"github.com/coralsearesorts/hims/internal/driver"
	uc "github.com/coralsearesorts/hims/internal/unifi"
)

// Driver collects UniFi AP inventory.
type Driver struct{}

// New returns the driver.
func New() *Driver { return &Driver{} }

// Name implements driver.Driver.
func (*Driver) Name() string { return "unifi" }

// Template implements driver.Driver.
func (*Driver) Template() string { return "wireless_controller" }

// Fingerprint is NoMatch — wlan_controller classifies; this is the deep path.
func (*Driver) Fingerprint(driver.Probe) driver.Match { return driver.NoMatch }

// Session carries the UniFi client (already logged in).
type Session struct {
	driver.SessionBase
	Client *uc.Client
	Ctx    context.Context //nolint:containedctx
}

// Collect lists APs and maps them into driver.Facts (WLAN summary + APs).
func (d *Driver) Collect(sess driver.Session, _ driver.Probe) (driver.Facts, error) {
	us, ok := sess.(*Session)
	if !ok {
		return driver.Facts{}, fmt.Errorf("unifi: expected *Session, got %T", sess)
	}
	aps, err := us.Client.ListAPs(us.Ctx)
	if err != nil {
		return driver.Facts{}, err
	}
	f := driver.Facts{Vendor: "Ubiquiti", KV: map[string]string{}, Raw: map[string]any{}}
	var clients int32
	for _, ap := range aps {
		clients += ap.ClientCount
		f.APs = append(f.APs, driver.APSnap{
			Name: ap.Name, MAC: ap.MAC, Model: ap.Model, IP: ap.IP,
			Status: ap.Status, ClientCount: ap.ClientCount,
		})
	}
	f.WLAN = &driver.WLANSnap{Vendor: "Ubiquiti", APCount: int32(len(aps)), ClientCount: clients}
	return f, nil
}
