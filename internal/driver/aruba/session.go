package aruba

import "github.com/coralsearesorts/hims/internal/driver/swsnmp"

// Session aliases the shared SNMP session (swsnmp.Session) so the discovery
// pipeline builds one concrete session type for every SNMP driver. Kept as a
// package-local name for readability + test ergonomics.
type Session = swsnmp.Session
