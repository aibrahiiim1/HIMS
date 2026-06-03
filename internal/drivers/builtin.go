// Package drivers wires the built-in driver set into a registry. It sits
// above the driver-contract package (internal/driver) and the individual
// driver packages, so the contract has no dependency on any concrete
// driver (no import cycle).
package drivers

import (
	"github.com/coralsearesorts/hims/internal/driver"
	"github.com/coralsearesorts/hims/internal/driver/aruba"
	"github.com/coralsearesorts/hims/internal/driver/cctv"
	"github.com/coralsearesorts/hims/internal/driver/cisco"
	"github.com/coralsearesorts/hims/internal/driver/esxi"
	"github.com/coralsearesorts/hims/internal/driver/fortigate"
	"github.com/coralsearesorts/hims/internal/driver/hostsnmp"
	"github.com/coralsearesorts/hims/internal/driver/huawei"
	"github.com/coralsearesorts/hims/internal/driver/wireless"
)

// Builtin returns a Registry populated with the drivers compiled into this
// build. Phase 1: Aruba/HPE. Phase 2: Cisco IOS + Huawei VRP. Phase 3a:
// host_snmp (servers). Phase 4: fortigate (firewall). Later phases append
// hikvision, vmware, …
func Builtin() *driver.Registry {
	r := driver.NewRegistry()
	r.Register(aruba.New())
	r.Register(cisco.New())
	r.Register(huawei.New())
	r.Register(hostsnmp.New())
	r.Register(fortigate.New())
	r.Register(esxi.New())
	r.Register(cctv.New())
	r.Register(wireless.New())
	return r
}
