// Package money is exact decimal arithmetic for amounts on documents.
//
// Stillhouse already keeps money as NUMERIC in the database and as a
// decimal string on the wire, for the reason stated in stage 157:
// rendering 34.95 through a float64 and back is how a cent goes missing,
// and these are amounts somebody invoices. What was missing was the
// middle — a way to multiply a quantity by a price without going through
// a float on the way.
//
// Backed by big.Rat, so nothing is approximate until it is deliberately
// rounded. Sixty bottles at $34.95 is exactly $2,097.00 here, and a tax
// line at 0.13 is exactly thirteen percent of it.
package money

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// Amount is an exact decimal quantity of money.
//
// The zero value is a usable zero, so a struct holding one need not
// initialise it.
type Amount struct {
	r *big.Rat
}

// Zero is the additive identity.
func Zero() Amount { return Amount{r: new(big.Rat)} }

func (a Amount) rat() *big.Rat {
	if a.r == nil {
		return new(big.Rat)
	}
	return a.r
}

// Parse reads a decimal string. An empty string is zero, because an
// omitted amount on a form means the operator left it blank and blank
// means nothing rather than an error.
func Parse(s string) (Amount, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Zero(), nil
	}
	// Tolerate what people type: a leading currency symbol, thousands
	// separators, and a trailing minus.
	s = strings.NewReplacer("$", "", ",", "", " ", "").Replace(s)
	if strings.HasSuffix(s, "-") {
		s = "-" + strings.TrimSuffix(s, "-")
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return Zero(), fmt.Errorf("%q is not an amount", s)
	}
	return Amount{r: r}, nil
}

// MustParse is Parse for literals that are known good.
func MustParse(s string) Amount {
	a, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return a
}

func (a Amount) Add(b Amount) Amount { return Amount{r: new(big.Rat).Add(a.rat(), b.rat())} }
func (a Amount) Sub(b Amount) Amount { return Amount{r: new(big.Rat).Sub(a.rat(), b.rat())} }
func (a Amount) Mul(b Amount) Amount { return Amount{r: new(big.Rat).Mul(a.rat(), b.rat())} }
func (a Amount) Neg() Amount         { return Amount{r: new(big.Rat).Neg(a.rat())} }

func (a Amount) IsZero() bool           { return a.rat().Sign() == 0 }
func (a Amount) Sign() int              { return a.rat().Sign() }
func (a Amount) Cmp(b Amount) int       { return a.rat().Cmp(b.rat()) }
func (a Amount) LessThan(b Amount) bool { return a.Cmp(b) < 0 }

// RoundTo returns a rounded to places decimal digits, half away from zero.
//
// Half away from zero rather than banker's rounding: it is what an
// invoice reader expects, and what every other system a licensee will
// reconcile against does.
func (a Amount) RoundTo(places int) Amount {
	pow := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(places)), nil))
	scaled := new(big.Rat).Mul(a.rat(), pow)

	num, den := scaled.Num(), scaled.Denom()
	q, rem := new(big.Int).QuoRem(num, den, new(big.Int))
	// 2*|rem| >= |den| means the fraction is at or past the half.
	twice := new(big.Int).Abs(new(big.Int).Lsh(rem, 1))
	if twice.Cmp(new(big.Int).Abs(den)) >= 0 {
		if scaled.Sign() < 0 {
			q.Sub(q, big.NewInt(1))
		} else {
			q.Add(q, big.NewInt(1))
		}
	}
	return Amount{r: new(big.Rat).Quo(new(big.Rat).SetInt(q), pow)}
}

// String renders the amount to places decimal digits, rounded.
func (a Amount) String(places int) string {
	return a.RoundTo(places).rat().FloatString(places)
}

// Float is for display and for the few proto fields that carry a double.
// Never for arithmetic that will be stored.
func (a Amount) Float() float64 {
	f, _ := a.rat().Float64()
	return f
}

// Numeric converts to the pgtype the columns use, at the given scale.
func (a Amount) Numeric(places int) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	if err := n.Scan(a.String(places)); err != nil {
		return n, err
	}
	return n, nil
}

// FromNumeric reads a NUMERIC back exactly.
func FromNumeric(n pgtype.Numeric) Amount {
	if !n.Valid || n.NaN || n.Int == nil {
		return Zero()
	}
	r := new(big.Rat).SetInt(new(big.Int).Set(n.Int))
	pow := new(big.Rat).SetInt(new(big.Int).Exp(
		big.NewInt(10), big.NewInt(int64(abs32(n.Exp))), nil))
	if n.Exp < 0 {
		r.Quo(r, pow)
	} else {
		r.Mul(r, pow)
	}
	return Amount{r: r}
}

func abs32(x int32) int32 {
	if x < 0 {
		return -x
	}
	return x
}
