// Package zkteco is a minimal, READ-ONLY connector for ZKTeco fingerprint / time-attendance
// / access-control devices over their native protocol (TCP, default port 4370). It does ONE
// safe thing: connect (optionally authenticate with a communication key), read the device
// identity (serial number, firmware version, device name, platform), then disconnect. It does
// NOT read or write attendance logs or users — identity only — so onboarding can prove the
// device is reachable + authenticated and classify it honestly as biometric/zkteco without
// touching sensitive data. The packet format mirrors the well-known ZK protocol (as used by
// pyzk): an 8-byte command header (command, checksum, session, reply) + data, wrapped for TCP
// with an 8-byte top header.
package zkteco

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"strings"
	"time"
)

const (
	cmdConnect      = 1000
	cmdExit         = 1001
	cmdAuth         = 1102
	cmdGetVersion   = 1100
	cmdOptionsRRQ   = 11
	cmdGetFreeSizes = 50 // device status/capacity buffer (enrolled users/fingers/records counts)
	cmdACKOK        = 2000
	cmdACKUnauth    = 2005
	tcpMagic1       = 0x5050
	tcpMagic2       = 0x7d82
	ushrtMax        = 65535
)

// Identity is what a safe ZKTeco identity probe can read. Empty fields = the device did not
// expose that value (never fabricated).
type Identity struct {
	Connected  bool
	Serial     string
	Firmware   string
	DeviceName string
	Platform   string
	AuthUsed   bool
	Reason     string // honest reason when not connected (auth_failed / unreachable / protocol)
}

// checksum implements the ZK 16-bit one's-complement checksum over buf.
func checksum(buf []byte) uint16 {
	var sum int
	i := 0
	for ; i+1 < len(buf); i += 2 {
		sum += int(binary.LittleEndian.Uint16(buf[i : i+2]))
		if sum > ushrtMax {
			sum -= ushrtMax
		}
	}
	if i < len(buf) {
		sum += int(buf[i])
	}
	for sum > ushrtMax {
		sum -= ushrtMax
	}
	c := ^sum
	for c < 0 {
		c += ushrtMax
	}
	return uint16(c)
}

// header builds a command packet (with checksum) wrapped in the TCP top header. Matching the
// ZK protocol (pyzk): the checksum is computed over the buffer with the CURRENT reply_id, then
// the SENT reply_id is incremented (wrapping at USHRT_MAX). Real devices validate this exact
// sequence and silently drop a packet whose checksum was computed over a different reply_id.
func header(command, sessionID, replyID uint16, data []byte) []byte {
	buf := make([]byte, 8+len(data))
	binary.LittleEndian.PutUint16(buf[0:], command)
	binary.LittleEndian.PutUint16(buf[2:], 0) // checksum placeholder
	binary.LittleEndian.PutUint16(buf[4:], sessionID)
	binary.LittleEndian.PutUint16(buf[6:], replyID)
	copy(buf[8:], data)
	cs := checksum(buf)
	binary.LittleEndian.PutUint16(buf[2:], cs)
	sent := replyID + 1
	if sent >= ushrtMax {
		sent -= ushrtMax
	}
	binary.LittleEndian.PutUint16(buf[6:], sent)
	top := make([]byte, 8+len(buf))
	binary.LittleEndian.PutUint16(top[0:], tcpMagic1)
	binary.LittleEndian.PutUint16(top[2:], tcpMagic2)
	binary.LittleEndian.PutUint32(top[4:], uint32(len(buf)))
	copy(top[8:], buf)
	return top
}

