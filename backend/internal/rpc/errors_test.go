package rpc

import (
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgconn"
)

// TestClassifyWriteErr covers the mapping from raw Postgres failures to
// answers an operator can act on. The alternative — everything becomes
// "internal error" — is what made a repeated fermenter charge look like a
// server fault.
func TestClassifyWriteErr(t *testing.T) {
	t.Run("nil stays nil", func(t *testing.T) {
		if got := classifyWriteErr(nil, "nope"); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})

	t.Run("an already-typed connect error passes through", func(t *testing.T) {
		want := connect.NewError(connect.CodeFailedPrecondition, errors.New("barrel is empty"))
		got := classifyWriteErr(want, "nope")
		var ce *connect.Error
		if !errors.As(got, &ce) || ce.Code() != connect.CodeFailedPrecondition {
			t.Errorf("got %v, want the original failed_precondition", got)
		}
	})

	t.Run("a foreign key violation is a missing parent", func(t *testing.T) {
		got := classifyWriteErr(&pgconn.PgError{Code: pgFKViolation}, "fermentation run not found")
		var ce *connect.Error
		if !errors.As(got, &ce) {
			t.Fatalf("got %T, want *connect.Error", got)
		}
		if ce.Code() != connect.CodeNotFound {
			t.Errorf("code = %v, want not_found", ce.Code())
		}
		if ce.Message() != "fermentation run not found" {
			t.Errorf("message = %q, want the caller's", ce.Message())
		}
	})

	t.Run("a known unique violation explains itself", func(t *testing.T) {
		got := classifyWriteErr(&pgconn.PgError{
			Code:           pgUniqueViolation,
			ConstraintName: "distillation_charges_distillation_run_id_fermentation_run_i_key",
		}, "not found")
		var ce *connect.Error
		if !errors.As(got, &ce) {
			t.Fatalf("got %T, want *connect.Error", got)
		}
		if ce.Code() != connect.CodeAlreadyExists {
			t.Errorf("code = %v, want already_exists", ce.Code())
		}
		if ce.Message() == "that record already exists" {
			t.Error("a known constraint should give its specific message, not the fallback")
		}
	})

	t.Run("an unknown unique violation is still a conflict", func(t *testing.T) {
		got := classifyWriteErr(&pgconn.PgError{
			Code:           pgUniqueViolation,
			ConstraintName: "some_constraint_nobody_mapped",
		}, "not found")
		var ce *connect.Error
		if !errors.As(got, &ce) || ce.Code() != connect.CodeAlreadyExists {
			t.Errorf("got %v, want already_exists rather than a fall-through to 500", got)
		}
	})

	t.Run("an unrecognised failure falls through to the caller", func(t *testing.T) {
		if got := classifyWriteErr(errors.New("connection reset"), "nope"); got != nil {
			t.Errorf("got %v, want nil so the caller logs and returns Internal", got)
		}
		if got := classifyWriteErr(&pgconn.PgError{Code: "40001"}, "nope"); got != nil {
			t.Errorf("serialization failure should fall through, got %v", got)
		}
	})
}
