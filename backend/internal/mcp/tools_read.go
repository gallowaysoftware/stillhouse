package mcp

import (
	"context"

	"connectrpc.com/connect"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	pb "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// registerReadTools attaches every read-only tool to s. Each tool runs
// in a tenant-scoped tx via the underlying RPC service, so the user
// closed over by the session sees only their own tenant's data.
func registerReadTools(s *mcpsdk.Server, d Deps, user sqlcgen.User) {
	addDashboard(s, d, user)
	addListBulkContainers(s, d, user)
	addGetBulkContainer(s, d, user)
	addListBarrels(s, d, user)
	addGetBarrel(s, d, user)
	addListRecentBulkMovements(s, d, user)
	addListRecipes(s, d, user)
	addGetRecipe(s, d, user)
	addListRecipeVersions(s, d, user)
	addListProducts(s, d, user)
	addListB266Periods(s, d, user)
}

// dashboardOutput is a compact rollup useful as the "what's the state
// of the distillery right now" first call.
type dashboardOutput struct {
	TotalBulkLAA           float64 `json:"total_bulk_laa"`
	BulkContainerCount     int32   `json:"bulk_container_count"`
	TotalBarrelLAA         float64 `json:"total_barrel_laa"`
	BarrelCount            int32   `json:"barrel_count"`
	AgingBarrelCount       int32   `json:"aging_barrel_count"`
	CanadianWhiskyEligible int32   `json:"canadian_whisky_eligible_barrel_count"`
}

func addDashboard(s *mcpsdk.Server, d Deps, user sqlcgen.User) {
	type in struct{}
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "get_dashboard",
		Description: "One-shot summary of current distillery state: total bulk LAA, total barrel LAA, barrel count, and Canadian Whisky eligible barrels. Use this first to orient.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ in) (*mcpsdk.CallToolResult, any, error) {
		ctx = withUser(ctx, user)
		bulks, err := d.Bulk.ListBulkContainers(ctx, connect.NewRequest(&pb.ListBulkContainersRequest{}))
		if err != nil {
			return errResult(err), nil, nil
		}
		barrels, err := d.Barrel.ListBarrels(ctx, connect.NewRequest(&pb.ListBarrelsRequest{}))
		if err != nil {
			return errResult(err), nil, nil
		}
		out := dashboardOutput{
			TotalBulkLAA:           bulks.Msg.GetSummary().GetTotalLaa(),
			BulkContainerCount:     bulks.Msg.GetSummary().GetContainerCount(),
			TotalBarrelLAA:         barrels.Msg.GetTotalLaa(),
			BarrelCount:            barrels.Msg.GetTotalCount(),
			AgingBarrelCount:       barrels.Msg.GetAgingCount(),
			CanadianWhiskyEligible: barrels.Msg.GetEligibleCount(),
		}
		return jsonResultRaw(out), nil, nil
	})
}

