package api

import (
	"net/http"
	"os"
	"regexp"
	"time"
)

// dbURLPasswordRe matches the password segment of a "scheme://user:password@host"
// URL so it can be masked without disturbing the rest of the URL.
var dbURLPasswordRe = regexp.MustCompile(`://([^:@/?#]+):[^@/?#]*@`)

// RuntimeInfo is the safe, non-secret identity of this running API process.
// It carries nothing sensitive: the DB URL is stored raw here only so the
// handler can redact its password before returning it. The encryption key is
// never stored in this struct.
type RuntimeInfo struct {
	StartedAt   time.Time
	Version     string
	Commit      string
	Addr        string
	DBURL       string
	Env         string
	ServiceMode string // "windows-service" | "systemd" | "foreground" | "docker"
	LogPath     string // where this process writes its log (operator hint)
}

// SetRuntime records the process identity captured at startup (called once
// from main after NewServer).
func (s *Server) SetRuntime(rt RuntimeInfo) { s.rt = rt }

// redactDBURL returns a database URL with its password masked so it is safe to
// display/log. It never returns the original password; URLs without an embedded
// password are returned unchanged.
func redactDBURL(raw string) string {
	if raw == "" {
		return ""
	}
	return dbURLPasswordRe.ReplaceAllString(raw, "://$1:****@")
}

// systemRuntime returns the identity of THIS API process so operators can tell
// exactly which instance is serving — and trace the encryption state to a
// specific PID. No secrets are returned: the key is never included, and the DB
// URL has its password redacted.
func (s *Server) systemRuntime(w http.ResponseWriter, r *http.Request) {
	host, _ := os.Hostname()
	es := s.computeEncState(r)

	keyID := ""
	if c := s.cipher(); c != nil {
		keyID = c.KeyID()
	}

	startedAt := ""
	uptime := time.Duration(0)
	if !s.rt.StartedAt.IsZero() {
		startedAt = s.rt.StartedAt.Format(time.RFC3339)
		uptime = time.Since(s.rt.StartedAt).Round(time.Second)
	}

	addr := s.rt.Addr
	if addr == "" {
		addr = ":8090"
	}

	// Live deployment checks so System Health can confirm a service restart came
	// up correctly: DB reachable, encryption actually enabled, and the Relay Agent
	// installer is staged/servable.
	ctx := r.Context()
	_, dbErr := s.queries.CountEncryptedCredentials(ctx) // cheap round-trip = DB reachable
	dbConnected := dbErr == nil
	_, winOK := agentBinaryPath("windows")
	_, linOK := agentBinaryPath("linux")
	relayInstaller := winOK || linOK

	writeJSON(w, http.StatusOK, map[string]any{
		"process_id":                os.Getpid(),
		"started_at":                startedAt,
		"uptime":                    uptime.String(),
		"uptime_seconds":            int64(uptime.Seconds()),
		"api_version":               firstNonEmpty(s.rt.Version, "dev"),
		"git_commit":                firstNonEmpty(s.rt.Commit, "unknown"),
		"database_url_redacted":     redactDBURL(s.rt.DBURL),
		"database_connected":        dbConnected,
		"encryption_state":          es.Status,
		"encryption_enabled":        es.Status == encEnabled,
		"key_id":                    keyID,
		"port":                      addr,
		"environment":               firstNonEmpty(s.rt.Env, "development"),
		"hostname":                  host,
		"service_mode":              firstNonEmpty(s.rt.ServiceMode, "foreground"),
		"log_path":                  s.rt.LogPath,
		"relay_installer_available": relayInstaller,
	})
}
