package aruba

import (
	"context"

	"github.com/coralsearesorts/hims/internal/driver"
	"github.com/coralsearesorts/hims/internal/snmp"
)

// Session wraps a live SNMP client + context for the duration of one collect
// call. It satisfies driver.Session via the embedded SessionBase marker.
type Session struct {
	driver.SessionBase
	Client snmp.Client
	Ctx    context.Context //nolint:containedctx // deliberate: driver.Session is transport-agnostic
}