func addListBulkContainers(s *mcpsdk.Server, d Deps, user sqlcgen.User) {
	type in struct {
		IncludeArchived bool `json:"include_archived,omitempty" jsonschema:"include archived containers in the result"`
	}
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_bulk_containers",
		Description: "List every bulk container (spirit receiver, tank, IBC, tote, blend tank, bottling tank) with its current volume (L), ABV (%), and LAA. Barrels are listed separately via list_barrels.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx = withUser(ctx, user)
		resp, err := d.Bulk.ListBulkContainers(ctx, connect.NewRequest(&pb.ListBulkContainersRequest{
			IncludeArchived: args.IncludeArchived,
		}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

func addGetBulkContainer(s *mcpsdk.Server, d Deps, user sqlcgen.User) {
	type in struct {
		ID string `json:"id" jsonschema:"UUID of the bulk container"`
	}
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "get_bulk_container",
		Description: "Get one bulk container plus its full movement history (deposits, withdrawals, blends, losses).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx = withUser(ctx, user)
		resp, err := d.Bulk.GetBulkContainer(ctx, connect.NewRequest(&pb.GetBulkContainerRequest{Id: args.ID}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

func addListBarrels(s *mcpsdk.Server, d Deps, user sqlcgen.User) {
	type in struct {
		IncludeArchived bool `json:"include_archived,omitempty" jsonschema:"include archived barrels in the result"`
	}
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_barrels",
		Description: "List every barrel with current LAA, fill date, days aged, and Canadian Whisky (FDR B.02.020) eligibility — small wood (≤700 L) aged ≥1095 days in Canada.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx = withUser(ctx, user)
		resp, err := d.Barrel.ListBarrels(ctx, connect.NewRequest(&pb.ListBarrelsRequest{
			IncludeArchived: args.IncludeArchived,
		}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

func addGetBarrel(s *mcpsdk.Server, d Deps, user sqlcgen.User) {
	type in struct {
		ID string `json:"id" jsonschema:"UUID of the barrel"`
	}
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "get_barrel",
		Description: "Get one barrel plus its full event history (fills, regauges, samples, dumps).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx = withUser(ctx, user)
		resp, err := d.Barrel.GetBarrel(ctx, connect.NewRequest(&pb.GetBarrelRequest{Id: args.ID}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

func addListRecentBulkMovements(s *mcpsdk.Server, d Deps, user sqlcgen.User) {
	type in struct{}
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_recent_bulk_movements",
		Description: "Recent bulk-alcohol journal entries: production gauges, inter-tank transfers, blends, losses, etc.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ in) (*mcpsdk.CallToolResult, any, error) {
		ctx = withUser(ctx, user)
		resp, err := d.Bulk.ListRecentBulkMovements(ctx, connect.NewRequest(&pb.ListRecentBulkMovementsRequest{}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

func addListRecipes(s *mcpsdk.Server, d Deps, user sqlcgen.User) {
	type in struct {
		IncludeArchived bool `json:"include_archived,omitempty" jsonschema:"include archived recipes"`
	}
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_recipes",
		Description: "List recipes (mash bills) with projected LAA per batch.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx = withUser(ctx, user)
		resp, err := d.Recipe.ListRecipes(ctx, connect.NewRequest(&pb.ListRecipesRequest{
			IncludeArchived: args.IncludeArchived,
		}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

func addGetRecipe(s *mcpsdk.Server, d Deps, user sqlcgen.User) {
	type in struct {
		ID string `json:"id" jsonschema:"UUID of the recipe"`
	}
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "get_recipe",
		Description: "Drill into one recipe: the recipe row, its current version (mash bill for whisky or botanical bill + NGS/maceration/method for gin), the projected LAA, and the latest sensory scores if any. Use this to inspect a recipe before scoring it or before iterating to a new version.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx = withUser(ctx, user)
		resp, err := d.Recipe.GetRecipe(ctx, connect.NewRequest(&pb.GetRecipeRequest{Id: args.ID}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

func addListRecipeVersions(s *mcpsdk.Server, d Deps, user sqlcgen.User) {
	type in struct {
		RecipeID string `json:"recipe_id" jsonschema:"UUID of the recipe"`
	}
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_recipe_versions",
		Description: "List every saved version of a recipe (newest first) — the iteration history. Returns version metadata (no ingredient bill); call get_recipe for the current version's full ingredient + sensory drill-down.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx = withUser(ctx, user)
		resp, err := d.Recipe.ListRecipeVersions(ctx, connect.NewRequest(&pb.ListRecipeVersionsRequest{
			RecipeId: args.RecipeID,
		}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

func addListProducts(s *mcpsdk.Server, d Deps, user sqlcgen.User) {
	type in struct {
		IncludeArchived bool `json:"include_archived,omitempty" jsonschema:"include archived products"`
	}
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_products",
		Description: "List product SKUs (bottle size, target ABV, label info).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx = withUser(ctx, user)
		resp, err := d.Product.ListProducts(ctx, connect.NewRequest(&pb.ListProductsRequest{
			IncludeArchived: args.IncludeArchived,
		}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

func addListB266Periods(s *mcpsdk.Server, d Deps, user sqlcgen.User) {
	type in struct{}
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_b266_periods",
		Description: "List CRA Form B266 reporting periods — generated, signed, submitted, reopened — with the values that go on each return.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ in) (*mcpsdk.CallToolResult, any, error) {
		ctx = withUser(ctx, user)
		resp, err := d.B266.ListB266Periods(ctx, connect.NewRequest(&pb.ListB266PeriodsRequest{}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}
