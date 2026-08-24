package mcp

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	pb "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/rpc"
)

// review_filing answers "can I file yet?" at the still. The thing that
// makes it safe to answer there is that it only reads: generating a B266
// writes a draft period, and B266 generation is a back-office operation
// that stays in the web UI. A tool that created periods as a side effect
// of being asked a question would be the wrong shape however convenient.

// The invariant, stated as a test rather than as a comment: every
// procedure review_filing reaches must be callable by a viewer. A viewer
// is the read-only role, so a procedure a viewer may call is one that
// cannot write.
func TestReviewFilingIsReadOnly(t *testing.T) {
	b, err := os.ReadFile("tools_read.go")
	if err != nil {
		t.Fatalf("read tools_read.go: %v", err)
	}
	src := string(b)
	start := strings.Index(src, "func addReviewFiling(")
	if start < 0 {
		t.Fatal("addReviewFiling not found — did it move?")
	}
	// Bounded by the next top-level func so this reads only the tool.
	rest := src[start+1:]
	if end := strings.Index(rest, "\nfunc "); end >= 0 {
		rest = rest[:end]
	}
	body := src[start:][:len(rest)+1]

	procs := regexp.MustCompile(`guard\(ctx, user, "([^"]+)"\)`).FindAllStringSubmatch(body, -1)
	if len(procs) == 0 {
		t.Fatal("review_filing guards nothing")
	}
	for _, m := range procs {
		if err := rpc.AuthorizeProcedure(m[1], sqlcgen.UserRoleViewer); err != nil {
			t.Errorf("review_filing reaches %s, which a viewer may not call — "+
				"the tool is supposed to read only: %v", m[1], err)
		}
	}

	// The specific thing it must never do, named rather than inferred.
	for _, forbidden := range []string{"GenerateB266", "SubmitB266", "ReopenB266Period", "ClassifyLosses"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("review_filing calls %s — it is a read-only tool", forbidden)
		}
	}
}

// Ordering is the whole point of next_steps. An operator handed five
// things at once does none of them, and the five are neither equally
// urgent nor equally cheap: ruling on losses first avoids generating a
// return that then has to be regenerated.
func TestFilingNextSteps_OrdersTheWork(t *testing.T) {
	o := &filingReviewOutput{
		SuggestedPeriodStart:  "2026-07-01",
		SuggestedPeriodEnd:    "2026-07-31",
		UnclassifiedLossCount: 3,
		UnclassifiedLossLAA:   12.5,
		PeriodExists:          false,
	}
	steps, note := filingNextSteps(o)

	if len(steps) < 2 {
		t.Fatalf("steps: got %d, want at least the losses and the generate: %v", len(steps), steps)
	}
	if !strings.Contains(steps[0], "Rule on 3 losses") {
		t.Errorf("losses must come first and inflect correctly: %q", steps[0])
	}
	if !strings.Contains(steps[1], "Generate the return") {
		t.Errorf("generate must follow the losses: %q", steps[1])
	}
	if !strings.Contains(note, "never files") {
		t.Errorf("note should say Stillhouse does not decide: %q", note)
	}
}

// "loss" takes "es". QA already found a shared plural helper appending it
// to "destruction" and producing "destructiones" on a filing blocker, so
// the singular case is worth an assertion rather than an assumption.
func TestFilingNextSteps_SingularLoss(t *testing.T) {
	steps, _ := filingNextSteps(&filingReviewOutput{
		UnclassifiedLossCount: 1, UnclassifiedLossLAA: 0.5, PeriodExists: true,
	})
	if len(steps) == 0 || !strings.Contains(steps[0], "Rule on 1 loss ") {
		t.Errorf("singular: %v", steps)
	}
	if len(steps) > 0 && strings.Contains(steps[0], "losses") {
		t.Errorf("pluralised a single loss: %q", steps[0])
	}
}

// Nothing outstanding must not read as a green light. Stillhouse never
// files, and a clean list means nothing is missing — not that the figures
// are right. Those are different claims and only one of them is true.
func TestFilingNextSteps_CleanIsNotAGreenLight(t *testing.T) {
	steps, note := filingNextSteps(&filingReviewOutput{
		PeriodExists: true,
		PeriodStatus: pb.B266Status_B266_STATUS_DRAFT.String(),
	})
	if len(steps) != 0 {
		t.Fatalf("expected no steps: %v", steps)
	}
	if !strings.Contains(note, "not a promise the figures are right") {
		t.Errorf("a clean review must not read as a green light: %q", note)
	}
}

