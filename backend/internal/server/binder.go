package server

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// The audit binder.
//
// Everything in it already existed in pieces — period-locked snapshots,
// the audit log, gauge determination paths, the instruments behind them,
// movement-level detail. Nobody had assembled them, so answering "show me
// how you arrived at line 3" meant somebody exporting four things and
// explaining the join by hand.
//
// One bundle per period: the figures as filed, the movements behind each
// line, the determinations and instruments behind each movement, and the
// trail. A print-ready document for reading, CSVs for working with, and a
// manifest so the bundle is tamper-evident.
//
// The one rule that matters: for a submitted period the figures come from
// the FROZEN SNAPSHOT, never recomputed. A binder that quietly recalculated
// would show today's answer under the heading of what was filed, which is
// the one thing an audit binder must not do.

// binderFile is one entry in the bundle, kept in memory so its checksum
// can go in the manifest. A period's binder is a few hundred kilobytes;
// streaming it to save that is not worth losing the manifest over.
type binderFile struct {
	name string
	body []byte
}

func b266BinderHandler(sm *scs.SessionManager, pool *pgxpool.Pool, queries *sqlcgen.Queries, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		userID, tenantID, ok := sessionIdentity(sm, ctx)
		if !ok {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		user, err := queries.GetUserByID(ctx, userID)
		if err != nil || user.TenantID != tenantID {
			http.Error(w, "session refers to missing user", http.StatusUnauthorized)
			return
		}
		// Owner-only, like the tenant export and for the same reason: the
		// binder carries the audit trail, which is every operator's
		// keystrokes.
		if user.Role != sqlcgen.UserRoleOwner {
			http.Error(w, "owner role required", http.StatusForbidden)
			return
		}
		periodID, err := uuid.Parse(r.URL.Query().Get("period_id"))
		if err != nil {
			http.Error(w, "period_id is required", http.StatusBadRequest)
			return
		}

		files, name, err := buildB266Binder(ctx, pool, queries, tenantID, periodID, user, time.Now().UTC())
		if err != nil {
			logger.Error("b266Binder", "err", err)
			// Nothing has been written yet, so this can still be a status
			// code rather than a truncated download.
			http.Error(w, "could not build the binder", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(name))
		zw := zip.NewWriter(w)
		for _, f := range files {
			fw, err := zw.Create(f.name)
			if err != nil {
				logger.Error("b266Binder: zip entry", "file", f.name, "err", err)
				return
			}
			if _, err := fw.Write(f.body); err != nil {
				logger.Error("b266Binder: write", "file", f.name, "err", err)
				return
			}
		}
		if err := zw.Close(); err != nil {
			logger.Error("b266Binder: finalise zip", "err", err)
		}
	})
}

func sessionIdentity(sm *scs.SessionManager, ctx context.Context) (uuid.UUID, uuid.UUID, bool) {
	userID, err := uuid.Parse(sm.GetString(ctx, "user_id"))
	if err != nil {
		return uuid.Nil, uuid.Nil, false
	}
	tenantID, err := uuid.Parse(sm.GetString(ctx, "tenant_id"))
	if err != nil {
		return uuid.Nil, uuid.Nil, false
	}
	return userID, tenantID, true
}

