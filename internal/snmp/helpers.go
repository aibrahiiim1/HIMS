package snmp

import (
	"fmt"
	"strconv"
	"strings"
)

// PDUTypeName returns a short, stable label for a PDU type (used by the MIB
// Explorer to show the value type of each walked row).
func PDUTypeName(t PDUType) string {
	switch t {
	case TypeInt:
		return "Integer"
	case TypeUInt32:
		return "Unsigned32"
	case TypeCounter32:
		return "Counter32"
	case TypeCounter64:
		return "Counter64"
	case TypeGauge32:
		return "Gauge32"
	case TypeTimeTicks:
		return "TimeTicks"
	case TypeOctetString:
		return "OctetString"
	case TypeOIDValue:
		return "OID"
	case TypeIPAddress:
		return "IpAddress"
	case TypeNoSuchObject:
		return "noSuchObject"
	case TypeNoSuchInstance:
		return "noSuchInstance"
	case TypeEndOfMIBView:
		return "endOfMibView"
	}
	return "other"
}

// PDUDisplay renders a PDU value for human inspection, choosing the most useful
// representation: a 6-byte OctetString → MAC; an all-printable OctetString →
// text; any other binary → hex; IpAddress → dotted quad; numbers as decimal.
// This is what makes a raw MIB walk legible (MAC / IP / hex) without guessing.
func PDUDisplay(p PDU) string {
	if b, ok := p.Value.([]byte); ok {
		if p.Type == TypeOctetString || p.Type == TypeOther {
			if len(b) == 6 && !allPrintable(b) {
				return macHex(b)
			}
			if len(b) > 0 && allPrintable(b) {
				return string(b)
			}
			if len(b) == 0 {
				return ""
			}
			return "0x" + bytesHex(b)
		}
	}
	return PDUString(p)
}

func allPrintable(b []byte) bool {
	for _, c := range b {
		if c < 0x20 || c >= 0x7f {
			return false
		}
	}
	return true
}

func macHex(b []byte) string {
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", b[0], b[1], b[2], b[3], b[4], b[5])
}

func bytesHex(b []byte) string {
	const h = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, h[c>>4], h[c&0x0f])
	}
	return string(out)
}

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
	if !oidPrefixMatch(oid, prefix) {
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
// without leading dots). The match is component-boundary aware: root
// "1.3.6.1.2.6.1" matches "...6.1" and "...6.1.x" but NOT the sibling "...6.12".
func HasOIDPrefix(oid, root string) bool {
	oid = strings.TrimPrefix(oid, ".")
	root = strings.TrimPrefix(root, ".")
	return oidPrefixMatch(oid, root)
}

// oidPrefixMatch reports whether prefix is a sub-identifier-aligned prefix of
// oid: either equal, or prefix is immediately followed by a '.' in oid. This is
// what callers actually mean by "OID under this subtree" — a plain string
// HasPrefix wrongly treats ".6.1" as a prefix of the sibling ".6.12".
func oidPrefixMatch(oid, prefix string) bool {
	if prefix == "" {
		return true
	}
	if !strings.HasPrefix(oid, prefix) {
		return false
	}
	return len(oid) == len(prefix) || oid[len(prefix)] == '.'
}
