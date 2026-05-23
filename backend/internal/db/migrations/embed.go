// Package migrations exposes the .sql migration files as an embed.FS so
// production builds can apply them at startup without shipping the files
// alongside the binary. Source of truth is still the .sql files in this
// directory — go:embed picks them up at compile time.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
