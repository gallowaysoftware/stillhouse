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
	// APITokenService — every user manages their own tokens, so viewer is
	// the floor even for mutations. Ownership is enforced inside the RPCs.
	"/stillhouse.v1.APITokenService/IssueAPIToken":  roleViewer,
	"/stillhouse.v1.APITokenService/ListAPITokens":  roleViewer,
	"/stillhouse.v1.APITokenService/RevokeAPIToken": roleViewer,
	// Revoking your own tokens is never an action to gate behind a role —
	// the person who most needs it is the one who thinks they have been
	// compromised, whatever their role.
	"/stillhouse.v1.APITokenService/RevokeAllMyAPITokens": roleViewer,

	// AlcoholometryService — pure calculation against the published
	// tables, touching nothing. Viewer is the floor because the strength
	// widget every operator uses at the tank calls ResolveStrength on
	// every keystroke; left unclassified it fell to the owner-only
	// default and the correction silently stopped working for the people
	// the feature exists for.
	"/stillhouse.v1.AlcoholometryService/ResolveStrength": roleViewer,
	"/stillhouse.v1.AlcoholometryService/TablesInfo":      roleViewer,
	"/stillhouse.v1.AlcoholometryService/PlanReduction":   roleViewer,
	"/stillhouse.v1.AlcoholometryService/PlanBlend":       roleViewer,

	// AuditService
	"/stillhouse.v1.AuditService/ListAuditEvents": roleViewer,

	// RedistillationService. Putting spirit back through the still moves
	// alcohol out of stock onto a filed return, so it is an operator
	// action like every other movement — the person at the still is who
	// knows what went in and what came out.
	"/stillhouse.v1.RedistillationService/ListRedistillations":        roleViewer,
	"/stillhouse.v1.RedistillationService/RedistillationSummary":      roleViewer,
	"/stillhouse.v1.RedistillationService/StartRedistillation":        roleOperator,
	"/stillhouse.v1.RedistillationService/RecordRedistillationOutput": roleOperator,

	// WorkOrderService. Raising and moving work is an operator action —
	// the whole point is that the person doing the job can pick it up and
	// mark it done without asking the owner. Reading the board is a
	// viewer one.
	"/stillhouse.v1.WorkOrderService/ListWorkOrders":     roleViewer,
	"/stillhouse.v1.WorkOrderService/SaveWorkOrder":      roleOperator,
	"/stillhouse.v1.WorkOrderService/SetWorkOrderStatus": roleOperator,

	// LocationService. Moving a cask between premises is an operator
	// action — the person with the forklift is who knows. Defining what
	// the premises *are* follows the licence, so it sits with the owner.
	"/stillhouse.v1.LocationService/ListLocations":        roleViewer,
	"/stillhouse.v1.LocationService/RetailSupplyReport":   roleViewer,
	"/stillhouse.v1.LocationService/SetContainerLocation": roleOperator,
	"/stillhouse.v1.LocationService/SaveLocation":         roleOwner,

	// PurchasingService. Receiving is an operator action — the person on
	// the loading dock is who knows what arrived. Committing the
	// distillery to a purchase is not, and neither is the supplier
	// record behind it.
	"/stillhouse.v1.PurchasingService/ListSuppliers":           roleViewer,
	"/stillhouse.v1.PurchasingService/ListPurchaseOrders":      roleViewer,
	"/stillhouse.v1.PurchasingService/GetPurchaseOrder":        roleViewer,
	"/stillhouse.v1.PurchasingService/ListGRNI":                roleViewer,
	"/stillhouse.v1.PurchasingService/ReceiveAgainstPO":        roleOperator,
	"/stillhouse.v1.PurchasingService/SetLandedCharges":        roleOperator,
	"/stillhouse.v1.PurchasingService/MarkLotInvoiced":         roleOperator,
	"/stillhouse.v1.PurchasingService/SaveSupplier":            roleOwner,
	"/stillhouse.v1.PurchasingService/CreatePurchaseOrder":     roleOwner,
	"/stillhouse.v1.PurchasingService/AddPurchaseOrderLine":    roleOwner,
	"/stillhouse.v1.PurchasingService/RemovePurchaseOrderLine": roleOwner,
	"/stillhouse.v1.PurchasingService/SetPurchaseOrderStatus":  roleOwner,

	// LabService. Recording a result and holding a lot are operator
	// actions — the person who has the certificate in their hand, or who
	// found the problem, is who should be able to act. Releasing is not:
	// it is a named sign-off that stock may leave, and the point of the
	// gate is that it takes somebody with the authority to open it.
	"/stillhouse.v1.LabService/RecordLabResult": roleOperator,
	"/stillhouse.v1.LabService/ListLabResults":  roleViewer,
	"/stillhouse.v1.LabService/HoldLot":         roleOperator,
	"/stillhouse.v1.LabService/ReleaseLot":      roleOwner,
	// Whether release is required at all is a decision about how the
	// distillery works.
	"/stillhouse.v1.TenantService/SetBatchReleaseRequired": roleOwner,

	// ImportService. A bulk import creates casks, stock and customers in
	// one action with no upstream production behind any of it — the same
	// power as AdoptOpeningInventory, at scale — so it sits with the back
	// office rather than with the operator at the still. Reading the
	// column list is not a write and is not gated.
	"/stillhouse.v1.ImportService/DescribeImport": roleViewer,
	"/stillhouse.v1.ImportService/RunImport":      roleOwner,

	// JournalService. The chart of accounts is the licensee's own
	// bookkeeping, so mapping is owner-level; previewing what would be
	// exported is a viewer read.
	"/stillhouse.v1.JournalService/PreviewJournal":      roleViewer,
	"/stillhouse.v1.JournalService/ListJournalAccounts": roleViewer,
	"/stillhouse.v1.JournalService/SetJournalAccount":   roleOwner,

	// AlertService. Everything here is viewer-level on purpose: an alert
	// exists to reach whoever is looking, and the person most likely to
	// notice that a ferment has gone quiet is the one on the floor, not
	// the owner. Acknowledging is a statement about the acknowledger's
	// own attention; nobody can resolve an alert through this service at
	// any role, because only the evaluator may say a condition has
	// stopped being true.
	"/stillhouse.v1.AlertService/ListAlerts":       roleViewer,
	"/stillhouse.v1.AlertService/AcknowledgeAlert": roleViewer,
	"/stillhouse.v1.AlertService/EvaluateAlerts":   roleViewer,
	"/stillhouse.v1.AlertService/SetAlertEmail":    roleViewer,

	// Managing your own second factor is never a role question. The
	// viewer who reads the ledger from a phone in a rackhouse needs it
	// as much as the owner does.
	"/stillhouse.v1.AuthService/MFAStatus":           roleViewer,
	"/stillhouse.v1.AuthService/BeginMFAEnrolment":   roleViewer,
	"/stillhouse.v1.AuthService/ConfirmMFAEnrolment": roleViewer,
	"/stillhouse.v1.AuthService/DisableMFA":          roleViewer,

	// CustomerService. Reading the customer list is a viewer action —
	// an operator recording a removal has to pick from it. Creating and
	// editing a customer is not: the customer's kind decides whether a
	// removal to them is duty-paid, non-duty-paid or an export, so
	// getting it wrong misstates a filed return. That makes it a
	// back-office decision, same reasoning as the filing calendar.
	"/stillhouse.v1.CustomerService/ListCustomers":       roleViewer,
	"/stillhouse.v1.CustomerService/GetCustomer":         roleViewer,
	"/stillhouse.v1.CustomerService/CreateCustomer":      roleOwner,
	"/stillhouse.v1.CustomerService/UpdateCustomer":      roleOwner,
	"/stillhouse.v1.CustomerService/SetCustomerArchived": roleOwner,
	// Price lists are commercial terms, not production data.
	"/stillhouse.v1.CustomerService/ListPriceLists":    roleViewer,
	"/stillhouse.v1.CustomerService/GetPriceList":      roleViewer,
	"/stillhouse.v1.CustomerService/CreatePriceList":   roleOwner,
	"/stillhouse.v1.CustomerService/SetPriceListEntry": roleOwner,

	// BarrelService
	"/stillhouse.v1.BarrelService/ListBarrels":     roleViewer,
	"/stillhouse.v1.BarrelService/GetBarrel":       roleViewer,
	"/stillhouse.v1.BarrelService/CreateBarrel":    roleOperator,
	"/stillhouse.v1.BarrelService/FillBarrel":      roleOperator,
	"/stillhouse.v1.BarrelService/DumpBarrel":      roleOperator,
	"/stillhouse.v1.BarrelService/RegaugeBarrel":   roleOperator,
	"/stillhouse.v1.BarrelService/VoidBarrelEvent": roleOperator,

	// BottlingService
	"/stillhouse.v1.BottlingService/CreateBottlingRun":     roleOperator,
	"/stillhouse.v1.BottlingService/GetBottlingRun":        roleViewer,
	"/stillhouse.v1.BottlingService/ListBottlingRuns":      roleViewer,
	"/stillhouse.v1.BottlingService/ListPackagedInventory": roleViewer,
	"/stillhouse.v1.BottlingService/VoidBottlingRun":       roleOperator,

	// BulkService
	"/stillhouse.v1.BulkService/CreateBulkContainer": roleOperator,
	// Adopting stock puts alcohol into the ledger with no upstream
	// production behind it. A legitimate day-one operation, and a powerful
	// one, so it sits with the back office rather than with the operator
	// capturing readings at the still. (Anything absent from this map fails
	// closed at roleOwner anyway; listed explicitly so the intent is on the
	// page rather than implied.)
	"/stillhouse.v1.BulkService/AdoptOpeningInventory": roleOwner,

	// An inventory adjustment is a deliberate, attributable reconciliation
	// entry that lands on line D of a filed return, so it is an operator
	// action with a mandatory explanation rather than a viewer one.
	// Ruling on a loss's duty treatment changes what the return charges, so
	// it is an operator action; reading the outstanding list is not.
	// The reporting calendar is a pair of CRA elections, so changing it is
	// owner-only; asking which period to file is not.
	"/stillhouse.v1.TenantService/UpdateFilingCalendar":   roleOwner,
	"/stillhouse.v1.B266Service/SuggestB266Period":        roleViewer,
	"/stillhouse.v1.B266Service/GetFilingAcknowledgement": roleViewer,

	"/stillhouse.v1.BulkService/ListLosses":     roleViewer,
	"/stillhouse.v1.BulkService/ClassifyLosses": roleOperator,

	// Alcohol arriving on or leaving the premises lands on a filed return
	// and names a counterparty, so it is an operator action.
	"/stillhouse.v1.BulkService/RecordBulkExternalMovement": roleOperator,
	"/stillhouse.v1.BulkService/RecordInventoryAdjustment":  roleOperator,
	"/stillhouse.v1.BulkService/ListInventoryAdjustments":   roleViewer,
	"/stillhouse.v1.BulkService/UpdateBulkContainer":        roleOperator,
	"/stillhouse.v1.BulkService/SetBulkContainerArchived":   roleOperator,
	"/stillhouse.v1.BulkService/ListBulkContainers":         roleViewer,
	"/stillhouse.v1.BulkService/GetBulkContainer":           roleViewer,
	"/stillhouse.v1.BulkService/ListRecentBulkMovements":    roleViewer,
	"/stillhouse.v1.BulkService/CreateBlend":                roleOperator,

	// DistillationService
	"/stillhouse.v1.DistillationService/CreateDistillationRun":    roleOperator,
	"/stillhouse.v1.DistillationService/GetDistillationRun":       roleViewer,
	"/stillhouse.v1.DistillationService/ListDistillationRuns":     roleViewer,
	"/stillhouse.v1.DistillationService/UpdateDistillationStatus": roleOperator,
	"/stillhouse.v1.DistillationService/AddDistillationCharge":    roleOperator,
	"/stillhouse.v1.DistillationService/AddDistillationCut":       roleOperator,
	"/stillhouse.v1.DistillationService/UpdateDistillationCut":    roleOperator,
	"/stillhouse.v1.DistillationService/DeleteDistillationCut":    roleOperator,
	"/stillhouse.v1.DistillationService/RecordProductionGauge":    roleOperator,
	"/stillhouse.v1.DistillationService/VoidDistillationRun":      roleOperator,

	// ExciseStampService
	"/stillhouse.v1.ExciseStampService/CreateStampOrder":  roleOperator,
	"/stillhouse.v1.ExciseStampService/ReceiveStampOrder": roleOperator,
	"/stillhouse.v1.ExciseStampService/ListStampOrders":   roleViewer,
	"/stillhouse.v1.ExciseStampService/VoidStamps":        roleOperator,
	// Stamps are Crown-controlled and the licensee is accountable for
	// each one. Recording where a stamp went — including that it was
	// lost or stolen — is an operator action, because the operator is
	// who knows. Reading the reconciliation is a viewer one: it is the
	// answer to "can we account for all of them?".
	"/stillhouse.v1.ExciseStampService/RecordStampDisposition": roleOperator,
	"/stillhouse.v1.ExciseStampService/ListStampDispositions":  roleViewer,
	"/stillhouse.v1.ExciseStampService/ReconcileStampOrder":    roleViewer,

	// The instrument register. Reading it is a viewer action — an operator
	// at the bench has to be able to see which hydrometer is approved.
	// Registering and calibrating are operator actions. Suspending or
	// retiring an instrument invalidates nothing already recorded but does
	// stop future determinations, so it stays owner-only.
	"/stillhouse.v1.InstrumentService/CreateInstrument":    roleOperator,
	"/stillhouse.v1.InstrumentService/UpdateInstrument":    roleOperator,
	"/stillhouse.v1.InstrumentService/SetInstrumentStatus": roleOwner,
	"/stillhouse.v1.InstrumentService/ListInstruments":     roleViewer,
	"/stillhouse.v1.InstrumentService/GetInstrument":       roleViewer,
	"/stillhouse.v1.InstrumentService/RecordCalibration":   roleOperator,

	// FermentationService
	"/stillhouse.v1.FermentationService/CreateFermentationRun":    roleOperator,
	"/stillhouse.v1.FermentationService/GetFermentationRun":       roleViewer,
	"/stillhouse.v1.FermentationService/ListFermentationRuns":     roleViewer,
	"/stillhouse.v1.FermentationService/UpdateFermentationStatus": roleOperator,
	"/stillhouse.v1.FermentationService/AddFermentationLog":       roleOperator,

	// MashService
	"/stillhouse.v1.MashService/CreateMashRun":     roleOperator,
	"/stillhouse.v1.MashService/GetMashRun":        roleViewer,
	"/stillhouse.v1.MashService/ListMashRuns":      roleViewer,
	"/stillhouse.v1.MashService/UpdateMashStatus":  roleOperator,
	"/stillhouse.v1.MashService/AddMashIngredient": roleOperator,
	"/stillhouse.v1.MashService/AddMashMetric":     roleOperator,
	// Strike-water calculation; reads nothing and writes nothing.
	"/stillhouse.v1.MashService/PlanStrike": roleViewer,

	// MaterialService
	"/stillhouse.v1.MaterialService/CreateMaterial":        roleOperator,
	"/stillhouse.v1.MaterialService/UpdateMaterial":        roleOperator,
	"/stillhouse.v1.MaterialService/GetMaterial":           roleViewer,
	"/stillhouse.v1.MaterialService/ListMaterials":         roleViewer,
	"/stillhouse.v1.MaterialService/ArchiveMaterial":       roleOperator,
	"/stillhouse.v1.MaterialService/RecordMaterialReceipt": roleOperator,
	"/stillhouse.v1.MaterialService/ListMaterialLots":      roleViewer,
	"/stillhouse.v1.MaterialService/BottlingRunCost":       roleViewer,
	"/stillhouse.v1.MaterialService/ProductCostSummary":    roleViewer,

	// PricingService
	"/stillhouse.v1.PricingService/ComputeProvincialPricing": roleViewer,

	// ProductService
	// Trade and label details are commercial rather than production
	// data, and a wrong GTIN sends the wrong case to a distributor.
	"/stillhouse.v1.ProductService/UpdateProductSKU":   roleOperator,
	"/stillhouse.v1.ProductService/CreateProduct":      roleOperator,
	"/stillhouse.v1.ProductService/UpdateProduct":      roleOperator,
	"/stillhouse.v1.ProductService/ListProducts":       roleViewer,
	"/stillhouse.v1.ProductService/GetProduct":         roleViewer,
	"/stillhouse.v1.ProductService/SetProductArchived": roleOperator,

	// RecipeService
	"/stillhouse.v1.RecipeService/CreateRecipe":                   roleOperator,
	"/stillhouse.v1.RecipeService/DuplicateRecipe":                roleOperator,
	"/stillhouse.v1.RecipeService/ListRecipes":                    roleViewer,
	"/stillhouse.v1.RecipeService/GetRecipe":                      roleViewer,
	"/stillhouse.v1.RecipeService/ArchiveRecipe":                  roleOperator,
	"/stillhouse.v1.RecipeService/SaveRecipeVersion":              roleOperator,
	"/stillhouse.v1.RecipeService/ListRecipeVersions":             roleViewer,
	"/stillhouse.v1.RecipeService/SaveRecipeVersionSensory":       roleOperator,
	"/stillhouse.v1.RecipeService/SaveRecipeVersionWhiskySensory": roleOperator,

	// RemovalService
	// SalesService. Picking and shipping are operator actions — the
	// person putting cases on the truck is who knows what actually went
	// on it, and ShipShipment is where the removals get written, so
	// gating it above the picker would mean the removals get typed later
	// by somebody who was not there. That is the failure this track
	// exists to end.
	//
	// Committing the distillery to an order, and cancelling one, is a
	// back-office act like a purchase order, so it sits with the owner.
	"/stillhouse.v1.SalesService/ListSalesOrders":      roleViewer,
	"/stillhouse.v1.SalesService/GetSalesOrder":        roleViewer,
	"/stillhouse.v1.SalesService/ListShipments":        roleViewer,
	"/stillhouse.v1.SalesService/GetShipment":          roleViewer,
	"/stillhouse.v1.SalesService/ListStockCommitments": roleViewer,
	"/stillhouse.v1.SalesService/CreateShipment":       roleOperator,
	"/stillhouse.v1.SalesService/AddShipmentLine":      roleOperator,
	"/stillhouse.v1.SalesService/RemoveShipmentLine":   roleOperator,
	"/stillhouse.v1.SalesService/ShipShipment":         roleOperator,
	"/stillhouse.v1.SalesService/CancelShipment":       roleOperator,
	"/stillhouse.v1.SalesService/CreateSalesOrder":     roleOwner,
	"/stillhouse.v1.SalesService/AddSalesOrderLine":    roleOwner,
	"/stillhouse.v1.SalesService/RemoveSalesOrderLine": roleOwner,
	"/stillhouse.v1.SalesService/SetSalesOrderStatus":  roleOwner,

	// Ownership and possession of bulk spirits. Recording where the
	// spirits physically are is an operator act — the person who loaded
	// the truck is who knows — and it writes an in-bond transfer onto the
	// return, which is the same class of thing as any other movement.
	// Saying who *owns* them is a commercial fact about a contract, so it
	// sits with the owner.
	"/stillhouse.v1.BulkService/ListThirdPartySpirits":      roleViewer,
	"/stillhouse.v1.BulkService/SetBulkContainerPossession": roleOperator,
	"/stillhouse.v1.BulkService/SetBulkContainerOwner":      roleOwner,

	"/stillhouse.v1.RemovalService/CreateRemoval": roleOperator,
	"/stillhouse.v1.RemovalService/ListRemovals":  roleViewer,
	"/stillhouse.v1.RemovalService/VoidRemoval":   roleOperator,

	// TenantService — UpdateTenant changes the CRA licence on file, owner only.
	// DeleteMyTenant is destructive and owner-only.
	"/stillhouse.v1.TenantService/GetTenant": roleViewer,
	// The licence register. Reading it is a viewer action — it is the
	// answer to "are we licensed for this?". Changing it is not: which
	// licences are held decides which returns exist and where the duty
	// point falls, so an edit changes what the system believes it is
	// filing.
	"/stillhouse.v1.TenantService/ListExciseLicences": roleViewer,
	"/stillhouse.v1.TenantService/SaveExciseLicence":  roleOwner,
	"/stillhouse.v1.TenantService/UpdateTenant":       roleOwner,
	"/stillhouse.v1.TenantService/DeleteMyTenant":     roleOwner,

	// TraceabilityService
	"/stillhouse.v1.TraceabilityService/TraceBottlingRun": roleViewer,

	// UserService — ChangeMyPassword stays viewer-level because every user,
	// including read-only ones, must be able to rotate their own credentials.
	"/stillhouse.v1.UserService/GetMe":            roleViewer,
	"/stillhouse.v1.UserService/ListUsers":        roleViewer,
	"/stillhouse.v1.UserService/CreateUser":       roleOwner,
	"/stillhouse.v1.UserService/ChangeMyPassword": roleViewer,

	// InviteService — invite management is owner-only; SignupWithInvite is
	// public (covered by publicProcedures, never reaches this map).
	"/stillhouse.v1.InviteService/CreateInviteCode":  roleOwner,
	"/stillhouse.v1.InviteService/ListMyInviteCodes": roleOwner,
	"/stillhouse.v1.InviteService/RevokeInviteCode":  roleOwner,

	// B266Service — generating a draft is operator-level; signing/submitting
	// the filing is an owner action.
	"/stillhouse.v1.B266Service/GenerateB266":     roleOperator,
	"/stillhouse.v1.B266Service/SubmitB266":       roleOwner,
	"/stillhouse.v1.B266Service/ListB266Periods":  roleViewer,
	"/stillhouse.v1.B266Service/GetB266Period":    roleViewer,
	"/stillhouse.v1.B266Service/ReopenB266Period": roleOwner,
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
	case sqlcgen.UserRoleAccountant:
		// An accountant reads like a viewer. What they may do beyond
		// that is enumerated in accountantAlso rather than expressed as
		// a rank, because their permissions are not a prefix of anyone
		// else's: more than a viewer on the compliance surface, and less
		// than an operator everywhere else.
		return roleViewer
	}
	return 0
}

