// Command hims-api is the HIMS API server entrypoint. Phase 0 is a
// placeholder that proves wiring compiles; the HTTP server, routes, and
// auth land in Phase 1 alongside the first device-type slice.
package main

import (
	"fmt"

	"github.com/coralsearesorts/hims/internal/drivers"
)

func main() {
	reg := drivers.Builtin()
	fmt.Printf("hims-api (phase 0 scaffold) — %d driver(s) registered: %v\n",
		len(reg.Names()), reg.Names())
}
