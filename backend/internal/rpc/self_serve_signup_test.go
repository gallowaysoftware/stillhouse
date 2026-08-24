package rpc

import (
	"log/slog"
	"os"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/testdb"
)

// PLAN H9's buildable half. Billing needs a payment processor and its
// credentials; self-serve signup does not.
//
// The whole risk is in the default. CreateTenant is a PUBLIC endpoint —
// a fresh install has nobody to authenticate as — and what keeps that
// safe is refusing once a tenant exists: it is open for exactly as long
// as it is useless to an attacker. Self-serve signup removes that
// refusal, so these tests are mostly about it staying removed only when
// somebody asked.
//
// Needs STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.

func signupReq(licence string) *connect.Request[stillhousev1.CreateTenantRequest] {
	return connect.NewRequest(&stillhousev1.CreateTenantRequest{
		Name:                    "New Distillery " + uuid.NewString()[:8],
		CraSpiritsLicenceNumber: licence,
		DefaultJurisdiction:     "CA-NS",
		OwnerEmail:              "owner-" + uuid.NewString() + "@example.com",
		OwnerPassword:           "a-long-enough-password",
		OwnerDisplayName:        "New Owner",
	})
}

// The default, and the one that matters most: an install nobody
// configured stays bootstrapped-once.
func TestSelfServeSignup_OffByDefault(t *testing.T) {
	f := newLedgerFixture(t) // a tenant now exists
	svc := NewTenantService(testdb.AppPool(t), f.q,
		slog.New(slog.NewTextHandler(os.Stderr, nil)))

	_, err := svc.CreateTenant(f.ctx, signupReq("NEW-"+uuid.NewString()[:8]))
	if err == nil {
		t.Fatal("created a second tenant on an install where signup was never enabled — " +
			"that is drive-by takeover of somebody's rackhouse box")
	}
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("code: %v", connect.CodeOf(err))
	}
	if !strings.Contains(err.Error(), "already bootstrapped") {
		t.Errorf("message: %v", err)
	}
}

// Turned on, it works — otherwise the flag is decoration.
func TestSelfServeSignup_OnCreatesATenant(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewTenantService(testdb.AppPool(t), f.q,
		slog.New(slog.NewTextHandler(os.Stderr, nil))).
		WithSelfServeSignup(true, false)

	licence := "SS-" + uuid.NewString()[:10]
	resp, err := svc.CreateTenant(f.ctx, signupReq(licence))
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if resp.Msg.GetTenant().GetName() == "" {
		t.Error("no tenant came back")
	}
	t.Cleanup(func() {
		_, _ = testdb.AdminPool(t).Exec(f.ctx,
			`DELETE FROM tenants WHERE cra_spirits_licence_number = $1`, licence)
	})
}

// A licence number is not free text once anybody can type one. Two
// tenants under one licence would each file a B266 that CRA reads as the
// same licensee's, and neither would reconcile against the other.
func TestSelfServeSignup_RefusesADuplicateLicence(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewTenantService(testdb.AppPool(t), f.q,
		slog.New(slog.NewTextHandler(os.Stderr, nil))).
		WithSelfServeSignup(true, false)

	_, err := svc.CreateTenant(f.ctx, signupReq(f.tenant.CraSpiritsLicenceNumber))
	if err == nil {
		t.Fatal("two distilleries now claim one spirits licence")
	}
	if connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Errorf("code: %v", connect.CodeOf(err))
	}
	// Case must not be a way around it.
	_, err = svc.CreateTenant(f.ctx,
		signupReq(strings.ToLower(f.tenant.CraSpiritsLicenceNumber)))
	if err == nil {
		t.Error("lower-casing the licence number got past the check")
	}
}

// An open write endpoint needs a budget, or signup is a way to fill
// somebody's disk from a laptop.
func TestSelfServeSignup_IsRateLimited(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewTenantService(testdb.AppPool(t), f.q,
		slog.New(slog.NewTextHandler(os.Stderr, nil))).
		WithSelfServeSignup(true, false)

	var licences []string
	t.Cleanup(func() {
		for _, l := range licences {
			_, _ = testdb.AdminPool(t).Exec(f.ctx,
				`DELETE FROM tenants WHERE cra_spirits_licence_number = $1`, l)
		}
	})

	var throttled bool
	for i := 0; i < 15; i++ {
		l := "RL-" + uuid.NewString()[:10]
		licences = append(licences, l)
		if _, err := svc.CreateTenant(f.ctx, signupReq(l)); err != nil {
			if connect.CodeOf(err) == connect.CodeResourceExhausted {
				throttled = true
				break
			}
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if !throttled {
		t.Error("fifteen distilleries were created from one address without being throttled")
	}
}
