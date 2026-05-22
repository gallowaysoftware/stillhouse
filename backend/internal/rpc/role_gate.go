package rpc

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
)

// minRole is the lowest tenant role that may invoke a given procedure.
// Roles are totally ordered: owner > operator > viewer.
type minRole int

const (
	roleViewer   minRole = 1
	roleOperator minRole = 2
	roleOwner    minRole = 3
)

// procedureMinRole pins each procedure to its minimum role. Anything missing
// from this map fails closed at roleOwner — adding a new write endpoint without
// classifying it is a bug we want to be loud about, not a silent permission
// downgrade. Public (no-auth) procedures are skipped via publicProcedures and
// never reach this gate.
var procedureMinRole = map[string]minRole{
	// AuditService
	"/stillhouse.v1.AuditService/ListAuditEvents": roleViewer,

	// BarrelService
	"/stillhouse.v1.BarrelService/ListBarrels":  roleViewer,
	"/stillhouse.v1.BarrelService/GetBarrel":    roleViewer,
	"/stillhouse.v1.BarrelService/CreateBarrel": roleOperator,
	"/stillhouse.v1.BarrelService/FillBarrel":   roleOperator,
	"/stillhouse.v1.BarrelService/DumpBarrel":   roleOperator,
	"/stillhouse.v1.BarrelService/RegaugeBarrel": roleOperator,

	// BottlingService
	"/stillhouse.v1.BottlingService/CreateBottlingRun":    roleOperator,
	"/stillhouse.v1.BottlingService/GetBottlingRun":       roleViewer,
	"/stillhouse.v1.BottlingService/ListBottlingRuns":     roleViewer,
	"/stillhouse.v1.BottlingService/ListPackagedInventory": roleViewer,

	// BulkService
	"/stillhouse.v1.BulkService/CreateBulkContainer":     roleOperator,
	"/stillhouse.v1.BulkService/UpdateBulkContainer":     roleOperator,
	"/stillhouse.v1.BulkService/SetBulkContainerArchived": roleOperator,
	"/stillhouse.v1.BulkService/ListBulkContainers":      roleViewer,
	"/stillhouse.v1.BulkService/GetBulkContainer":        roleViewer,
	"/stillhouse.v1.BulkService/ListRecentBulkMovements": roleViewer,

	// DistillationService
	"/stillhouse.v1.DistillationService/CreateDistillationRun":   roleOperator,
	"/stillhouse.v1.DistillationService/GetDistillationRun":      roleViewer,
	"/stillhouse.v1.DistillationService/ListDistillationRuns":    roleViewer,
	"/stillhouse.v1.DistillationService/UpdateDistillationStatus": roleOperator,
	"/stillhouse.v1.DistillationService/AddDistillationCharge":   roleOperator,
	"/stillhouse.v1.DistillationService/AddDistillationCut":      roleOperator,
	"/stillhouse.v1.DistillationService/RecordProductionGauge":   roleOperator,

	// ExciseStampService
	"/stillhouse.v1.ExciseStampService/CreateStampOrder":  roleOperator,
	"/stillhouse.v1.ExciseStampService/ReceiveStampOrder": roleOperator,
	"/stillhouse.v1.ExciseStampService/ListStampOrders":   roleViewer,
	"/stillhouse.v1.ExciseStampService/VoidStamps":        roleOperator,

	// FermentationService
	"/stillhouse.v1.FermentationService/CreateFermentationRun":  roleOperator,
	"/stillhouse.v1.FermentationService/GetFermentationRun":     roleViewer,
	"/stillhouse.v1.FermentationService/ListFermentationRuns":   roleViewer,
	"/stillhouse.v1.FermentationService/UpdateFermentationStatus": roleOperator,
	"/stillhouse.v1.FermentationService/AddFermentationLog":     roleOperator,

	// MashService
	"/stillhouse.v1.MashService/CreateMashRun":     roleOperator,
	"/stillhouse.v1.MashService/GetMashRun":        roleViewer,
	"/stillhouse.v1.MashService/ListMashRuns":      roleViewer,
	"/stillhouse.v1.MashService/UpdateMashStatus":  roleOperator,
	"/stillhouse.v1.MashService/AddMashIngredient": roleOperator,
	"/stillhouse.v1.MashService/AddMashMetric":     roleOperator,

	// MaterialService
	"/stillhouse.v1.MaterialService/CreateMaterial":        roleOperator,
	"/stillhouse.v1.MaterialService/UpdateMaterial":        roleOperator,
	"/stillhouse.v1.MaterialService/GetMaterial":           roleViewer,
	"/stillhouse.v1.MaterialService/ListMaterials":         roleViewer,
	"/stillhouse.v1.MaterialService/ArchiveMaterial":       roleOperator,
	"/stillhouse.v1.MaterialService/RecordMaterialReceipt": roleOperator,
	"/stillhouse.v1.MaterialService/ListMaterialLots":      roleViewer,

	// PricingService
	"/stillhouse.v1.PricingService/ComputeProvincialPricing": roleViewer,

	// ProductService
	"/stillhouse.v1.ProductService/CreateProduct":     roleOperator,
	"/stillhouse.v1.ProductService/UpdateProduct":     roleOperator,
	"/stillhouse.v1.ProductService/ListProducts":      roleViewer,
	"/stillhouse.v1.ProductService/GetProduct":        roleViewer,
	"/stillhouse.v1.ProductService/SetProductArchived": roleOperator,

	// RecipeService
	"/stillhouse.v1.RecipeService/CreateRecipe":       roleOperator,
	"/stillhouse.v1.RecipeService/DuplicateRecipe":    roleOperator,
	"/stillhouse.v1.RecipeService/ListRecipes":        roleViewer,
	"/stillhouse.v1.RecipeService/GetRecipe":          roleViewer,
	"/stillhouse.v1.RecipeService/ArchiveRecipe":      roleOperator,
	"/stillhouse.v1.RecipeService/SaveRecipeVersion":  roleOperator,
	"/stillhouse.v1.RecipeService/ListRecipeVersions": roleViewer,

	// RemovalService
	"/stillhouse.v1.RemovalService/CreateRemoval": roleOperator,
	"/stillhouse.v1.RemovalService/ListRemovals":  roleViewer,
	"/stillhouse.v1.RemovalService/VoidRemoval":   roleOperator,

	// TenantService — UpdateTenant changes the CRA licence on file, owner only.
	"/stillhouse.v1.TenantService/GetTenant":    roleViewer,
	"/stillhouse.v1.TenantService/UpdateTenant": roleOwner,

	// TraceabilityService
	"/stillhouse.v1.TraceabilityService/TraceBottlingRun": roleViewer,

	// UserService — ChangeMyPassword stays viewer-level because every user,
	// including read-only ones, must be able to rotate their own credentials.
	"/stillhouse.v1.UserService/GetMe":             roleViewer,
	"/stillhouse.v1.UserService/ListUsers":         roleViewer,
	"/stillhouse.v1.UserService/CreateUser":        roleOwner,
	"/stillhouse.v1.UserService/ChangeMyPassword":  roleViewer,

	// B266Service — generating a draft is operator-level; signing/submitting
	// the filing is an owner action.
	"/stillhouse.v1.B266Service/GenerateB266":   roleOperator,
	"/stillhouse.v1.B266Service/SubmitB266":     roleOwner,
	"/stillhouse.v1.B266Service/ListB266Periods": roleViewer,
	"/stillhouse.v1.B266Service/GetB266Period":   roleViewer,
}

