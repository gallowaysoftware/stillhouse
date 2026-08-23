package totp

import (
	"strings"
	"testing"
	"time"
)

// The published vectors. RFC 6238 Appendix B tabulates codes for the
// ASCII secret "12345678901234567890" at named times; RFC 4226 Appendix D
// does the same for counter values 0–9. Both are reproduced here because
// they are the whole reason this algorithm is written out rather than
// imported: a second factor should be checked against the specification,
// not against a package's popularity.
//
// Appendix B's table is for eight digits. This implementation emits six,
// which is what authenticator apps show, so the expected values are the
// low six digits of the tabulated eight — the truncation in RFC 4226 §5.3
// is a modulo, so dropping to six digits is dropping the leading two.
func TestRFC6238Vectors(t *testing.T) {
	secret := []byte("12345678901234567890")
	cases := []struct {
		unix    int64
		eight   string // as tabulated in RFC 6238 Appendix B, SHA-1 row
		wantSix string
	}{
		{59, "94287082", "287082"},
		{1111111109, "07081804", "081804"},
		{1111111111, "14050471", "050471"},
		{1234567890, "89005924", "005924"},
		{2000000000, "69279037", "279037"},
		{20000000000, "65353130", "353130"},
	}
	for _, c := range cases {
		at := time.Unix(c.unix, 0).UTC()
		got := Code(secret, Step(at))
		if got != c.wantSix {
			t.Errorf("Code at T=%d: got %s, want %s (RFC 6238 App. B lists %s)",
				c.unix, got, c.wantSix, c.eight)
		}
	}
}

// RFC 4226 Appendix D, the HOTP values for counters 0–9 against the same
// secret. Code() is HOTP with the counter supplied by the clock, so these
// exercise the truncation directly.
func TestRFC4226Vectors(t *testing.T) {
	secret := []byte("12345678901234567890")
	want := []string{
		"755224", "287082", "359152", "969429", "338314",
		"254676", "287922", "162583", "399871", "520489",
	}
	for counter, w := range want {
		if got := Code(secret, int64(counter)); got != w {
			t.Errorf("Code(counter=%d) = %s, want %s", counter, got, w)
		}
	}
}

func TestValidateAcceptsTheWindowAndNothingElse(t *testing.T) {
	secret := []byte("12345678901234567890")
	now := time.Unix(1111111111, 0).UTC()
	current := Step(now)

	for _, delta := range []int64{-1, 0, 1} {
		code := Code(secret, current+delta)
		step, ok := Validate(secret, code, now, 1)
		if !ok {
			t.Errorf("code for step %+d was rejected", delta)
			continue
		}
		if step != current+delta {
			t.Errorf("code for step %+d matched step %d, want %d", delta, step, current+delta)
		}
	}

	// Two steps out is a minute away and must not be accepted: the
	// window is the tolerance for a drifting clock, not for a code
	// somebody wrote down.
	for _, delta := range []int64{-2, 2, 100} {
		if _, ok := Validate(secret, Code(secret, current+delta), now, 1); ok {
			t.Errorf("code for step %+d was accepted", delta)
		}
	}

	if _, ok := Validate(secret, "12345", now, 1); ok {
		t.Error("a five-digit code was accepted")
	}
	if _, ok := Validate(secret, "", now, 1); ok {
		t.Error("an empty code was accepted")
	}
	// Spaces around the code are what a paste produces.
	if _, ok := Validate(secret, "  "+Code(secret, current)+" ", now, 1); !ok {
		t.Error("a code with surrounding whitespace was rejected")
	}
}

func TestSecretRoundTrip(t *testing.T) {
	secret, err := NewSecret()
	if err != nil {
		t.Fatalf("NewSecret: %v", err)
	}
	if len(secret) != SecretBytes {
		t.Errorf("secret is %d bytes, want %d (RFC 4226 §4 R6 recommends 160 bits)",
			len(secret), SecretBytes)
	}
	// The displayed form is grouped for retyping; decoding has to accept
	// it back, along with the lowercase a person will produce.
	encoded := EncodeSecret(secret)
	for _, form := range []string{encoded, strings.ToLower(encoded), strings.ReplaceAll(encoded, " ", "")} {
		got, err := DecodeSecret(form)
		if err != nil {
			t.Fatalf("DecodeSecret(%q): %v", form, err)
		}
		if string(got) != string(secret) {
			t.Errorf("DecodeSecret(%q) did not round-trip", form)
		}
	}
}

func TestEnrolmentURICarriesTheParameters(t *testing.T) {
	secret := []byte("12345678901234567890")
	uri := EnrolmentURI("Stillhouse — Demo Distillery", "kyle@example.com", secret)
	for _, want := range []string{
		"otpauth://totp/",
		"algorithm=SHA1",
		"digits=6",
		"period=30",
		"secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ",
	} {
		if !strings.Contains(uri, want) {
			t.Errorf("enrolment URI %q is missing %q", uri, want)
		}
	}
}
