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
	cmdConnect    = 1000
	cmdExit       = 1001
	cmdAuth       = 1102
	cmdGetVersion = 1100
	cmdOptionsRRQ = 11
	cmdACKOK      = 2000
	cmdACKUnauth  = 2005
	tcpMagic1     = 0x5050
	tcpMagic2     = 0x7d82
	ushrtMax      = 65535
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

// header builds a command packet (with checksum) wrapped in the TCP top header.
func header(command, sessionID, replyID uint16, data []byte) []byte {
	buf := make([]byte, 8+len(data))
	binary.LittleEndian.PutUint16(buf[0:], command)
	binary.LittleEndian.PutUint16(buf[2:], 0) // checksum placeholder
	binary.LittleEndian.PutUint16(buf[4:], sessionID)
	binary.LittleEndian.PutUint16(buf[6:], replyID)
	copy(buf[8:], data)
	cs := checksum(buf)
	binary.LittleEndian.PutUint16(buf[2:], cs)
	top := make([]byte, 8+len(buf))
	binary.LittleEndian.PutUint16(top[0:], tcpMagic1)
	binary.LittleEndian.PutUint16(top[2:], tcpMagic2)
	binary.LittleEndian.PutUint32(top[4:], uint32(len(buf)))
	copy(top[8:], buf)
	return top
}

// makeCommKey derives the CMD_AUTH payload from the device communication key + session id
// (the ZK comm-key algorithm). Only used when a key is configured on the device.
func makeCommKey(key, sessionID uint32) []byte {
	const ticks = 50
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
	var tick byte = ticks & 0xff
	return []byte{b[0] ^ tick, b[1] ^ tick, b[2], tick ^ b[3]}
}

type reply struct {
	command   uint16
	sessionID uint16
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
	if port == 0 {
		port = 4370
	}
	timeout := 8 * time.Second
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, fmt.Sprintf("%d", port)))
	if err != nil {
		return Identity{Reason: "unreachable: " + condProto(err)}
	}
	defer conn.Close()

	var replyID uint16
	send := func(command uint16, sessionID uint16, data []byte) (reply, error) {
		_ = conn.SetWriteDeadline(time.Now().Add(timeout))
		if _, werr := conn.Write(header(command, sessionID, replyID, data)); werr != nil {
			return reply{}, werr
		}
		replyID++
		return recv(conn, timeout)
	}

	// 1) CONNECT
	rp, err := send(cmdConnect, 0, nil)
	if err != nil {
		return Identity{Reason: "protocol: no/!invalid handshake response"}
	}
	sessionID := rp.sessionID
	authUsed := false
	if rp.command == cmdACKUnauth {
		// 2) AUTH with the communication key (if supplied).
		key := parseCommKey(commKey)
		if key == 0 && strings.TrimSpace(commKey) == "" {
			return Identity{Reason: "auth_failed: device requires a communication key"}
		}
		authUsed = true
		rp, err = send(cmdAuth, sessionID, makeCommKey(key, uint32(sessionID)))
		if err != nil || rp.command != cmdACKOK {
			return Identity{Reason: "auth_failed: communication key rejected"}
		}
	} else if rp.command != cmdACKOK {
		return Identity{Reason: "auth_failed: device rejected the connection"}
	}

	id := Identity{Connected: true, AuthUsed: authUsed}
	// 3) identity (read-only) — best-effort; absence is honest, never fabricated.
	if v := readOption(send, sessionID, "~SerialNumber"); v != "" {
		id.Serial = v
	}
	if v := readOption(send, sessionID, "~DeviceName"); v != "" {
		id.DeviceName = v
	}
	if v := readOption(send, sessionID, "~Platform"); v != "" {
		id.Platform = v
	}
	if rp, err := send(cmdGetVersion, sessionID, nil); err == nil && rp.command == cmdACKOK {
		id.Firmware = cleanStr(rp.data)
	}
	// 4) disconnect (best-effort)
	_, _ = send(cmdExit, sessionID, nil)
	return id
}

// readOption sends CMD_OPTIONS_RRQ for an option name and returns the value after '='.
func readOption(send func(uint16, uint16, []byte) (reply, error), sessionID uint16, name string) string {
	rp, err := send(cmdOptionsRRQ, sessionID, []byte(name+"\x00"))
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
