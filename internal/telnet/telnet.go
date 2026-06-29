// Package telnet is a minimal, dependency-free telnet client: enough to drive a legacy device
// CLI (login prompt → credentials → command output) that exposes no SSH. It transparently
// answers Telnet IAC option negotiation (refusing all options, which every server tolerates)
// and exposes ReadUntil/Write so a caller can script a login + read-only command sequence.
// It is used by the OmniPCX (Alcatel) mgr collector, whose call server speaks telnet only.
package telnet

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

// Telnet protocol bytes (RFC 854).
const (
	iac   = 255 // Interpret As Command
	dont  = 254
	doCmd = 253
	wont  = 252
	will  = 251
	sb    = 250 // subnegotiation begin
	se    = 240 // subnegotiation end
)

// Client is a telnet session over one TCP connection. Not safe for concurrent use.
type Client struct {
	conn net.Conn
}

// Dial opens a telnet connection.
func Dial(ctx context.Context, addr string, timeout time.Duration) (*Client, error) {
	d := net.Dialer{Timeout: timeout}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	return &Client{conn: c}, nil
}

// Close closes the connection.
func (c *Client) Close() error { return c.conn.Close() }

// readDecoded reads one chunk and returns the data bytes with IAC negotiation stripped +
// answered (every option refused: DO→WONT, WILL→DONT). Subnegotiations are skipped.
func (c *Client) readDecoded(timeout time.Duration) ([]byte, error) {
	_ = c.conn.SetReadDeadline(time.Now().Add(timeout))
	raw := make([]byte, 4096)
	n, err := c.conn.Read(raw)
	out := make([]byte, 0, n)
	for i := 0; i < n; {
		b := raw[i]
		if b != iac {
			out = append(out, b)
			i++
			continue
		}
		if i+1 >= n {
			break
		}
		switch cmd := raw[i+1]; cmd {
		case iac: // escaped literal 0xFF
			out = append(out, iac)
			i += 2
		case will, wont, doCmd, dont:
			if i+2 >= n {
				i = n
				break
			}
			opt := raw[i+2]
			resp := byte(wont) // refuse: respond to DO/DONT with WONT
			if cmd == will || cmd == wont {
				resp = dont // refuse: respond to WILL/WONT with DONT
			}
			_, _ = c.conn.Write([]byte{iac, resp, opt})
			i += 3
		case sb: // skip subnegotiation up to IAC SE
			j := i + 2
			for j+1 < n && !(raw[j] == iac && raw[j+1] == se) {
				j++
			}
			i = j + 2
		default: // 2-byte command we don't care about
			i += 2
		}
	}
	return out, err
}

// ReadUntil reads (decoding IAC) until any pattern (case-insensitive) appears, or the overall
// timeout elapses. It returns everything read so far; on timeout it returns the buffer + an
// error so the caller can decide whether the partial output is usable.
func (c *Client) ReadUntil(timeout time.Duration, patterns ...string) (string, error) {
	deadline := time.Now().Add(timeout)
	var acc bytes.Buffer
	match := func() bool {
		low := strings.ToLower(acc.String())
		for _, p := range patterns {
			if p != "" && strings.Contains(low, strings.ToLower(p)) {
				return true
			}
		}
		return false
	}
	for {
		if match() {
			return acc.String(), nil
		}
		if time.Now().After(deadline) {
			return acc.String(), fmt.Errorf("timeout waiting for %v", patterns)
		}
		dec, err := c.readDecoded(2 * time.Second)
		acc.Write(dec)
		if err != nil {
			if match() {
				return acc.String(), nil
			}
			return acc.String(), err
		}
	}
}

// ReadUntilIdle reads (decoding IAC) until no new data arrives for `quiet`, or `total` elapses.
// Robust for legacy/custom CLIs whose prompt is unknown and where command chaining isn't
// available — a command's output is "done" when the device goes quiet.
func (c *Client) ReadUntilIdle(quiet, total time.Duration) (string, error) {
	deadline := time.Now().Add(total)
	var acc bytes.Buffer
	for {
		if time.Now().After(deadline) {
			return acc.String(), nil
		}
		dec, err := c.readDecoded(quiet)
		if len(dec) > 0 {
			acc.Write(dec)
			continue // got data — keep reading until a quiet gap
		}
		if err != nil {
			// a read timeout with no data == the device went quiet == output complete
			return acc.String(), nil
		}
	}
}

// Write sends raw bytes (the caller appends CR/LF as the device expects).
func (c *Client) Write(s string) error {
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := c.conn.Write([]byte(s))
	return err
}
