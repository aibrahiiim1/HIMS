// Package secret is HIMS's encryption-at-rest primitive. Credential secrets
// (SNMP communities, SSH passwords, API keys) are sealed with AES-256-GCM
// before they touch the database and opened only in memory at point of use.
//
// Security invariants (see project memory):
//   - Plaintext secrets are NEVER logged, returned over the API, or rendered.
//   - The encryption key lives only in the process environment, never in the
//     DB or git.
//   - Each sealed blob is self-describing: it carries its own random nonce,
//     and the stored KeyID lets a future key rotation tell which key sealed
//     a given row.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// ErrKeyMismatch is returned when a blob was sealed with a different key than
// the one currently loaded (its KeyID doesn't match).
var ErrKeyMismatch = errors.New("secret: blob sealed with a different key (KeyID mismatch)")

// Cipher seals and opens secrets with one AES-256-GCM key.
type Cipher struct {
	aead        cipher.AEAD
	keyID       string
	fingerprint string
}

// colonHex renders bytes as upper-case colon-separated hex (AA:BB:CC…), the
// conventional fingerprint display.
func colonHex(b []byte) string {
	const hexd = "0123456789ABCDEF"
	out := make([]byte, 0, len(b)*3)
	for i, x := range b {
		if i > 0 {
			out = append(out, ':')
		}
		out = append(out, hexd[x>>4], hexd[x&0x0f])
	}
	return string(out)
}

// GenerateKey returns a fresh cryptographically-secure 32-byte AES-256 key,
// base64-encoded — suitable for HIMS_ENCRYPTION_KEY. The raw key is the
// caller's responsibility; it is never persisted by this package.
func GenerateKey() (string, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(key), nil
}

// FingerprintForKey returns the full SHA-256 fingerprint (colon-hex) of a
// base64 key without constructing a cipher — used to record/compare key
// identity. It validates the key is decodable 32 bytes.
func FingerprintForKey(keyB64 string) (fingerprint, keyID string, err error) {
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return "", "", fmt.Errorf("secret: key is not valid base64: %w", err)
	}
	if len(key) != 32 {
		return "", "", fmt.Errorf("secret: key must be 32 bytes (AES-256), got %d", len(key))
	}
	sum := sha256.Sum256(key)
	return colonHex(sum[:]), hex.EncodeToString(sum[:4]), nil
}

// Fingerprint is the full SHA-256 fingerprint (colon-hex) of the loaded key.
// It is one-way: it identifies the key without revealing it.
func (c *Cipher) Fingerprint() string { return c.fingerprint }

// NewCipher builds a Cipher from a base64-encoded 32-byte key. The KeyID is a
// short, non-reversible fingerprint of the key (first 8 hex of SHA-256) used
// to tag sealed blobs for rotation — it reveals nothing about the key.
func NewCipher(keyB64 string) (*Cipher, error) {
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return nil, fmt.Errorf("secret: key is not valid base64: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("secret: key must be 32 bytes (AES-256), got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(key)
	return &Cipher{aead: aead, keyID: hex.EncodeToString(sum[:4]), fingerprint: colonHex(sum[:])}, nil
}

// KeyID is the fingerprint of the loaded key.
func (c *Cipher) KeyID() string { return c.keyID }

// Seal encrypts plaintext, returning the blob (nonce ‖ ciphertext ‖ tag) and
// the KeyID to store alongside it. A fresh random nonce is used every call.
func (c *Cipher) Seal(plaintext []byte) (blob []byte, keyID string, err error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, "", err
	}
	// Seal appends the ciphertext+tag to its first arg, so the nonce prefixes
	// the blob and Open can recover it without a separate field.
	blob = c.aead.Seal(nonce, nonce, plaintext, nil)
	return blob, c.keyID, nil
}

// ReKey re-seals a blob from the old key under the new key: open with old,
// seal with new. Used by the credential key-rotation tool. Returns the new
// blob + new KeyID to store. Plaintext exists only transiently in memory.
func ReKey(oldC, newC *Cipher, blob []byte, keyID string) (newBlob []byte, newKeyID string, err error) {
	plain, err := oldC.Open(blob, keyID)
	if err != nil {
		return nil, "", err
	}
	return newC.Seal(plain)
}

// Open decrypts a blob sealed by Seal. It verifies the KeyID matches the
// loaded key first, then authenticates + decrypts (GCM rejects any tamper).
func (c *Cipher) Open(blob []byte, keyID string) ([]byte, error) {
	if keyID != c.keyID {
		return nil, ErrKeyMismatch
	}
	ns := c.aead.NonceSize()
	if len(blob) < ns {
		return nil, errors.New("secret: blob too short")
	}
	nonce, ciphertext := blob[:ns], blob[ns:]
	return c.aead.Open(nil, nonce, ciphertext, nil)
}
