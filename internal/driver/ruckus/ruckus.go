// Package ruckus is the HIMS driver boundary for Ruckus SmartZone controllers.
// Collection-only (Fingerprint NoMatch — the wlan_controller driver
// classifies); Collect maps the SmartZone AP list into Facts.WLAN + Facts.APs.
package ruckus

import (
	"context"
	"fmt"

	"github.com/coralsearesorts/hims/internal/driver"
	rc "github.com/coralsearesorts/hims/internal/ruckus"
)

// Driver collects Ruckus SmartZone AP inventory.
type Driver struct{}

// New returns the driver.
func New() *Driver { return &Driver{} }

// Name implements driver.Driver.
func (*Driver) Name() string { return "ruckus" }

// Template implements driver.Driver.
func (*Driver) Template() string { return "wireless_controller" }

// Fingerprint is NoMatch — wlan_controller classifies; this is the deep path.
func (*Driver) Fingerprint(driver.Probe) driver.Match { return driver.NoMatch }

// Session carries the (logged-in) Ruckus client.
type Session struct {
	driver.SessionBase
	Client *rc.Client
	Ctx    context.Context //nolint:containedctx
}

// Collect lists APs and maps them into driver.Facts.
func (d *Driver) Collect(sess driver.Session, _ driver.Probe) (driver.Facts, error) {
	rs, ok := sess.(*Session)
	if !ok {
		return driver.Facts{}, fmt.Errorf("ruckus: expected *Session, got %T", sess)
	}
	aps, err := rs.Client.ListAPs(rs.Ctx)
	if err != nil {
		return driver.Facts{}, err
	}
	f := driver.Facts{Vendor: "Ruckus", KV: map[string]string{}, Raw: map[string]any{}}
	var clients int32
	for _, ap := range aps {
		clients += ap.ClientCount
		f.APs = append(f.APs, driver.APSnap{
			Name: ap.Name, MAC: ap.MAC, Model: ap.Model, IP: ap.IP, Status: ap.Status, ClientCount: ap.ClientCount,
		})
	}
	f.WLAN = &driver.WLANSnap{Vendor: "Ruckus", APCount: int32(len(aps)), ClientCount: clients}
	return f, nil
}