// accountantAlso is the compliance surface an accountant reaches on top
// of everything a viewer can see. It is a list rather than a rank
// because the role deliberately does not sit on the
// owner > operator > viewer line.
//
// What is on it is what an outside bookkeeper or excise consultant is
// engaged to do: prepare and file the return, rule on a loss's duty
// treatment, set the reporting calendar, and pull the binder that
// evidences all of it.
//
// What is *not* on it matters more. No production writes — no gauge, no
// bottling, no removal, no adjustment. Someone who both books a movement
// and rules on its treatment is precisely the segregation-of-duties
// problem the audit trail exists to make visible, and handing the
// outside accountant an owner account (which is what happens today,
// because nothing else fits) gives them exactly that, plus user
// management and tenant deletion.
var accountantAlso = map[string]bool{
	// Preparing and filing the return.
	"/stillhouse.v1.B266Service/GenerateB266":     true,
	"/stillhouse.v1.B266Service/SubmitB266":       true,
	"/stillhouse.v1.B266Service/ReopenB266Period": true,
	// The statements shown before a period is marked submitted — the
	// accountant is the one reading them.
	"/stillhouse.v1.B266Service/GetFilingAcknowledgement": true,
	// The reporting calendar is a pair of CRA elections (B268, B284).
	// Advising on which to make is the engagement.
	"/stillhouse.v1.TenantService/UpdateFilingCalendar": true,
	// Whether a loss is relieved or duty-payable is an excise judgement,
	// not an operational one.
	"/stillhouse.v1.BulkService/ClassifyLosses": true,
	// The chart of accounts is the engagement's other half. An outside
	// bookkeeper who cannot map Stillhouse's events to their own accounts
	// has to ask the owner to do it for them, which is nobody's idea of
	// a division of labour.
	"/stillhouse.v1.JournalService/SetJournalAccount": true,
	// Which licences are held is the first thing an excise consultant
	// establishes, and keeping the register current is part of the
	// engagement rather than something to ask the owner for.
	"/stillhouse.v1.TenantService/SaveExciseLicence": true,
	// Premises follow the licence, and the licence register is already
	// on this list.
	"/stillhouse.v1.LocationService/SaveLocation": true,
}

// checkRole returns nil if the user role may invoke procedure, or a
// connect-typed error otherwise. Public procedures bypass entirely; private
// procedures with no entry in procedureMinRole fail closed at owner so a
// newly added endpoint is loud rather than silently open.
// AuthorizeProcedure applies the role gate outside the ConnectRPC
// interceptor chain.
//
// The MCP server at /mcp is mounted as a plain http.Handler, so the
// interceptors — including the role gate — never run for it. It
// authenticated the bearer token and then called the service methods
// directly, and those methods carry no role checks of their own, so a
// viewer could mint a token (IssueAPIToken is deliberately viewer-level so
// everyone can manage their own) and use it to fill, dump and regauge
// barrels: writes that move dutiable alcohol onto the B266. Any transport
// that reaches a service method without passing through the interceptor
// must call this with the procedure it is standing in for.
func AuthorizeProcedure(procedure string, role sqlcgen.UserRole) error {
	return checkRole(procedure, true, role)
}

func checkRole(procedure string, hasUser bool, role sqlcgen.UserRole) error {
	if publicProcedures[procedure] {
		return nil
	}
	if !hasUser {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	if role == sqlcgen.UserRoleAccountant && accountantAlso[procedure] {
		return nil
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
