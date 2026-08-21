// Package auth provides password hashing and verification.
//
// Argon2id is the OWASP-recommended password hash. The encoded format here is
// the standard PHC-string form:
//
//	$argon2id$v=19$m=65536,t=2,p=1$<salt-b64>$<hash-b64>
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonTime    uint32 = 2
	argonMemory  uint32 = 64 * 1024 // 64 MiB
	argonThreads uint8  = 1
	argonKeyLen  uint32 = 32
	saltLen      int    = 16
)

var (
	ErrInvalidHash         = errors.New("auth: invalid encoded hash")
	ErrIncompatibleVersion = errors.New("auth: incompatible argon2 version")
	ErrPasswordMismatch    = errors.New("auth: password mismatch")
)

// HashPassword returns the PHC-encoded Argon2id hash of password.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: read salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

// VerifyPassword returns nil iff password produces the same Argon2id hash as
// the supplied encoded value (using its embedded parameters and salt).
func VerifyPassword(password, encoded string) error {
	params, salt, hash, err := decodeHash(encoded)
	if err != nil {
		return err
	}
	derived := argon2.IDKey([]byte(password), salt, params.time, params.memory, params.threads, uint32(len(hash)))
	if subtle.ConstantTimeCompare(hash, derived) != 1 {
		return ErrPasswordMismatch
	}
	return nil
}

type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

func decodeHash(encoded string) (*argonParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return nil, nil, nil, ErrInvalidHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return nil, nil, nil, fmt.Errorf("auth: parse version: %w", err)
	}
	if version != argon2.Version {
		return nil, nil, nil, ErrIncompatibleVersion
	}
	p := &argonParams{}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return nil, nil, nil, fmt.Errorf("auth: parse params: %w", err)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return nil, nil, nil, fmt.Errorf("auth: decode salt: %w", err)
	}
	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return nil, nil, nil, fmt.Errorf("auth: decode hash: %w", err)
	}
	// Length checks, not cosmetics. VerifyPassword derives with
	// uint32(len(hash)) as the key length, and argon2.IDKey panics on a
	// zero key length — so a stored hash ending in "$" (empty digest
	// segment) takes down the request. That path is reachable from the
	// unauthenticated login handler, which makes a corrupt or hand-edited
	// row a denial of service rather than a failed login. A short digest
	// would also silently shorten the comparison.
	if len(salt) == 0 {
		return nil, nil, nil, fmt.Errorf("auth: %w: empty salt", ErrInvalidHash)
	}
	if len(hash) != int(argonKeyLen) {
		return nil, nil, nil, fmt.Errorf("auth: %w: digest is %d bytes, want %d",
			ErrInvalidHash, len(hash), argonKeyLen)
	}
	return p, salt, hash, nil
}
