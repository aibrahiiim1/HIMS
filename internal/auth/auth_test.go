package auth

import "testing"

func TestPasswordHashing(t *testing.T) {
	h, err := HashPassword("s3cret-pw")
	if err != nil {
		t.Fatal(err)
	}
	if h == "" || h == "s3cret-pw" {
		t.Fatal("hash must not be empty or the plaintext")
	}
	if !CheckPassword(h, "s3cret-pw") {
		t.Error("correct password should verify")
	}
	if CheckPassword(h, "wrong") {
		t.Error("wrong password must not verify")
	}
	if CheckPassword("", "anything") {
		t.Error("blank hash (no password set) must never verify")
	}
}

func TestTokens(t *testing.T) {
	a, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewToken()
	if a == b || len(a) != 64 {
		t.Fatalf("tokens must be unique 32-byte hex; got %q / %q", a, b)
	}
	// HashToken is deterministic + one-way (not the token itself).
	if HashToken(a) != HashToken(a) {
		t.Error("HashToken must be deterministic")
	}
	if HashToken(a) == a {
		t.Error("HashToken must differ from the raw token")
	}
}
