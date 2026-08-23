// Command mcp-token is a recovery / bootstrap path for issuing personal
// access tokens directly from the database. The normal way to manage
// tokens is the web UI at /settings/api-tokens. This binary exists for
// the cases where the UI isn't available: first-run admin setup before
// anyone has logged in, or recovery if all admin tokens have been
// revoked and the operator needs to issue a new one without a session.
//
// The plaintext token is shown ONCE — only the SHA-256 hash is stored.
// Usage:
//
//	make mcp-token EMAIL=kyle@example.com NAME="phone"
//
// Connects with the admin DSN so it can write through api_tokens'
// GRANTs. Falls back to DATABASE_URL when ADMIN_DATABASE_URL is unset.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/rpc"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

func main() {
	email := flag.String("email", "", "email of the user to issue the token for")
	name := flag.String("name", "mcp", "human label for the token (shown when listing)")
	tenant := flag.String("tenant", "", "tenant id, when the email holds an account at more than one distillery")
	flag.Parse()
	if *email == "" {
		log.Fatal("--email is required")
	}

	dsn := os.Getenv("ADMIN_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		log.Fatal("ADMIN_DATABASE_URL or DATABASE_URL must be set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	q := sqlcgen.New(pool)
	// An email address is unique per tenant, not per install, so it can
	// name accounts at more than one distillery. A recovery CLI must not
	// guess which — it names them and stops.
	users, err := q.ListUsersByEmail(ctx, *email)
	if err != nil {
		log.Fatalf("get user: %v", err)
	}
	if *tenant != "" {
		filtered := users[:0]
		for _, u := range users {
			if u.TenantID.String() == *tenant {
				filtered = append(filtered, u)
			}
		}
		users = filtered
	}
	switch {
	case len(users) == 0 && *tenant != "":
		log.Fatalf("no user with email %q at tenant %s", *email, *tenant)
	case len(users) == 0:
		log.Fatalf("no user with email %q", *email)
	case len(users) > 1:
		fmt.Fprintf(os.Stderr, "%q has an account at %d distilleries:\n", *email, len(users))
		for _, u := range users {
			label := u.TenantID.String()
			if t, err := q.GetTenantByID(ctx, u.TenantID); err == nil {
				label = fmt.Sprintf("%s  %s", u.TenantID, t.Name)
			}
			fmt.Fprintf(os.Stderr, "  --tenant %s\n", label)
		}
		log.Fatal("pass --tenant to say which")
	}
	u := users[0]

	// api_tokens is under row-level security as of migration 000033, so
	// the insert needs a tenant context even here. Going through
	// WithTenantTx makes this binary correct against either DSN: the
	// admin one (superuser, RLS bypassed anyway) and the app one.
	tok, hash := newToken()
	if err := tenantdb.New(pool).WithTenantTx(ctx, u.TenantID,
		func(ctx context.Context, q *sqlcgen.Queries) error {
			_, err := q.CreateAPIToken(ctx, sqlcgen.CreateAPITokenParams{
				TokenHash: hash,
				TenantID:  u.TenantID,
				UserID:    u.ID,
				Name:      *name,
			})
			return err
		}); err != nil {
		log.Fatalf("insert token: %v", err)
	}

	fmt.Printf("Issued token %q for %s\n", *name, u.Email)
	fmt.Println()
	fmt.Println("*** Personal access token (shown once) ***")
	fmt.Println(tok)
	fmt.Println()
	fmt.Println("Use it with:  Authorization: Bearer " + tok)
}

// newToken returns (plaintext, sha256-of-plaintext). 32 bytes of
// randomness encoded as URL-safe base64 → 43 chars + the "sh_" prefix.
func newToken() (string, []byte) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("rand: %v", err)
	}
	tok := rpc.APITokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(tok))
	return tok, sum[:]
}
