package rpc

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// The step between "here are your figures" and a filed return.
//
// Stage 104 got the hard part right: Stillhouse never submits to CRA and
// says so on the screen. What was missing was a moment where a named
// person says they have checked the figures against their own records —
// recorded, dated, and kept.
//
// The wording lives here rather than in the client, so that what is
// recorded against a period is what was actually shown. The client sends
// back the text it displayed and the server refuses if it does not match:
// a confirmation to text the person never saw is not a confirmation.

// filingAcknowledgementStatements are shown as separate lines, each one
// its own claim. Written as sentences a person can disagree with — a
// paragraph of legal throat-clearing gets clicked past, and the point of
// this step is that it does not.
var filingAcknowledgementStatements = []string{
	"I have checked these figures against my own records.",
	"I understand Stillhouse does not file anything with CRA, and that marking this period submitted only freezes a snapshot here.",
	"I am filing this return myself, and I remain responsible for what it says.",
}

// filingAcknowledgementText is what gets stored on the period. Derived
// from the statements above rather than written twice, so the two cannot
// drift — and frozen on the row, because a tick box whose wording changed
// in a later release proves nothing about what somebody agreed to two
// years ago.
func filingAcknowledgementText() string {
	return strings.Join(filingAcknowledgementStatements, " ")
}

func (s *B266Service) GetFilingAcknowledgement(
	ctx context.Context,
	_ *connect.Request[stillhousev1.FilingAcknowledgementRequest],
) (*connect.Response[stillhousev1.FilingAcknowledgementResponse], error) {
	if _, ok := CurrentUser(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	return connect.NewResponse(&stillhousev1.FilingAcknowledgementResponse{
		Statements:          filingAcknowledgementStatements,
		AcknowledgementText: filingAcknowledgementText(),
	}), nil
}

// checkFilingAcknowledgement refuses a submit whose acknowledgement is
// missing or is not the text the server serves.
func checkFilingAcknowledgement(got string) error {
	got = strings.TrimSpace(got)
	if got == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New(
			"a period cannot be marked submitted without confirming you have checked the figures"))
	}
	if got != filingAcknowledgementText() {
		// Almost certainly a client running against a newer or older
		// server rather than anything deliberate, but either way the text
		// on file would not be the text the person read.
		return connect.NewError(connect.CodeFailedPrecondition, errors.New(
			"the confirmation text does not match what this server asks for — reload the page and try again"))
	}
	return nil
}
