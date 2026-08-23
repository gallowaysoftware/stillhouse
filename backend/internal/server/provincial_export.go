package server

import (
	"context"
	"encoding/csv"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
	"github.com/gallowaysoftware/stillhouse/backend/internal/wire"
)

// provincialExportHandler streams a jurisdiction's period as CSV.
//
// Every board wants a different form and most want it uploaded into a
// portal, so Stillhouse does not attempt to produce any of them. What it
// produces is the figures, in the shape somebody can paste or transform:
// one row per product, with the identifiers a board is likely to key on.
//
// The caveats ride in the file as comment rows, the same as the journal
// export. A figure handed over with its basis in a help page nobody opens
// is a figure that will be misused.
func provincialExportHandler(
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
		jur := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("jurisdiction")))
		var jurArg pgtype.Text
		if jur != "" {
			jurArg = pgtype.Text{String: jur, Valid: true}
		}

		var (
			rows         []sqlcgen.ProvincialSalesInPeriodRow
			unattributed sqlcgen.ProvincialSalesUnattributedRow
		)
		if err := tdb.WithTenantTx(ctx, tenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
			var e error
			rows, e = q.ProvincialSalesInPeriod(ctx, sqlcgen.ProvincialSalesInPeriodParams{
				PeriodStart:  pgtype.Date{Valid: true, Time: start},
				PeriodEnd:    pgtype.Date{Valid: true, Time: end},
				Jurisdiction: jurArg,
			})
			if e != nil {
				return e
			}
			unattributed, e = q.ProvincialSalesUnattributed(ctx,
				sqlcgen.ProvincialSalesUnattributedParams{
					PeriodStart: pgtype.Date{Valid: true, Time: start},
					PeriodEnd:   pgtype.Date{Valid: true, Time: end},
				})
			return e
		}); err != nil {
			logger.Error("provincial export", "err", err)
			http.Error(w, "could not build the report", http.StatusInternalServerError)
			return
		}

		label := jur
		if label == "" {
			label = "all"
		}
		filename := fmt.Sprintf("stillhouse-provincial-%s-%s-to-%s.csv",
			strings.ToLower(label), start.Format("2006-01-02"), end.Format("2006-01-02"))
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)

		cw := csv.NewWriter(w)
		defer cw.Flush()

		_ = cw.Write([]string{"# Stillhouse provincial sales", label,
			start.Format("2006-01-02") + " to " + end.Format("2006-01-02")})
		_ = cw.Write([]string{"# basis",
			"Removals to a customer in this jurisdiction, by the date they left. " +
				"The jurisdiction is the buyer's, not the stamps' — a case stamped " +
				"for one province and sold into another belongs to the second."})
		_ = cw.Write([]string{"# duty",
			"Federal excise duty as Stillhouse charged it. Not any provincial levy, " +
				"mark-up or container deposit."})
		if unattributed.Removals > 0 {
			_ = cw.Write([]string{"# WARNING", "unattributed removals",
				fmt.Sprintf("%d removal(s) totalling %d bottles and %.4f LAA in this "+
					"period name no customer, so they are in no jurisdiction's figures. "+
					"They are not included below.",
					unattributed.Removals, unattributed.Bottles, unattributed.Laa)})
		}

		_ = cw.Write([]string{
			"jurisdiction", "product", "gtin", "bottle_size_ml", "abv_pct",
			"bottles", "litres", "laa", "federal_duty_cad", "removals",
		})
		for _, row := range rows {
			_ = cw.Write([]string{
				row.Jurisdiction,
				row.ProductName,
				row.Gtin,
				strconv.Itoa(int(row.BottleSizeMl)),
				strconv.FormatFloat(row.TargetAbvPct, 'f', -1, 64),
				strconv.Itoa(int(row.Bottles)),
				strconv.FormatFloat(wire.Round(row.Litres), 'f', -1, 64),
				strconv.FormatFloat(wire.Round(row.Laa), 'f', -1, 64),
				strconv.FormatFloat(row.DutyCad, 'f', 2, 64),
				strconv.Itoa(int(row.Removals)),
			})
		}
	})
}
