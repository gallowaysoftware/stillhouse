// Command seed creates a starter tenant + owner user for a fresh install.
//
// Idempotent-ish: if any tenant already exists, seed exits without changes.
// Prints the generated owner password to stdout — capture it.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gallowaysoftware/stillhouse/backend/internal/auth"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
)

func main() {
	// Prefer ADMIN_DATABASE_URL when set — the prod container ships with both
	// (admin = superuser for migrations + seed, app = the limited role the
	// server uses at runtime). Falls back to DATABASE_URL for dev, which is
	// already the superuser DSN there.
	databaseURL := os.Getenv("ADMIN_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("DATABASE_URL")
	}
	if databaseURL == "" {
		databaseURL = "postgres://stillhouse:stillhouse@localhost:5432/stillhouse?sslmode=disable"
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		log.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	q := sqlcgen.New(pool)

	count, err := q.CountTenants(ctx)
	if err != nil {
		log.Fatalf("count tenants: %v", err)
	}
	if count > 0 {
		fmt.Println("a tenant already exists — skipping seed.")
		return
	}

	tenant, err := q.CreateTenant(ctx, sqlcgen.CreateTenantParams{
		Name:                         "Stillhouse Demo Distillery",
		CraSpiritsLicenceNumber:      "DEMO-0001",
		ExciseWarehouseLicenceNumber: pgtype.Text{Valid: false},
		DefaultJurisdiction:          "CA-ON",
	})
	if err != nil {
		log.Fatalf("create tenant: %v", err)
	}

	// Seed the licence register alongside the tenant. Without this a
	// fresh install starts with an empty register while the tenant record
	// carries a licence number — two places disagreeing about the same
	// fact from the first minute, which is what SignupWithInvite was
	// fixed for in stage 162. The expiry is left unset on purpose: this
	// is a demo number and inventing a renewal date would produce a
	// reminder for a day that means nothing.
	if _, err := q.CreateDefaultLocation(ctx, sqlcgen.CreateDefaultLocationParams{
		TenantID: tenant.ID, Name: tenant.Name,
	}); err != nil {
		log.Fatalf("create default location: %v", err)
	}

	if _, err := q.CreateExciseLicence(ctx, sqlcgen.CreateExciseLicenceParams{
		TenantID:      tenant.ID,
		Kind:          sqlcgen.ExciseLicenceKindSpirits,
		LicenceNumber: tenant.CraSpiritsLicenceNumber,
		EffectiveFrom: pgtype.Date{Valid: true, Time: time.Now().UTC()},
		Notes:         "From seed. Replace with your real licence and set its expiry.",
	}); err != nil {
		log.Fatalf("create licence register entry: %v", err)
	}

	password := randomPassword()
	hash, err := auth.HashPassword(password)
	if err != nil {
		log.Fatalf("hash: %v", err)
	}

	user, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{
		TenantID:     tenant.ID,
		Email:        "admin@example.com",
		PasswordHash: hash,
		DisplayName:  "Demo Admin",
		Role:         sqlcgen.UserRoleOwner,
	})
	if err != nil {
		log.Fatalf("create user: %v", err)
	}

	fmt.Printf("Seeded tenant %q (id=%s)\n", tenant.Name, tenant.ID)
	fmt.Printf("Seeded user  %s (id=%s)\n", user.Email, user.ID)
	fmt.Println()
	fmt.Println("*** Login credentials ***")
	fmt.Printf("email:    %s\n", user.Email)
	fmt.Printf("password: %s\n", password)
}

func randomPassword() string {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("rand: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