// makeCommKey derives the CMD_AUTH payload from the device communication key + session id
// (the ZK comm-key algorithm, as used by pyzk / the ZK SDK). The third output byte is the
// ticks value itself — NOT the scrambled byte. This was live-validated against real ZKTeco
// hardware: with key=0 (the SDK default) this exact payload yields CMD_ACK_OK, which is how
// IP-only tools authenticate without the operator entering any secret.
func makeCommKey(key, sessionID uint32) []byte {
	const ticks byte = 50
	k := uint32(0)
	for i := uint(0); i < 32; i++ {
		if key&(1<<i) != 0 {
			k = (k << 1) | 1
		} else {
			k = k << 1
		}
	}
	k += sessionID
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, k)
	b[0] ^= 'Z'
	b[1] ^= 'K'
	b[2] ^= 'S'
	b[3] ^= 'O'
	// swap the two 16-bit halves
	b = []byte{b[2], b[3], b[0], b[1]}
	return []byte{b[0] ^ ticks, b[1] ^ ticks, ticks, b[3] ^ ticks}
}

type reply struct {
	command   uint16
	sessionID uint16
	replyID   uint16
	data      []byte
}

// recv reads one TCP-framed ZK reply.
func recv(conn net.Conn, timeout time.Duration) (reply, error) {
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	top := make([]byte, 8)
	if _, err := readFull(conn, top); err != nil {
		return reply{}, err
	}
	if binary.LittleEndian.Uint16(top[0:]) != tcpMagic1 {
		return reply{}, fmt.Errorf("not a ZK protocol response")
	}
	n := binary.LittleEndian.Uint32(top[4:])
	if n < 8 || n > 1<<16 {
		return reply{}, fmt.Errorf("bad ZK packet length")
	}
	pkt := make([]byte, n)
	if _, err := readFull(conn, pkt); err != nil {
		return reply{}, err
	}
	return reply{
		command:   binary.LittleEndian.Uint16(pkt[0:]),
		sessionID: binary.LittleEndian.Uint16(pkt[4:]),
		replyID:   binary.LittleEndian.Uint16(pkt[6:]),
		data:      pkt[8:],
	}, nil
}

func readFull(conn net.Conn, b []byte) (int, error) {
	got := 0
	for got < len(b) {
		n, err := conn.Read(b[got:])
		if n > 0 {
			got += n
		}
		if err != nil {
			return got, err
		}
	}
	return got, nil
}

// Probe connects to a ZKTeco device and reads its identity (read-only). commKey may be ""/"0"
// for devices with no communication key. Returns Identity.Connected=false with an honest
// Reason on failure — never fabricated values.
func Probe(ctx context.Context, host string, port int, commKey string) Identity {
	s, authUsed, reason := connect(ctx, host, port, commKey)
	if s == nil {
		return Identity{Reason: reason}
	}
	defer s.close()

	id := Identity{Connected: true, AuthUsed: authUsed}
	// identity (read-only) — best-effort; absence is honest, never fabricated.
	if v := readOption(s.send, "~SerialNumber"); v != "" {
		id.Serial = v
	}
	if v := readOption(s.send, "~DeviceName"); v != "" {
		id.DeviceName = v
	}
	if v := readOption(s.send, "~Platform"); v != "" {
		id.Platform = v
	}
	if rp, err := s.send(cmdGetVersion, nil); err == nil && rp.command == cmdACKOK {
		id.Firmware = cleanStr(rp.data)
	}
	return id
}

// session is an authenticated ZK protocol connection. The device drives the
// session_id + reply_id sequence; the checksum must follow it exactly.
type session struct {
	conn      net.Conn
	timeout   time.Duration
	replyID   uint16
	sessionID uint16
}

func (s *session) send(command uint16, data []byte) (reply, error) {
	_ = s.conn.SetWriteDeadline(time.Now().Add(s.timeout))
	if _, werr := s.conn.Write(header(command, s.sessionID, s.replyID, data)); werr != nil {
		return reply{}, werr
	}
	rp, rerr := recv(s.conn, s.timeout)
	if rerr == nil {
		s.sessionID = rp.sessionID
		s.replyID = rp.replyID
	}
	return rp, rerr
}

func (s *session) close() {
	_, _ = s.send(cmdExit, nil)
	_ = s.conn.Close()
}

