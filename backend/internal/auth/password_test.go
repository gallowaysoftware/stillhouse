package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestHashVerifyRoundTrip(t *testing.T) {
	for _, pw := range []string{
		"correct horse battery staple",
		"short",
		"",
		"pässwörd with ünicode 🥃",
		strings.Repeat("x", 1024),
	} {
		encoded, err := HashPassword(pw)
		if err != nil {
			t.Fatalf("HashPassword(%q): %v", pw, err)
		}
		if err := VerifyPassword(pw, encoded); err != nil {
			t.Errorf("VerifyPassword(%q) rejected its own hash: %v", pw, err)
		}
	}
}

func TestWrongPasswordIsRejected(t *testing.T) {
	encoded, err := HashPassword("the real one")
	if err != nil {
		t.Fatal(err)
	}
	for _, wrong := range []string{"the real onf", "The real one", "", "the real one "} {
		if err := VerifyPassword(wrong, encoded); !errors.Is(err, ErrPasswordMismatch) {
			t.Errorf("VerifyPassword(%q) = %v, want ErrPasswordMismatch", wrong, err)
		}
	}
}

// TestSaltIsPerHash — two hashes of the same password must differ, or the
// hash file leaks which accounts share a password.
func TestSaltIsPerHash(t *testing.T) {
	a, err := HashPassword("same password")
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashPassword("same password")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("two hashes of one password are identical — the salt is not random")
	}
	// Both must still verify.
	if err := VerifyPassword("same password", a); err != nil {
		t.Errorf("first hash failed to verify: %v", err)
	}
	if err := VerifyPassword("same password", b); err != nil {
		t.Errorf("second hash failed to verify: %v", err)
	}
}

func TestEncodedFormIsPHC(t *testing.T) {
	encoded, err := HashPassword("whatever")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 {
		t.Fatalf("encoded hash has %d segments, want 6: %q", len(parts), encoded)
	}
	if parts[1] != "argon2id" {
		t.Errorf("algorithm segment = %q, want argon2id", parts[1])
	}
	if !strings.HasPrefix(parts[2], "v=") {
		t.Errorf("version segment = %q", parts[2])
	}
	// The parameters must be embedded, so tuning the cost later doesn't
	// invalidate hashes already in the database.
	if !strings.Contains(parts[3], "m=") || !strings.Contains(parts[3], "t=") || !strings.Contains(parts[3], "p=") {
		t.Errorf("parameter segment = %q, want m=,t=,p=", parts[3])
	}
}

// TestVerifyUsesTheHashesOwnParameters is the property that makes the cost
// tunable: a hash written with different parameters must still verify
// after the package's constants change.
func TestVerifyUsesTheHashesOwnParameters(t *testing.T) {
	// A hash produced with a deliberately lower memory cost than the
	// current constant.
	encoded := buildHashWith(t, "tunable", 32*1024, 1, 1)
	if err := VerifyPassword("tunable", encoded); err != nil {
		t.Errorf("a hash with older parameters must still verify, got %v", err)
	}
	if err := VerifyPassword("wrong", encoded); !errors.Is(err, ErrPasswordMismatch) {
		t.Errorf("wrong password against older parameters = %v, want mismatch", err)
	}
}

func TestMalformedHashesAreRejected(t *testing.T) {
	good, err := HashPassword("x")
	if err != nil {
		t.Fatal(err)
	}
	for name, encoded := range map[string]string{
		"empty":            "",
		"not a phc string": "hunter2",
		"too few segments": "$argon2id$v=19$m=65536,t=2,p=1$salt",
		"wrong algorithm":  strings.Replace(good, "argon2id", "bcrypt00", 1),
		"bad salt base64":  mutateSegment(good, 4, "!!!not base64!!!"),
		"bad hash base64":  mutateSegment(good, 5, "!!!not base64!!!"),
		"bad params":       mutateSegment(good, 3, "m=x,t=y,p=z"),
	} {
		t.Run(name, func(t *testing.T) {
			if err := VerifyPassword("x", encoded); err == nil {
				t.Errorf("VerifyPassword accepted a malformed hash: %q", encoded)
			}
		})
	}
}

func TestIncompatibleVersionIsNamed(t *testing.T) {
	good, err := HashPassword("x")
	if err != nil {
		t.Fatal(err)
	}
	bumped := mutateSegment(good, 2, "v=99")
	if err := VerifyPassword("x", bumped); !errors.Is(err, ErrIncompatibleVersion) {
		t.Errorf("got %v, want ErrIncompatibleVersion", err)
	}
}

// TestTamperedHashFails — flipping a character in the digest must not
// verify, which is the whole point.
func TestTamperedHashFails(t *testing.T) {
	encoded, err := HashPassword("secret")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(encoded, "$")
	digest := []byte(parts[5])
	if digest[0] == 'A' {
		digest[0] = 'B'
	} else {
		digest[0] = 'A'
	}
	parts[5] = string(digest)
	if err := VerifyPassword("secret", strings.Join(parts, "$")); err == nil {
		t.Error("a tampered digest verified")
	}
}

func mutateSegment(encoded string, idx int, replacement string) string {
	parts := strings.Split(encoded, "$")
	parts[idx] = replacement
	return strings.Join(parts, "$")
}

// TestVerifyRejectsMalformedDigestLengths: VerifyPassword derives with
// uint32(len(hash)) as the key length, and argon2.IDKey panics outright on
// a zero key length. A stored hash whose digest segment is empty therefore
// crashed the request — from the unauthenticated login handler, which
// turns one corrupt row into a denial of service. A short-but-nonempty
// digest was worse in a quieter way: it silently shortened the comparison.
func TestVerifyRejectsMalformedDigestLengths(t *testing.T) {
	valid, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	parts := strings.Split(valid, "$")
	if len(parts) != 6 {
		t.Fatalf("unexpected hash shape: %q", valid)
	}

	for name, digest := range map[string]string{
		"empty digest":     "",
		"one byte digest":  "AA",
		"truncated digest": parts[5][:20],
		"empty salt":       "",
	} {
		t.Run(name, func(t *testing.T) {
			bad := make([]string, len(parts))
			copy(bad, parts)
			if name == "empty salt" {
				bad[4] = ""
			} else {
				bad[5] = digest
			}
			// Must return an error, and must not panic.
			if err := VerifyPassword("correct horse battery staple", strings.Join(bad, "$")); err == nil {
				t.Error("malformed hash was accepted")
			}
		})
	}
}

// TestArgonParametersArePinned: the package documents OWASP-grade cost, but
// nothing asserted the numbers. Dropping argonMemory from 64 MiB to 64 KiB
// — a plausible unit slip — left every other test in this package passing
// while destroying the property the package exists for.
func TestArgonParametersArePinned(t *testing.T) {
	if argonMemory != 64*1024 {
		t.Errorf("argonMemory = %d KiB, want 65536 (64 MiB) — OWASP guidance for Argon2id", argonMemory)
	}
	if argonTime != 2 {
		t.Errorf("argonTime = %d, want 2", argonTime)
	}
	if argonKeyLen != 32 {
		t.Errorf("argonKeyLen = %d, want 32", argonKeyLen)
	}
	if saltLen != 16 {
		t.Errorf("saltLen = %d, want 16", saltLen)
	}
}
