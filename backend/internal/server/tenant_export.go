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
// Every table named here MUST be RLS-scoped, because the dump is a bare
// SELECT * and RLS is the only thing keeping it to one tenant.
//
// tenants and users are NOT in this list and must never be added. Both are
// deliberately created without row-level security (migration 000001) —
// login has to find a user before any tenant context exists — so a
// SELECT * over either returns EVERY tenant's rows. Exporting them handed
// the caller every distillery's CRA licence number and every user's
// Argon2id password hash. They are exported through
// exportOwnTenantIdentity below instead, scoped by parameter and with the
// credential column left out.
//
// Order is roughly the data-flow direction (materials → recipes → production
// → barrels → bottling → removals → B266 → audit) so a reviewer reading the
// zip top-to-bottom can follow the chain.
var exportTables = []string{
	// Premises and licences come first. Everything below happens at a
	// location, under a licence, and a reviewer needs to know which.
	"locations",
	"excise_licences",
	// The instrument register: everything downstream that determined a
	// quantity points back at it, and a reviewer reading the zip
	// top-to-bottom needs to know what the serial numbers mean before
	// meeting them on a gauge.
	"instruments",
	"instrument_calibrations",
	"suppliers",
	"purchase_orders",
	"purchase_order_lines",
	"materials",
	// Every receipt of grain, with what it cost landed. Named
	// material_receipts here until stage 174, which is a table that has
	// never existed — so the export silently skipped it, and an s.206
	// retention export went out with no record of what came in the door.
	"material_lots",
	"recipes",
	"recipe_versions",
	"recipe_ingredients",
	"recipe_version_sensory",
	"recipe_version_whisky_sensory",
	"work_orders",
	"mash_runs",
	// Likewise mash_ingredients, which is called mash_ingredient_usage.
	"mash_ingredient_usage",
	"mash_metrics",
	"fermentation_runs",
	"fermentation_logs",
	"bulk_containers",
	"bulk_movements",
	// Line D: the reason-coded entries reconciling book to physical, with
	// the explanation and the author an auditor reads.
	"inventory_adjustments",
	"distillation_runs",
	"distillation_charges",
	"distillation_cuts",
	"redistillations",
	// The determinations themselves. Without these the export could show
	// what the quantities were and not how any of them was arrived at,
	// which is the half an auditor actually asks about.
	"production_gauges",
	// And likewise barrels, which is bulk_containers plus the maturation
	// record hanging off it. Char, cooperage, fill date and the
	// maturation clock were all missing from the zip.
	"barrel_attributes",
	"barrel_events",
	"products",
	"excise_stamp_orders",
	"excise_stamp_dispositions",
	"bottling_runs",
	"bottling_run_stamp_usage",
	"packaged_inventory",
	"lab_results",
	"customers",
	"price_lists",
	"price_list_entries",
	"packaging_removals",
	// The sales chain sits between the stock and the return: a removal
	// names the shipment it came from, so exporting removals without
	// shipments would leave that reference pointing at nothing.
	"sales_orders",
	"sales_order_lines",
	"shipments",
	"shipment_lines",
	"b266_periods",
	"journal_accounts",
	// What the licensee's own costing policy was, and the hours it was
	// applied to. Both are inputs to figures an accountant will be
	// looking at; exporting the figures without them leaves a cost of
	// sales nobody can reproduce.
	"cost_rates",
	"labour_entries",
	"alerts",
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
		// Close writes the zip's central directory. If it fails the operator
		// gets a truncated archive, and since headers are already sent we
		// can't signal that with a status code — so at least log it rather
		// than handing back a silently corrupt export.
		defer func() {
			if err := zw.Close(); err != nil {
				logger.Error("tenantExport: finalise zip", "err", err)
			}
		}()

		err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx,
				"SELECT set_config('app.current_tenant_id', $1, true)",
				tenantID.String(),
			); err != nil {
				return fmt.Errorf("set tenant context: %w", err)
			}
			// The two non-RLS tables, scoped by parameter rather than by
			// tenant context.
			if err := exportOwnTenantIdentity(ctx, tx, zw, tenantID); err != nil {
				return fmt.Errorf("export tenant identity: %w", err)
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

// exportOwnTenantIdentity writes tenants.csv and users.csv scoped by an
// explicit WHERE, since neither table is RLS-protected. users.csv carries
// no password_hash: an operator's data export has no business containing
// credential material, and those hashes are offline-crackable.
func exportOwnTenantIdentity(ctx context.Context, tx pgx.Tx, zw *zip.Writer, tenantID uuid.UUID) error {
	if err := dumpQueryToZip(ctx, tx, zw, "tenants",
		`SELECT id, name, cra_spirits_licence_number, excise_warehouse_licence_number,
		        default_jurisdiction, created_at, updated_at
		   FROM tenants WHERE id = $1`, tenantID); err != nil {
		return err
	}
	return dumpQueryToZip(ctx, tx, zw, "users",
		`SELECT id, tenant_id, email, display_name, role, created_at, updated_at
		   FROM users WHERE tenant_id = $1 ORDER BY created_at`, tenantID)
}

func dumpTableToZip(ctx context.Context, tx pgx.Tx, zw *zip.Writer, table string) error {
	return dumpQueryToZip(ctx, tx, zw, table, "SELECT * FROM "+table)
}

func dumpQueryToZip(ctx context.Context, tx pgx.Tx, zw *zip.Writer, name, sql string, args ...any) error {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	entry, err := zw.Create(name + ".csv")
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
