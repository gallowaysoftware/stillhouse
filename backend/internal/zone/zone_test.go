package zone

import (
	"context"
	"testing"
	"time"
)

func TestEveryProvinceHasAZone(t *testing.T) {
	for j := range byJurisdiction {
		if For(j) == time.UTC {
			t.Errorf("%s has no zone", j)
		}
	}
	if For("US-ME") != time.UTC || For("") != time.UTC {
		t.Error("somewhere that is not a province was given a zone")
	}
}

func TestAnEveningBelongsToItsOwnDay(t *testing.T) {
	pei := For("CA-PE")
	ctx := With(context.Background(), pei)
	evening := time.Date(2026, 10, 31, 22, 30, 0, 0, pei)
	if got := Date(ctx, evening).Time.Format("2006-01-02"); got != "2026-10-31" {
		t.Errorf("22:30 on 31 October is on %s", got)
	}
	if november := Start(ctx, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)).Time; !evening.Before(november) {
		t.Errorf("November begins at %s, before 22:30 on 31 October", november)
	}
	if evening.UTC().Day() != 1 {
		t.Error("the case no longer crosses UTC midnight, so it proves nothing")
	}
}

func TestADateIsTheSameDayEverywhere(t *testing.T) {
	for _, month := range []time.Month{time.January, time.July} {
		day := time.Date(2026, month, 1, 0, 0, 0, 0, time.UTC)
		for j := range byJurisdiction {
			if got := Day(For(j), DuringDay(day).Time); !got.Equal(day) {
				t.Errorf("%s: %s is recorded on %s", j, day.Format("2006-01-02"), got.Format("2006-01-02"))
			}
		}
	}
}
