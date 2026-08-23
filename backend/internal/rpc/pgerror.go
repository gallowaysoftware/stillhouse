package rpc

import (
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgconn"
)

// uniqueViolation turns Postgres's 23505 into something an operator can
// act on.
//
// Found by QA: creating a second container called "Tank 1" answered
// `internal error` with a 500. The constraint knew exactly what was
// wrong; the handler threw that away and logged it somewhere the operator
// cannot see. A name collision is the single most likely way any of these
// forms fails, and it is entirely the caller's to fix.
//
// The subject is supplied by the caller because a constraint name is not
// a sentence: bulk_containers_tenant_id_name_key is accurate and useless.
func uniqueViolation(err error, subject string) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return nil
	}
	what := "one of its details"
	switch {
	case strings.Contains(pgErr.ConstraintName, "_name_"),
		strings.HasSuffix(pgErr.ConstraintName, "_name_key"):
		what = "that name"
	case strings.Contains(pgErr.ConstraintName, "lot_code"):
		what = "that lot code"
	case strings.Contains(pgErr.ConstraintName, "email"):
		what = "that email address"
	case strings.Contains(pgErr.ConstraintName, "gtin"):
		what = "that GTIN"
	case strings.Contains(pgErr.ConstraintName, "_no_"),
		strings.HasSuffix(pgErr.ConstraintName, "_no_key"):
		what = "that number"
	}
	return connect.NewError(connect.CodeAlreadyExists,
		fmt.Errorf("another %s already has %s", subject, what))
}
