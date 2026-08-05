package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/coralsearesorts/hims/internal/credtest"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// credentialDTO is the ONLY shape a credential is ever returned in: metadata
// only. The encrypted blob and key id never leave the server, and the
// plaintext secret is never echoed back.
type credentialDTO struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Kind               string `json:"kind"`
	Weak               bool   `json:"weak"`
	NeedsSecretReentry bool   `json:"needs_secret_reentry"`
	CreatedAt          string `json:"created_at"`
	// UsageCount = distinct (scoped) devices that use this credential as their
	// bound management credential or their CCTV web credential. The Credentials
	// UI shows it per credential and links the count to the device list.
	UsageCount int `json:"usage_count"`
}

func toCredentialDTO(c db.Credential) credentialDTO {
	return credentialDTO{
		ID:                 c.ID.String(),
		Name:               c.Name,
		Kind:               c.Kind,
		Weak:               c.Weak,
		NeedsSecretReentry: c.NeedsSecretReentry,
		CreatedAt:          c.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

func (s *Server) listCredentials(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := s.queries.ListCredentials(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	// Per-credential usage = distinct scoped devices bound to it (primary or CCTV).
	usage := map[uuid.UUID]int{}
	if devs, derr := s.queries.ListAllDevices(ctx); derr == nil {
		usage = credentialUsage(s.scopeDevices(ctx, devs))
	}
	out := make([]credentialDTO, len(rows))
	for i, c := range rows {
		d := toCredentialDTO(c) // strips blob + key id
		d.UsageCount = usage[c.ID]
		out[i] = d
	}
	writeJSON(w, http.StatusOK, out)
}

// credentialUsage tallies, per credential id, the DISTINCT devices that use it as
// their bound management credential (devices.credential_id) OR their CCTV web
// credential (devices.cctv_credential_id). A device counts once per credential
// even if both its bindings point at the same credential.
func credentialUsage(devs []db.Device) map[uuid.UUID]int {
	seen := map[uuid.UUID]map[uuid.UUID]bool{}
	mark := func(cred *uuid.UUID, dev uuid.UUID) {
		if cred == nil {
			return
		}
		set := seen[*cred]
		if set == nil {
			set = map[uuid.UUID]bool{}
			seen[*cred] = set
		}
		set[dev] = true
	}
	for _, d := range devs {
		mark(d.CredentialID, d.ID)
		mark(d.CctvCredentialID, d.ID)
	}
	out := make(map[uuid.UUID]int, len(seen))
	for c, set := range seen {
		out[c] = len(set)
	}
	return out
}

// credentialDeviceDTO is the slim per-device shape returned by the credential
// usage drill-down (GET /credentials/{id}/devices). bound_primary / bound_cctv
// say HOW the device uses the credential.
type credentialDeviceDTO struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	PrimaryIP    string `json:"primary_ip"`
	Category     string `json:"category"`
	Status       string `json:"status"`
	BoundPrimary bool   `json:"bound_primary"`
	BoundCCTV    bool   `json:"bound_cctv"`
}

// credentialDevices handles GET /credentials/{id}/devices — every (scoped) device
// that uses this credential as its bound management credential or CCTV web
// credential. Powers the clickable usage count on the Credentials page.
func (s *Server) credentialDevices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid credential id", http.StatusBadRequest)
		return
	}
	devs, err := s.queries.ListAllDevices(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := []credentialDeviceDTO{}
	for _, d := range s.scopeDevices(ctx, devs) {
		bp := d.CredentialID != nil && *d.CredentialID == id
		bc := d.CctvCredentialID != nil && *d.CctvCredentialID == id
		if !bp && !bc {
			continue
		}
		ip := ""
		if d.PrimaryIp != nil && d.PrimaryIp.IsValid() {
			ip = d.PrimaryIp.String()
		}
		out = append(out, credentialDeviceDTO{
			ID: d.ID.String(), Name: d.Name, PrimaryIP: ip,
			Category: d.Category, Status: d.Status, BoundPrimary: bp, BoundCCTV: bc,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// credentialKinds mirrors the credentials_kind_check CHECK constraint
// (migrations/000093_credential_zkteco_kind.up.sql). Without this the DB is the
// only gate, and a bad kind surfaces to the operator as the raw driver error
// 'violates check constraint "credentials_kind_check" (SQLSTATE 23514)', which
// names neither the offending value nor the valid ones.
//
// Retired kinds ('winrm', 'wmi' — superseded by the unified 'windows' in
// migration 000089) are deliberately absent: they must fail. Keep this in step
// with the constraint whenever a migration changes it.
var credentialKinds = []string{
	"snmp_v2c", "snmp_v3", "ssh", "cli", "windows",
	"http_basic", "onvif", "vendor_api", "ldap", "zkteco",
}

// validCredentialKind reports whether kind is storable, plus a message naming
// the accepted values (and the replacement for a retired kind) when it is not.
func validCredentialKind(kind string) (bool, string) {
	if slices.Contains(credentialKinds, kind) {
		return true, ""
	}
	hint := ""
	if kind == "winrm" || kind == "wmi" {
		hint = " — 'winrm' and 'wmi' were replaced by the single 'windows' kind, which covers WinRM, WMI/DCOM, SMB and the relay agent"
	}
	return false, "unknown credential kind " + strconv.Quote(kind) + hint +
		". Valid kinds: " + strings.Join(credentialKinds, ", ")
}

type createCredentialReq struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`   // snmp_v2c, ssh, http_basic, …
	Secret string `json:"secret"` // community / password / token — encrypted, never stored plain
	Weak   bool   `json:"weak"`
}

func (s *Server) createCredential(w http.ResponseWriter, r *http.Request) {
	cph := s.cipher()
	if cph == nil {
		http.Error(w, "encryption key not configured (set HIMS_ENCRYPTION_KEY)", http.StatusServiceUnavailable)
		return
	}
	var req createCredentialReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Name == "" || req.Kind == "" || req.Secret == "" {
		http.Error(w, "name, kind, and secret are required", http.StatusBadRequest)
		return
	}
	if ok, msg := validCredentialKind(req.Kind); !ok {
		http.Error(w, msg, http.StatusBadRequest)
		return
	}
	if malformedUserPassSecret(req.Kind, req.Secret) {
		http.Error(w, "a "+req.Kind+" credential must be 'username:password' with a non-empty password", http.StatusBadRequest)
		return
	}
	blob, keyID, err := cph.Seal([]byte(req.Secret))
	if err != nil {
		writeErr(w, err)
		return
	}
	weak := req.Weak || isWeakSecret(req.Kind, req.Secret)
	c, err := s.queries.CreateCredential(r.Context(), db.CreateCredentialParams{
		Name:          req.Name,
		Kind:          req.Kind,
		EncryptedBlob: blob,
		KeyID:         keyID,
		Weak:          weak,
		Metadata:      []byte("{}"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	// req.Secret goes out of scope here; it is never logged or returned.
	s.audit(r, "credential", "credential.create", "credential", c.ID.String(), "Created credential "+c.Name+" ("+c.Kind+")", nil)
	writeJSON(w, http.StatusCreated, toCredentialDTO(c))
}

type updateCredentialReq struct {
	Name   string `json:"name"`   // rename (optional)
	Secret string `json:"secret"` // rotate the secret (optional; re-sealed)
}

// updateCredential handles PATCH /credentials/{id} — rename and/or rotate the
// secret. The secret is re-sealed; the plaintext is never logged or returned.
func (s *Server) updateCredential(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req updateCredentialReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	cur, err := s.queries.GetCredential(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = cur.Name
	}
	weak := cur.Weak
	if req.Secret != "" {
		if malformedUserPassSecret(cur.Kind, req.Secret) {
			http.Error(w, "a "+cur.Kind+" credential must be 'username:password' with a non-empty password", http.StatusBadRequest)
			return
		}
		cph := s.cipher()
		if cph == nil {
			http.Error(w, "encryption key not configured (set HIMS_ENCRYPTION_KEY)", http.StatusServiceUnavailable)
			return
		}
		blob, keyID, err := cph.Seal([]byte(req.Secret))
		if err != nil {
			writeErr(w, err)
			return
		}
		if err := s.queries.UpdateCredentialSecret(ctx, db.UpdateCredentialSecretParams{ID: id, EncryptedBlob: blob, KeyID: keyID}); err != nil {
			writeErr(w, err)
			return
		}
		weak = isWeakSecret(cur.Kind, req.Secret)
		// A freshly entered secret clears any "needs re-entry" flag from a reset.
		_ = s.queries.ClearReentryFlag(ctx, id)
		s.audit(r, "credential", "credential.secret.reenter", "credential", id.String(), "Re-entered credential secret", nil)
	}
	c, err := s.queries.UpdateCredentialMeta(ctx, db.UpdateCredentialMetaParams{ID: id, Name: name, Weak: weak})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toCredentialDTO(c))
}

// deleteCredential handles DELETE /credentials/{id}. It un-binds the credential
// from any devices (FK SET NULL) and drops its group memberships (FK CASCADE).
func (s *Server) deleteCredential(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if err := s.queries.DeleteCredential(ctx, id); err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "credential", "credential.delete", "credential", id.String(), "Deleted credential", nil)
	w.WriteHeader(http.StatusNoContent)
}

type bindCredentialReq struct {
	CredentialID *string `json:"credential_id"` // null clears the binding
}

func (s *Server) bindDeviceCredential(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	var req bindCredentialReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.queries.SetDeviceCredential(ctx, db.SetDeviceCredentialParams{
		ID: id, CredentialID: parseUUIDPtr(req.CredentialID),
	}); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// isWeakSecret flags obviously-weak SNMP communities so the resolver can sink
// them. We never log the value — only the boolean verdict is kept.
func isWeakSecret(kind, secret string) bool {
	if !strings.HasPrefix(kind, "snmp") {
		return false
	}
	switch strings.ToLower(secret) {
	case "public", "private", "community":
		return true
	}
	return false
}

// malformedUserPassSecret reports whether a user:password credential is missing its
// password — the secret has no ':' (so it silently becomes username-only with an empty
// password) or the password half is blank. Such a credential can NEVER authenticate yet
// fails opaquely at scan time (the 150.0.0.0/24 ESXi "C0r@lSe@" cred → user="C0r@lSe@",
// pass=""; and the earlier empty-password SSH cred). SNMP communities (no colon, the whole
// secret IS the community) are excluded — only user:password protocols are checked.
func malformedUserPassSecret(kind, secret string) bool {
	switch credtest.ProtocolForKind(kind) {
	case "ssh", "winrm", "wmi", "onvif", "http": // user:password protocols (incl. vendor_api/http_basic)
		_, pass := credtest.SplitUserPass(secret)
		return pass == ""
	}
	return false
}
