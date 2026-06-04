package api

import (
	"net/http"

	"github.com/coralsearesorts/hims/internal/secret"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// Encryption Key Lifecycle Management.
//
// The raw key lives ONLY in HIMS_ENCRYPTION_KEY (process env) — never in the
// DB, logs, or any response after its one-time display. This module exposes
// the key's health (fingerprint + lifecycle), and the generate / validate /
// rotate / reset workflows. Fingerprints are one-way (SHA-256) and safe to
// store + show.

type encryptionStatus struct {
	Status            string   `json:"status"` // enabled | pending_restart | missing
	Configured        bool     `json:"configured"`
	Enabled           bool     `json:"enabled"`
	Algorithm         string   `json:"algorithm"`
	Fingerprint       string   `json:"fingerprint"`
	KeyID             string   `json:"key_id"`
	Version           int32    `json:"version"`
	CreatedAt         *string  `json:"created_at"`
	LastRotationAt    *string  `json:"last_rotation_at"`
	LastValidationAt  *string  `json:"last_validation_at"`
	EncryptedCount    int64    `json:"encrypted_count"`
	NeedsResetCount   int64    `json:"needs_reset_count"`
	UndecryptableCount int64   `json:"undecryptable_count"`
	FingerprintMatch  bool     `json:"fingerprint_match"`
	Warnings          []string `json:"warnings"`
}

func tsPtr(t interface{ IsZero() bool }) *string { return nil } // unused placeholder

func (s *Server) encryptionStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	meta, metaErr := s.queries.GetEncryptionMetadata(ctx)
	hasMeta := metaErr == nil

	out := encryptionStatus{Algorithm: "AES-256-GCM", Version: 1, Warnings: []string{}}
	if hasMeta {
		out.Algorithm = meta.Algorithm
		out.Version = meta.Version
		c := meta.CreatedAt.Format("2006-01-02T15:04:05Z07:00")
		out.CreatedAt = &c
		if meta.LastRotationAt != nil {
			v := meta.LastRotationAt.Format("2006-01-02T15:04:05Z07:00")
			out.LastRotationAt = &v
		}
		if meta.LastValidationAt != nil {
			v := meta.LastValidationAt.Format("2006-01-02T15:04:05Z07:00")
			out.LastValidationAt = &v
		}
	}

	if encN, err := s.queries.CountEncryptedCredentials(ctx); err == nil {
		out.EncryptedCount = encN
	}
	if rn, err := s.queries.CountCredentialsNeedingReentry(ctx); err == nil {
		out.NeedsResetCount = rn
	}

	if s.cipher != nil {
		out.Configured = true
		out.Enabled = true
		out.Status = "enabled"
		out.Fingerprint = s.cipher.Fingerprint()
		out.KeyID = s.cipher.KeyID()
		// Adopt the running key as the recorded baseline if none exists yet.
		if !hasMeta {
			_ = s.queries.UpsertEncryptionMetadata(ctx, db.UpsertEncryptionMetadataParams{Fingerprint: s.cipher.Fingerprint(), KeyID: s.cipher.KeyID()})
			out.FingerprintMatch = true
		} else {
			out.FingerprintMatch = meta.Fingerprint == "" || meta.Fingerprint == s.cipher.Fingerprint()
			if !out.FingerprintMatch {
				out.Warnings = append(out.Warnings, "Loaded key fingerprint does not match the recorded fingerprint — the wrong key may be configured.")
			}
		}
		if und, err := s.queries.CountUndecryptableCredentials(ctx, s.cipher.KeyID()); err == nil {
			out.UndecryptableCount = und
			if und > 0 {
				out.Warnings = append(out.Warnings, "Some credentials were sealed with a different key and cannot be decrypted by the current key.")
			}
		}
	} else {
		out.Configured = hasMeta && meta.Fingerprint != ""
		out.Enabled = false
		if out.Configured {
			out.Status = "pending_restart"
			out.Fingerprint = meta.Fingerprint
			out.KeyID = meta.KeyID
			out.Warnings = append(out.Warnings, "A key has been generated but HIMS_ENCRYPTION_KEY is not set in this process. Set it and restart to activate encryption.")
		} else {
			out.Status = "missing"
		}
		// Without a loaded key, every encrypted credential is undecryptable.
		out.UndecryptableCount = out.EncryptedCount
		if out.EncryptedCount > 0 {
			out.Warnings = append(out.Warnings, "No encryption key is loaded but encrypted credentials exist — credential operations are disabled until the key is restored.")
		}
	}
	if out.NeedsResetCount > 0 {
		out.Warnings = append(out.Warnings, "Some credentials need their secret re-entered after a key reset.")
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) encryptionGenerate(w http.ResponseWriter, r *http.Request) {
	if s.cipher != nil {
		http.Error(w, "an encryption key is already active; use Rotate to change it", http.StatusConflict)
		return
	}
	key, err := secret.GenerateKey()
	if err != nil {
		writeErr(w, err)
		return
	}
	fp, kid, err := secret.FingerprintForKey(key)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := s.queries.UpsertEncryptionMetadata(r.Context(), db.UpsertEncryptionMetadataParams{Fingerprint: fp, KeyID: kid}); err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "security", "encryption.key.generate", "encryption_key", kid, "Generated a new encryption key", map[string]any{"fingerprint": fp})
	// The key is returned exactly once. It is never stored or logged.
	writeJSON(w, http.StatusCreated, map[string]any{
		"key":         key,
		"fingerprint": fp,
		"key_id":      kid,
		"instructions": "Set HIMS_ENCRYPTION_KEY to this value in the API process environment and restart. " +
			"Save it now — it cannot be shown again.",
	})
}

