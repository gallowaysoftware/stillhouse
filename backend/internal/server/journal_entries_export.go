package server

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/journal"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

// journalEntriesExportHandler streams a period as importable journal
// entries. PLAN G2/G3.
//
// The difference from /export/journal.csv beside it is the audience.
// That one is for a person: both accounts on one row, warnings as comment
// rows above the header, everything arranged to be read. This one is for
// QuickBooks Online or Xero: one row per side tied by an entry number, no
// comment rows — a `#` line is a parse error to an importer — and no
// partial files.
//
// Which is the real difference. The human export prints its warnings and
// lets the reader judge. An import file cannot: it goes into somebody's
// books, and a row with a blank account either fails the import or lands
// in a suspense account nobody looks at again. Half a journal reconciles
// to within the missing half, which is precisely why nobody notices. So
// this refuses, with 409 and the names of the unmapped kinds, and the
// operator maps them and asks again.
func journalEntriesExportHandler(
	sm *scs.SessionManager, tdb *tenantdb.DB, logger *slog.Logger,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		_, tenantID, ok := sessionIdentity(sm, ctx)
		if !ok {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}

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
			logger.Error("journal entries export", "err", err)
			http.Error(w, "could not build the journal", http.StatusInternalServerError)
			return
		}

		rows, err := journal.Entries(j)
		if err != nil {
			var ue *journal.UnmappedError
			var be *journal.UnbalancedError
			switch {
			case errors.As(err, &ue), errors.As(err, &be):
				// Refused on purpose, and the operator is told exactly
				// what to fix. 409 rather than 400: the request was
				// fine, the data is not ready.
				http.Error(w, err.Error(), http.StatusConflict)
			default:
				logger.Error("journal entries export", "err", err)
				http.Error(w, "could not build the journal", http.StatusInternalServerError)
			}
			return
		}

		filename := fmt.Sprintf("stillhouse-journal-entries-%s-to-%s.csv",
			start.Format("2006-01-02"), end.Format("2006-01-02"))
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)

		cw := csv.NewWriter(w)
		defer cw.Flush()

		// The header both QBO and Xero accept. Deliberately no comment
		// rows and no blank line: this file is parsed, not read.
		_ = cw.Write([]string{
			"JournalNo", "JournalDate", "AccountCode", "AccountName",
			"Debit", "Credit", "Description", "Memo", "Reference",
		})
		for _, row := range rows {
			_ = cw.Write([]string{
				fmt.Sprintf("%d", row.EntryNo),
				row.Date,
				row.Account,
				row.AccountName,
				row.Debit,
				row.Credit,
				row.Description,
				row.Memo,
				row.Reference,
			})
		}
	})
}
