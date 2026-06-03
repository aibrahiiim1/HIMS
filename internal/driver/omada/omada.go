// Package omada is the HIMS driver boundary for TP-Link Omada controllers.
// Collection-only (Fingerprint NoMatch — the wlan_controller driver
// classifies); Collect maps the Omada AP list into Facts.WLAN + Facts.APs.
package omada

import (
	"context"
	"fmt"

	"github.com/coralsearesorts/hims/internal/driver"
	oc "github.com/coralsearesorts/hims/internal/omada"
)

// Driver collects Omada AP inventory.
type Driver struct{}

// New returns the driver.
func New() *Driver { return &Driver{} }

// Name implements driver.Driver.
func (*Driver) Name() string { return "omada" }

// Template implements driver.Driver.
func (*Driver) Template() string { return "wireless_controller" }

// Fingerprint is NoMatch — wlan_controller classifies; this is the deep path.
func (*Driver) Fingerprint(driver.Probe) driver.Match { return driver.NoMatch }

// Session carries the (logged-in) Omada client.
type Session struct {
	driver.SessionBase
	Client *oc.Client
	Ctx    context.Context //nolint:containedctx
}

// Collect lists APs and maps them into driver.Facts.
func (d *Driver) Collect(sess driver.Session, _ driver.Probe) (driver.Facts, error) {
	os, ok := sess.(*Session)
	if !ok {
		return driver.Facts{}, fmt.Errorf("omada: expected *Session, got %T", sess)
	}
	aps, err := os.Client.ListAPs(os.Ctx)
	if err != nil {
		return driver.Facts{}, err
	}
	f := driver.Facts{Vendor: "TP-Link", KV: map[string]string{}, Raw: map[string]any{}}
	var clients int32
	for _, ap := range aps {
		clients += ap.ClientCount
		f.APs = append(f.APs, driver.APSnap{
			Name: ap.Name, MAC: ap.MAC, Model: ap.Model, IP: ap.IP, Status: ap.Status, ClientCount: ap.ClientCount,
		})
	}
	f.WLAN = &driver.WLANSnap{Vendor: "TP-Link", APCount: int32(len(aps)), ClientCount: clients}
	return f, nil
}