// connect dials, performs CMD_CONNECT and (if challenged) CMD_AUTH with the comm key (default
// 0). Returns the session + whether auth was used, or (nil, false, honest reason) on failure.
func connect(ctx context.Context, host string, port int, commKey string) (*session, bool, string) {
	if port == 0 {
		port = 4370
	}
	timeout := 8 * time.Second
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, fmt.Sprintf("%d", port)))
	if err != nil {
		return nil, false, "unreachable: " + condProto(err)
	}
	s := &session{conn: conn, timeout: timeout, replyID: ushrtMax - 1}

	rp, err := s.send(cmdConnect, nil)
	if err != nil {
		_ = conn.Close()
		return nil, false, "protocol: no/invalid handshake response"
	}
	authUsed := false
	if rp.command == cmdACKUnauth {
		// ZKTeco answers CONNECT with ACK_UNAUTH; the SDK default key is 0, so an empty key
		// defaults to 0 (parity with IP-only tools). Only if 0 is rejected is a real key needed.
		key := parseCommKey(commKey)
		authUsed = true
		rp, err = s.send(cmdAuth, makeCommKey(key, uint32(s.sessionID)))
		if err != nil || rp.command != cmdACKOK {
			_ = conn.Close()
			if strings.TrimSpace(commKey) == "" {
				return nil, false, "zkteco_comm_key_required: device rejected the default key (0)"
			}
			return nil, false, "credential_failed: communication key rejected"
		}
	} else if rp.command != cmdACKOK {
		_ = conn.Close()
		return nil, false, "auth_failed: device rejected the connection"
	}
	return s, authUsed, ""
}

// Enrollment is the device's read-only enrollment summary: how many users + fingerprints are
// enrolled (and stored attendance-record count). NO user records, names, templates, or
// attendance logs are read here — counts only.
type Enrollment struct {
	Connected  bool
	Users      int
	Fingers    int
	Records    int
	UsersCap   int
	FingersCap int
	RecordsCap int
	Reason     string
}

// ReadEnrollment connects (read-only) and reads CMD_GET_FREE_SIZES — the device's capacity/usage
// buffer — returning the enrolled user + fingerprint counts. Counts only; no personal data.
func ReadEnrollment(ctx context.Context, host string, port int, commKey string) Enrollment {
	s, _, reason := connect(ctx, host, port, commKey)
	if s == nil {
		return Enrollment{Reason: reason}
	}
	defer s.close()

	rp, err := s.send(cmdGetFreeSizes, nil)
	if err != nil || rp.command != cmdACKOK || len(rp.data) < 80 {
		return Enrollment{Connected: true, Reason: "enrollment sizes not exposed by this device"}
	}
	// The buffer is an array of little-endian int32 status fields (pyzk read_sizes layout):
	// users at index 4, fingerprints at 6, attendance records at 8.
	field := func(i int) int {
		if (i+1)*4 > len(rp.data) {
			return 0
		}
		return int(int32(binary.LittleEndian.Uint32(rp.data[i*4:])))
	}
	return Enrollment{
		Connected: true,
		Users:     field(4), Fingers: field(6), Records: field(8),
		UsersCap: field(14), FingersCap: field(15), RecordsCap: field(16),
	}
}

// readOption sends CMD_OPTIONS_RRQ for an option name and returns the value after '='.
func readOption(send func(uint16, []byte) (reply, error), name string) string {
	rp, err := send(cmdOptionsRRQ, []byte(name+"\x00"))
	if err != nil || rp.command != cmdACKOK {
		return ""
	}
	s := cleanStr(rp.data)
	if i := strings.IndexByte(s, '='); i >= 0 {
		return strings.TrimSpace(s[i+1:])
	}
	return strings.TrimSpace(s)
}

func cleanStr(b []byte) string {
	return strings.TrimRight(strings.TrimSpace(string(b)), "\x00")
}

func parseCommKey(s string) uint32 {
	s = strings.TrimSpace(s)
	var k uint32
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		k = k*10 + uint32(c-'0')
	}
	return k
}

func condProto(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
