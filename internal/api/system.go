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
	StartedAt time.Time
	Version   string
	Commit    string
	Addr      string
	DBURL     string
	Env       string
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

	writeJSON(w, http.StatusOK, map[string]any{
		"process_id":            os.Getpid(),
		"started_at":            startedAt,
		"uptime":                uptime.String(),
		"uptime_seconds":        int64(uptime.Seconds()),
		"api_version":           firstNonEmpty(s.rt.Version, "dev"),
		"git_commit":            firstNonEmpty(s.rt.Commit, "unknown"),
		"database_url_redacted": redactDBURL(s.rt.DBURL),
		"encryption_state":      es.Status,
		"key_id":                keyID,
		"port":                  addr,
		"environment":           firstNonEmpty(s.rt.Env, "development"),
		"hostname":              host,
	})
}