// A submitted period is done, and saying "nothing outstanding" about it
// would invite filing it twice.
func TestFilingNextSteps_SubmittedSaysSo(t *testing.T) {
	_, note := filingNextSteps(&filingReviewOutput{
		PeriodExists: true,
		PeriodStatus: pb.B266Status_B266_STATUS_SUBMITTED.String(),
	})
	if !strings.Contains(note, "Already submitted") {
		t.Errorf("note: %q", note)
	}
}

// The blockers computed on the return — continuity breaks included — are
// the operator's actual work list, so they have to reach next_steps rather
// than being summarised away.
func TestFilingNextSteps_BlockersReachTheList(t *testing.T) {
	blocker := "Bulk opening balance is 130.0000 but the return filed for 2026-04-01 to 2026-04-30 closed at 100.0000"
	steps, _ := filingNextSteps(&filingReviewOutput{
		PeriodExists:   true,
		FilingBlockers: []string{blocker},
	})
	var found bool
	for _, s := range steps {
		if s == blocker {
			found = true
		}
	}
	if !found {
		t.Errorf("filing blockers did not reach next_steps: %v", steps)
	}
}

// PLAN J4's work-order flow. The status mapping is where a chat surface
// can do real damage: an unrecognised word must not quietly become
// "planned", which would move a finished job backwards on somebody's
// board.
func TestWorkOrderStatusFromName(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want pb.WorkOrderStatus
	}{
		{"planned", pb.WorkOrderStatus_WORK_ORDER_STATUS_PLANNED},
		{"in_progress", pb.WorkOrderStatus_WORK_ORDER_STATUS_IN_PROGRESS},
		{"in progress", pb.WorkOrderStatus_WORK_ORDER_STATUS_IN_PROGRESS},
		{"started", pb.WorkOrderStatus_WORK_ORDER_STATUS_IN_PROGRESS},
		{"  DONE ", pb.WorkOrderStatus_WORK_ORDER_STATUS_DONE},
		{"finished", pb.WorkOrderStatus_WORK_ORDER_STATUS_DONE},
		{"cancelled", pb.WorkOrderStatus_WORK_ORDER_STATUS_CANCELLED},
		{"canceled", pb.WorkOrderStatus_WORK_ORDER_STATUS_CANCELLED},
	} {
		got, err := workOrderStatusFromName(tc.in)
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%q: got %v, want %v", tc.in, got, tc.want)
		}
	}

	// Anything else is an error, not a default. A model that says
	// "paused" must be told, not silently understood as planned.
	for _, bad := range []string{"", "paused", "on hold", "PLANNE", "unspecified"} {
		got, err := workOrderStatusFromName(bad)
		if err == nil {
			t.Errorf("%q was accepted as %v", bad, got)
		}
		if got != pb.WorkOrderStatus_WORK_ORDER_STATUS_UNSPECIFIED {
			t.Errorf("%q returned %v alongside its error", bad, got)
		}
	}
}

// The board read is a read and the status change is a write, and the role
// gate has to see them that way — a viewer must not be able to move
// somebody's job.
func TestWorkOrderToolsAreClassifiedCorrectly(t *testing.T) {
	if err := rpc.AuthorizeProcedure(
		"/stillhouse.v1.WorkOrderService/ListWorkOrders", sqlcgen.UserRoleViewer); err != nil {
		t.Errorf("a viewer cannot read the board: %v", err)
	}
	if err := rpc.AuthorizeProcedure(
		"/stillhouse.v1.WorkOrderService/SetWorkOrderStatus", sqlcgen.UserRoleViewer); err == nil {
		t.Error("a viewer can move a job along — that is a write")
	}
	if err := rpc.AuthorizeProcedure(
		"/stillhouse.v1.WorkOrderService/SetWorkOrderStatus", sqlcgen.UserRoleOperator); err != nil {
		t.Errorf("an operator cannot move a job along: %v", err)
	}
	// Raising a job stays off this surface: five fields a form asks at
	// once and a chat asks one at a time, badly.
	b, err := os.ReadFile("tools_write.go")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// The call, not the word: this file's own comments explain why
	// SaveWorkOrder is absent, and a substring check reads those as the
	// thing they are warning about.
	if strings.Contains(string(b), "d.WorkOrder.SaveWorkOrder(") ||
		strings.Contains(string(b), `guard(ctx, user, "/stillhouse.v1.WorkOrderService/SaveWorkOrder")`) {
		t.Error("SaveWorkOrder reached the MCP surface — multi-field creation belongs in the web UI")
	}
}
