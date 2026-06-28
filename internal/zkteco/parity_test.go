package zkteco

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// makeCommKeyV builds the CMD_AUTH payload for a given variant, so we can test which one a
// real device accepts (the fake server accepts any AUTH, so only live devices disambiguate).
// variant: "cur" = current shipping impl; "pyzk" = third byte forced to ticks (the documented
// pyzk algorithm). Both reverse the key bits, add session, xor with 'ZKSO', swap halves.
func makeCommKeyV(key, sessionID uint32, ticks byte, variant string) []byte {
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
	b = []byte{b[2], b[3], b[0], b[1]} // swap 16-bit halves
	switch variant {
	case "pyzk":
		return []byte{b[0] ^ ticks, b[1] ^ ticks, ticks, b[3] ^ ticks}
	default: // "cur"
		return []byte{b[0] ^ ticks, b[1] ^ ticks, b[2], ticks ^ b[3]}
	}
}

// rawConn abstracts TCP (with 8-byte top header) vs UDP (raw header) framing.
type rawConn struct {
	conn  net.Conn
	udp   bool
	reply uint16
	sess  uint16
}

func (c *rawConn) cmd(command uint16, data []byte) (cmd, sess uint16, body []byte, err error) {
	pkt := header(command, c.sess, c.reply, data)
	if c.udp {
		pkt = pkt[8:] // strip TCP top
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err = c.conn.Write(pkt); err != nil {
		return
	}
	_ = c.conn.SetReadDeadline(time.Now().Add(6 * time.Second))
	if c.udp {
		buf := make([]byte, 2048)
		n, rerr := c.conn.Read(buf)
		if rerr != nil {
			return 0, 0, nil, rerr
		}
		p := buf[:n]
		cmd = binary.LittleEndian.Uint16(p[0:])
		sess = binary.LittleEndian.Uint16(p[4:])
		c.reply = binary.LittleEndian.Uint16(p[6:])
		c.sess = sess
		return cmd, sess, p[8:], nil
	}
	rp, rerr := recv(c.conn, 6*time.Second)
	if rerr != nil {
		return 0, 0, nil, rerr
	}
	c.reply = rp.replyID
	c.sess = rp.sessionID
	return rp.command, rp.sessionID, rp.data, nil
}

func cmdName(c uint16) string {
	switch c {
	case cmdACKOK:
		return "ACK_OK"
	case cmdACKUnauth:
		return "ACK_UNAUTH"
	case 0:
		return "(none)"
	default:
		return fmt.Sprintf("0x%04x", c)
	}
}

// TestZKParity runs the full evidence matrix against real devices. ZK_PARITY=ip1,ip2,...
func TestZKParity(t *testing.T) {
	hosts := os.Getenv("ZK_PARITY")
	if hosts == "" {
		t.Skip("set ZK_PARITY=ip,ip,...")
	}
	type scenario struct {
		transport string
		keymode   string // "nokey-noauth" | "key0-cur" | "key0-pyzk" | "nokey-auth0-pyzk"
	}
	scen := []scenario{
		{"tcp", "nokey-noauth"},
		{"tcp", "key0-cur"},
		{"tcp", "key0-pyzk"},
		{"udp", "nokey-noauth"},
		{"udp", "key0-pyzk"},
	}
	for _, h := range strings.Split(hosts, ",") {
		h = strings.TrimSpace(h)
		for _, s := range scen {
			runScenario(t, h, s.transport, s.keymode)
		}
	}
}

func runScenario(t *testing.T, host, transport, keymode string) {
	addr := net.JoinHostPort(host, "4370")
	conn, err := net.DialTimeout(transport, addr, 5*time.Second)
	if err != nil {
		t.Logf("%s | %s | %-16s | dial err=%v", host, transport, keymode, err)
		return
	}
	defer conn.Close()
	rc := &rawConn{conn: conn, udp: transport == "udp", reply: ushrtMax - 1}

	connCmd, sess, _, err := rc.cmd(cmdConnect, nil)
	if err != nil {
		t.Logf("%s | %s | %-16s | CONNECT err=%v", host, transport, keymode, err)
		return
	}
	authSent, authResult := "no", "-"
	final := cmdName(connCmd)
	if connCmd == cmdACKUnauth && keymode != "nokey-noauth" {
		var payload []byte
		switch keymode {
		case "key0-cur":
			payload = makeCommKeyV(0, uint32(sess), 50, "cur")
		case "key0-pyzk", "nokey-auth0-pyzk":
			payload = makeCommKeyV(0, uint32(sess), 50, "pyzk")
		}
		authSent = "yes"
		ac, _, _, aerr := rc.cmd(cmdAuth, payload)
		if aerr != nil {
			authResult = "err:" + aerr.Error()
		} else {
			authResult = cmdName(ac)
		}
		final = authResult
	}
	// try identity if we ended OK
	ident := "-"
	if final == "ACK_OK" {
		sc, _, body, ierr := rc.cmd(cmdOptionsRRQ, []byte("~SerialNumber\x00"))
		if ierr == nil && sc == cmdACKOK {
			ident = cleanStr(body)
		} else {
			ident = "rrq=" + cmdName(sc)
		}
	}
	t.Logf("%s | %-3s | 4370 | %-16s | connect=%-10s | auth_sent=%-3s | auth=%-10s | ident=%q",
		host, transport, keymode, cmdName(connCmd), authSent, authResult, ident)
}
