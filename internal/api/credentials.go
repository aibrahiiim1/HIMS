package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// credentialDTO is the ONLY shape a credential is ever returned in: metadata
// only. The encrypted blob and key id never leave the server, and the
// plaintext secret is never echoed back.
type credentialDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Weak      bool   `json:"weak"`
	CreatedAt string `json:"created_at"`
}

func toCredentialDTO(c db.Credential) credentialDTO {
	return credentialDTO{
		ID:        c.ID.String(),
		Name:      c.Name,
		Kind:      c.Kind,
		Weak:      c.Weak,
		CreatedAt: c.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

func (s *Server) listCredentials(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListCredentials(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]credentialDTO, len(rows))
	for i, c := range rows {
		out[i] = toCredentialDTO(c) // strips blob + key id
	}
	writeJSON(w, http.StatusOK, out)
}

type createCredentialReq struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`   // snmp_v2c, ssh, http_basic, …
	Secret string `json:"secret"` // community / password / token — encrypted, never stored plain
	Weak   bool   `json:"weak"`
}

func (s *Server) createCredential(w http.ResponseWriter, r *http.Request) {
	if s.cipher == nil {
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
	blob, keyID, err := s.cipher.Seal([]byte(req.Secret))
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
	writeJSON(w, http.StatusCreated, toCredentialDTO(c))
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
