// Command hims-collector runs discovery + monitoring. Phase 0 is a
// placeholder that proves the driver registry wires up; the discovery
// pipeline + SNMP/SSH transport + monitoring scheduler land in Phase 1.
package main

import (
	"fmt"

	"github.com/coralsearesorts/hims/internal/drivers"
)

func main() {
	reg := drivers.Builtin()
	fmt.Printf("hims-collector (phase 0 scaffold) — drivers: %v\n", reg.Names())
}
