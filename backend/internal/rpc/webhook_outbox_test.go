package rpc

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/secrets"
	"github.com/gallowaysoftware/stillhouse/backend/internal/testdb"
)

// The outbox claim, tested rather than asserted in a comment: a delivery
// row is written in the same transaction as the event, so an event that
// rolls back cannot leave a webhook saying it happened.
//
// Needs STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.

func seedEndpoint(t *testing.T, f *dutyFixture, kinds ...string) uuid.UUID {
	t.Helper()
	sealed, err := secrets.Seal([]byte("test-signing-secret"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	var id uuid.UUID
	if err := f.db.WithTenantTx(f.ctx, f.tenant.ID, func(ctx context.Context, q *sqlcgen.Queries) error {
		r, e := q.CreateWebhookEndpoint(ctx, sqlcgen.CreateWebhookEndpointParams{
			TenantID: f.tenant.ID, Url: "https://example.com/hook",
			SecretSealed: sealed, Kinds: kinds, Description: "test",
		})
		id = r.ID
		return e
	}); err != nil {
		t.Fatalf("create endpoint: %v", err)
	}
	return id
}

func deliveryCount(t *testing.T, f *dutyFixture) int {
	t.Helper()
	var n int
	if err := testdb.AdminPool(t).QueryRow(f.ctx,
		`SELECT count(*) FROM webhook_deliveries WHERE tenant_id = $1`, f.tenant.ID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// Submitting a return enqueues one delivery per subscribed endpoint, and
// none for an endpoint that did not subscribe to that kind.
func TestWebhookOutbox_SubmitEnqueuesForSubscribersOnly(t *testing.T) {
	withSecretKey(t)
	f := newDutyFixture(t)
	b266 := NewB266Service(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	seedEndpoint(t, f, string(sqlcgen.WebhookEventKindB266PeriodSubmitted))
	// A second endpoint subscribed to something else entirely.
	sealed, _ := secrets.Seal([]byte("other"))
	if err := f.db.WithTenantTx(f.ctx, f.tenant.ID, func(ctx context.Context, q *sqlcgen.Queries) error {
		_, e := q.CreateWebhookEndpoint(ctx, sqlcgen.CreateWebhookEndpointParams{
			TenantID: f.tenant.ID, Url: "https://example.com/other",
			SecretSealed: sealed, Kinds: []string{string(sqlcgen.WebhookEventKindLossRecorded)},
		})
		return e
	}); err != nil {
		t.Fatalf("second endpoint: %v", err)
	}

	if n := deliveryCount(t, f); n != 0 {
		t.Fatalf("deliveries before: %d", n)
	}

	gen, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: "2026-04-01", PeriodEnd: "2026-04-30",
	}))
	if err != nil {
		t.Fatalf("GenerateB266: %v", err)
	}
	if _, err := b266.SubmitB266(f.ctx, connect.NewRequest(&stillhousev1.SubmitB266Request{
		PeriodId:        gen.Msg.GetPeriod().GetId(),
		Acknowledgement: filingAcknowledgementText(),
	})); err != nil {
		t.Fatalf("SubmitB266: %v", err)
	}

	if n := deliveryCount(t, f); n != 1 {
		t.Errorf("deliveries after submit: got %d, want 1 — the loss-only endpoint must not be queued", n)
	}
}

// The transactional claim. A submit that fails must leave no delivery
// behind: a webhook announcing a filing that did not happen cannot be
// retracted, and the receiver has no way to find out otherwise.
func TestWebhookOutbox_FailedSubmitEnqueuesNothing(t *testing.T) {
	withSecretKey(t)
	f := newDutyFixture(t)
	b266 := NewB266Service(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	seedEndpoint(t, f, string(sqlcgen.WebhookEventKindB266PeriodSubmitted))

	gen, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: "2026-04-01", PeriodEnd: "2026-04-30",
	}))
	if err != nil {
		t.Fatalf("GenerateB266: %v", err)
	}
	// Submit once, successfully.
	if _, err := b266.SubmitB266(f.ctx, connect.NewRequest(&stillhousev1.SubmitB266Request{
		PeriodId:        gen.Msg.GetPeriod().GetId(),
		Acknowledgement: filingAcknowledgementText(),
	})); err != nil {
		t.Fatalf("first SubmitB266: %v", err)
	}
	after := deliveryCount(t, f)

	// Submit again: refused, because the period is already submitted. The
	// refusal happens inside the transaction, after the outbox row would
	// have been written on the happy path.
	if _, err := b266.SubmitB266(f.ctx, connect.NewRequest(&stillhousev1.SubmitB266Request{
		PeriodId:        gen.Msg.GetPeriod().GetId(),
		Acknowledgement: filingAcknowledgementText(),
	})); err == nil {
		t.Fatal("second submit succeeded")
	}
	if n := deliveryCount(t, f); n != after {
		t.Errorf("a refused submit enqueued a delivery: %d → %d", after, n)
	}
}

// An endpoint that is disabled is not queued for. Otherwise disabling one
// would silently build a backlog that fires the moment it is re-enabled.
func TestWebhookOutbox_DisabledEndpointIsNotQueued(t *testing.T) {
	withSecretKey(t)
	f := newDutyFixture(t)
	b266 := NewB266Service(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	id := seedEndpoint(t, f, string(sqlcgen.WebhookEventKindB266PeriodSubmitted))

	if err := f.db.WithTenantTx(f.ctx, f.tenant.ID, func(ctx context.Context, q *sqlcgen.Queries) error {
		_, e := q.SetWebhookEndpointEnabled(ctx, sqlcgen.SetWebhookEndpointEnabledParams{
			ID: id, Enabled: false,
		})
		return e
	}); err != nil {
		t.Fatalf("disable: %v", err)
	}

	gen, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: "2026-04-01", PeriodEnd: "2026-04-30",
	}))
	if err != nil {
		t.Fatalf("GenerateB266: %v", err)
	}
	if _, err := b266.SubmitB266(f.ctx, connect.NewRequest(&stillhousev1.SubmitB266Request{
		PeriodId:        gen.Msg.GetPeriod().GetId(),
		Acknowledgement: filingAcknowledgementText(),
	})); err != nil {
		t.Fatalf("SubmitB266: %v", err)
	}
	if n := deliveryCount(t, f); n != 0 {
		t.Errorf("a disabled endpoint was queued for: %d", n)
	}
}
