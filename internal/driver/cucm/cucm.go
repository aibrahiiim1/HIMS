// Package cucm is the HIMS driver boundary for Cisco Unified Communications
// Manager (call manager / PBX). Fingerprint identifies CUCM by its HTTP banner
// or its AXL/Tomcat 8443 service; Collect lists the registered phones via AXL
// (SOAP over HTTP Basic) and maps them into Facts.Phones.
//
// Live-validation trigger: requires a real CUCM publisher + an AXL-enabled
// application user; the AXL schema version must match the CUCM release. The
// SOAP-response parsing is exercised by internal/cucm tests against a sample
// listPhone payload.
package cucm

import (
	"context"
	"fmt"
	"strings"

	cc "github.com/coralsearesorts/hims/internal/cucm"
	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
)

// Driver identifies CUCM and collects its phone registry.
type Driver struct{}

// New returns the driver.
func New() *Driver { return &Driver{} }

// Name implements driver.Driver.
func (*Driver) Name() string { return "cucm" }

// Template implements driver.Driver.
func (*Driver) Template() string { return "pbx" }

// Fingerprint scores CUCM evidence by HTTP banner / title. CUCM fronts its UI
// and AXL on Tomcat 8443; the "cisco unified" string is the authoritative hint.
func (*Driver) Fingerprint(p driver.Probe) driver.Match {
	banner := strings.ToLower(p.HTTPServer + " " + hint(p, "http_title"))
	if strings.Contains(banner, "cisco unified") || strings.Contains(banner, "communicationmanager") {
		return driver.Match{Confidence: 75, Category: domain.CatPBX}
	}
	return driver.NoMatch
}

func hint(p driver.Probe, k string) string {
	if p.Hints == nil {
		return ""
	}
	return p.Hints[k]
}

// Session carries the AXL client.
type Session struct {
	driver.SessionBase
	Client *cc.Client
	Ctx    context.Context //nolint:containedctx
}

// Collect lists phones via AXL and maps them into driver.Facts.
func (d *Driver) Collect(sess driver.Session, _ driver.Probe) (driver.Facts, error) {
	cs, ok := sess.(*Session)
	if !ok {
		return driver.Facts{}, fmt.Errorf("cucm: expected *Session, got %T", sess)
	}
	phones, err := cs.Client.ListPhones(cs.Ctx)
	if err != nil {
		return driver.Facts{}, err
	}
	f := driver.Facts{Vendor: "Cisco", KV: map[string]string{}, Raw: map[string]any{}}
	for _, p := range phones {
		f.Phones = append(f.Phones, driver.PhoneSnap{
			Name: p.Name, Model: p.Model, Description: p.Description, DevicePool: p.DevicePool,
		})
	}
	f.KV["phone_count"] = fmt.Sprintf("%d", len(phones))
	return f, nil
}
