package auth

import (
	"encoding/base64"
	"fmt"
	"testing"

	"golang.org/x/crypto/argon2"
)

// buildHashWith produces a PHC string with explicit Argon2id parameters, so
// tests can prove that verification honours the parameters embedded in a
// stored hash rather than the package's current constants.
func buildHashWith(t *testing.T, password string, memory, time uint32, threads uint8) string {
	t.Helper()
	salt := []byte("0123456789abcdef")
	hash := argon2.IDKey([]byte(password), salt, time, memory, threads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, memory, time, threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	)
}
