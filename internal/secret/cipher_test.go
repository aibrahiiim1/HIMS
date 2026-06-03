package secret

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"
)

func testKey(t *testing.T) string {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(key)
}

func TestSealOpen_RoundTrip(t *testing.T) {
	c, err := NewCipher(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("public-community-v2c")
	blob, keyID, err := c.Seal(plain)
	if err != nil {
		t.Fatal(err)
	}
	if keyID != c.KeyID() {
		t.Fatalf("keyID = %q; want %q", keyID, c.KeyID())
	}
	if bytes.Contains(blob, plain) {
		t.Fatal("blob contains the plaintext — not encrypted")
	}
	got, err := c.Open(blob, keyID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("Open = %q; want %q", got, plain)
	}
}

func TestSeal_FreshNoncePerCall(t *testing.T) {
	c, _ := NewCipher(testKey(t))
	b1, _, _ := c.Seal([]byte("same"))
	b2, _, _ := c.Seal([]byte("same"))
	if bytes.Equal(b1, b2) {
		t.Fatal("two seals of the same plaintext produced identical blobs — nonce reuse")
	}
}

func TestOpen_TamperDetected(t *testing.T) {
	c, _ := NewCipher(testKey(t))
	blob, keyID, _ := c.Seal([]byte("secret"))
	blob[len(blob)-1] ^= 0xFF // flip a tag bit
	if _, err := c.Open(blob, keyID); err == nil {
		t.Fatal("Open accepted a tampered blob")
	}
}

func TestOpen_WrongKey(t *testing.T) {
	c1, _ := NewCipher(testKey(t))
	c2, _ := NewCipher(testKey(t))
	blob, keyID, _ := c1.Seal([]byte("secret"))
	// c2 has a different key → KeyID mismatch reported before decrypt.
	if _, err := c2.Open(blob, keyID); !errors.Is(err, ErrKeyMismatch) {
		t.Fatalf("err = %v; want ErrKeyMismatch", err)
	}
}

func TestReKey_RotatesUnderNewKey(t *testing.T) {
	oldC, _ := NewCipher(testKey(t))
	newC, _ := NewCipher(testKey(t))
	plain := []byte("community-to-rotate")
	blob, keyID, _ := oldC.Seal(plain)

	newBlob, newKeyID, err := ReKey(oldC, newC, blob, keyID)
	if err != nil {
		t.Fatal(err)
	}
	if newKeyID != newC.KeyID() || newKeyID == keyID {
		t.Fatalf("re-keyed blob should carry the new KeyID")
	}
	// The new key opens the re-keyed blob; the old key no longer matches.
	got, err := newC.Open(newBlob, newKeyID)
	if err != nil || string(got) != string(plain) {
		t.Fatalf("new key failed to open re-keyed blob: %v / %q", err, got)
	}
	if _, err := oldC.Open(newBlob, newKeyID); !errors.Is(err, ErrKeyMismatch) {
		t.Fatalf("old key should not open re-keyed blob")
	}
}

func TestNewCipher_BadKey(t *testing.T) {
	if _, err := NewCipher("not-base64!!!"); err == nil {
		t.Fatal("expected error for non-base64 key")
	}
	short := base64.StdEncoding.EncodeToString(make([]byte, 16))
	if _, err := NewCipher(short); err == nil {
		t.Fatal("expected error for 16-byte key (need 32)")
	}
}
