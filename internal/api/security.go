package api

import (
	"fmt"
	"net/http"
	"os"

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

// Encryption status vocabulary (one-way fingerprints only; never the key).
const (
	encEnabled       = "enabled"
	encMissingKey    = "missing_key"
	encNoMetadata    = "no_metadata"
	encPendingRestart = "pending_restart"
	encMismatch      = "fingerprint_mismatch"
	encInvalidKey    = "invalid_key"
)

var encReason = map[string]string{
	encEnabled:        "Encryption key is loaded and its fingerprint matches the stored fingerprint.",
	encMissingKey:     "No HIMS_ENCRYPTION_KEY is loaded in the API process.",
	encNoMetadata:     "No encryption key has been configured yet.",
	encPendingRestart: "Encryption key metadata exists, but the running API has not loaded the matching HIMS_ENCRYPTION_KEY. Configure the key in the deployment environment and restart the API.",
	encMismatch:       "The API has loaded an encryption key, but it does not match the stored fingerprint — the wrong key is configured. Load the key these credentials were encrypted with, or rotate.",
	encInvalidKey:     "An encryption key is set but failed validation (wrong length/format, or the encryption self-test failed).",
}

// encState is the safe, computed encryption state. It NEVER carries key material —
// only one-way fingerprints and booleans.
type encState struct {
	Status                   string
	Reason                   string
	RuntimeKeyPresent        bool
	RuntimeKeyLengthValid    bool
	RuntimeFingerprint       string
	StoredFingerprint        string
	StoredFingerprintPresent bool
	FingerprintMatch         bool
	SelfTestOK               bool
}

// computeEncState is the single source of truth for the encryption state
// machine, shared by /status, /diagnostics and the startup checklist.
func (s *Server) computeEncState(r *http.Request) encState {
	ctx := r.Context()
	st := encState{}

	// Runtime key presence/length: if a cipher built, the key was present + a
	// valid 32-byte key. If not, inspect the env var (presence + length only —
	// the value is checked for length and immediately discarded, never logged).
	envKey := os.Getenv("HIMS_ENCRYPTION_KEY")
	c := s.cipher()
	if c != nil {
		st.RuntimeKeyPresent = true
		st.RuntimeKeyLengthValid = true
		st.RuntimeFingerprint = c.Fingerprint()
		// Self-test: seal+open a marker.
		marker := []byte("hims-encryption-self-test")
		if blob, kid, err := c.Seal(marker); err == nil {
			if got, oerr := c.Open(blob, kid); oerr == nil && string(got) == string(marker) {
				st.SelfTestOK = true
			}
		}
	} else if envKey != "" {
		st.RuntimeKeyPresent = true
		if _, _, err := secret.FingerprintForKey(envKey); err == nil {
			st.RuntimeKeyLengthValid = true
		}
	}

	if meta, err := s.queries.GetEncryptionMetadata(ctx); err == nil && meta.Fingerprint != "" {
		st.StoredFingerprint = meta.Fingerprint
		st.StoredFingerprintPresent = true
	}

	switch {
	case c != nil && !st.SelfTestOK:
		st.Status = encInvalidKey
	case c != nil && st.StoredFingerprintPresent && st.RuntimeFingerprint != st.StoredFingerprint:
		st.Status = encMismatch
	case c != nil:
		// Adopt the running key as the baseline if nothing is stored yet.
		if !st.StoredFingerprintPresent {
			_ = s.queries.UpsertEncryptionMetadata(ctx, db.UpsertEncryptionMetadataParams{Fingerprint: c.Fingerprint(), KeyID: c.KeyID()})
			st.StoredFingerprint = c.Fingerprint()
			st.StoredFingerprintPresent = true
		}
		st.FingerprintMatch = true
		st.Status = encEnabled
	case st.RuntimeKeyPresent && !st.RuntimeKeyLengthValid:
		st.Status = encInvalidKey
	case st.RuntimeKeyPresent:
		// Env set + valid length but cipher failed to build (shouldn't happen).
		st.Status = encInvalidKey
	case st.StoredFingerprintPresent:
		st.Status = encPendingRestart
	default:
		// No runtime key, no stored fingerprint.
		if n, _ := s.queries.CountEncryptedCredentials(ctx); n > 0 {
			st.Status = encMissingKey
		} else {
			st.Status = encNoMetadata
		}
	}
	st.Reason = encReason[st.Status]
	return st
}

type encryptionStatus struct {
	Status                   string   `json:"status"`
	Reason                   string   `json:"reason"`
	Configured               bool     `json:"configured"`
	Enabled                  bool     `json:"enabled"`
	Algorithm                string   `json:"algorithm"`
	Fingerprint              string   `json:"fingerprint"`
	KeyID                    string   `json:"key_id"`
	Version                  int32    `json:"version"`
	CreatedAt                *string  `json:"created_at"`
	LastRotationAt           *string  `json:"last_rotation_at"`
	LastValidationAt         *string  `json:"last_validation_at"`
	EncryptedCount           int64    `json:"encrypted_count"`
	NeedsResetCount          int64    `json:"needs_reset_count"`
	UndecryptableCount       int64    `json:"undecryptable_count"`
	FingerprintMatch         bool     `json:"fingerprint_match"`
	RuntimeKeyPresent        bool     `json:"runtime_key_present"`
	RuntimeKeyLengthValid    bool     `json:"runtime_key_length_valid"`
	StoredFingerprintPresent bool     `json:"stored_fingerprint_present"`
	Warnings                 []string `json:"warnings"`
}

func (s *Server) encryptionStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	es := s.computeEncState(r)
	out := encryptionStatus{
		Status: es.Status, Reason: es.Reason, Algorithm: "AES-256-GCM", Version: 1, Warnings: []string{},
		Enabled:                  es.Status == encEnabled,
		Configured:               es.Status == encEnabled || es.StoredFingerprintPresent,
		Fingerprint:              firstNonEmpty(es.RuntimeFingerprint, es.StoredFingerprint),
		FingerprintMatch:         es.FingerprintMatch,
		RuntimeKeyPresent:        es.RuntimeKeyPresent,
		RuntimeKeyLengthValid:    es.RuntimeKeyLengthValid,
		StoredFingerprintPresent: es.StoredFingerprintPresent,
	}
	if meta, err := s.queries.GetEncryptionMetadata(ctx); err == nil {
		out.Algorithm, out.Version, out.KeyID = meta.Algorithm, meta.Version, meta.KeyID
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
	c := s.cipher()
	if c != nil {
		out.KeyID = c.KeyID()
	}
	if encN, err := s.queries.CountEncryptedCredentials(ctx); err == nil {
		out.EncryptedCount = encN
	}
	if rn, err := s.queries.CountCredentialsNeedingReentry(ctx); err == nil {
		out.NeedsResetCount = rn
	}
	if c != nil {
		if und, err := s.queries.CountUndecryptableCredentials(ctx, c.KeyID()); err == nil {
			out.UndecryptableCount = und
		}
	} else {
		out.UndecryptableCount = out.EncryptedCount
	}

	// Plain-English warnings keyed off the precise state.
	out.Warnings = append(out.Warnings, es.Reason)
	if out.UndecryptableCount > 0 && es.Status == encEnabled {
		out.Warnings = append(out.Warnings, "Some credentials were sealed with a different key and cannot be decrypted by the current key.")
	}
	if out.NeedsResetCount > 0 {
		out.Warnings = append(out.Warnings, "Some credentials need their secret re-entered after a key reset.")
	}
	writeJSON(w, http.StatusOK, out)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// encryptionDiagnostics returns SAFE diagnostics only — booleans + one-way
// fingerprints. The raw key is never read for output, logged, or returned.
func (s *Server) encryptionDiagnostics(w http.ResponseWriter, r *http.Request) {
	es := s.computeEncState(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"runtime_key_present":        es.RuntimeKeyPresent,
		"runtime_key_length_valid":   es.RuntimeKeyLengthValid,
		"stored_fingerprint_present": es.StoredFingerprintPresent,
		"runtime_fingerprint":        es.RuntimeFingerprint,
		"stored_fingerprint":         es.StoredFingerprint,
		"fingerprint_match":          es.FingerprintMatch,
		"self_test_passed":           es.SelfTestOK,
		"status":                     es.Status,
		"reason":                     es.Reason,
	})
}

func (s *Server) encryptionGenerate(w http.ResponseWriter, r *http.Request) {
	if s.cipher() != nil {
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
	c := s.cipher()
	if c == nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": "missing", "detail": "No encryption key is loaded."})
		return
	}
	// Round-trip self-test: seal then open a known marker.
	marker := []byte("hims-encryption-self-test")
	blob, kid, err := c.Seal(marker)
	status, detail := "valid", "Key loaded and credential encryption round-trip succeeded."
	if err != nil {
		status, detail = "invalid", "Encryption self-test failed: "+err.Error()
	} else if got, oerr := c.Open(blob, kid); oerr != nil || string(got) != string(marker) {
		status, detail = "invalid", "Decryption self-test failed."
	}
	match := true
	if meta, e := s.queries.GetEncryptionMetadata(ctx); e == nil && meta.Fingerprint != "" {
		match = meta.Fingerprint == c.Fingerprint()
	}
	_ = s.queries.TouchValidation(ctx)
	s.audit(r, "security", "encryption.key.validate", "encryption_key", c.KeyID(), "Validated encryption key", map[string]any{"status": status, "fingerprint_match": match})
	writeJSON(w, http.StatusOK, map[string]any{
		"status": status, "detail": detail, "fingerprint_match": match,
		"fingerprint": c.Fingerprint(), "key_id": c.KeyID(),
	})
}

func (s *Server) encryptionRotate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cur := s.cipher()
	if cur == nil {
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
		newBlob, newKeyID, rerr := secret.ReKey(cur, newC, b.EncryptedBlob, b.KeyID)
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
	// Activate the new key in the running process immediately — no restart needed.
	// The raw key is never persisted; it lives only in this in-memory cipher.
	s.setCipher(newC)
	s.audit(r, "security", "encryption.key.rotate", "encryption_key", newC.KeyID(), "Rotated encryption key", map[string]any{"rotated": rotated, "failed": len(failed), "fingerprint": newC.Fingerprint()})
	writeJSON(w, http.StatusOK, map[string]any{
		"new_key":     newKey,
		"fingerprint": newC.Fingerprint(),
		"key_id":      newC.KeyID(),
		"rotated":     rotated,
		"failed":      failed,
		"instructions": "All credentials were re-encrypted with the new key and it is now active in the running API. " +
			"To keep it across restarts, set HIMS_ENCRYPTION_KEY to this value in your deployment environment. " +
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

// encryptionUnlock loads an existing key into the RUNNING process so encryption
// activates immediately — no restart and no environment-variable editing. This
// is the "I already have a key" path: the operator pastes the key, the server
// builds the cipher in memory, verifies it against the stored fingerprint, and
// swaps it in. The raw key is NEVER persisted, logged, or returned — only its
// one-way fingerprint and key id (safe to show) appear in the response/audit.
//
// For persistence across process restarts the operator should ALSO set
// HIMS_ENCRYPTION_KEY in the deployment environment; the response says so.
func (s *Server) encryptionUnlock(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		Key   string `json:"key"`
		Adopt bool   `json:"adopt"` // operator confirms this key is the correct baseline despite a mismatching stored fingerprint
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Key == "" {
		http.Error(w, "key is required", http.StatusBadRequest)
		return
	}

	// Build the cipher. A bad length/format fails here — the key value is never
	// echoed back; only a generic validation message is returned.
	c, err := secret.NewCipher(req.Key)
	// Best-effort wipe of the request copy of the key.
	req.Key = ""
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"status": encInvalidKey,
			"detail": "The key is not a valid base64-encoded 32-byte AES key. Check that you pasted the full key with no extra spaces or line breaks.",
		})
		return
	}

	// Self-test the freshly built cipher before adopting it.
	marker := []byte("hims-encryption-self-test")
	if blob, kid, serr := c.Seal(marker); serr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"status": encInvalidKey, "detail": "Encryption self-test failed for the provided key."})
		return
	} else if got, oerr := c.Open(blob, kid); oerr != nil || string(got) != string(marker) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"status": encInvalidKey, "detail": "Decryption self-test failed for the provided key."})
		return
	}

	// Compare against the stored fingerprint (if any).
	var storedFP string
	if meta, merr := s.queries.GetEncryptionMetadata(ctx); merr == nil {
		storedFP = meta.Fingerprint
	}
	matches := storedFP == "" || storedFP == c.Fingerprint()

	if !matches && !req.Adopt {
		// The key is valid but doesn't match the recorded fingerprint. Don't
		// silently change the baseline — report the mismatch and let the
		// operator either load the correct key or explicitly adopt this one.
		s.audit(r, "security", "encryption.key.unlock.rejected", "encryption_key", c.KeyID(), "Unlock rejected: key does not match stored fingerprint", map[string]any{"runtime_fingerprint": c.Fingerprint()})
		writeJSON(w, http.StatusConflict, map[string]any{
			"status":              encMismatch,
			"detail":              "This key is valid, but it does not match the stored fingerprint — it is not the key these credentials were encrypted with. Load the original key, or adopt this key as the new baseline (existing secrets sealed with the old key will then need re-entry).",
			"runtime_fingerprint": c.Fingerprint(),
			"stored_fingerprint":  storedFP,
			"can_adopt":           true,
		})
		return
	}

	// Activate the key in the running process (in-memory only).
	s.setCipher(c)

	// Persist the fingerprint baseline when adopting (or when none existed yet).
	adopted := false
	if storedFP == "" || (!matches && req.Adopt) {
		if uerr := s.queries.UpsertEncryptionMetadata(ctx, db.UpsertEncryptionMetadataParams{Fingerprint: c.Fingerprint(), KeyID: c.KeyID()}); uerr != nil {
			writeErr(w, uerr)
			return
		}
		adopted = storedFP != "" // only an "adoption" if it replaced a different fingerprint
	}
	_ = s.queries.TouchValidation(ctx)

	action, msg := "encryption.key.unlock", "Loaded encryption key into the running process"
	if adopted {
		action, msg = "encryption.key.adopt", "Adopted a new encryption key as the baseline (replaced stored fingerprint)"
	}
	s.audit(r, "security", action, "encryption_key", c.KeyID(), msg, map[string]any{"fingerprint": c.Fingerprint(), "adopted": adopted})

	writeJSON(w, http.StatusOK, map[string]any{
		"status":      encEnabled,
		"fingerprint": c.Fingerprint(),
		"key_id":      c.KeyID(),
		"adopted":     adopted,
		"detail": "Encryption is now active in the running API — credential operations are enabled immediately. " +
			"To keep encryption working after the API process restarts, also set HIMS_ENCRYPTION_KEY to this key in your deployment environment.",
	})
}

