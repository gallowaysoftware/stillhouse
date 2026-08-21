package mashing

import "testing"

// TestBenchSaysUnknownWhenNoCerealIsPublished: a bill where nothing has a
// published gelatinisation range — an all-oat mash — returned a 0–0 °C
// range with no finding at all, because the "nothing known" early return
// sat above the block that raises cereal_unknown. Every existing test
// included barley, so the branch never ran.
//
// Reporting zero degrees as though it were a figure is precisely what this
// package exists not to do: where the curriculum has no number, the answer
// is "unknown", said out loud.
func TestBenchSaysUnknownWhenNoCerealIsPublished(t *testing.T) {
	b := Assess([]GrainBillItem{
		{Name: "Oats (naked)", MassKg: 200, Cereal: CerealUnspecified},
	}, Readings{})

	if b.GelatinisationKnown {
		t.Error("GelatinisationKnown is true for a bill whose cereals are all unpublished")
	}
	var found *Finding
	for i := range b.Findings {
		if b.Findings[i].Code == "cereal_unknown" {
			found = &b.Findings[i]
		}
	}
	if found == nil {
		t.Fatalf("no cereal_unknown finding — the bench reported %v–%v °C and said nothing "+
			"about why. findings: %v",
			b.GelatinisationC.MinC, b.GelatinisationC.MaxC, b.Findings)
	}
	if found.Severity < SeverityWarning {
		t.Errorf("cereal_unknown severity = %v; when NOTHING in the bill is known this is more "+
			"than an aside", found.Severity)
	}
}

// And the ordinary mixed case must keep working: barley is known, oats
// are not, so there IS guidance and the unknown is an informational note
// beside it.
func TestBenchStillGuidesWhenSomeCerealsAreKnown(t *testing.T) {
	b := Assess([]GrainBillItem{
		{Name: "Malted barley", MassKg: 100, Cereal: CerealBarley},
		{Name: "Oats", MassKg: 50, Cereal: CerealUnspecified},
	}, Readings{})

	if b.GelatinisationC.MaxC <= 0 {
		t.Errorf("no gelatinisation guidance for a bill containing barley: %v", b.GelatinisationC)
	}
	if b.GelatinisationKnown {
		t.Error("GelatinisationKnown should be false while any cereal is unpublished")
	}
}
