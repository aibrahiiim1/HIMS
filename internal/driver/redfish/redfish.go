// Package redfish is the HIMS driver for server out-of-band management
// controllers (HPE iLO, Dell iDRAC) reached over the Redfish HTTP/JSON API.
// It fingerprints a BMC by its HTTPS banner and collects normalized BMC
// inventory + hardware health via the internal/redfish client.
//
// Unlike the SNMP drivers this collects over HTTP Basic auth, so its Session
// carries a *redfish.Client (built from an http_basic credential) rather than
// an SNMP client. The collector path that wires this lives in the collector's
// -redfish mode and the API redfish-collect endpoint.
package redfish

import (
	"context"
	"fmt"
	"strings"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
	rf "github.com/coralsearesorts/hims/internal/redfish"
)

// Driver identifies + collects Redfish BMCs.
type Driver struct{}

// New returns the driver.
func New() *Driver { return &Driver{} }

// Name implements driver.Driver.
func (*Driver) Name() string { return "redfish_bmc" }

// Template implements driver.Driver. A BMC represents the physical server's
// management plane, so it renders with the server template (+ a BMC section).
func (*Driver) Template() string { return "server" }

// Fingerprint matches an iLO/iDRAC/Redfish HTTPS banner. Confidence 72 — above
// a bare web server but below an authoritative switch OID (90), so a managed
// switch with a web UI never misclassifies as a BMC.
func (*Driver) Fingerprint(p driver.Probe) driver.Match {
	banner := strings.ToLower(p.HTTPServer + " " + hint(p, "http_title") + " " + hint(p, "redfish"))
	for _, kw := range []string{"ilo", "idrac", "redfish", "integrated lights-out", "ibmc"} {
		if strings.Contains(banner, kw) {
			return driver.Match{Confidence: 72, Category: domain.CatServer}
		}
	}
	return driver.NoMatch
}

// Session carries the Redfish client for collection.
type Session struct {
	driver.SessionBase
	Client *rf.Client
	Ctx    context.Context //nolint:containedctx
}

// Collect fetches BMC inventory + health and maps it into driver.Facts.
func (d *Driver) Collect(sess driver.Session, _ driver.Probe) (driver.Facts, error) {
	rs, ok := sess.(*Session)
	if !ok {
		return driver.Facts{}, fmt.Errorf("redfish_bmc: expected *Session, got %T", sess)
	}
	bf, err := rf.Collect(rs.Ctx, rs.Client)
	if err != nil {
		return driver.Facts{}, err
	}
	f := driver.Facts{KV: map[string]string{}, Raw: map[string]any{}}
	f.Vendor = bf.Vendor
	f.Model = bf.Model
	f.Serial = bf.Serial
	f.OSVersion = bf.BiosVersion
	if bf.ProcessorCount > 0 {
		f.KV["cpu.count"] = fmt.Sprintf("%d", bf.ProcessorCount)
	}
	if bf.ProcessorModel != "" {
		f.KV["cpu.model"] = bf.ProcessorModel
	}
	if bf.MemoryGiB > 0 {
		f.KV["memory.total_bytes"] = fmt.Sprintf("%.0f", bf.MemoryGiB*1024*1024*1024)
	}
	f.BMC = &driver.BMCSnap{
		Vendor: bf.Vendor, ControllerKind: bf.ControllerKind, Model: bf.Model,
		Serial: bf.Serial, FirmwareVersion: bf.FirmwareVersion,
		PowerState: bf.PowerState, Health: bf.Health,
	}
	for _, s := range bf.Sensors {
		f.BMCSensors = append(f.BMCSensors, driver.BMCSensorSnap{
			Kind: s.Kind, Name: s.Name, Status: s.Status,
			Reading: s.Reading, Unit: s.Unit, HasReading: s.HasReading,
		})
	}
	return f, nil
}

func hint(p driver.Probe, k string) string {
	if p.Hints == nil {
		return ""
	}
	return p.Hints[k]
}
