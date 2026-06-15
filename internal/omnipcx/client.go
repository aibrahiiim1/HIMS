// Package omnipcx is a minimal collector for Alcatel-Lucent OmniPCX Enterprise
// (OXE) communication servers. OXE exposes no clean API on the node itself — the
// management account (mtcl) lands in a restricted Unix shell whose only
// structured-object access is `mgr`, an ncurses application. This package drives
// `mgr` over telnet with a fixed, reverse-engineered key sequence to list the
// subscriber (Users) directory, then parses the resulting instance grid.
//
// Reverse-engineered against OXE R7.1.3 (live, 150.0.0.131):
//   - login (telnet, IAC-refused) -> shell prompt "(N)xbNNNNNN>"
//   - run "mgr" -> ncurses; it enables application cursor-key mode (DECCKM), so
//     arrows are SS3 (ESC O B), not CSI.
//   - object menu: 7x Down -> "Users", Enter -> action menu, Down -> Review/Modify,
//     Enter -> review form; Enter selects the "All instances" button (<< >>),
//     CTRL-V validates (mgr "Validate" key, from its Keyboard-mapping screen).
//   - mgr enumerates ("[ N ] Instances: Users") then renders a paginated grid of
//     directory numbers; CTRL-F (mgr "Page-Down") advances a page.
//
// v1 collects the subscriber directory numbers + the total count. Per-subscriber
// name/set-type needs a per-instance drilldown (N telnet round-trips) and is left
// to a future enrichment.
package omnipcx

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Subscriber is one OXE Users-directory entry. v1 carries the directory number;
// Name/SetType are reserved for a future per-instance drilldown.
type Subscriber struct {
	Number  string
	Name    string
	SetType string
}

// Result is the parsed OXE inventory.
type Result struct {
	Endpoint    string       // host:port that answered
	Total       int          // mgr-reported "[ N ] Instances"
	Subscribers []Subscriber // unique directory numbers collected
}

// Client targets one OXE node's telnet management port.
type Client struct {
	Host string // "ip" or "ip:port"; defaults to :23
	User string
	Pass string
}

const (
	keyDownSS3   = "\x1bOB" // application cursor-key mode
	keyEnter     = "\r"
	keyValidate  = "\x16" // CTRL-V
	keyPageRight = "\x12" // CTRL-R — mgr "Page-Right"; the Users instance grid is
	// column-major and overflows horizontally, so Page-Right (not Page-Down)
	// reveals the next screenful of directory numbers. Verified live against R7.1.
	keyCancel = "\x03" // CTRL-C
)

