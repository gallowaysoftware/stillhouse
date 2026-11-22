package units

import "strings"

// kilograms per unit. The pound is exact, by definition.
var kilograms = map[string]float64{
	"kg": 1, "kilogram": 1, "kilograms": 1,
	"g": 0.001, "gram": 0.001, "grams": 0.001,
	"t": 1000, "tonne": 1000, "tonnes": 1000,
	"lb": 0.45359237, "lbs": 0.45359237, "pound": 0.45359237, "pounds": 0.45359237,
}

// Kg is a mass in kilograms. ok is false for a unit that is not a mass, and
// the caller must not read the number as if it were one.
func Kg(quantity float64, uom string) (kg float64, ok bool) {
	f, ok := kilograms[strings.ToLower(strings.TrimSpace(uom))]
	return quantity * f, ok
}
