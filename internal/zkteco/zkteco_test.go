package zkteco

import (
	"context"
	"encoding/binary"
	"net"
	"strings"
	"testing"
)

// fakeZK is a minimal in-memory ZK device speaking the TCP framing, enough to validate the
// connector's connect → (auth) → identity → disconnect flow without a real device. When
// expectedKey is non-nil the device requires CMD_AUTH and VALIDATES the payload against
// makeCommKey(*expectedKey, session) — so the default-key-0 path and the comm-key-required
// path are both exercised (the real-hardware makeCommKey correctness is proven separately by
// the gated TestZKParity live harness).
func fakeZK(t *testing.T, expectedKey *uint32) (string, func()) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	reply := func(conn net.Conn, command, session uint16, data []byte) {
		buf := make([]byte, 8+len(data))
		binary.LittleEndian.PutUint16(buf[0:], command)
		binary.LittleEndian.PutUint16(buf[4:], session)
		copy(buf[8:], data)
		top := make([]byte, 8+len(buf))
		binary.LittleEndian.PutUint16(top[0:], tcpMagic1)
		binary.LittleEndian.PutUint16(top[2:], tcpMagic2)
		binary.LittleEndian.PutUint32(top[4:], uint32(len(buf)))
		copy(top[8:], buf)
		_, _ = conn.Write(top)
	}
	go func() {
		conn, aerr := ln.Accept()
		if aerr != nil {
			return
		}
		defer conn.Close()
		const session = 0x4321
		authed := expectedKey == nil
		for {
			top := make([]byte, 8)
			if _, e := readFull(conn, top); e != nil {
				return
			}
			n := binary.LittleEndian.Uint32(top[4:])
			pkt := make([]byte, n)
			if _, e := readFull(conn, pkt); e != nil {
				return
			}
			cmd := binary.LittleEndian.Uint16(pkt[0:])
			data := pkt[8:]
			switch {
			case cmd == cmdConnect && !authed:
				reply(conn, cmdACKUnauth, session, nil)
			case cmd == cmdConnect:
				reply(conn, cmdACKOK, session, nil)
			case cmd == cmdAuth:
				// Validate the comm-key payload the connector sent.
				want := makeCommKey(*expectedKey, session)
				if string(data) == string(want) {
					authed = true
					reply(conn, cmdACKOK, session, nil)
				} else {
					reply(conn, cmdACKUnauth, session, nil)
				}
			case cmd == cmdOptionsRRQ:
				name := strings.TrimRight(string(data), "\x00")
				val := map[string]string{"~SerialNumber": "~SerialNumber=ZK123456", "~DeviceName": "~DeviceName=ProFaceX", "~Platform": "~Platform=ZMM220"}[name]
				reply(conn, cmdACKOK, session, []byte(val))
			case cmd == cmdGetVersion:
				reply(conn, cmdACKOK, session, []byte("Ver 6.60 Apr 2 2020"))
			case cmd == cmdGetFreeSizes:
				buf := make([]byte, 80) // 20 int32 fields
				put := func(i int, v uint32) { binary.LittleEndian.PutUint32(buf[i*4:], v) }
				put(4, 7)      // users
				put(6, 9)      // fingers
				put(8, 3)      // records
				put(14, 500)   // users cap
				put(15, 1000)  // fingers cap
				put(16, 50000) // records cap
				reply(conn, cmdACKOK, session, buf)
			case cmd == cmdExit:
				reply(conn, cmdACKOK, session, nil)
				return
			default:
				reply(conn, cmdACKOK, session, nil)
			}
		}
	}()
	return ln.Addr().String(), func() { ln.Close() }
}

func hostPort(addr string) (string, int) {
	host, portStr, _ := net.SplitHostPort(addr)
	p := 0
	for _, c := range portStr {
		p = p*10 + int(c-'0')
	}
	return host, p
}

