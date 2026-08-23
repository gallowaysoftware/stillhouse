package webhook

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/secrets"
)

// Worker drains the delivery outbox.
//
// One loop for the whole install, polling rather than listening. Polling
// because the alternative — LISTEN/NOTIFY — would need the notification
// to survive the same crash window the outbox exists to close, and a
// five-second poll is not the bottleneck in a system whose events are
// human-paced.
type Worker struct {
	q      *sqlcgen.Queries
	client *http.Client
	logger *slog.Logger
	// Batch bounds how many deliveries are in flight per tick. Small on
	// purpose: a burst of failures against a slow endpoint should not tie
	// up the loop for everyone else.
	Batch int32
	Tick  time.Duration
}

func NewWorker(q *sqlcgen.Queries, logger *slog.Logger) *Worker {
	return &Worker{q: q, client: Client(), logger: logger, Batch: 20, Tick: 5 * time.Second}
}

// Run blocks until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	t := time.NewTicker(w.Tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n := w.once(ctx); n > 0 {
				w.logger.Debug("webhook deliveries attempted", "n", n)
			}
		}
	}
}

// once claims and attempts one batch. Errors are logged and swallowed:
// the loop must outlive any single bad delivery, and a claimed row is
// already marked as attempted, so a crash mid-batch costs at most one
// attempt rather than the delivery.
func (w *Worker) once(ctx context.Context) int {
	rows, err := w.q.ClaimDueWebhookDeliveries(ctx, w.Batch)
	if err != nil {
		w.logger.Error("webhook: claim", "err", err)
		return 0
	}
	for _, r := range rows {
		w.attempt(ctx, r)
	}
	return len(rows)
}

func (w *Worker) attempt(ctx context.Context, r sqlcgen.ClaimDueWebhookDeliveriesRow) {
	secret, err := secrets.Open(r.SecretSealed)
	if err != nil {
		// An unsealable secret is not retryable: the key changed or the
		// row is corrupt, and every further attempt fails identically.
		// Recorded as failed with the reason rather than burning the
		// retry budget to arrive at the same place.
		w.record(ctx, r.ID, Result{Err: "cannot unseal endpoint secret: " + err.Error()}, 0)
		return
	}
	body, err := json.Marshal(envelope{
		ID:         r.ID.String(),
		Kind:       r.Kind,
		OccurredAt: time.Now().UTC().Format(time.RFC3339),
		Attempt:    r.Attempts,
		Data:       json.RawMessage(r.Payload),
	})
	if err != nil {
		w.record(ctx, r.ID, Result{Err: "cannot marshal payload: " + err.Error()}, 0)
		return
	}

	res := Deliver(ctx, w.client, r.Url, secret, body, r.Kind, r.ID.String())
	// r.Attempts is the count INCLUDING the attempt just made, because
	// the claim incremented it. Backoff answers both questions at once:
	// whether a further attempt is allowed, and how long to wait before
	// it. Passing the real count is what makes the wait grow — an earlier
	// draft passed a constant here and every retry waited 30 seconds,
	// which is a retry storm wearing a backoff's name.
	delay, retry := Backoff(r.Attempts)
	if res.OK || !retry {
		w.record(ctx, r.ID, res, 0)
		return
	}
	w.record(ctx, r.ID, res, delay)
}

// record writes the outcome. A zero retryIn means do not retry — either
// it succeeded or the attempts are spent — which the SQL turns into
// delivered or failed respectively.
func (w *Worker) record(ctx context.Context, id uuid.UUID, res Result, retryIn time.Duration) {
	arg := sqlcgen.RecordWebhookResultParams{
		ID:   id,
		Ok:   res.OK,
		Code: int32(res.StatusCode),
		Err:  res.Err,
	}
	if retryIn > 0 {
		arg.RetryAfter = pgtype.Interval{Valid: true, Microseconds: int64(retryIn / time.Microsecond)}
	}
	if err := w.q.RecordWebhookResult(ctx, arg); err != nil {
		w.logger.Error("webhook: record result", "err", err, "delivery", id)
	}
}

// envelope is the body every webhook carries. Versioned by shape rather
// than by a version field: adding a field is compatible, and anything
// that is not would be a new event kind.
type envelope struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`
	OccurredAt string          `json:"occurred_at"`
	Attempt    int32           `json:"attempt"`
	Data       json.RawMessage `json:"data"`
}
