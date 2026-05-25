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
	if errors.As(err, &pgErr) && pgErr.Code == pgFKViolation {
		return connect.NewError(connect.CodeNotFound, errors.New(notFoundMsg))
	}
	return nil // signal "I don't know how to classify this — caller should fall back to Internal"
}
