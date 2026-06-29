package telnet

import (
	"context"
	"net"
	"testing"
	"time"
)

// TestReadUntil_IACAndPattern verifies the client strips/answers IAC negotiation, returns the
// real data, and stops at a pattern.
func TestReadUntil_IACAndPattern(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// IAC DO SUPPRESS-GO-AHEAD (255,253,3) + IAC WILL ECHO (255,251,1), then data + prompt.
		_, _ = conn.Write([]byte{iac, doCmd, 3, iac, will, 1})
		_, _ = conn.Write([]byte("Welcome\r\nlogin: "))
		// Stay open until the client disconnects, so the data isn't RST away on close.
		buf := make([]byte, 64)
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		for {
			if _, err := conn.Read(buf); err != nil {
				return
			}
		}
	}()
	c, err := Dial(context.Background(), ln.Addr().String(), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	out, err := c.ReadUntil(4*time.Second, "login:")
	if err != nil {
		t.Fatalf("ReadUntil: %v (got %q)", err, out)
	}
	if want := "Welcome\r\nlogin: "; out != want {
		t.Errorf("data with IAC stripped = %q, want %q", out, want)
	}
}
