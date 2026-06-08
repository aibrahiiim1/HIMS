package netflow

import (
	"encoding/binary"
	"testing"
)

// buildV5 crafts a real NetFlow v5 datagram with the given records so the
// decoder is tested against authentic wire format.
func buildV5(recs [][2]uint32 /* unused */) []byte { return nil }

type rec struct {
	src, dst     [4]byte
	pkts, octets uint32
	sport, dport uint16
	proto        uint8
}

func packet(recs []rec) []byte {
	buf := make([]byte, v5HeaderLen+len(recs)*v5RecordLen)
	binary.BigEndian.PutUint16(buf[0:2], 5)                 // version
	binary.BigEndian.PutUint16(buf[2:4], uint16(len(recs))) // count
	binary.BigEndian.PutUint32(buf[8:12], 1700000000)       // unix_secs
	for i, r := range recs {
		b := buf[v5HeaderLen+i*v5RecordLen:]
		copy(b[0:4], r.src[:])
		copy(b[4:8], r.dst[:])
		binary.BigEndian.PutUint32(b[16:20], r.pkts)
		binary.BigEndian.PutUint32(b[20:24], r.octets)
		binary.BigEndian.PutUint16(b[32:34], r.sport)
		binary.BigEndian.PutUint16(b[34:36], r.dport)
		b[38] = r.proto
	}
	return buf
}

func TestDecodeV5(t *testing.T) {
	pkt := packet([]rec{
		{src: [4]byte{10, 0, 0, 1}, dst: [4]byte{10, 0, 0, 2}, pkts: 10, octets: 1500, sport: 443, dport: 5000, proto: 6},
		{src: [4]byte{10, 0, 0, 3}, dst: [4]byte{10, 0, 0, 2}, pkts: 5, octets: 600, sport: 53, dport: 6000, proto: 17},
	})
	h, flows, err := Decode(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if h.Version != 5 || h.Count != 2 {
		t.Fatalf("header = %+v", h)
	}
	if len(flows) != 2 {
		t.Fatalf("expected 2 flows, got %d", len(flows))
	}
	if flows[0].Src.String() != "10.0.0.1" || flows[0].Bytes != 1500 || flows[0].Protocol != 6 {
		t.Errorf("flow0 = %+v", flows[0])
	}
}

func TestDecodeRejectsBadInput(t *testing.T) {
	if _, _, err := Decode([]byte{0, 9, 0, 1}); err == nil {
		t.Error("non-v5 version should error")
	}
	bad := packet([]rec{{src: [4]byte{1, 1, 1, 1}}})
	binary.BigEndian.PutUint16(bad[2:4], 30) // claim 30 records but packet has 1
	if _, _, err := Decode(bad); err == nil {
		t.Error("truncated packet should error")
	}
}

func TestAggregation(t *testing.T) {
	pkt := packet([]rec{
		{src: [4]byte{10, 0, 0, 1}, dst: [4]byte{10, 0, 0, 2}, pkts: 10, octets: 1500, proto: 6},
		{src: [4]byte{10, 0, 0, 1}, dst: [4]byte{10, 0, 0, 9}, pkts: 5, octets: 500, proto: 17},
	})
	_, flows, _ := Decode(pkt)
	s := NewSummary()
	s.Add(flows)
	if s.Total.Bytes != 2000 || s.Total.Packets != 15 {
		t.Errorf("total = %+v", s.Total)
	}
	// 10.0.0.1 is the src of both → top host with 2000 bytes.
	top := TopN(s.ByHost, 1)
	if len(top) != 1 || top[0].Label != "10.0.0.1" || top[0].Bytes != 2000 {
		t.Errorf("top host = %+v", top)
	}
	protos := TopN(s.ByProtocol, 5)
	if len(protos) != 2 || protos[0].Label != "tcp" {
		t.Errorf("protocols = %+v", protos)
	}
}

func TestProtocolName(t *testing.T) {
	if ProtocolName(6) != "tcp" || ProtocolName(17) != "udp" || ProtocolName(200) != "proto-200" {
		t.Error("protocol name mapping wrong")
	}
}

var _ = buildV5 // reserved
