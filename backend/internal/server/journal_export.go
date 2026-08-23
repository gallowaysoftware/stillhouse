package server

import (
	"context"
	"encoding/csv"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/journal"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

// journalExportHandler streams a period's journal as CSV.
//
// One row per event, with the debit and credit accounts on the same row
// rather than as two rows. Every event Stillhouse emits moves one amount
// between exactly two accounts, so the two-row form would carry no
// information the one-row form doesn't and would double the number of
// rows an accountant has to eyeball. Anything that imports journals
// takes either shape.
//
// The warnings ride in the file, as comment rows above the header. They
// are the difference between "here is your duty for the month" and "here
// is your duty for the month, and eleven material lots have no cost so
// your inventory is short by whatever they were worth" — and a warning
// in a log nobody reads is not a warning.
func journalExportHandler(
	sm *scs.SessionManager, tdb *tenantdb.DB, logger *slog.Logger,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		userID, tenantID, ok := sessionIdentity(sm, ctx)
		if !ok {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		_ = userID

		start, err := time.Parse("2006-01-02", r.URL.Query().Get("from"))
		if err != nil {
			http.Error(w, "from must be YYYY-MM-DD", http.StatusBadRequest)
			return
		}
		end, err := time.Parse("2006-01-02", r.URL.Query().Get("to"))
		if err != nil {
			http.Error(w, "to must be YYYY-MM-DD", http.StatusBadRequest)
			return
		}
		if end.Before(start) {
			http.Error(w, "to is before from", http.StatusBadRequest)
			return
		}

		var j *journal.Journal
		if err := tdb.WithTenantTx(ctx, tenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
			var e error
			j, e = journal.Build(ctx, q, start, end)
			return e
		}); err != nil {
			logger.Error("journal export", "err", err)
			http.Error(w, "could not build the journal", http.StatusInternalServerError)
			return
		}

		filename := fmt.Sprintf("stillhouse-journal-%s-to-%s.csv",
			start.Format("2006-01-02"), end.Format("2006-01-02"))
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)

		cw := csv.NewWriter(w)
		defer cw.Flush()

		_ = cw.Write([]string{"# Stillhouse journal export",
			start.Format("2006-01-02") + " to " + end.Format("2006-01-02")})
		for _, warn := range j.Warnings {
			_ = cw.Write([]string{"# WARNING", warn.Kind, warn.Detail})
		}
		if len(j.Warnings) == 0 {
			_ = cw.Write([]string{"# no warnings", "every event in this period was priced and mapped"})
		}

		_ = cw.Write([]string{
			"date", "kind", "description", "reference", "amount_cad",
			"debit_account", "debit_name", "credit_account", "credit_name",
			"memo", "basis",
		})
		for _, l := range j.Lines {
			_ = cw.Write([]string{
				l.Date.Format("2006-01-02"),
				string(l.Kind),
				l.Description,
				l.Reference,
				strconv.FormatFloat(l.AmountCAD, 'f', 2, 64),
				l.Debit, l.DebitName, l.Credit, l.CreditName,
				l.Memo, l.Basis,
			})
		}
	})
}
