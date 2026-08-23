// Package totp implements time-based one-time passwords, RFC 6238, over
// the HMAC-based codes of RFC 4226.
//
// Written out rather than taken as a dependency because the algorithm is
// forty lines, fully specified, and — the part that decides it — comes
// with published test vectors. A second factor whose correctness rests
// on "the library is popular" is a worse position than one checked
// against the numbers in the RFC, which is what totp_test.go does.
//
// The parameters are the ones every authenticator app assumes: SHA-1,
// six digits, a thirty-second step. They are not configurable; an
// operator who cannot scan a standard enrolment URI is not helped by a
// knob here.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	// Digits is the code length. Six is what authenticator apps show.
	Digits = 6
	// Period is the step length in seconds. Thirty is the default every
	// app assumes when the URI omits it.
	Period = 30
	// SecretBytes is the length of a generated shared secret. RFC 4226
	// §4 R6 requires at least 128 bits and recommends 160, which is the
	// output length of the SHA-1 HMAC used here.
	SecretBytes = 20
)

// NewSecret returns a fresh random shared secret.
func NewSecret() ([]byte, error) {
	b := make([]byte, SecretBytes)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

// EncodeSecret renders a secret as the unpadded base32 an authenticator
// app expects, in groups of four so a human can retype it without
// losing their place.
func EncodeSecret(secret []byte) string {
	raw := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
	var b strings.Builder
	for i, r := range raw {
		if i > 0 && i%4 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// DecodeSecret parses a base32 secret, tolerating the spaces and
// lowercase a person retyping it will produce.
func DecodeSecret(s string) ([]byte, error) {
	s = strings.ToUpper(strings.ReplaceAll(s, " ", ""))
	s = strings.TrimRight(s, "=")
	return base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(s)
}

// EnrolmentURI builds the otpauth:// URI an authenticator app consumes.
// issuer names the install, account names the person — both appear in
// the app's list, which is the only thing distinguishing one distillery
// from another on a phone that holds several.
func EnrolmentURI(issuer, account string, secret []byte) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret))
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(Digits))
	q.Set("period", fmt.Sprint(Period))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// Step is the counter value for a moment in time: RFC 6238's T.
func Step(t time.Time) int64 { return t.Unix() / Period }

// Code returns the code for a given counter value. RFC 4226 §5.3.
func Code(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)

	// Dynamic truncation: the low nibble of the last byte selects a
	// four-byte window, whose high bit is masked off so the result is
	// positive regardless of the platform's signedness.
	offset := sum[len(sum)-1] & 0x0f
	value := (int64(sum[offset]&0x7f) << 24) |
		(int64(sum[offset+1]) << 16) |
		(int64(sum[offset+2]) << 8) |
		int64(sum[offset+3])

	mod := int64(1)
	for i := 0; i < Digits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", Digits, value%mod)
}

// Validate checks code against the steps within skew of now, and returns
// the step that matched.
//
// The returned step is not a courtesy: the caller must record it and
// refuse anything at or below it next time. Without that, a code
// shoulder-surfed or read off a phishing page stays good for the rest of
// its window, which is the one attack a second factor is supposed to
// make expensive. See RFC 6238 §5.2.
//
// skew of 1 accepts the previous and next step — ninety seconds of
// tolerance in total. That is the usual allowance for a phone whose
// clock has drifted and for a person still typing when the window turns.
func Validate(secret []byte, code string, now time.Time, skew int64) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != Digits {
		return 0, false
	}
	current := Step(now)
	for delta := -skew; delta <= skew; delta++ {
		step := current + delta
		// Constant time: a comparison that returns early leaks how much
		// of a guess was right, one digit at a time.
		if subtle.ConstantTimeCompare([]byte(Code(secret, step)), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}
