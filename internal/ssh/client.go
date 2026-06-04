// Package ssh runs a single command over SSH against a network device and
// returns its output. It is used by Config Backup to pull running-configs from
// switches/firewalls. Host keys are not pinned (devices rotate keys on reimage
// and HIMS authenticates with operator credentials, not host-key trust); a
// future enhancement may add a known-hosts store.
package ssh

import (
	"context"
	"fmt"
	"net"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

// Creds is the SSH login. Password auth today; key auth is a future addition.
type Creds struct {
	Username string
	Password string
}

// buildConfig assembles the client config. When legacyKEX is set, older
// key-exchange + cipher algorithms are appended for legacy switches (e.g. older
// Cisco/Aruba that only speak diffie-hellman-group1/14-sha1 + CBC ciphers).
func buildConfig(c Creds, legacyKEX bool, timeout time.Duration) *gossh.ClientConfig {
	cfg := &gossh.ClientConfig{
		User:            c.Username,
		Auth:            []gossh.AuthMethod{gossh.Password(c.Password)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(), //nolint:gosec // device host keys are not pinned (see package doc)
		Timeout:         timeout,
	}
	if legacyKEX {
		base := gossh.Config{}
		base.SetDefaults()
		cfg.KeyExchanges = append(base.KeyExchanges, "diffie-hellman-group14-sha1", "diffie-hellman-group1-sha1")
		cfg.Ciphers = append(base.Ciphers, "aes128-cbc", "aes256-cbc", "3des-cbc")
	}
	return cfg
}

// CheckAuth opens an SSH connection to host:port and completes the handshake +
// authentication only — no command is run — then closes. It returns nil when
// the credentials authenticate. Used by credential testing: it proves a login
// works without side effects on the device. The password is never logged.
func CheckAuth(ctx context.Context, host string, port int, c Creds, legacyKEX bool, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	d := net.Dialer{Timeout: timeout}
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := d.DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	cfg := buildConfig(c, legacyKEX, timeout)
	sshConn, chans, reqs, err := gossh.NewClientConn(conn, addr, cfg)
	if err != nil {
		conn.Close()
		return fmt.Errorf("ssh handshake: %w", err) // never includes the password
	}
	client := gossh.NewClient(sshConn, chans, reqs)
	client.Close()
	return nil
}

// Run opens an SSH session to host:port, executes command, and returns the
// combined stdout/stderr. The password is never logged.
func Run(ctx context.Context, host string, port int, c Creds, legacyKEX bool, command string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))

	d := net.Dialer{Timeout: timeout}
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := d.DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return "", fmt.Errorf("dial %s: %w", addr, err)
	}

	cfg := buildConfig(c, legacyKEX, timeout)
	sshConn, chans, reqs, err := gossh.NewClientConn(conn, addr, cfg)
	if err != nil {
		conn.Close()
		return "", fmt.Errorf("ssh handshake: %w", err) // never includes the password
	}
	client := gossh.NewClient(sshConn, chans, reqs)
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("ssh session: %w", err)
	}
	defer session.Close()

	// Bound the command by the context/timeout via a goroutine.
	type res struct {
		out []byte
		err error
	}
	done := make(chan res, 1)
	go func() {
		out, err := session.CombinedOutput(command)
		done <- res{out, err}
	}()
	select {
	case <-dialCtx.Done():
		return "", dialCtx.Err()
	case r := <-done:
		if r.err != nil {
			return string(r.out), fmt.Errorf("run %q: %w", command, r.err)
		}
		return string(r.out), nil
	}
}
