package rpc

import (
	"log/slog"
	"os"
	"strings"
	"testing"

	"connectrpc.com/connect"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// PLAN H1. Stage 104 got the hard part right: Stillhouse never submits to
// CRA and says so on the screen. What was missing was the step in between
// — a moment where a named person says they have checked the figures
// against their own records, recorded with the date and the wording.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.

func newAckFixture(t *testing.T) (*dutyFixture, *B266Service) {
	t.Helper()
	f := newDutyFixture(t)
	return f, NewB266Service(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
}

func generatePeriod(t *testing.T, f *dutyFixture, b266 *B266Service) string {
	t.Helper()
	gen, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: "2026-06-01", PeriodEnd: "2026-06-30",
	}))
	if err != nil {
		t.Fatalf("GenerateB266: %v", err)
	}
	return gen.Msg.GetPeriod().GetId()
}

// A period cannot be marked submitted without somebody confirming they
// checked the figures. This is the whole item.
func TestSubmitRefusesWithoutAnAcknowledgement(t *testing.T) {
	f, b266 := newAckFixture(t)
	id := generatePeriod(t, f, b266)

	_, err := b266.SubmitB266(f.ctx, connect.NewRequest(&stillhousev1.SubmitB266Request{
		PeriodId: id,
	}))
	if err == nil {
		t.Fatal("a period was marked submitted with nobody confirming the figures")
	}
	if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want invalid_argument (err: %v)", got, err)
	}
	// And nothing moved: the period is still a draft.
	got, err := b266.GetB266Period(f.ctx, connect.NewRequest(&stillhousev1.GetB266PeriodRequest{Id: id}))
	if err != nil {
		t.Fatalf("GetB266Period: %v", err)
	}
	if s := got.Msg.GetPeriod().GetStatus(); s != stillhousev1.B266Status_B266_STATUS_DRAFT {
		t.Errorf("status after a refused submit: got %v, want draft", s)
	}
}

// A confirmation to text the person never saw is not a confirmation.
func TestSubmitRefusesAnAcknowledgementThatIsNotOurs(t *testing.T) {
	f, b266 := newAckFixture(t)
	id := generatePeriod(t, f, b266)

	_, err := b266.SubmitB266(f.ctx, connect.NewRequest(&stillhousev1.SubmitB266Request{
		PeriodId: id, Acknowledgement: "yeah fine whatever",
	}))
	if err == nil {
		t.Fatal("a period was submitted against wording this server never served")
	}
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Errorf("code = %v, want failed_precondition (err: %v)", got, err)
	}
}

// The wording is served by the server, stored on the period, and comes
// back with the name of whoever agreed to it — because this row exists to
// be read years later, when the release that displayed it is long gone.
func TestTheAcknowledgementIsRecordedWithItsWordingAndItsAuthor(t *testing.T) {
	f, b266 := newAckFixture(t)
	id := generatePeriod(t, f, b266)

	statements, err := b266.GetFilingAcknowledgement(f.ctx,
		connect.NewRequest(&stillhousev1.FilingAcknowledgementRequest{}))
	if err != nil {
		t.Fatalf("GetFilingAcknowledgement: %v", err)
	}
	if len(statements.Msg.GetStatements()) < 3 {
		t.Errorf("got %d statements, want at least three separate claims",
			len(statements.Msg.GetStatements()))
	}
	// Each one has to be something a person could disagree with, not
	// throat-clearing. The three claims that matter:
	joined := strings.Join(statements.Msg.GetStatements(), " ")
	for _, want := range []string{"checked these figures", "does not file", "responsible"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the statements do not say %q: %q", want, joined)
		}
	}

	if _, err := b266.SubmitB266(f.ctx, connect.NewRequest(&stillhousev1.SubmitB266Request{
		PeriodId: id, Acknowledgement: statements.Msg.GetAcknowledgementText(),
	})); err != nil {
		t.Fatalf("SubmitB266: %v", err)
	}

	got, err := b266.GetB266Period(f.ctx, connect.NewRequest(&stillhousev1.GetB266PeriodRequest{Id: id}))
	if err != nil {
		t.Fatalf("GetB266Period: %v", err)
	}
	p := got.Msg.GetPeriod()
	if p.GetFilingAcknowledgement() != statements.Msg.GetAcknowledgementText() {
		t.Errorf("stored wording:\n got %q\nwant %q",
			p.GetFilingAcknowledgement(), statements.Msg.GetAcknowledgementText())
	}
	if p.GetFilingAcknowledgedAt() == nil {
		t.Error("the acknowledgement has no date")
	}
	if p.GetFilingAcknowledgedBy() != f.user.ID.String() {
		t.Errorf("acknowledged by: got %q, want %q", p.GetFilingAcknowledgedBy(), f.user.ID.String())
	}
	if p.GetFilingAcknowledgedByName() == "" {
		t.Error("the period does not name who confirmed the figures")
	}
}

// The database holds the same guarantee the handler does, for any path
// that is not the handler. A submitted period without an acknowledgement
// must be impossible to write at all.
func TestTheDatabaseRefusesASubmittedPeriodWithNoAcknowledgement(t *testing.T) {
	f, b266 := newAckFixture(t)
	id := generatePeriod(t, f, b266)

	_, err := f.pool.Exec(f.ctx, `
		UPDATE b266_periods
		SET status = 'submitted', submitted_at = NOW(), submitted_by = $2
		WHERE id = $1`, id, f.user.ID)
	if err == nil {
		t.Fatal("a submitted period was written with no acknowledgement, bypassing the handler")
	}
	if !strings.Contains(err.Error(), "acknowledgement_is_complete") {
		t.Errorf("refused for the wrong reason: %v", err)
	}
}

// The wording stored on the period is derived from the statements shown,
// not written twice — so the two cannot drift into saying different
// things.
func TestTheStoredWordingIsTheWordingShown(t *testing.T) {
	text := filingAcknowledgementText()
	for _, s := range filingAcknowledgementStatements {
		if !strings.Contains(text, s) {
			t.Errorf("a statement shown to the operator is not in the stored wording: %q", s)
		}
	}
	if err := checkFilingAcknowledgement(text); err != nil {
		t.Errorf("the server's own wording was refused: %v", err)
	}
	// Whitespace is forgiven; different text is not.
	if err := checkFilingAcknowledgement("  " + text + "  "); err != nil {
		t.Errorf("padded wording was refused: %v", err)
	}
	if err := checkFilingAcknowledgement(text + " and also I accept no blame"); err == nil {
		t.Error("wording with something appended was accepted")
	}
}
