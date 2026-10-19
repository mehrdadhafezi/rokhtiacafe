// Package auth implements password hashing, server-side sessions, CSRF protection and the
// central authorization policy (docs/SECURITY.md §2–4).
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters from docs/SECURITY.md §2 (OWASP minimum, sized for shared hosting).
const (
	argonMemoryKiB = 19456
	argonTime      = 2
	argonThreads   = 1
	argonSaltLen   = 16
	argonKeyLen    = 32
)

// Password length limits, counted in characters.
const (
	MinPasswordLen = 10
	MaxPasswordLen = 128
)

// ErrWeakPassword is returned by ValidatePassword; the message is safe to show.
var ErrWeakPassword = errors.New("password does not meet the policy")

// Small embedded list of passwords that pass the length rule but are still trivially guessable.
var commonPasswords = map[string]bool{
	"1234567890": true, "0123456789": true, "12345678910": true, "123456789a": true,
	"qwertyuiop": true, "password123": true, "password1234": true, "passw0rd123": true,
	"1qaz2wsx3edc": true, "qwerty12345": true, "iloveyou123": true, "adminadmin": true,
	"administrator": true, "rokhtiacafe": true, "rokh123456": true, "0000000000": true,
	"1111111111": true, "abcdefghij": true, "aaaaaaaaaa": true, "zxcvbnmasd": true,
}

// ValidatePassword enforces length bounds and rejects well-known passwords.
func ValidatePassword(pw string) error {
	n := utf8.RuneCountInString(pw)
	switch {
	case n < MinPasswordLen:
		return fmt.Errorf("%w: at least %d characters", ErrWeakPassword, MinPasswordLen)
	case n > MaxPasswordLen:
		return fmt.Errorf("%w: at most %d characters", ErrWeakPassword, MaxPasswordLen)
	case commonPasswords[strings.ToLower(pw)]:
		return fmt.Errorf("%w: too common", ErrWeakPassword)
	}
	return nil
}

// HashPassword returns an argon2id PHC string: $argon2id$v=19$m=..,t=..,p=..$salt$hash
func HashPassword(pw string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemoryKiB, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword reports whether pw matches the PHC hash. The comparison is constant-time.
func VerifyPassword(pw, phc string) (bool, error) {
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("unsupported password hash format")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errors.New("unsupported argon2 version")
	}
	var mem uint32
	var t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &t, &p); err != nil || mem == 0 || t == 0 || p == 0 {
		return false, errors.New("invalid argon2 parameters")
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, errors.New("invalid argon2 salt")
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, errors.New("invalid argon2 hash")
	}
	got := argon2.IDKey([]byte(pw), salt, t, mem, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// dummyHash is verified against when the username does not exist, so a failed login takes the
// same time whether or not the account exists.
var dummyHash = func() string {
	h, err := HashPassword("not-a-real-password-for-timing-only")
	if err != nil {
		panic(err)
	}
	return h
}()

// VerifyDummy burns the same CPU as a real verification.
func VerifyDummy(pw string) { _, _ = VerifyPassword(pw, dummyHash) }
