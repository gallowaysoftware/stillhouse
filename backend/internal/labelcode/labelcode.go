// Package labelcode turns a row's UUID into something you can print on a
// cask, read out over a noisy still house, and scan with a five-dollar
// wedge scanner.
//
// A UUID is 36 characters. As a Code 128 symbol at a size that fits on a
// bung stave it is unreadable, and as something to read down a phone it
// is worse. What goes on the label is 14 characters: a kind letter and
// 13 base-32 digits carrying the first 64 bits of the id.
//
// Derived rather than stored, on purpose. A stored code needs a column, a
// uniqueness constraint, a generator at every insert site and a backfill
// for every row that already exists — four things to get right and keep
// right — and buys nothing here, because the id is already unique and
// already immutable. Every cask that has ever existed has a label code
// from the moment this ships.
//
// The truncation is safe by a wide margin but not by definition, so the
// resolver treats a prefix matching two rows as an ambiguity to report
// rather than a coin to flip. See Resolve in the label service. Sixty-four
// bits is 1.8e19; at a hundred thousand rows in one tenant the chance of
// any collision is about three in ten billion.
//
// The alphabet is Crockford base 32: no I, L, O or U, so nothing reads as
// something else on a smudged thermal label and there is no word to
// misprint on a cask end.
package labelcode

import (
	"errors"
	"strings"

	"github.com/google/uuid"
)

// Kind is what a code points at. The letter is part of the printed code,
// so a scan is unambiguous about what it addresses before anything looks
// it up — scanning a case label at the regauge screen says "that is a
// packaged lot, not a cask" rather than "not found".
type Kind string

const (
	KindBarrel    Kind = "B"
	KindContainer Kind = "V" // vessel: a tank or any non-barrel container
	KindLot       Kind = "L" // packaged inventory
	KindShipment  Kind = "S"
	KindProduct   Kind = "P"
)

var kinds = map[Kind]bool{
	KindBarrel: true, KindContainer: true, KindLot: true,
	KindShipment: true, KindProduct: true,
}

// KindLabel is what to call a kind in a sentence an operator reads.
func KindLabel(k Kind) string {
	switch k {
	case KindBarrel:
		return "cask"
	case KindContainer:
		return "vessel"
	case KindLot:
		return "packaged lot"
	case KindShipment:
		return "shipment"
	case KindProduct:
		return "product"
	default:
		return "unknown"
	}
}

// alphabet is Crockford base 32.
const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// Digits is how many base-32 digits carry the 64 bits. 13 × 5 = 65, so
// the top digit only ever uses one bit.
const Digits = 13

// Length is the full printed length, kind letter included.
const Length = Digits + 1

var (
	// ErrMalformed means the string is not a label code at all.
	ErrMalformed = errors.New("not a label code")
	// ErrKind means it is a label code for something else.
	ErrKind = errors.New("that code is for a different kind of thing")
)

// Encode renders the code printed on the label.
func Encode(k Kind, id uuid.UUID) string {
	var n uint64
	for i := 0; i < 8; i++ {
		n = n<<8 | uint64(id[i])
	}
	out := make([]byte, Digits)
	for i := Digits - 1; i >= 0; i-- {
		out[i] = alphabet[n&31]
		n >>= 5
	}
	return string(k) + string(out)
}

// Normalise makes a scanned or typed string comparable: upper case, no
// spaces or hyphens, and the four Crockford substitutions so a code read
// off a label by eye still resolves. O is zero; I and L are one.
func Normalise(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(s)) {
		switch r {
		case ' ', '-', '\t':
			continue
		case 'O':
			b.WriteRune('0')
		case 'I', 'L':
			// L is a kind letter as well as a digit substitution, so it is
			// only rewritten after the first character; handled below.
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Decode parses a printed code back to its kind and the 64-bit prefix of
// the id it points at. The prefix is what the resolver matches on; it is
// deliberately not a whole id, because it isn't one.
func Decode(s string) (Kind, uint64, error) {
	s = Normalise(s)
	if len(s) != Length {
		return "", 0, ErrMalformed
	}
	k := Kind(s[:1])
	if !kinds[k] {
		return "", 0, ErrMalformed
	}
	var n uint64
	for i := 1; i < len(s); i++ {
		c := s[i]
		// The digit substitutions, applied here rather than in Normalise
		// so that the kind letter L is never rewritten.
		switch c {
		case 'I', 'L':
			c = '1'
		}
		idx := strings.IndexByte(alphabet, c)
		if idx < 0 {
			return "", 0, ErrMalformed
		}
		n = n<<5 | uint64(idx)
	}
	return k, n, nil
}

// Prefix is the 64-bit prefix of an id, for comparing a decoded code
// against a candidate row.
func Prefix(id uuid.UUID) uint64 {
	var n uint64
	for i := 0; i < 8; i++ {
		n = n<<8 | uint64(id[i])
	}
	return n
}

// Matches reports whether id is the row a decoded code points at.
func Matches(id uuid.UUID, prefix uint64) bool { return Prefix(id) == prefix }
