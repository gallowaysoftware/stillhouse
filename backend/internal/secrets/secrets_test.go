package secrets

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"testing"
)

// The package reads its key once, from the environment, so these tests
// build an AEAD directly rather than fighting the sync.Once. What is
// under test is the sealing shape — self-contained nonce, authenticated
// ciphertext, distinct output for identical input — not the env parsing,
// which ConfigErr reports plainly.
func testAEAD(t *testing.T) cipher.AEAD {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	a, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("gcm: %v", err)
	}
	return a
}

func TestSealIsSelfContainedAndAuthenticated(t *testing.T) {
	prev := aead
	prevLoad := load
	once.Do(func() {})
	aead, load = testAEAD(t), nil
	t.Cleanup(func() { aead, load = prev, prevLoad })

	secret := []byte("a TOTP shared secret")
	sealed, err := Seal(secret)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Contains(sealed, secret) {
		t.Error("the plaintext is present in the sealed value")
	}

	got, err := Open(sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(got, secret) {
		t.Errorf("round trip returned %q", got)
	}

	// Two seals of the same value must differ, or a database leak tells
	// an attacker which users share a secret.
	again, err := Seal(secret)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Equal(sealed, again) {
		t.Error("sealing the same value twice produced identical ciphertext")
	}

	// Tampering must be detected, not decrypted into nonsense.
	corrupt := append([]byte(nil), sealed...)
	corrupt[len(corrupt)-1] ^= 0xff
	if _, err := Open(corrupt); err == nil {
		t.Error("a modified ciphertext was accepted")
	}
	if _, err := Open([]byte("short")); err == nil {
		t.Error("a value too short to hold a nonce was accepted")
	}
}