func (s *Server) encryptionValidate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if s.cipher == nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": "missing", "detail": "No encryption key is loaded."})
		return
	}
	// Round-trip self-test: seal then open a known marker.
	marker := []byte("hims-encryption-self-test")
	blob, kid, err := s.cipher.Seal(marker)
	status, detail := "valid", "Key loaded and credential encryption round-trip succeeded."
	if err != nil {
		status, detail = "invalid", "Encryption self-test failed: "+err.Error()
	} else if got, oerr := s.cipher.Open(blob, kid); oerr != nil || string(got) != string(marker) {
		status, detail = "invalid", "Decryption self-test failed."
	}
	match := true
	if meta, e := s.queries.GetEncryptionMetadata(ctx); e == nil && meta.Fingerprint != "" {
		match = meta.Fingerprint == s.cipher.Fingerprint()
	}
	_ = s.queries.TouchValidation(ctx)
	s.audit(r, "security", "encryption.key.validate", "encryption_key", s.cipher.KeyID(), "Validated encryption key", map[string]any{"status": status, "fingerprint_match": match})
	writeJSON(w, http.StatusOK, map[string]any{
		"status": status, "detail": detail, "fingerprint_match": match,
		"fingerprint": s.cipher.Fingerprint(), "key_id": s.cipher.KeyID(),
	})
}

