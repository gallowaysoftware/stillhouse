package rpc

import (
	"testing"

	"connectrpc.com/connect"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
)

// The accountant role exists because three roles did not fit the person
// who files the return, and the shape of what it may do is the whole
// design. A viewer cannot record the filing acknowledgement or rule on a
// loss's treatment, which is most of the engagement. An owner gets user
// management and tenant deletion thrown in.
//
// This pins both halves: the compliance surface it reaches, and — the
// part that matters more — the production surface it does not. Someone
// who both books a movement and rules on its treatment is the
// segregation-of-duties problem the audit trail exists to make visible,
// and handing the outside accountant an owner account is exactly that.
func TestAccountantRoleReachesComplianceAndNotProduction(t *testing.T) {
	may := []string{
		// The engagement.
		"/stillhouse.v1.B266Service/GenerateB266",
		"/stillhouse.v1.B266Service/SubmitB266",
		"/stillhouse.v1.B266Service/ReopenB266Period",
		"/stillhouse.v1.B266Service/GetFilingAcknowledgement",
		"/stillhouse.v1.TenantService/UpdateFilingCalendar",
		"/stillhouse.v1.BulkService/ClassifyLosses",
		// Everything a viewer can read, because that is the floor.
		"/stillhouse.v1.B266Service/ListB266Periods",
		"/stillhouse.v1.AuditService/ListAuditEvents",
		"/stillhouse.v1.BulkService/ListBulkContainers",
		"/stillhouse.v1.CustomerService/ListCustomers",
		"/stillhouse.v1.BulkService/ListLosses",
		"/stillhouse.v1.BulkService/ListInventoryAdjustments",
	}
	mayNot := []string{
		// Production writes. An accountant recording a gauge is the
		// conflict this role exists to avoid.
		"/stillhouse.v1.DistillationService/RecordProductionGauge",
		"/stillhouse.v1.BottlingService/CreateBottlingRun",
		"/stillhouse.v1.RemovalService/CreateRemoval",
		"/stillhouse.v1.BarrelService/FillBarrel",
		"/stillhouse.v1.BulkService/RecordInventoryAdjustment",
		"/stillhouse.v1.BulkService/RecordBulkExternalMovement",
		"/stillhouse.v1.BulkService/AdoptOpeningInventory",
		// Nor the owner's authority over the tenant and its people.
		"/stillhouse.v1.UserService/CreateUser",
		"/stillhouse.v1.TenantService/UpdateTenant",
		"/stillhouse.v1.TenantService/DeleteMyTenant",
		"/stillhouse.v1.InviteService/GenerateInviteCode",
	}

	for _, p := range may {
		if err := checkRole(p, true, sqlcgen.UserRoleAccountant); err != nil {
			t.Errorf("an accountant is refused %s: %v", p, err)
		}
	}
	for _, p := range mayNot {
		err := checkRole(p, true, sqlcgen.UserRoleAccountant)
		if err == nil {
			t.Errorf("an accountant may call %s, which is a production or "+
				"tenant-administration action they must not reach", p)
			continue
		}
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("%s refused an accountant with %v, want PermissionDenied",
				p, connect.CodeOf(err))
		}
	}
}

// A viewer must not pick up the accountant's extras by accident: the
// allow-list is consulted only for the accountant role.
func TestAccountantExtrasAreNotGrantedToOtherRoles(t *testing.T) {
	for p := range accountantAlso {
		required, listed := procedureMinRole[p]
		if !listed {
			// Unlisted procedures fail closed at owner, so an accountant
			// reaching one is the allow-list doing its job and a viewer
			// still cannot.
			if err := checkRole(p, true, sqlcgen.UserRoleViewer); err == nil {
				t.Errorf("a viewer may call %s", p)
			}
			continue
		}
		if required <= roleViewer {
			continue // genuinely a viewer action; the extras list is redundant there
		}
		if err := checkRole(p, true, sqlcgen.UserRoleViewer); err == nil {
			t.Errorf("a viewer may call %s, which is on the accountant's extras list", p)
		}
	}
}
