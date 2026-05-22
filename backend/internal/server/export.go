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
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

// auditExportHandler streams the tenant's audit log as CSV. Session-gated
// (not Connect, so it doesn't use the auth interceptor — does its own
// session check via scs).
//
// Query params:
//
//	entity_type=...  optional filter
func auditExportHandler(sm *scs.SessionManager, tdb *tenantdb.DB, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		tenantIDStr := sm.GetString(ctx, "tenant_id")
		if tenantIDStr == "" {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		tenantID, err := uuid.Parse(tenantIDStr)
		if err != nil {
			http.Error(w, "invalid session", http.StatusUnauthorized)
			return
		}

		var entityType pgtype.Text
		if t := r.URL.Query().Get("entity_type"); t != "" {
			entityType = pgtype.Text{String: t, Valid: true}
		}
		var fromTS, toTS pgtype.Timestamptz
		fromStr := r.URL.Query().Get("from")
		toStr := r.URL.Query().Get("to")
		if fromStr != "" {
			if d, err := time.Parse("2006-01-02", fromStr); err == nil {
				fromTS = pgtype.Timestamptz{Time: d, Valid: true}
			}
		}
		if toStr != "" {
			if d, err := time.Parse("2006-01-02", toStr); err == nil {
				// to is inclusive; bump to next-day exclusive bound.
				toTS = pgtype.Timestamptz{Time: d.AddDate(0, 0, 1), Valid: true}
			}
		}

		filename := fmt.Sprintf("stillhouse-audit-%s.csv", time.Now().UTC().Format("2006-01-02"))
		if fromStr != "" && toStr != "" {
			filename = fmt.Sprintf("stillhouse-audit-%s-to-%s.csv", fromStr, toStr)
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(filename))

		cw := csv.NewWriter(w)
		defer cw.Flush()
		_ = cw.Write([]string{
			"id", "occurred_at", "user_email", "user_display_name",
			"action", "entity_type", "entity_id", "payload",
		})

		const chunk int32 = 500
		offset := int32(0)
		for {
			var rows []sqlcgen.ListAuditEventsRow
			err := tdb.WithTenantTx(ctx, tenantID, func(c context.Context, q *sqlcgen.Queries) error {
				var e error
				rows, e = q.ListAuditEvents(c, sqlcgen.ListAuditEventsParams{
					EntityType: entityType,
					FromTs:     fromTS,
					ToTs:       toTS,
					Limit:      chunk,
					Offset:     offset,
				})
				return e
			})
			if err != nil {
				logger.Error("auditExport", "err", err)
				return
			}
			for _, row := range rows {
				_ = cw.Write([]string{
					row.ID.String(),
					row.OccurredAt.Time.Format(time.RFC3339),
					nullableText(row.UserEmail),
					nullableText(row.UserDisplayName),
					string(row.Action),
					row.EntityType,
					row.EntityID,
					string(row.Payload),
				})
			}
			cw.Flush()
			if int32(len(rows)) < chunk {
				return
			}
			offset += chunk
		}
	})
}

func nullableText(t pgtype.Text) string {
	if t.Valid {
		return t.String
	}
	return ""
}
