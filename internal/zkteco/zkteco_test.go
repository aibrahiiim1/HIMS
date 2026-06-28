package zkteco

import (
	"context"
	"encoding/binary"
	"net"
	"strings"
	"testing"
)

// fakeZK is a minimal in-memory ZK device speaking the TCP framing, enough to validate the
// connector's connect → identity → disconnect flow without a real device.
func fakeZK(t *testing.T, requireAuth bool) (string, func()) {
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
		authed := !requireAuth
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
				authed = true
				reply(conn, cmdACKOK, session, nil)
			case cmd == cmdOptionsRRQ:
				name := strings.TrimRight(string(data), "\x00")
				val := map[string]string{"~SerialNumber": "~SerialNumber=ZK123456", "~DeviceName": "~DeviceName=ProFaceX", "~Platform": "~Platform=ZMM220"}[name]
				reply(conn, cmdACKOK, session, []byte(val))
			case cmd == cmdGetVersion:
				reply(conn, cmdACKOK, session, []byte("Ver 6.60 Apr 2 2020"))
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

func TestZKProbe_NoAuth(t *testing.T) {
	addr, stop := fakeZK(t, false)
	defer stop()
	host, portStr, _ := net.SplitHostPort(addr)
	var p int
	for _, c := range portStr {
		p = p*10 + int(c-'0')
	}
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

func TestZKProbe_AuthRequired_NoKey(t *testing.T) {
	addr, stop := fakeZK(t, true)
	defer stop()
	host, portStr, _ := net.SplitHostPort(addr)
	var p int
	for _, c := range portStr {
		p = p*10 + int(c-'0')
	}
	// no comm key supplied → honest auth_failed (device requires a key)
	id := Probe(context.Background(), host, p, "")
	if id.Connected || !strings.Contains(id.Reason, "communication key") {
		t.Errorf("expected auth_failed needing key, got connected=%v reason=%q", id.Connected, id.Reason)
	}
}

func TestZKProbe_AuthRequired_WithKey(t *testing.T) {
	addr, stop := fakeZK(t, true)
	defer stop()
	host, portStr, _ := net.SplitHostPort(addr)
	var p int
	for _, c := range portStr {
		p = p*10 + int(c-'0')
	}
	id := Probe(context.Background(), host, p, "123456")
	if !id.Connected || !id.AuthUsed {
		t.Errorf("expected connected via auth, got %+v", id)
	}
}

func TestZKChecksumRoundTrip(t *testing.T) {
	// A header's embedded checksum must validate over the (command,0,session,reply,data) buf.
	pkt := header(cmdConnect, 0, 0, nil)
	body := pkt[8:] // strip tcp top
	got := binary.LittleEndian.Uint16(body[2:])
	tmp := make([]byte, len(body))
	copy(tmp, body)
	binary.LittleEndian.PutUint16(tmp[2:], 0)
	if want := checksum(tmp); want != got {
		t.Errorf("checksum mismatch embedded=%d recompute=%d", got, want)
	}
}