var (
	ansiRe      = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]|\x1b[()][AB0]|\x1b[=>]|\x1bO.`)
	instancesRe = regexp.MustCompile(`\[\s*(\d+)\s*\]\s*Instances:\s*Users`)
	numTokenRe  = regexp.MustCompile(`\b\d{3,6}\b`) // OXE directory numbers (3-6 digits)
)

// ListSubscribers logs in, drives mgr to the Users "All instances" grid, and
// returns the directory numbers. ctx bounds the whole session.
func (c *Client) ListSubscribers(ctx context.Context) (*Result, error) {
	host := c.Host
	if !strings.Contains(host, ":") {
		host += ":23"
	}
	d := net.Dialer{Timeout: 10 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, fmt.Errorf("omnipcx: dial %s: %w", host, err)
	}
	defer conn.Close()
	s := &session{conn: conn}

	if !s.waitFor("ogin:", 25*time.Second) {
		return nil, fmt.Errorf("omnipcx: no login prompt")
	}
	s.send(c.User + "\r\n")
	if !s.waitFor("assword", 20*time.Second) {
		return nil, fmt.Errorf("omnipcx: no password prompt")
	}
	s.send(c.Pass + "\r\n")
	// OXE prints a long MOTD/version banner before the shell prompt; on a
	// higher-latency remote link this takes a while, so wait generously for the
	// "(N)xbNNNNNN>" prompt (match the trailing ">" once the banner settles).
	if !s.waitFor(">", 45*time.Second) {
		return nil, fmt.Errorf("omnipcx: login failed (no shell prompt)")
	}
	// Launch mgr and reach the Users -> Review/Modify -> All instances form.
	s.reset()
	s.send("mgr\r\n")
	if !s.waitFor("Select an object", 35*time.Second) {
		return nil, fmt.Errorf("omnipcx: mgr did not start")
	}
	for i := 0; i < 7; i++ { // Shelf -> Users
		s.send(keyDownSS3)
		time.Sleep(250 * time.Millisecond)
	}
	s.drain(600 * time.Millisecond)
	s.send(keyEnter) // open Users action menu
	if !s.waitFor("Review/Modify", 20*time.Second) {
		return nil, fmt.Errorf("omnipcx: Users action menu not shown")
	}
	s.send(keyDownSS3) // Descend hierarchy -> Review/Modify
	time.Sleep(250 * time.Millisecond)
	s.send(keyEnter) // open review form
	if !s.waitFor("All instances", 20*time.Second) {
		return nil, fmt.Errorf("omnipcx: review form not shown")
	}
	s.send(keyEnter) // select the "All instances" button (<< >>)
	time.Sleep(300 * time.Millisecond)
	s.send(keyValidate) // CTRL-V: execute

	// Enumeration runs for a few seconds, then the instance grid renders.
	if !s.waitFor("Instances: Users", 45*time.Second) {
		return nil, fmt.Errorf("omnipcx: instance list never rendered")
	}
	s.drain(1200 * time.Millisecond)

	res := &Result{Endpoint: host}
	if m := instancesRe.FindStringSubmatch(s.text()); m != nil {
		res.Total, _ = strconv.Atoi(m[1])
	}
	// Anchor parsing at the grid header so the pre-grid progress counter
	// ("1 2 3 ... 786") is never mistaken for directory numbers.
	if idx := strings.Index(s.text(), "Instances: Users"); idx >= 0 {
		s.gridMark = idx
	}
	seen := map[string]bool{}
	collect := func() int {
		added := 0
		for _, n := range parseGridNumbers(s.textSinceGrid()) {
			if !seen[n] {
				seen[n] = true
				res.Subscribers = append(res.Subscribers, Subscriber{Number: n})
				added++
			}
		}
		return added
	}
	collect()
	// Page through with CTRL-F until we have them all (or pages stop adding).
	dry := 0
	for page := 0; page < 40; page++ {
		if res.Total > 0 && len(res.Subscribers) >= res.Total {
			break
		}
		s.markGrid()
		s.send(keyPageRight) // grid is column-major; Page-Right reveals next screenful
		s.drain(1200 * time.Millisecond)
		if collect() == 0 {
			if dry++; dry >= 3 {
				break
			}
		} else {
			dry = 0
		}
	}
	// Leave mgr cleanly.
	for i := 0; i < 8; i++ {
		s.send(keyCancel)
		time.Sleep(120 * time.Millisecond)
	}
	if len(res.Subscribers) == 0 {
		return res, fmt.Errorf("omnipcx: no directory numbers parsed (total reported %d)", res.Total)
	}
	return res, nil
}

// parseGridNumbers extracts directory numbers from a rendered mgr instance grid.
// It strips ANSI, drops the "[ N ] Instances: Users" header line (so the count
// isn't mistaken for an extension), and keeps digit tokens that appear in the
// grid body.
func parseGridNumbers(rendered string) []string {
	clean := ansiRe.ReplaceAllString(rendered, " ")
	var out []string
	for _, line := range strings.Split(clean, "\n") {
		if instancesRe.MatchString(line) {
			continue // header line carries the total count, not an extension
		}
		// grid body lines are framed by box-drawing 'x' columns once ANSI is gone
		for _, tok := range numTokenRe.FindAllString(line, -1) {
			out = append(out, tok)
		}
	}
	return out
}

// --- telnet session helper ---------------------------------------------------

type session struct {
	conn     net.Conn
	buf      strings.Builder
	gridMark int
}

func (s *session) send(b string) { s.conn.Write([]byte(b)) }
func (s *session) text() string  { return s.buf.String() }
func (s *session) reset()        { s.buf.Reset(); s.gridMark = 0 }
func (s *session) markGrid()     { s.gridMark = s.buf.Len() }

// textSinceGrid returns output since the last markGrid (the current page) so each
// CTRL-F page is parsed fresh; before the first mark it returns the whole buffer.
func (s *session) textSinceGrid() string {
	t := s.buf.String()
	if s.gridMark > 0 && s.gridMark <= len(t) {
		return t[s.gridMark:]
	}
	return t
}

func (s *session) drain(d time.Duration) {
	end := time.Now().Add(d)
	b := make([]byte, 8192)
	for time.Now().Before(end) {
		s.conn.SetReadDeadline(time.Now().Add(d))
		n, err := s.conn.Read(b)
		if n > 0 {
			data, reply := stripIAC(b[:n])
			if len(reply) > 0 {
				s.conn.Write(reply)
			}
			s.buf.Write(data)
		}
		if err != nil {
			return
		}
	}
}

func (s *session) waitFor(sub string, max time.Duration) bool {
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		s.drain(600 * time.Millisecond)
		if strings.Contains(s.buf.String(), sub) {
			return true
		}
	}
	return strings.Contains(s.buf.String(), sub)
}

// stripIAC consumes Telnet IAC negotiation, refusing every option.
func stripIAC(b []byte) (data, reply []byte) {
	for i := 0; i < len(b); {
		if b[i] != 255 {
			data = append(data, b[i])
			i++
			continue
		}
		if i+1 >= len(b) {
			break
		}
		switch b[i+1] {
		case 255:
			data = append(data, 255)
			i += 2
		case 251, 252: // WILL/WONT -> DONT
			if i+2 < len(b) {
				reply = append(reply, 255, 254, b[i+2])
				i += 3
			} else {
				i = len(b)
			}
		case 253, 254: // DO/DONT -> WONT
			if i+2 < len(b) {
				reply = append(reply, 255, 252, b[i+2])
				i += 3
			} else {
				i = len(b)
			}
		case 250:
			j := i + 2
			for j+1 < len(b) && !(b[j] == 255 && b[j+1] == 240) {
				j++
			}
			i = j + 2
		default:
			i += 2
		}
	}
	return
}
