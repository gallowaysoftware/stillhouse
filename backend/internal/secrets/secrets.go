// Package secrets seals small values that must survive a database leak.
//
// There is exactly one of these today: the TOTP shared secret. A second
// factor exists so that a stolen password is not enough, and a TOTP
// secret sitting in plaintext in a nightly backup hands over both at
// once — the backup is the likeliest way this data leaves the building,
// which is the whole reason stage 150 spent so long on it.
//
// AES-256-GCM under a key the operator supplies. Not a key derived from
// anything the database also holds, because a key stored beside the
// ciphertext is decoration.
//
// When no key is configured the package reports so and refuses to seal.
// Nothing is silently stored in the clear: the enrolment RPC turns the
// refusal into a message naming the environment variable and how to
// generate one, the same way a missing alcoholometric table refuses
// temperature correction rather than guessing.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"sync"
)

// KeyEnv names the environment variable holding the key: 32 bytes,
// base64. Generate one with
//
//	openssl rand -base64 32
const KeyEnv = "STILLHOUSE_SECRET_KEY"

// ErrNoKey is returned by Seal and Open when no key is configured.
var ErrNoKey = errors.New(
	KeyEnv + " is not set, so secrets cannot be encrypted at rest; " +
		"generate one with `openssl rand -base64 32`")

var (
	once sync.Once
	aead cipher.AEAD
	load error
)

func initKey() {
	raw := os.Getenv(KeyEnv)
	if raw == "" {
		load = ErrNoKey
		return
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		load = fmt.Errorf("%s is not valid base64: %w", KeyEnv, err)
		return
	}
	if len(key) != 32 {
		load = fmt.Errorf("%s decodes to %d bytes; AES-256 needs 32", KeyEnv, len(key))
		return
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		load = err
		return
	}
	aead, load = cipher.NewGCM(block)
}

// Configured reports whether a usable key is present. Callers use it to
// refuse a feature up front, with a message, rather than failing at the
// point of writing.
func Configured() bool {
	once.Do(initKey)
	return load == nil
}

// ConfigErr returns why the key is unusable, or nil.
func ConfigErr() error {
	once.Do(initKey)
	return load
}

// Seal encrypts plaintext. The nonce is generated per call and prefixed
// to the ciphertext, so the stored value is self-contained and two
// identical secrets do not produce identical rows.
func Seal(plaintext []byte) ([]byte, error) {
	once.Do(initKey)
	if load != nil {
		return nil, load
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Open decrypts what Seal produced. A key that has been rotated or lost
// surfaces here as an error rather than as silent nonsense, because GCM
// authenticates.
func Open(sealed []byte) ([]byte, error) {
	once.Do(initKey)
	if load != nil {
		return nil, load
	}
	if len(sealed) < aead.NonceSize() {
		return nil, errors.New("sealed value is too short to contain a nonce")
	}
	nonce, body := sealed[:aead.NonceSize()], sealed[aead.NonceSize():]
	out, err := aead.Open(nil, nonce, body, nil)
	if err != nil {
		return nil, fmt.Errorf("could not decrypt: %w (has %s changed?)", err, KeyEnv)
	}
	return out, nil
}

// SetForTest installs an AEAD directly, bypassing the environment. Tests
// of features built on this package need a working key without arranging
// process environment before the sync.Once fires; what the env parsing
// does is covered by ConfigErr and by this package's own tests.
func SetForTest(t interface{ Cleanup(func()) }, a cipher.AEAD) {
	once.Do(func() {})
	prevAEAD, prevLoad := aead, load
	aead, load = a, nil
	t.Cleanup(func() { aead, load = prevAEAD, prevLoad })
}
