package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strings"

	"golang.org/x/crypto/argon2"
)

const passwordPrefix = "$argon2id$v=19$m=19456,t=2,p=1$"

// HashPassword uses the OWASP minimum Argon2id profile. Admission is bounded by the service.
func HashPassword(password string) (string, error) {
	if len(password) < 12 || len(password) > 128 {
		return "", ErrInvalidInput
	}
	var salt [16]byte
	if _, err := rand.Read(salt[:]); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt[:], 2, 19456, 1, 32)
	return passwordPrefix + base64.RawStdEncoding.EncodeToString(salt[:]) + "$" + base64.RawStdEncoding.EncodeToString(key), nil
}

func verifyPassword(encoded, password string) bool {
	// Malformed or missing hashes perform the same expensive operation.
	salt := make([]byte, 16)
	expected := make([]byte, 32)
	valid := false
	if strings.HasPrefix(encoded, passwordPrefix) {
		parts := strings.Split(strings.TrimPrefix(encoded, passwordPrefix), "$")
		if len(parts) == 2 {
			a, e1 := base64.RawStdEncoding.DecodeString(parts[0])
			b, e2 := base64.RawStdEncoding.DecodeString(parts[1])
			if e1 == nil && e2 == nil && len(a) == 16 && len(b) == 32 {
				salt = a
				expected = b
				valid = true
			}
		}
	}
	key := argon2.IDKey([]byte(password), salt, 2, 19456, 1, 32)
	return subtle.ConstantTimeCompare(key, expected) == 1 && valid
}

var (
	ErrInvalidInput = errors.New("invalid authentication input")
	ErrUnauthorized = errors.New("invalid credentials")
	ErrBusy         = errors.New("authentication capacity exceeded")
)
