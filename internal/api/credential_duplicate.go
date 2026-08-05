package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

type duplicateCredentialReq struct {
	NewKind  string `json:"new_kind"` // ssh | windows | http_basic | vendor_api | onvif | ... (see credentialKinds)
	Username string `json:"username"` // required only when reusing a secret-only source (SNMP community / token) as a user:password login
	Name     string `json:"name"`     // optional; defaults to "<source> (as <new_kind>)"
}

// userPassKind reports whether a credential kind stores a "user:password" secret
// (as opposed to a bare community string / token like snmp_v2c / snmp_v3).
// CredWinRM/CredWMI are retired for new credentials (migration 000089) but are
// still listed so any pre-existing row keeps being treated as user:password.
func userPassKind(k string) bool {
	switch domain.CredentialKind(k) {
	case domain.CredSSH, domain.CredWindows, domain.CredCLI, domain.CredHTTPBasic,
		domain.CredVendorAPI, domain.CredONVIF, domain.CredLDAP,
		domain.CredWinRM, domain.CredWMI:
		return true
	}
	return false
}

// duplicateCredential handles POST /credentials/{id}/duplicate — reuse an
// existing credential's stored secret under a DIFFERENT kind WITHOUT the
// operator re-typing the secret. The classic case the operator hits: the right
// password was saved only as an SNMP community (snmp_v2c), but a device needs it
// as an SSH / vendor_api / http_basic login — discovery then "fails auth" even
// though the secret exists. Instead of guessing, the operator clicks
// "Duplicate as login credential" and supplies just the username.
//
// The secret is decrypted and re-sealed entirely server-side; the plaintext is
// never returned or logged. When the source is already a user:password kind, the
// secret is reused verbatim (only the kind changes).
func (s *Server) duplicateCredential(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	cph := s.cipher()
	if cph == nil {
		http.Error(w, "encryption key not configured (set HIMS_ENCRYPTION_KEY)", http.StatusServiceUnavailable)
		return
	}
	var req duplicateCredentialReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	req.NewKind = strings.TrimSpace(req.NewKind)
	if req.NewKind == "" {
		http.Error(w, "new_kind is required", http.StatusBadRequest)
		return
	}
	if ok, msg := validCredentialKind(req.NewKind); !ok {
		http.Error(w, msg, http.StatusBadRequest)
		return
	}

	src, err := s.queries.GetCredential(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	plain, err := cph.Open(src.EncryptedBlob, src.KeyID)
	if err != nil {
		http.Error(w, "cannot decrypt the source credential (encryption key rotated?) — re-enter it instead", http.StatusConflict)
		return
	}
	secret := string(plain)

	// Shape the new secret for the target kind.
	newSecret := secret
	if userPassKind(req.NewKind) {
		if userPassKind(src.Kind) && strings.Contains(secret, ":") {
			newSecret = secret // already user:password — only the kind changes
		} else {
			u := strings.TrimSpace(req.Username)
			if u == "" {
				http.Error(w, "username is required to reuse this secret as a "+req.NewKind+" login (e.g. root, admin, administrator)", http.StatusBadRequest)
				return
			}
			newSecret = u + ":" + secret
		}
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = src.Name + " (as " + req.NewKind + ")"
	}
	blob, keyID, err := cph.Seal([]byte(newSecret))
	if err != nil {
		writeErr(w, err)
		return
	}
	c, err := s.queries.CreateCredential(ctx, db.CreateCredentialParams{
		Name:          name,
		Kind:          req.NewKind,
		EncryptedBlob: blob,
		KeyID:         keyID,
		Weak:          isWeakSecret(req.NewKind, newSecret),
		Metadata:      []byte("{}"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "credential", "credential.duplicate", "credential", c.ID.String(),
		"Duplicated credential "+src.Name+" ("+src.Kind+") as "+c.Name+" ("+c.Kind+")", map[string]any{"source": src.ID.String()})
	writeJSON(w, http.StatusCreated, toCredentialDTO(c))
}
