package rpc

import (
	"context"
	"encoding/json"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
)

// enqueueWebhook writes one outbox row per subscribed endpoint.
//
// Call it inside the transaction that performs the event, never after.
// That is the whole point of an outbox: a webhook that announces
// something which then rolls back cannot be retracted, and a webhook
// dispatched after commit has a window where the process dies and the
// event is silently never delivered. Neither failure is visible to the
// receiver, which is what makes them worth this much care.
//
// A failure to enqueue therefore fails the whole operation. That is
// deliberate and is the trade this design makes: an operator would rather
// be told the submit failed and retry it than have it succeed with a
// notification nobody will ever receive.
func enqueueWebhook(ctx context.Context, q *sqlcgen.Queries, kind string, data map[string]any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return q.EnqueueWebhookDelivery(ctx, sqlcgen.EnqueueWebhookDeliveryParams{
		Kind:    kind,
		Payload: payload,
	})
}
