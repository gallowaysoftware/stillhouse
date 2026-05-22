package server

import (
	"archive/zip"
	"context"
	"encoding/csv"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
)

// exportTables is the ordered list of tables included in /export/tenant.zip.
// RLS scopes each SELECT * to the caller's tenant automatically — see
// WithTenantTx for how app.current_tenant_id gets set on the transaction.
//
// Order is roughly the data-flow direction (materials → recipes → production
// → barrels → bottling → removals → B266 → audit) so a reviewer reading the
// zip top-to-bottom can follow the chain.
var exportTables = []string{
	"tenants",
	"users",
	"materials",
	"material_receipts",
	"recipes",
	"recipe_versions",
	"recipe_ingredients",
	"mash_runs",
	"mash_ingredients",
	"mash_metrics",
	"fermentation_runs",
	"fermentation_logs",
	"bulk_containers",
	"bulk_movements",
	"distillation_runs",
	"distillation_charges",
	"distillation_cuts",
	"barrels",
	"products",
	"excise_stamp_orders",
	"bottling_runs",
	"bottling_run_stamp_usage",
	"packaged_inventory",
	"packaging_removals",
	"b266_periods",
	"audit_events",
}

// tenantExportHandler streams a zip containing one CSV per significant
// tenant-owned table. Owner-only — Excise Act s.206 retention and PIPEDA
// right-to-data are both owner-level decisions, and the export contains
// every operator's keystrokes via audit_events.
func tenantExportHandler(sm *scs.SessionManager, pool *pgxpool.Pool, queries *sqlcgen.Queries, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		userIDStr := sm.GetString(ctx, "user_id")
		tenantIDStr := sm.GetString(ctx, "tenant_id")
		if userIDStr == "" || tenantIDStr == "" {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		userID, err := uuid.Parse(userIDStr)
		if err != nil {
			http.Error(w, "invalid session", http.StatusUnauthorized)
			return
		}
		tenantID, err := uuid.Parse(tenantIDStr)
		if err != nil {
			http.Error(w, "invalid session", http.StatusUnauthorized)
			return
		}
		// queries is admin-pool-backed, but GetUserByID just reads; no
		// tenant-scoped write happens here. Role check is done in code,
		// not via RLS, because we're outside a tenant tx at this point.
		user, err := queries.GetUserByID(ctx, userID)
		if err != nil || user.TenantID != tenantID {
			http.Error(w, "session refers to missing user", http.StatusUnauthorized)
			return
		}
		if user.Role != sqlcgen.UserRoleOwner {
			http.Error(w, "owner role required", http.StatusForbidden)
			return
		}

		filename := fmt.Sprintf("stillhouse-export-%s.zip", time.Now().UTC().Format("2006-01-02"))
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(filename))

		zw := zip.NewWriter(w)
		defer zw.Close()

		err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx,
				"SELECT set_config('app.current_tenant_id', $1, true)",
				tenantID.String(),
			); err != nil {
				return fmt.Errorf("set tenant context: %w", err)
			}
			for _, table := range exportTables {
				if err := dumpTableToZip(ctx, tx, zw, table); err != nil {
					// Tables not present in this deployment (e.g., features rolled
					// back) shouldn't kill the whole export. Log and continue.
					logger.Warn("tenantExport: skip table", "table", table, "err", err)
					continue
				}
			}
			return nil
		})
		if err != nil {
			logger.Error("tenantExport", "err", err)
			// Headers already sent; the partial zip will surface as a client error.
			return
		}
	})
}

func dumpTableToZip(ctx context.Context, tx pgx.Tx, zw *zip.Writer, table string) error {
	rows, err := tx.Query(ctx, "SELECT * FROM "+table)
	if err != nil {
		return err
	}
	defer rows.Close()

	entry, err := zw.Create(table + ".csv")
	if err != nil {
		return err
	}
	cw := csv.NewWriter(entry)
	defer cw.Flush()

	fields := rows.FieldDescriptions()
	header := make([]string, len(fields))
	for i, f := range fields {
		header[i] = string(f.Name)
	}
	if err := cw.Write(header); err != nil {
		return err
	}

	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return err
		}
		record := make([]string, len(values))
		for i, v := range values {
			record[i] = stringifyCellForCSV(v)
		}
		if err := cw.Write(record); err != nil {
			return err
		}
	}
	cw.Flush()
	return rows.Err()
}

// stringifyCellForCSV renders a pgx column value as a CSV-safe string. We
// intentionally drop type fidelity here — the export is for human auditors
// and downstream tools that can re-parse ISO 8601 timestamps and UUIDs.
func stringifyCellForCSV(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(x)
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano)
	case fmt.Stringer:
		return x.String()
	default:
		return fmt.Sprintf("%v", x)
	}
}