func (s *Server) encryptionRotate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if s.cipher == nil {
		http.Error(w, "the current encryption key is not available; rotation requires the current key to be loaded", http.StatusBadRequest)
		return
	}
	newKey, err := secret.GenerateKey()
	if err != nil {
		writeErr(w, err)
		return
	}
	newC, err := secret.NewCipher(newKey)
	if err != nil {
		writeErr(w, err)
		return
	}
	blobs, err := s.queries.ListCredentialBlobs(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	rotated := 0
	failed := []map[string]string{}
	for _, b := range blobs {
		newBlob, newKeyID, rerr := secret.ReKey(s.cipher, newC, b.EncryptedBlob, b.KeyID)
		if rerr != nil {
			failed = append(failed, map[string]string{"name": b.Name, "reason": rerr.Error()})
			continue
		}
		if uerr := s.queries.UpdateCredentialSecret(ctx, db.UpdateCredentialSecretParams{ID: b.ID, EncryptedBlob: newBlob, KeyID: newKeyID}); uerr != nil {
			failed = append(failed, map[string]string{"name": b.Name, "reason": uerr.Error()})
			continue
		}
		rotated++
	}
	if err := s.queries.RecordRotation(ctx, db.RecordRotationParams{Fingerprint: newC.Fingerprint(), KeyID: newC.KeyID()}); err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "security", "encryption.key.rotate", "encryption_key", newC.KeyID(), "Rotated encryption key", map[string]any{"rotated": rotated, "failed": len(failed), "fingerprint": newC.Fingerprint()})
	writeJSON(w, http.StatusOK, map[string]any{
		"new_key":     newKey,
		"fingerprint": newC.Fingerprint(),
		"key_id":      newC.KeyID(),
		"rotated":     rotated,
		"failed":      failed,
		"instructions": "All credentials were re-encrypted with the new key. Set HIMS_ENCRYPTION_KEY to this value and restart the API " +
			"to activate it — until then credential operations are paused because the running process still holds the previous key. " +
			"Save the key now; it cannot be shown again.",
	})
}

func (s *Server) encryptionResetCredentials(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Confirm string `json:"confirm"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Confirm != "RESET CREDENTIALS" {
		http.Error(w, `confirmation phrase required: type "RESET CREDENTIALS"`, http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	n, _ := s.queries.CountEncryptedCredentials(ctx)
	if err := s.queries.ClearAllCredentialSecrets(ctx); err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "security", "encryption.credentials.reset", "credential", "", "Reset credential secrets (lost-key recovery)", map[string]any{"affected": n})
	writeJSON(w, http.StatusOK, map[string]any{
		"reset":   n,
		"message": "Credential secrets were cleared. Records, metadata, assignments and group memberships are preserved; affected credentials are flagged for secret re-entry.",
	})
}

func (s *Server) credentialsNeedingReentry(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListCredentialsNeedingReentry(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) encryptionRecoveryGuide(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, recoveryGuideSections)
}

type guideSection struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

var recoveryGuideSections = []guideSection{
	{"What the encryption key is", "HIMS encrypts every credential secret (SSH/WinRM passwords, SNMP communities, SNMPv3 credentials, API tokens) with AES-256-GCM before it is written to the database. The key is a 32-byte AES key supplied to the API process as the HIMS_ENCRYPTION_KEY environment variable (base64). The database stores only the ciphertext and a short non-reversible key id; it never stores the key itself."},
	{"Why it matters", "The key is the ONLY thing that can decrypt stored credential secrets. If it is lost, existing secrets cannot be recovered — credential-based discovery, credential testing, and authenticated scans will fail. Inventory, monitoring, topology, reports and search continue to work because they do not depend on credential secrets."},
	{"How to back it up", "Immediately after generating the key, store it in your organization's secrets manager (e.g. Vault, 1Password, AWS/Azure secrets) AND an offline copy in a sealed location. The Generate and Rotate workflows show the key exactly once and let you download a recovery file (hims-recovery-key.txt). HIMS cannot show the key again afterwards."},
	{"How to restore a server", "Provision the new host, restore the database, then set HIMS_ENCRYPTION_KEY to the SAME key the database's credentials were encrypted with, and start the API. Verify on Settings → Security → Encryption that Status is Enabled and the fingerprint matches your records, then run Validate."},
	{"How to rotate a key", "With the current key loaded, run Rotate. HIMS re-encrypts every credential under a freshly generated key and shows it once. Set HIMS_ENCRYPTION_KEY to the new key and restart the API. Validate afterwards. Keep the previous key until you have confirmed the new key works."},
	{"What happens if the key is lost", "If the key is unrecoverable, the encrypted secrets cannot be decrypted. Use Reset Credential Secrets to clear only the secret fields while preserving credential records, metadata, site assignments and group memberships. Then generate a new key, restart, and re-enter each secret from the Credentials Needing Re-entry page."},
}