// buildB266Binder assembles the whole bundle in memory.
func buildB266Binder(
	ctx context.Context,
	pool *pgxpool.Pool,
	queries *sqlcgen.Queries,
	tenantID, periodID uuid.UUID,
	by sqlcgen.User,
	generatedAt time.Time,
) ([]binderFile, string, error) {
	tenant, err := queries.GetTenantByID(ctx, tenantID)
	if err != nil {
		return nil, "", fmt.Errorf("read tenant: %w", err)
	}

	var (
		period    sqlcgen.B266Period
		schedules []binderFile
		counts    = map[string]int{}
	)
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			"SELECT set_config('app.current_tenant_id', $1, true)", tenantID.String()); err != nil {
			return fmt.Errorf("set tenant context: %w", err)
		}
		row := tx.QueryRow(ctx, `
			SELECT id, tenant_id, period_start, period_end, status, snapshot,
			       submitted_at, submitted_by, notes, created_at, updated_at, due_on,
			       filing_acknowledged_at, filing_acknowledged_by, filing_acknowledgement
			FROM b266_periods WHERE id = $1`, periodID)
		if err := row.Scan(&period.ID, &period.TenantID, &period.PeriodStart, &period.PeriodEnd,
			&period.Status, &period.Snapshot, &period.SubmittedAt, &period.SubmittedBy,
			&period.Notes, &period.CreatedAt, &period.UpdatedAt, &period.DueOn,
			&period.FilingAcknowledgedAt, &period.FilingAcknowledgedBy,
			&period.FilingAcknowledgement); err != nil {
			return fmt.Errorf("read period: %w", err)
		}

		// The period bounds every schedule. End is exclusive — the day
		// after the period's last day — so a movement dated on the final
		// day is inside it.
		start := period.PeriodStart.Time
		end := period.PeriodEnd.Time.AddDate(0, 0, 1)

		for _, t := range binderTables {
			args := []any{start, end}
			if t.standing {
				args = nil
			}
			body, n, err := dumpQueryToCSV(ctx, tx, t.sql, args...)
			if err != nil {
				return fmt.Errorf("%s: %w", t.file, err)
			}
			schedules = append(schedules, binderFile{name: t.file, body: body})
			counts[t.file] = n
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}

	report, fromSnapshot := binderReport(period)

	files := []binderFile{
		{name: "01-return.csv", body: returnCSV(report)},
	}
	files = append(files, schedules...)

	doc, err := renderBinderHTML(binderView{
		Tenant:       tenant,
		Period:       period,
		Report:       report,
		FromSnapshot: fromSnapshot,
		Counts:       counts,
		GeneratedAt:  generatedAt,
		GeneratedBy:  by,
	})
	if err != nil {
		return nil, "", fmt.Errorf("render binder: %w", err)
	}
	files = append([]binderFile{{name: "binder.html", body: doc}}, files...)
	files = append([]binderFile{{name: "README.txt", body: binderReadme(period, fromSnapshot, generatedAt, by, counts)}}, files...)

	// The manifest last: it hashes everything above it, so it has to be
	// built after them and cannot hash itself.
	files = append(files, binderFile{name: "manifest.txt", body: binderManifest(files, generatedAt)})

	name := fmt.Sprintf("stillhouse-b266-binder-%s-to-%s.zip",
		period.PeriodStart.Time.Format("2006-01-02"), period.PeriodEnd.Time.Format("2006-01-02"))
	return files, name, nil
}

// binderReport returns the figures the binder reports, and whether they
// came from the frozen snapshot.
//
// For a submitted period they always do. A binder that recomputed would
// print today's answer under the heading of what was filed — which is the
// single thing an audit binder must never do, and the reason the snapshot
// exists at all.
func binderReport(p sqlcgen.B266Period) (*stillhousev1.B266Report, bool) {
	if len(p.Snapshot) > 0 {
		var snap stillhousev1.B266Report
		if err := protojson.Unmarshal(p.Snapshot, &snap); err == nil {
			return &snap, true
		}
	}
	return nil, false
}

// dumpQueryToCSV runs one schedule and returns its CSV plus the row count.
func dumpQueryToCSV(ctx context.Context, tx pgx.Tx, sql string, args ...any) ([]byte, int, error) {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var buf bytes.Buffer
	cw := csv.NewWriter(&buf)

	header := make([]string, 0, len(rows.FieldDescriptions()))
	for _, fd := range rows.FieldDescriptions() {
		header = append(header, string(fd.Name))
	}
	if err := cw.Write(header); err != nil {
		return nil, 0, err
	}

	n := 0
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, 0, err
		}
		rec := make([]string, len(vals))
		for i, v := range vals {
			rec[i] = csvCell(v)
		}
		if err := cw.Write(rec); err != nil {
			return nil, 0, err
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	cw.Flush()
	return buf.Bytes(), n, cw.Error()
}

// csvCell renders a value the way somebody opening this in a spreadsheet
// in 2032 would want it: dates as ISO, nothing as empty, and no Go syntax
// anywhere.
func csvCell(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case time.Time:
		return t.UTC().Format(time.RFC3339)
	case []byte:
		return string(t)
	case bool:
		if t {
			return "yes"
		}
		return "no"
	case map[string]any:
		// audit_events.metadata. Rendered as compact JSON rather than Go's
		// map formatting, which is not stable and not parseable.
		return jsonCompact(t)
	default:
		return fmt.Sprint(v)
	}
}

func binderManifest(files []binderFile, generatedAt time.Time) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "Stillhouse audit binder — file manifest\n")
	fmt.Fprintf(&b, "Generated %s\n\n", generatedAt.Format(time.RFC3339))
	b.WriteString("SHA-256 of every file in this bundle. Check them with:\n")
	b.WriteString("    sha256sum -c manifest.txt        (ignoring the header lines)\n\n")
	for _, f := range files {
		sum := sha256.Sum256(f.body)
		fmt.Fprintf(&b, "%s  %s\n", hex.EncodeToString(sum[:]), f.name)
	}
	b.WriteString("\nmanifest.txt is not listed: it is what hashes the rest.\n")
	return []byte(b.String())
}
