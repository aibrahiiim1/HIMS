// Package drivers wires the built-in driver set into a registry. It sits
// above the driver-contract package (internal/driver) and the individual
// driver packages, so the contract has no dependency on any concrete
// driver (no import cycle).
package drivers

import (
	"github.com/coralsearesorts/hims/internal/driver"
	"github.com/coralsearesorts/hims/internal/driver/aruba"
)

// Builtin returns a Registry populated with the drivers compiled into this
// build. Phase 0 ships the Aruba/HPE reference driver; later phases append
// cisco_ios, huawei_vrp, fortigate, hikvision, vmware, …
func Builtin() *driver.Registry {
	r := driver.NewRegistry()
	r.Register(aruba.New())
	return r
}
