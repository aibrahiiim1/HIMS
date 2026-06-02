package snmp

import (
	"fmt"
	"strconv"
	"strings"
)

// PDUString extracts a printable string from OctetString / IPAddress PDUs.
func PDUString(p PDU) string {
	switch v := p.Value.(type) {
	case string:
		return v
	case []byte:
		// Emit printable characters only; replace control bytes with dots.
		b := make([]byte, len(v))
		for i, c := range v {
			if c >= 0x20 && c < 0x7f {
				b[i] = c
			} else {
				b[i] = '.'
			}
		}
		return string(b)
	}
	return fmt.Sprintf("%v", p.Value)
}

// PDUInt64 extracts a signed integer from any numeric PDU type.
func PDUInt64(p PDU) (int64, bool) {
	switch p.Type {
	case TypeInt, TypeCounter32, TypeGauge32, TypeUInt32, TypeTimeTicks:
	default:
		return 0, false
	}
	switch v := p.Value.(type) {
	case int:
		return int64(v), true
	case int32:
		return int64(v), true
	case int64:
		return v, true
	case uint:
		return int64(v), true
	case uint32:
		return int64(v), true
	case uint64:
		return int64(v), true
	}
	return 0, false
}

// PDUCounter64 extracts a Counter64 value.
func PDUCounter64(p PDU) (int64, bool) {
	if p.Type != TypeCounter64 {
		return 0, false
	}
	switch v := p.Value.(type) {
	case uint64:
		if v > (1<<63 - 1) {
			return 1<<63 - 1, true
		}
		return int64(v), true
	case int64:
		return v, true
	}
	return 0, false
}

// PDUMACAddress renders an OctetString value as lowercase colon-hex MAC.
func PDUMACAddress(p PDU) string {
	b, ok := p.Value.([]byte)
	if !ok || len(b) != 6 {
		return ""
	}
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", b[0], b[1], b[2], b[3], b[4], b[5])
}

// TrimOIDPrefix strips an OID prefix from a longer OID and returns the
// suffix as a slice of uint32 sub-identifiers.  ok is false when the
// oid does not start with the prefix.
func TrimOIDPrefix(oid, prefix string) ([]uint32, bool) {
	prefix = strings.TrimPrefix(prefix, ".")
	oid = strings.TrimPrefix(oid, ".")
	if !strings.HasPrefix(oid, prefix) {
		return nil, false
	}
	rest := strings.TrimPrefix(oid[len(prefix):], ".")
	if rest == "" {
		return []uint32{}, true
	}
	parts := strings.Split(rest, ".")
	out := make([]uint32, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.ParseUint(p, 10, 32)
		if err != nil {
			return nil, false
		}
		out = append(out, uint32(n))
	}
	return out, true
}

// ColumnAndIndex splits a walked row OID into the column number (first sub-
// identifier after the table entry prefix) and the remaining index.
func ColumnAndIndex(oid, entryRoot string) (column uint32, index []uint32, ok bool) {
	suffix, ok := TrimOIDPrefix(oid, entryRoot)
	if !ok || len(suffix) < 2 {
		return 0, nil, false
	}
	return suffix[0], suffix[1:], true
}

// HasOIDPrefix reports whether oid starts with root (either way, with or
// without leading dots).
func HasOIDPrefix(oid, root string) bool {
	oid = strings.TrimPrefix(oid, ".")
	root = strings.TrimPrefix(root, ".")
	return strings.HasPrefix(oid, root)
}
