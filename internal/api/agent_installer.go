package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/api/agentassets"
	"github.com/coralsearesorts/hims/internal/auth"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// Operator-grade Relay Agent installer packaging. The HIMS API serves a ready-to-
// run installer .zip per agent: a prebuilt agent binary + a generated installer
// script (interactive / silent / repair / uninstall) that registers a Windows
// service, with the agent's HIMS URL + one-time token + site identity baked in.
// No build tools are needed on the operator's side.
//
// The agent binary is staged on the HIMS server by the deployer (build-agents
// script) into HIMS_AGENT_DIST_DIR; the API never compiles anything at runtime.

// relayAgentVersion is the agent version stamped into generated scripts/README.
// Keep in sync with cmd/hims-agent agentVersion.
const relayAgentVersion = "1.0.0"

// agentDistDir resolves the directory holding the prebuilt agent binaries.
// Priority: HIMS_AGENT_DIST_DIR, then <dir-of-this-exe>/agents, then ./dist/agents.
func agentDistDir() string {
	if d := strings.TrimSpace(os.Getenv("HIMS_AGENT_DIST_DIR")); d != "" {
		return d
	}
	if exe, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), "agents")
		if st, err := os.Stat(cand); err == nil && st.IsDir() {
			return cand
		}
	}
	return filepath.Join("dist", "agents")
}

// agentBinaryFile maps an OS to the staged binary filename + the name it should
// carry inside the installer package.
func agentBinaryFile(osName string) (staged, inZip string) {
	switch osName {
	case "linux":
		return "hims-agent-linux-amd64", "hims-agent"
	default: // windows
		return "hims-agent-windows-amd64.exe", "hims-agent.exe"
	}
}

func agentBinaryPath(osName string) (string, bool) {
	staged, _ := agentBinaryFile(osName)
	p := filepath.Join(agentDistDir(), staged)
	if st, err := os.Stat(p); err == nil && !st.IsDir() {
		return p, true
	}
	return p, false
}

// publicBaseURL is the externally-reachable HIMS URL to bake into the installer
// (so the agent calls back to the same address the operator uses). HIMS_PUBLIC_URL
// overrides; otherwise it is derived from the request.
func publicBaseURL(r *http.Request) string {
	if u := strings.TrimSpace(os.Getenv("HIMS_PUBLIC_URL")); u != "" {
		return strings.TrimRight(u, "/")
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = p
	}
	return scheme + "://" + r.Host
}

// agentInstallerAvailability (GET /agents/installer-availability) tells the UI
// which OS installers can be downloaded (i.e. which agent binaries the deployer
// has staged on the server), so it can show the right buttons + a deployer hint.
func (s *Server) agentInstallerAvailability(w http.ResponseWriter, r *http.Request) {
	_, winOK := agentBinaryPath("windows")
	_, linOK := agentBinaryPath("linux")
	writeJSON(w, http.StatusOK, map[string]any{
		"windows":  winOK,
		"linux":    linOK,
		"dist_dir": agentDistDir(),
		"hint":     "Stage agent binaries with deploy/build-agents.ps1 (or .sh) into the dist dir; see README.",
	})
}

// regenerateAgentToken (POST /agents/{id}/regenerate-token) mints a NEW one-time
// token for an agent (invalidating the old one) so the operator can re-download a
// fresh installer if the previous token/package was lost. Returned once.
func (s *Server) regenerateAgentToken(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	a, err := s.queries.GetRelayAgent(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	token := genToken()
	if err := s.queries.SetRelayAgentToken(r.Context(), db.SetRelayAgentTokenParams{ID: id, TokenHash: auth.HashToken(token)}); err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "credential", "agent.regenerate_token", "relay_agent", id.String(), "Regenerated relay agent token for "+a.Name, nil)
	writeJSON(w, http.StatusOK, map[string]any{"token": token})
}

type installerTemplateData struct {
	URL, Token, AgentName, SiteName string
	ServiceName, DisplayName        string
	Version, ExeName, OSLabel       string
	InsecureTLS                     string
}

