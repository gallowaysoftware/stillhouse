package rpc

import (
	"errors"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgconn"
)

// pgFKViolation is the SQLSTATE code for foreign_key_violation. Returned
// when an INSERT/UPDATE references a parent row that doesn't exist —
// most commonly an LLM/operator guessing a UUID that isn't in the DB.
const pgFKViolation = "23503"

// pgUniqueViolation is the SQLSTATE code for unique_violation. These are
// almost always a real rule being enforced — "you already charged that
// fermenter into this run" — not a server fault, so they must not fall
// through to a 500 that tells the operator nothing.
const pgUniqueViolation = "23505"

// uniqueViolationMessages turns a constraint name into something an
// operator can act on. Postgres truncates generated constraint names to 63
// characters, which is why some of these look clipped — match on what the
// server actually reports.
//
// A constraint that isn't listed still gets a better answer than "internal
// error": it comes back as a conflict rather than a fault.
var uniqueViolationMessages = map[string]string{
	"distillation_charges_distillation_run_id_fermentation_run_i_key": "that fermentation run is already charged into this distillation — edit the existing charge rather than adding a second",
	"distillation_runs_tenant_id_run_no_key":                          "a distillation run with that number already exists",
	"materials_tenant_id_name_key":                                    "a material with that name already exists",
	"bulk_containers_tenant_id_name_key":                              "a container with that name already exists",
}

// classifyWriteErr maps low-level pg errors that happen during writes
// to user-actionable Connect codes, so the caller sees "your id is
// bad" instead of an opaque "internal error". Returns nil if err is
// nil; passes through err unchanged if it's already a typed connect
// error; otherwise inspects the underlying pg error code.
//
// notFoundMsg is the human-readable message to attach when the
// underlying problem is a missing parent row — usually something like
// "fermentation run not found".
func classifyWriteErr(err error, notFoundMsg string) error {
	if err == nil {
		return nil
	}
	var ce *connect.Error
	if errors.As(err, &ce) {
		return ce
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return nil // signal "not a pg error — caller should fall back to Internal"
	}
	switch pgErr.Code {
	case pgFKViolation:
		return connect.NewError(connect.CodeNotFound, errors.New(notFoundMsg))
	case pgUniqueViolation:
		msg, ok := uniqueViolationMessages[pgErr.ConstraintName]
		if !ok {
			msg = "that record already exists"
		}
		return connect.NewError(connect.CodeAlreadyExists, errors.New(msg))
	}
	return nil // caller should fall back to Internal
}
