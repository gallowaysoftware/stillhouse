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
	addGetMash(s, d, user)
	addPlanStrike(s, d, user)
	addPlanReduction(s, d, user)
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

// addGetMash exposes the mash bench: the grain bill, the readings taken so
// far, and the guidance derived from them — gelatinisation requirement,
// conversion window, whether the bill forces a separate cereal cook, mash
// thickness, and conversion efficiency once an OG is recorded.
//
// This is the read an operator wants mid-mash, and the one an LLM needs
// before it can answer "is this mash going the way it should?".
func addGetMash(s *mcpsdk.Server, d Deps, user sqlcgen.User) {
	type in struct {
		MashRunID string `json:"mash_run_id" jsonschema:"UUID of the mash run"`
	}
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name: "get_mash",
		Description: "Read a mash run: grain bill, recorded metrics, projected vs captured LAA, and the mash bench — " +
			"gelatinisation range required by the bill's cereals, the 60–70 °C conversion window, whether a separate " +
			"cereal cook is required (maize and rice force one), mash thickness, conversion efficiency from OG, and " +
			"any findings about temperature, pH or thickness being out of band.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx = withUser(ctx, user)
		resp, err := d.Mash.GetMashRun(ctx, connect.NewRequest(&pb.GetMashRunRequest{
			Id: args.MashRunID,
		}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

// addPlanStrike answers the question asked while the liquor is heating.
func addPlanStrike(s *mcpsdk.Server, d Deps, user sqlcgen.User) {
	type in struct {
		TargetTempC     float64 `json:"target_temp_c" jsonschema:"the rest temperature you want the mash to land on, °C"`
		GrainTempC      float64 `json:"grain_temp_c" jsonschema:"current temperature of the grain, °C (ambient store temperature is usually right)"`
		ThicknessLPerKg float64 `json:"thickness_l_per_kg" jsonschema:"mash thickness in litres of liquor per kilogram of grain; 2.5-3.5 is the usual band"`
		GrainKg         float64 `json:"grain_kg,omitempty" jsonschema:"optional grain mass; when supplied the response also gives the liquor volume needed"`
	}
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name: "plan_strike",
		Description: "Calculate the strike temperature: how hot to heat the mash liquor so the grain lands on the target " +
			"rest temperature. Flags the case where the required liquor temperature would denature the amylases on contact.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx = withUser(ctx, user)
		resp, err := d.Mash.PlanStrike(ctx, connect.NewRequest(&pb.PlanStrikeRequest{
			TargetTempC:     args.TargetTempC,
			GrainTempC:      args.GrainTempC,
			ThicknessLPerKg: args.ThicknessLPerKg,
			GrainKg:         args.GrainKg,
		}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

// addPlanReduction exposes the proofing-down calculator. Read-only — it
// computes a plan, it doesn't move any alcohol.
func addPlanReduction(s *mcpsdk.Server, d Deps, user sqlcgen.User) {
	type in struct {
		FromVolumeL     float64  `json:"from_volume_l,omitempty" jsonschema:"volume of spirit you're starting with, in litres at 20 °C. Use from_mass_kg instead if the vessel is on a scale"`
		FromMassKg      *float64 `json:"from_mass_kg,omitempty" jsonschema:"weight of the spirit you're starting with, in kilograms. More accurate than volume — a scale ignores temperature and mass doesn't contract on mixing. Wins over from_volume_l when both are given"`
		FromStrengthPct float64  `json:"from_strength_pct" jsonschema:"current strength as a percentage (0-100) at 20 °C"`
		ToStrengthPct   float64  `json:"to_strength_pct" jsonschema:"strength you want to land on, as a percentage at 20 °C. Must be lower than from_strength_pct — water only dilutes"`
	}
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name: "plan_reduction",
		Description: "Work out how to proof spirit down to a target strength: the final volume to fill to, and the water " +
			"to add. The water figure accounts for the volume contraction that happens when ethanol and water mix " +
			"(a blend holds less than its parts did apart), so it is larger than a plain volume balance suggests — " +
			"the response carries both figures and the difference. It also gives the plan by weight, which is exact — " +
			"mass is strictly additive, so there is no contraction to correct for and no temperature to compensate.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx = withUser(ctx, user)
		resp, err := d.Alcoholometry.PlanReduction(ctx, connect.NewRequest(&pb.PlanReductionRequest{
			FromVolumeL:     args.FromVolumeL,
			FromStrengthPct: args.FromStrengthPct,
			ToStrengthPct:   args.ToStrengthPct,
			FromMassKg:      derefFloat(args.FromMassKg),
			FromMassKgSet:   args.FromMassKg != nil,
		}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}