// downloadAgentInstaller (POST /agents/{id}/installer) builds and streams the
// per-agent installer .zip. The caller supplies the agent's current token (held
// transiently from registration/regeneration) — only the SHA-256 hash is stored,
// so a valid token proves authorization and lets us bake it into the package
// without ever persisting the plaintext.
func (s *Server) downloadAgentInstaller(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	var req struct {
		Token       string `json:"token"`
		OS          string `json:"os"`
		InsecureTLS bool   `json:"insecure_tls"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	osName := strings.ToLower(strings.TrimSpace(req.OS))
	if osName != "linux" {
		osName = "windows"
	}

	a, err := s.queries.GetRelayAgent(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if strings.TrimSpace(req.Token) == "" || auth.HashToken(req.Token) != a.TokenHash {
		http.Error(w, "the supplied token does not match this agent — click \"Regenerate token\" and download again", http.StatusForbidden)
		return
	}

	binPath, ok := agentBinaryPath(osName)
	if !ok {
		http.Error(w, "the "+osName+" agent binary is not staged on this HIMS server yet. Ask your administrator to run deploy/build-agents (it stages binaries into "+agentDistDir()+").", http.StatusServiceUnavailable)
		return
	}
	binBytes, err := os.ReadFile(binPath)
	if err != nil {
		writeErr(w, err)
		return
	}

	site := ""
	if a.LocationID != nil {
		if loc, lerr := s.queries.GetLocation(r.Context(), *a.LocationID); lerr == nil {
			site = loc.Name
		}
	}
	if site == "" {
		site = "(unassigned)"
	}
	_, inZip := agentBinaryFile(osName)
	data := installerTemplateData{
		URL: publicBaseURL(r), Token: req.Token, AgentName: a.Name, SiteName: site,
		ServiceName: serviceNameWindows, DisplayName: "HIMS Relay Agent",
		Version: relayAgentVersion, ExeName: inZip, OSLabel: osName + "/amd64",
		InsecureTLS: boolToFlag(req.InsecureTLS),
	}

	// Which generated text files go in the package, per OS.
	var files []zipEntry
	render := func(tmpl, outName string) error {
		b, rerr := renderAgentTemplate(tmpl, data)
		if rerr != nil {
			return rerr
		}
		files = append(files, zipEntry{name: outName, data: b})
		return nil
	}
	if osName == "windows" {
		if err := render("Install-HIMSRelayAgent.ps1.tmpl", "Install-HIMSRelayAgent.ps1"); err != nil {
			writeErr(w, err)
			return
		}
		if err := render("install.cmd.tmpl", "install.cmd"); err != nil {
			writeErr(w, err)
			return
		}
	} else {
		if err := render("install-linux.sh.tmpl", "install.sh"); err != nil {
			writeErr(w, err)
			return
		}
	}
	if err := render("README.txt.tmpl", "README.txt"); err != nil {
		writeErr(w, err)
		return
	}
	files = append(files, zipEntry{name: inZip, data: binBytes, exec: true})

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		hdr := &zip.FileHeader{Name: f.name, Method: zip.Deflate}
		if f.exec {
			hdr.SetMode(0o755)
		}
		fw, zerr := zw.CreateHeader(hdr)
		if zerr != nil {
			writeErr(w, zerr)
			return
		}
		if _, zerr := fw.Write(f.data); zerr != nil {
			writeErr(w, zerr)
			return
		}
	}
	if err := zw.Close(); err != nil {
		writeErr(w, err)
		return
	}

	s.audit(r, "credential", "agent.download_installer", "relay_agent", id.String(),
		"Downloaded "+osName+" installer for relay agent "+a.Name, map[string]any{"os": osName})

	fname := "hims-relay-agent-" + slugify(a.Name) + "-" + osName + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+fname+"\"")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(buf.Bytes())
}

type zipEntry struct {
	name string
	data []byte
	exec bool
}

// serviceNameWindows mirrors cmd/hims-agent serviceName so the installer
// registers (and the agent answers to) the same Windows Service name.
const serviceNameWindows = "HIMSRelayAgent"

func renderAgentTemplate(name string, data installerTemplateData) ([]byte, error) {
	raw, err := agentassets.FS.ReadFile(name)
	if err != nil {
		return nil, err
	}
	t, err := template.New(name).Parse(string(raw))
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	if err := t.Execute(&b, data); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func boolToFlag(b bool) string {
	if b {
		return "1"
	}
	return ""
}

// slugify makes an agent name safe for a download filename.
func slugify(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_' || r == '.':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "agent"
	}
	return out
}
