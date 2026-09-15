package auth

import "testing"

func TestPassword(t *testing.T) {
	hash, err := HashPassword("a long test password")
	if err != nil {
		t.Fatal(err)
	}
	if !verifyPassword(hash, "a long test password") {
		t.Fatal("correct password rejected")
	}
	if verifyPassword(hash, "incorrect password") || verifyPassword("broken", "a long test password") {
		t.Fatal("invalid password accepted")
	}
	if _, err := HashPassword("short"); err != ErrInvalidInput {
		t.Fatal(err)
	}
}