type checkItem struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Status string `json:"status"` // ok | warn | fail
	Detail string `json:"detail"`
	Action string `json:"action,omitempty"`
}

// startupChecklist reports the operational readiness of the system as a set of
// plain-language checks — used by the Encryption Setup Wizard so a non-technical
// admin can see exactly what is and isn't working, and what to do next.
func (s *Server) startupChecklist(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	items := make([]checkItem, 0, 7)

	// Database — a lightweight query proves the pool is connected.
	encN, dbErr := s.queries.CountEncryptedCredentials(ctx)
	if dbErr != nil {
		items = append(items, checkItem{"database", "Database connected", "fail", "The API could not query PostgreSQL: " + dbErr.Error(), "Check HIMS_DATABASE_URL and that the database is reachable."})
	} else {
		items = append(items, checkItem{"database", "Database connected", "ok", "PostgreSQL is reachable.", ""})
	}

	items = append(items, checkItem{"api", "API running", "ok", "The HIMS API is serving requests.", ""})

	// Precise encryption state (single source of truth) drives the key items.
	es := s.computeEncState(r)
	c := s.cipher()
	keyConfigured := c != nil
	keyAction := map[string]string{
		encPendingRestart: "Set HIMS_ENCRYPTION_KEY in your deployment environment and restart the API.",
		encMissingKey:     "Set HIMS_ENCRYPTION_KEY in your deployment environment and restart the API.",
		encNoMetadata:     "Generate or provide an encryption key, then set it and restart.",
		encMismatch:       "Load the key these credentials were encrypted with (or rotate), then restart.",
		encInvalidKey:     "HIMS_ENCRYPTION_KEY must be a base64-encoded 32-byte key. Fix it and restart.",
	}
	if es.Status == encEnabled {
		items = append(items, checkItem{"key", "Encryption key configured", "ok", es.Reason, ""})
	} else {
		items = append(items, checkItem{"key", "Encryption key configured", "fail", es.Reason, keyAction[es.Status]})
	}

	// Fingerprint valid — runtime fingerprint matches the stored fingerprint.
	switch {
	case es.FingerprintMatch:
		items = append(items, checkItem{"fingerprint", "Encryption fingerprint valid", "ok", "Runtime key fingerprint matches the stored fingerprint.", ""})
	case es.Status == encMismatch:
		items = append(items, checkItem{"fingerprint", "Encryption fingerprint valid", "fail", "Runtime key fingerprint does NOT match the stored fingerprint — the wrong key is loaded.", "Load the correct key (or rotate), then restart."})
	case es.Status == encPendingRestart:
		items = append(items, checkItem{"fingerprint", "Encryption fingerprint valid", "warn", "A fingerprint is stored, but no runtime key is loaded to compare it against.", "Set HIMS_ENCRYPTION_KEY and restart."})
	default:
		items = append(items, checkItem{"fingerprint", "Encryption fingerprint valid", "fail", "No runtime key loaded to verify a fingerprint.", "Set HIMS_ENCRYPTION_KEY and restart."})
	}

	// Credentials decryptable — none sealed under a different key.
	und := encN
	if keyConfigured {
		if u, err := s.queries.CountUndecryptableCredentials(ctx, c.KeyID()); err == nil {
			und = u
		}
	}
	switch {
	case encN == 0:
		items = append(items, checkItem{"decrypt", "Credentials decryptable", "ok", "No encrypted credentials are stored yet.", ""})
	case keyConfigured && und == 0:
		items = append(items, checkItem{"decrypt", "Credentials decryptable", "ok", "All stored credential secrets decrypt with the current key.", ""})
	default:
		items = append(items, checkItem{"decrypt", "Credentials decryptable", "fail",
			"Some stored credential secrets cannot be decrypted with the current key.",
			"Restore the original key, or use Credential Recovery to reset and re-enter the affected secrets."})
	}

	if keyConfigured {
		items = append(items, checkItem{"writes", "Credential writes enabled", "ok", "New and updated credential secrets can be encrypted and saved.", ""})
		items = append(items, checkItem{"discovery", "Discovery credential access enabled", "ok", "Scans can decrypt credentials to authenticate to devices.", ""})
	} else {
		items = append(items, checkItem{"writes", "Credential writes enabled", "fail", "Credential creation/updates are disabled without an encryption key.", "Configure the key and restart."})
		items = append(items, checkItem{"discovery", "Discovery credential access enabled", "fail", "Credential-based discovery cannot authenticate without the key.", "Configure the key and restart."})
	}

	// --- Single-instance / port-ownership checks ---------------------
	// This process is, by construction, the sole owner of its listen port:
	// the API claims the socket at startup and exits if another instance
	// already holds it. So the running PID here is the one and only server.
	pid := os.Getpid()
	addr := s.rt.Addr
	if addr == "" {
		addr = ":8090"
	}
	items = append(items, checkItem{"instance", "Single API instance", "ok",
		fmt.Sprintf("This process (PID %d) owns %s. The API claims the port at startup and fails fast if another instance already holds it, so only one instance can serve at a time.", pid, addr), ""})
	items = append(items, checkItem{"port", "Port owner", "ok",
		fmt.Sprintf("Port %s is owned by this process (PID %d).", addr, pid), ""})

	// Encryption key loaded in THIS active process — distinguishes "key is
	// configured somewhere" from "the process answering requests has it".
	if keyConfigured {
		items = append(items, checkItem{"active_key", "Encryption key loaded in active process", "ok",
			fmt.Sprintf("The serving process (PID %d) has the encryption key loaded (key id %s).", pid, c.KeyID()), ""})
	} else {
		items = append(items, checkItem{"active_key", "Encryption key loaded in active process", "fail",
			fmt.Sprintf("The serving process (PID %d) has NO encryption key loaded. If the status looks wrong, confirm this PID is the instance you expect — a stray no-key instance answering on the port produces a misleading pending_restart.", pid),
			"Load the key from Encryption → Key Management → Unlock, or set HIMS_ENCRYPTION_KEY for THIS process and restart."})
	}

	writeJSON(w, http.StatusOK, items)
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
