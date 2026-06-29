package omnipcx

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
)

const motd = "\r\nLast login: Mon Jun 29 13:03:19 from 172.21.60.20\r\n\r\n" +
	"Alcatel OmniPCX Enterprise\r\n" +
	"standard installation last performed: 04-Dec-2004 23:16:00 \r\n\r\n" +
	"#       The role of the CPU is MAIN          \r\n" +
	"Application software identity\r\n\r\n" +
	"R7.1-f5.401-29-a-eg-c6s2\r\n\r\n" +
	"Business identification: R7.1\r\n\r\n" +
	"Release:\r\nDELIVERY f5.401\r\n" +
	"Patch identification: 29\r\n" +
	"Dynamic patch identification: a\r\n\r\n" +
	"Country: eg\r\nCpu: c6s2\r\n\r\n"

// fakeOmniPCX speaks just enough telnet to exercise the login + banner parse. When wantPass is
// non-empty it requires that password (else replies "Login incorrect" + a fresh login prompt),
// and it injects an IAC negotiation sequence to verify the telnet client handles it.
func fakeOmniPCX(t *testing.T, wantPass string) (string, func()) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		conn, aerr := ln.Accept()
		if aerr != nil {
			return
		}
		defer conn.Close()
		// IAC DO SUPPRESS-GO-AHEAD (255 253 3) then the login prompt.
		_, _ = conn.Write([]byte{255, 253, 3})
		_, _ = conn.Write([]byte("\r\nWelcome to xa000001\r\nAlcatel OmniPCX Enterprise\r\n\r\nlogin: "))
		r := bufio.NewReader(conn)
		_, _ = r.ReadString('\n') // username line
		_, _ = conn.Write([]byte("Password: "))
		passLine, _ := r.ReadString('\n')
		got := strings.TrimSpace(passLine)
		if wantPass != "" && got != wantPass {
			_, _ = conn.Write([]byte("\r\nLogin incorrect\r\nlogin: "))
			return
		}
		_, _ = conn.Write([]byte(motd))
		_, _ = r.ReadString('\n') // exit
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

func TestProbe_Success(t *testing.T) {
	addr, stop := fakeOmniPCX(t, "mtcl")
	defer stop()
	host, p := hostPort(addr)
	id := Probe(context.Background(), host, p, "mtcl", "mtcl")
	if !id.Connected {
		t.Fatalf("expected connected, reason=%q", id.Reason)
	}
	if id.Vendor != "Alcatel-Lucent" || id.Model != "OmniPCX Enterprise" {
		t.Errorf("vendor/model mismatch: %+v", id)
	}
	if id.Software != "R7.1-f5.401-29-a-eg-c6s2" || id.Release != "R7.1" || id.Delivery != "f5.401" || id.Patch != "29" || id.Country != "eg" || id.CPURole != "MAIN" {
		t.Errorf("identity parse mismatch: %+v", id)
	}
	if id.OSVersion() != "R7.1-f5.401-29-a-eg-c6s2" {
		t.Errorf("OSVersion: %q", id.OSVersion())
	}
}

func TestProbe_WrongPassword(t *testing.T) {
	addr, stop := fakeOmniPCX(t, "mtcl")
	defer stop()
	host, p := hostPort(addr)
	id := Probe(context.Background(), host, p, "mtcl", "wrong")
	if id.Connected || !strings.Contains(id.Reason, "credential_failed") {
		t.Errorf("expected credential_failed, got connected=%v reason=%q", id.Connected, id.Reason)
	}
}