// userRoleRank converts a stored user_role enum to its minRole rank for
// comparison against the procedure's required minimum.
func userRoleRank(r sqlcgen.UserRole) minRole {
	switch r {
	case sqlcgen.UserRoleOwner:
		return roleOwner
	case sqlcgen.UserRoleOperator:
		return roleOperator
	case sqlcgen.UserRoleViewer:
		return roleViewer
	}
	return 0
}

// checkRole returns nil if the user role may invoke procedure, or a
// connect-typed error otherwise. Public procedures bypass entirely; private
// procedures with no entry in procedureMinRole fail closed at owner so a
// newly added endpoint is loud rather than silently open.
func checkRole(procedure string, hasUser bool, role sqlcgen.UserRole) error {
	if publicProcedures[procedure] {
		return nil
	}
	if !hasUser {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	required, listed := procedureMinRole[procedure]
	if !listed {
		required = roleOwner
	}
	if userRoleRank(role) < required {
		return connect.NewError(connect.CodePermissionDenied, errors.New("insufficient role"))
	}
	return nil
}

// NewRoleGateInterceptor enforces procedureMinRole. Must run after
// NewAuthInterceptor so CurrentUser is populated.
func NewRoleGateInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			u, ok := CurrentUser(ctx)
			var role sqlcgen.UserRole
			if ok {
				role = u.Role
			}
			if err := checkRole(req.Spec().Procedure, ok, role); err != nil {
				return nil, err
			}
			return next(ctx, req)
		}
	}
}