func TestZKProbe_NoAuth(t *testing.T) {
	addr, stop := fakeZK(t, nil)
	defer stop()
	host, p := hostPort(addr)
	id := Probe(context.Background(), host, p, "")
	if !id.Connected {
		t.Fatalf("expected connected, got reason=%q", id.Reason)
	}
	if id.Serial != "ZK123456" || id.DeviceName != "ProFaceX" || id.Platform != "ZMM220" {
		t.Errorf("identity mismatch: %+v", id)
	}
	if !strings.Contains(id.Firmware, "6.60") {
		t.Errorf("firmware mismatch: %q", id.Firmware)
	}
}

// TestZKProbe_DefaultKey0 proves PARITY with IP-only SDK tools: a device that answers
// ACK_UNAUTH but uses the SDK default communication key (0) authenticates with NO operator
// secret, then yields identity.
func TestZKProbe_DefaultKey0(t *testing.T) {
	var zero uint32 // 0
	addr, stop := fakeZK(t, &zero)
	defer stop()
	host, p := hostPort(addr)
	id := Probe(context.Background(), host, p, "") // no key supplied
	if !id.Connected || !id.AuthUsed {
		t.Fatalf("expected connected via default key 0, got connected=%v reason=%q", id.Connected, id.Reason)
	}
	if id.Serial != "ZK123456" {
		t.Errorf("identity mismatch: %+v", id)
	}
}

// TestZKProbe_NonDefaultKey_NoKey: a device with a NON-default key rejects key=0, so an
// onboarding with no key reports comm-key-required (never connected, never fabricated).
func TestZKProbe_NonDefaultKey_NoKey(t *testing.T) {
	key := uint32(123456)
	addr, stop := fakeZK(t, &key)
	defer stop()
	host, p := hostPort(addr)
	id := Probe(context.Background(), host, p, "")
	if id.Connected || !strings.Contains(id.Reason, "zkteco_comm_key_required") {
		t.Errorf("expected zkteco_comm_key_required, got connected=%v reason=%q", id.Connected, id.Reason)
	}
}

func TestZKProbe_NonDefaultKey_WithKey(t *testing.T) {
	key := uint32(123456)
	addr, stop := fakeZK(t, &key)
	defer stop()
	host, p := hostPort(addr)
	id := Probe(context.Background(), host, p, "123456")
	if !id.Connected || !id.AuthUsed {
		t.Errorf("expected connected via supplied key, got %+v", id)
	}
}

func TestReadEnrollment(t *testing.T) {
	addr, stop := fakeZK(t, nil)
	defer stop()
	host, p := hostPort(addr)
	e := ReadEnrollment(context.Background(), host, p, "")
	if !e.Connected {
		t.Fatalf("expected connected, reason=%q", e.Reason)
	}
	if e.Users != 7 || e.Fingers != 9 || e.Records != 3 {
		t.Errorf("counts mismatch: %+v", e)
	}
	if e.UsersCap != 500 || e.FingersCap != 1000 || e.RecordsCap != 50000 {
		t.Errorf("capacity mismatch: %+v", e)
	}
}

func TestZKChecksumRoundTrip(t *testing.T) {
	// The ZK protocol (validated live against real hardware): the checksum is computed over
	// the buffer with the CURRENT reply_id, while the SENT reply_id is incremented. Verify
	// both: the embedded checksum is over reply_id=N, and the sent reply field is N+1.
	const N uint16 = 5
	pkt := header(cmdConnect, 0, N, nil)
	body := pkt[8:] // strip tcp top
	embedded := binary.LittleEndian.Uint16(body[2:])
	sentReply := binary.LittleEndian.Uint16(body[6:])
	if sentReply != N+1 {
		t.Errorf("sent reply_id = %d, want %d (N+1)", sentReply, N+1)
	}
	tmp := make([]byte, len(body))
	copy(tmp, body)
	binary.LittleEndian.PutUint16(tmp[2:], 0) // zero checksum field
	binary.LittleEndian.PutUint16(tmp[6:], N) // checksum is over reply_id=N, not the sent N+1
	if want := checksum(tmp); want != embedded {
		t.Errorf("checksum mismatch embedded=%d recompute(over N)=%d", embedded, want)
	}
}
