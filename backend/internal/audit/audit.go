// Package audit centralizes writes to the audit_events table. Every
// state-changing RPC handler should call audit.Write inside the same
// tenant tx as the change itself, so the audit log and the change either
// both commit or neither does. This is the s.206 (Excise Act, 2001)
// daily-records expectation: every meaningful action captured with actor
// + timestamp + before/after payload.
package audit

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
)

// Write inserts an audit event. payload is marshalled to JSON. If a
// user_id is zero-valued, it's stored as NULL (system events). Errors
// are returned so the caller can fail the surrounding tx.
func Write(
	ctx context.Context,
	q *sqlcgen.Queries,
	tenantID uuid.UUID,
	userID uuid.UUID,
	entityType string,
	entityID string,
	action sqlcgen.AuditAction,
	payload any,
) error {
	var payloadBytes []byte
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		payloadBytes = b
	}
	user := uuid.NullUUID{Valid: false}
	if userID != uuid.Nil {
		user = uuid.NullUUID{UUID: userID, Valid: true}
	}
	_, err := q.InsertAuditEvent(ctx, sqlcgen.InsertAuditEventParams{
		TenantID:   tenantID,
		UserID:     user,
		EntityType: entityType,
		EntityID:   entityID,
		Action:     action,
		Payload:    payloadBytes,
	})
	return err
}
