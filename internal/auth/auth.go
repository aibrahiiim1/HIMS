// Package auth holds the pure authentication primitives — password hashing
// (bcrypt) and opaque session-token generation/hashing — behind the HIMS login
// + session layer (Production Readiness P1). No DB or HTTP here, so it is
// unit-tested in isolation. Sessions are server-side: the raw token lives only
// in the operator's httpOnly cookie; the database stores only its SHA-256.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"

	"golang.org/x/crypto/bcrypt"
)

// HashPassword returns a bcrypt hash of a plaintext password.
func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	return string(b), err
}

// CheckPassword reports whether plain matches a bcrypt hash. A blank hash
// (no password set) never matches.
func CheckPassword(hash, plain string) bool {
	if hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// NewToken returns a fresh, cryptographically-random session token (the value
// placed in the cookie).
func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// HashToken returns the SHA-256 (hex) of a token — what gets stored/looked up
// in the sessions table. One-way, so a DB leak can't reconstruct live cookies.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
