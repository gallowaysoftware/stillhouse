// Package zone is where a distillery's days begin and end.
//
// A return is filed by calendar month, and a month ends at the licensee's
// midnight. Measured in UTC it ends at eight or nine in the evening in
// Atlantic Canada, so an evening's gauge on the last day lands on the next
// return.
package zone

import (
	"context"
	"time"
	_ "time/tzdata" // the image has no zone database

	"github.com/jackc/pgx/v5/pgtype"
)

// One zone a province. Where a province has two, this is the one most of
// its people live in.
var byJurisdiction = map[string]string{
	"CA-NL": "America/St_Johns",
	"CA-PE": "America/Halifax",
	"CA-NS": "America/Halifax",
	"CA-NB": "America/Moncton",
	"CA-QC": "America/Toronto",
	"CA-ON": "America/Toronto",
	"CA-MB": "America/Winnipeg",
	"CA-SK": "America/Regina",
	"CA-AB": "America/Edmonton",
	"CA-BC": "America/Vancouver",
	"CA-YT": "America/Whitehorse",
	"CA-NT": "America/Yellowknife",
	"CA-NU": "America/Iqaluit",
}

// For is the zone of a jurisdiction such as "CA-PE", and UTC for one that
// is not a province.
func For(jurisdiction string) *time.Location {
	name, ok := byJurisdiction[jurisdiction]
	if !ok {
		return time.UTC
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return loc
}

type ctxKey struct{}

// With carries a tenant's zone.
func With(ctx context.Context, loc *time.Location) context.Context {
	return context.WithValue(ctx, ctxKey{}, loc)
}

// From is the zone With put there, or UTC.
func From(ctx context.Context) *time.Location {
	if loc, ok := ctx.Value(ctxKey{}).(*time.Location); ok {
		return loc
	}
	return time.UTC
}

// Day is the calendar day an instant falls on, as the midnight-UTC value
// DATE columns are read and written as.
func Day(loc *time.Location, t time.Time) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Date is Day for a column.
func Date(ctx context.Context, t time.Time) pgtype.Date {
	return pgtype.Date{Valid: true, Time: Day(From(ctx), t)}
}

// Start is the instant a calendar day begins.
func Start(ctx context.Context, day time.Time) pgtype.Timestamptz {
	y, m, d := day.Date()
	return pgtype.Timestamptz{Valid: true, Time: time.Date(y, m, d, 0, 0, 0, 0, From(ctx))}
}

// DuringDay is when something known only by its date is taken to have
// happened: 16:00 UTC, which is the same calendar day, in the working part
// of it, from St. John's to Whitehorse.
func DuringDay(day time.Time) pgtype.Timestamptz {
	y, m, d := day.Date()
	return pgtype.Timestamptz{Valid: true, Time: time.Date(y, m, d, 16, 0, 0, 0, time.UTC)}
}
