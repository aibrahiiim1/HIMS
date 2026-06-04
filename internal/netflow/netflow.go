// Package netflow decodes NetFlow v5 export datagrams and aggregates them into
// top-talker / protocol / conversation summaries for the NetFlow Analytics
// feature (#12). NetFlow v5 has a fixed wire format (no templates), so decoding
// is a pure byte parse and fully unit-testable. v9/IPFIX (template-based) are a
// documented future extension. No I/O lives here — the UDP collector + storage
// sit in the API layer.
package netflow

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"sort"
)

// Header is the NetFlow v5 export-packet header (24 bytes).
type Header struct {
	Version      uint16
	Count        uint16
	SysUptimeMs  uint32
	UnixSecs     uint32
	FlowSequence uint32
}

// Flow is one decoded v5 flow record (the fields we use).
type Flow struct {
	Src      netip.Addr
	Dst      netip.Addr
	SrcPort  uint16
	DstPort  uint16
	Protocol uint8
	Packets  uint64
	Bytes    uint64
}

const (
	v5HeaderLen = 24
	v5RecordLen = 48
)

// Decode parses a NetFlow v5 datagram into its header and flow records. It
// validates the version, count, and length so a malformed/oversized packet is
// refused rather than mis-parsed.
func Decode(data []byte) (Header, []Flow, error) {
	if len(data) < v5HeaderLen {
		return Header{}, nil, fmt.Errorf("netflow: packet too short (%d bytes)", len(data))
	}
	h := Header{
		Version:      binary.BigEndian.Uint16(data[0:2]),
		Count:        binary.BigEndian.Uint16(data[2:4]),
		SysUptimeMs:  binary.BigEndian.Uint32(data[4:8]),
		UnixSecs:     binary.BigEndian.Uint32(data[8:12]),
		FlowSequence: binary.BigEndian.Uint32(data[16:20]),
	}
	if h.Version != 5 {
		return h, nil, fmt.Errorf("netflow: unsupported version %d (only v5 is decoded)", h.Version)
	}
	if h.Count == 0 || h.Count > 30 {
		return h, nil, fmt.Errorf("netflow: invalid record count %d", h.Count)
	}
	need := v5HeaderLen + int(h.Count)*v5RecordLen
	if len(data) < need {
		return h, nil, fmt.Errorf("netflow: truncated packet: have %d, need %d for %d records", len(data), need, h.Count)
	}
	flows := make([]Flow, 0, h.Count)
	for i := 0; i < int(h.Count); i++ {
		b := data[v5HeaderLen+i*v5RecordLen:]
		flows = append(flows, Flow{
			Src:      v4(b[0:4]),
			Dst:      v4(b[4:8]),
			Packets:  uint64(binary.BigEndian.Uint32(b[16:20])),
			Bytes:    uint64(binary.BigEndian.Uint32(b[20:24])),
			SrcPort:  binary.BigEndian.Uint16(b[32:34]),
			DstPort:  binary.BigEndian.Uint16(b[34:36]),
			Protocol: b[38],
		})
	}
	return h, flows, nil
}

func v4(b []byte) netip.Addr {
	return netip.AddrFrom4([4]byte{b[0], b[1], b[2], b[3]})
}

// Counter accumulates bytes + packets.
type Counter struct {
	Bytes   uint64 `json:"bytes"`
	Packets uint64 `json:"packets"`
}

func (c *Counter) add(b, p uint64) { c.Bytes += b; c.Packets += p }

// Summary is a rolling aggregation keyed by host, protocol, and conversation.
type Summary struct {
	Total         Counter
	ByHost        map[string]*Counter // per IP (counts where it is src or dst)
	ByProtocol    map[string]*Counter // per protocol name
	ByConversation map[string]*Counter // "src→dst"
}

// NewSummary returns an empty, ready-to-merge Summary.
func NewSummary() *Summary {
	return &Summary{
		ByHost:         map[string]*Counter{},
		ByProtocol:     map[string]*Counter{},
		ByConversation: map[string]*Counter{},
	}
}

func (s *Summary) bump(m map[string]*Counter, key string, b, p uint64) {
	c := m[key]
	if c == nil {
		c = &Counter{}
		m[key] = c
	}
	c.add(b, p)
}

// Add folds the flows into the summary.
func (s *Summary) Add(flows []Flow) {
	for _, f := range flows {
		s.Total.add(f.Bytes, f.Packets)
		s.bump(s.ByHost, f.Src.String(), f.Bytes, f.Packets)
		s.bump(s.ByHost, f.Dst.String(), f.Bytes, f.Packets)
		s.bump(s.ByProtocol, ProtocolName(f.Protocol), f.Bytes, f.Packets)
		s.bump(s.ByConversation, f.Src.String()+"→"+f.Dst.String(), f.Bytes, f.Packets)
	}
}

// Entry is a ranked aggregation row.
type Entry struct {
	Label   string `json:"label"`
	Bytes   uint64 `json:"bytes"`
	Packets uint64 `json:"packets"`
}

// TopN returns the n highest-byte entries of a map, descending (ties by label).
func TopN(m map[string]*Counter, n int) []Entry {
	out := make([]Entry, 0, len(m))
	for k, c := range m {
		out = append(out, Entry{Label: k, Bytes: c.Bytes, Packets: c.Packets})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bytes != out[j].Bytes {
			return out[i].Bytes > out[j].Bytes
		}
		return out[i].Label < out[j].Label
	})
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// ProtocolName maps an IP protocol number to a short name.
func ProtocolName(p uint8) string {
	switch p {
	case 1:
		return "icmp"
	case 6:
		return "tcp"
	case 17:
		return "udp"
	case 47:
		return "gre"
	case 50:
		return "esp"
	case 58:
		return "icmpv6"
	case 89:
		return "ospf"
	case 132:
		return "sctp"
	default:
		return fmt.Sprintf("proto-%d", p)
	}
}
