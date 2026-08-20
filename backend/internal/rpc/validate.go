package rpc

import (
	"errors"
	"fmt"
)

// Shared input guards. The pattern these close: a field whose name implies
// one unit while the code expects another, or a value the database will
// reject anyway — both of which used to reach the operator as a generic
// server error instead of a sentence naming the field.

// validateFraction checks a value that is a fraction in [0,1] despite being
// named `_pct`. Materials carry extract and moisture this way, and 78 typed
// for 0.78 projected a 1077% ABV wash from an ordinary rye bill — the
// number came back a hundredfold too big and looked exactly as confident
// as a real one. The message shows the value they probably meant, because
// the field name is the reason they got it wrong.
func validateFraction(name string, v float64) error {
	if v < 0 || v > 1 {
		if v > 1 && v <= 100 {
			return fmt.Errorf("%s is a fraction, not a percentage: %g is out of range — did you mean %g?",
				name, v, v/100)
		}
		return fmt.Errorf("%s must be a fraction in [0, 1], got %g", name, v)
	}
	return nil
}

// validateAbvPct checks a strength expressed the ordinary way, 0–100.
func validateAbvPct(name string, v float64) error {
	if v < 0 || v > 100 {
		return fmt.Errorf("%s must be in [0, 100], got %g", name, v)
	}
	return nil
}

// validateCapacityL checks a vessel's recorded capacity. Zero is rejected
// as well as negative: a zero-capacity vessel passes the "is capacity
// recorded" test and then fails every fill against it.
func validateCapacityL(v float64) error {
	if v <= 0 {
		return fmt.Errorf("capacity_l must be > 0, got %g", v)
	}
	return nil
}

// errInvalidInput marks an error as the caller's fault. The gauging paths
// funnel their errors through alcoholometryError, which knew about range
// errors and missing temperatures but treated anything else as a server
// fault — so a blank field came back as code=internal and the UI showed a
// generic failure for a problem the operator could have fixed in a second.
var errInvalidInput = errors.New("invalid input")

func invalidInput(err error) error { return fmt.Errorf("%w: %w", errInvalidInput, err) }
