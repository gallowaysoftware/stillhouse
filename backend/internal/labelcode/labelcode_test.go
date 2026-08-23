package labelcode

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestRoundTrip(t *testing.T) {
	for i := 0; i < 2000; i++ {
		id := uuid.New()
		for k := range kinds {
			code := Encode(k, id)
			if len(code) != Length {
				t.Fatalf("code %q is %d characters, want %d", code, len(code), Length)
			}
			gotKind, prefix, err := Decode(code)
			if err != nil {
				t.Fatalf("decode %q: %v", code, err)
			}
			if gotKind != k {
				t.Fatalf("kind = %q, want %q", gotKind, k)
			}
			if !Matches(id, prefix) {
				t.Fatalf("code %q does not match the id it was made from", code)
			}
		}
	}
}

// A code is printed on a cask end and read back by eye as often as it is
// scanned. Everything a person plausibly does to it on the way — lower
// case, a hyphen for legibility, a space, and the four characters
// Crockford drops because they read as each other — has to land on the
// same row.
func TestReadingItBackByEye(t *testing.T) {
	id := uuid.New()
	code := Encode(KindBarrel, id)
	_, want, err := Decode(code)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	variants := []string{
		strings.ToLower(code),
		code[:5] + "-" + code[5:],
		" " + code + " ",
		code[:1] + "-" + code[1:5] + " " + code[5:9] + "-" + code[9:],
	}
	for _, v := range variants {
		_, got, err := Decode(v)
		if err != nil {
			t.Errorf("%q: %v", v, err)
			continue
		}
		if got != want {
			t.Errorf("%q resolved to a different row", v)
		}
	}
}

// O for zero and I for one are the transcription errors the alphabet
// exists to absorb.
func TestCrockfordSubstitutions(t *testing.T) {
	// Build a code whose digits are all zeros and ones, then type it the
	// way somebody would who saw letters.
	code := "B" + strings.Repeat("0", 6) + strings.Repeat("1", 7)
	_, want, err := Decode(code)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, typed := range []string{
		"B" + strings.Repeat("O", 6) + strings.Repeat("I", 7),
		"B" + strings.Repeat("o", 6) + strings.Repeat("l", 7),
		"B" + strings.Repeat("0", 6) + strings.Repeat("L", 7),
	} {
		_, got, err := Decode(typed)
		if err != nil {
			t.Errorf("%q: %v", typed, err)
			continue
		}
		if got != want {
			t.Errorf("%q resolved to a different row than %q", typed, code)
		}
	}
}

// The kind letter L must survive its own substitution rule: a lot code
// starts with L, and rewriting that to 1 would make every lot code
// malformed.
func TestLotKindLetterSurvives(t *testing.T) {
	id := uuid.New()
	code := Encode(KindLot, id)
	if code[0] != 'L' {
		t.Fatalf("lot code starts with %q", code[0])
	}
	k, prefix, err := Decode(code)
	if err != nil {
		t.Fatalf("decode %q: %v", code, err)
	}
	if k != KindLot {
		t.Errorf("kind = %q, want %q", k, KindLot)
	}
	if !Matches(id, prefix) {
		t.Error("lot code did not match its own id")
	}
}

// The alphabet must not contain the characters it exists to exclude, or
// the substitutions above turn a valid code into a different valid code.
func TestAlphabetExcludesTheAmbiguousLetters(t *testing.T) {
	if len(alphabet) != 32 {
		t.Fatalf("alphabet is %d characters", len(alphabet))
	}
	seen := map[rune]bool{}
	for _, r := range alphabet {
		if seen[r] {
			t.Errorf("alphabet repeats %q", r)
		}
		seen[r] = true
	}
	for _, r := range "ILOU" {
		if strings.ContainsRune(alphabet, r) {
			t.Errorf("alphabet contains %q, which Crockford drops because it "+
				"reads as something else", r)
		}
	}
}

// Different kinds of thing must never produce the same printed code, so a
// case label scanned at the cask screen is refused rather than silently
// resolving to a cask that happens to share an id prefix.
func TestKindsAreDistinguished(t *testing.T) {
	id := uuid.New()
	seen := map[string]Kind{}
	for k := range kinds {
		code := Encode(k, id)
		if other, dup := seen[code]; dup {
			t.Fatalf("%s and %s produce the same code %q", k, other, code)
		}
		seen[code] = k
	}
}

func TestRejectsRubbish(t *testing.T) {
	for _, s := range []string{
		"", "B", "BX", "Z0123456789ABC", // unknown kind letter
		"B0123456789AB",   // one short
		"B0123456789ABCD", // one long
		"B012345678!ABC",  // not in the alphabet
		"1234567890123",   // no kind letter
	} {
		if _, _, err := Decode(s); err == nil {
			t.Errorf("Decode(%q) succeeded; it should not", s)
		}
	}
}

// Encoding is stable: the code on a cask printed today must be the code
// scanned in twelve years, and nothing about it may depend on when it was
// generated.
func TestEncodingIsStable(t *testing.T) {
	id := uuid.MustParse("3f2504e0-4f89-41d3-9a0c-0305e82c3301")
	const want = "B3Y984W17RJGEK"
	if got := Encode(KindBarrel, id); got != want {
		t.Errorf("Encode = %q, want %q — a change here orphans every label "+
			"already printed", got, want)
	}
}
