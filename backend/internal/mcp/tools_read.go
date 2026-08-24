package mcp

import (
	"context"
	"fmt"

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
	addPlanBlend(s, d, user)
	addReviewFiling(s, d, user)
	addListWorkOrders(s, d, user)
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
		ctx, err := guard(ctx, user, "/stillhouse.v1.BulkService/ListBulkContainers")
		if err != nil {
			return nil, nil, err
		}
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
		ctx, err := guard(ctx, user, "/stillhouse.v1.BulkService/ListBulkContainers")
		if err != nil {
			return nil, nil, err
		}
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
		ctx, err := guard(ctx, user, "/stillhouse.v1.BulkService/GetBulkContainer")
		if err != nil {
			return nil, nil, err
		}
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
		ctx, err := guard(ctx, user, "/stillhouse.v1.BarrelService/ListBarrels")
		if err != nil {
			return nil, nil, err
		}
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
		ctx, err := guard(ctx, user, "/stillhouse.v1.BarrelService/GetBarrel")
		if err != nil {
			return nil, nil, err
		}
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
		ctx, err := guard(ctx, user, "/stillhouse.v1.BulkService/ListRecentBulkMovements")
		if err != nil {
			return nil, nil, err
		}
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
		ctx, err := guard(ctx, user, "/stillhouse.v1.RecipeService/ListRecipes")
		if err != nil {
			return nil, nil, err
		}
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
		ctx, err := guard(ctx, user, "/stillhouse.v1.RecipeService/GetRecipe")
		if err != nil {
			return nil, nil, err
		}
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
		ctx, err := guard(ctx, user, "/stillhouse.v1.RecipeService/ListRecipeVersions")
		if err != nil {
			return nil, nil, err
		}
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
		ctx, err := guard(ctx, user, "/stillhouse.v1.ProductService/ListProducts")
		if err != nil {
			return nil, nil, err
		}
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
		ctx, err := guard(ctx, user, "/stillhouse.v1.B266Service/ListB266Periods")
		if err != nil {
			return nil, nil, err
		}
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
		ctx, err := guard(ctx, user, "/stillhouse.v1.MashService/GetMashRun")
		if err != nil {
			return nil, nil, err
		}
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
		ctx, err := guard(ctx, user, "/stillhouse.v1.MashService/PlanStrike")
		if err != nil {
			return nil, nil, err
		}
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
		ctx, err := guard(ctx, user, "/stillhouse.v1.AlcoholometryService/PlanReduction")
		if err != nil {
			return nil, nil, err
		}
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

// addPlanBlend exposes the vatting planner. Read-only — it computes a
// plan; CreateBlend in the web UI is what actually moves the alcohol.
func addPlanBlend(s *mcpsdk.Server, d Deps, user sqlcgen.User) {
	type source struct {
		Label       string  `json:"label,omitempty" jsonschema:"optional name for the parcel, e.g. the cask number"`
		VolumeL     float64 `json:"volume_l" jsonschema:"volume in litres at 20 °C"`
		StrengthPct float64 `json:"strength_pct" jsonschema:"strength as a percentage (0-100) at 20 °C"`
	}
	type in struct {
		Sources           []source `json:"sources" jsonschema:"the parcels being vatted together; at least two"`
		TargetStrengthPct float64  `json:"target_strength_pct,omitempty" jsonschema:"optional bottling strength to bring the vatting down to; must be below the blend's own strength"`
	}
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name: "plan_blend",
		Description: "Vat parcels together and see what comes out: the blend's volume, its strength, and — if a target " +
			"is given — the water needed to reduce it. The result is NOT the sum of the volumes or the weighted " +
			"average of the strengths: ethanol and water contract on mixing, so the parcels occupy less together " +
			"than apart. The response carries both the true figure and what simple addition would have said.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx, err := guard(ctx, user, "/stillhouse.v1.AlcoholometryService/PlanBlend")
		if err != nil {
			return nil, nil, err
		}
		srcs := make([]*pb.PlanBlendRequest_Source, 0, len(args.Sources))
		for _, s := range args.Sources {
			srcs = append(srcs, &pb.PlanBlendRequest_Source{
				Label: s.Label, VolumeL: s.VolumeL, StrengthPct: s.StrengthPct,
			})
		}
		resp, err := d.Alcoholometry.PlanBlend(ctx, connect.NewRequest(&pb.PlanBlendRequest{
			Sources:           srcs,
			TargetStrengthPct: args.TargetStrengthPct,
		}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

// filingReviewOutput answers one question — "can I file yet, and if not,
// what do I have to do first?" — from the records that already exist.
//
// Deliberately assembled from reads only. Generating a B266 writes a draft
// period, and B266 generation is a back-office write that stays in the web
// UI; a tool that quietly created periods as a side effect of being asked
// a question would be the wrong shape regardless of how convenient it is.
// So this reports on periods that exist and says so when the one being
// asked about does not.
type filingReviewOutput struct {
	// The period the licensee's own fiscal calendar says is next, and how
	// long is left on it.
	SuggestedPeriodStart string `json:"suggested_period_start"`
	SuggestedPeriodEnd   string `json:"suggested_period_end"`
	DueOn                string `json:"due_on"`
	DaysUntilDue         int32  `json:"days_until_due"`
	// True once the due date has passed with nothing submitted.
	Overdue bool `json:"overdue"`

	// Whether a period covering the reviewed span has been generated, and
	// what state it is in. Absent means nothing has been generated for it
	// yet, which is not an error — it is the first thing to do.
	PeriodExists bool   `json:"period_exists"`
	PeriodID     string `json:"period_id,omitempty"`
	PeriodStatus string `json:"period_status,omitempty"`

	// Reasons the period cannot be filed as it stands, in the operator's
	// words. Only present once the period has been generated, because they
	// are computed as part of generating it.
	FilingBlockers []string `json:"filing_blockers"`

	// Whether the return continues the last one filed. The only check on a
	// B266 that compares it against something outside its own ledger — see
	// stage 191. Null when no period has been generated yet.
	Continuity *filingContinuity `json:"continuity,omitempty"`

	// Losses nobody has ruled on. A period with any of these is not ready
	// to file: under EDM3-4-1 a relieved loss and one that cannot be
	// accounted for are charged differently, and Stillhouse will not guess.
	UnclassifiedLossCount int32   `json:"unclassified_loss_count"`
	UnclassifiedLossLAA   float64 `json:"unclassified_loss_laa"`

	// What to do next, ordered. Empty means nothing is outstanding — which
	// is not a promise the figures are right, only that nothing is missing.
	NextSteps []string `json:"next_steps"`
	// Said plainly rather than left to be inferred from an empty list.
	Note string `json:"note"`
}

type filingContinuity struct {
	Checked                bool    `json:"checked"`
	PriorPeriodStart       string  `json:"prior_period_start,omitempty"`
	PriorPeriodEnd         string  `json:"prior_period_end,omitempty"`
	PriorBulkClosingLAA    float64 `json:"prior_bulk_closing_laa"`
	BulkOpeningLAA         float64 `json:"bulk_opening_laa"`
	BulkDiscrepancyLAA     float64 `json:"bulk_discrepancy_laa"`
	PackagedDiscrepancyLAA float64 `json:"packaged_discrepancy_laa"`
	BackdatedEntryCount    int32   `json:"backdated_entry_count"`
	BackdatedNetLAA        float64 `json:"backdated_net_laa"`
	Gap                    bool    `json:"gap"`
	GapNote                string  `json:"gap_note,omitempty"`
}

// addReviewFiling is the filing-review half of PLAN J4, and the half of J1
// that belongs at the still rather than on the returns page: an operator
// with wet hands asking whether the return is ready, and being told what
// to do first rather than being handed a form.
func addReviewFiling(s *mcpsdk.Server, d Deps, user sqlcgen.User) {
	type in struct {
		On string `json:"on,omitempty" jsonschema:"review the period containing this ISO date (YYYY-MM-DD); empty means the period that is next due"`
	}
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name: "review_filing",
		Description: "Whether the current CRA B266 return is ready to file, and what has to be resolved first: " +
			"outstanding filing blockers, losses awaiting a duty treatment, and whether the return continues " +
			"the last one filed. Reads only — it does not generate or submit a return.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		// Every procedure this tool reaches is authorised on its own name
		// rather than under one guard for the first of them. All four
		// happen to require the same role today; that is a coincidence of
		// the current table, and a tool that assumed it would silently
		// stop checking the moment somebody raised one of them.
		sugCtx, err := guard(ctx, user, "/stillhouse.v1.B266Service/SuggestB266Period")
		if err != nil {
			return nil, nil, err
		}

		sug, err := d.B266.SuggestB266Period(sugCtx, connect.NewRequest(&pb.SuggestB266PeriodRequest{On: args.On}))
		if err != nil {
			return errResult(err), nil, nil
		}
		out := filingReviewOutput{
			SuggestedPeriodStart: sug.Msg.GetPeriodStart(),
			SuggestedPeriodEnd:   sug.Msg.GetPeriodEnd(),
			DueOn:                sug.Msg.GetDueOn(),
			DaysUntilDue:         sug.Msg.GetDaysUntilDue(),
			FilingBlockers:       []string{},
			NextSteps:            []string{},
		}

		// Find the generated period for that span, if there is one. Listing
		// rather than fetching by date keeps this to the read surface the
		// MCP session already has.
		listCtx, err := guard(ctx, user, "/stillhouse.v1.B266Service/ListB266Periods")
		if err != nil {
			return nil, nil, err
		}
		periods, err := d.B266.ListB266Periods(listCtx, connect.NewRequest(&pb.ListB266PeriodsRequest{}))
		if err != nil {
			return errResult(err), nil, nil
		}
		for _, p := range periods.Msg.GetPeriods() {
			if p.GetPeriodStart() == out.SuggestedPeriodStart && p.GetPeriodEnd() == out.SuggestedPeriodEnd {
				out.PeriodExists = true
				out.PeriodID = p.GetId()
				out.PeriodStatus = p.GetStatus().String()
				break
			}
		}
		out.Overdue = out.DaysUntilDue < 0 && out.PeriodStatus != pb.B266Status_B266_STATUS_SUBMITTED.String()

		// The blockers and the continuity comparison are computed as part
		// of generating the return, so they are only available once one has
		// been generated. Reading them off the stored period rather than
		// recomputing is what keeps this tool read-only.
		if out.PeriodExists {
			getCtx, e := guard(ctx, user, "/stillhouse.v1.B266Service/GetB266Period")
			if e != nil {
				return nil, nil, e
			}
			got, e := d.B266.GetB266Period(getCtx, connect.NewRequest(&pb.GetB266PeriodRequest{Id: out.PeriodID}))
			if e != nil {
				return errResult(e), nil, nil
			}
			if snap := got.Msg.GetSnapshot(); snap != nil {
				out.FilingBlockers = snap.GetFilingBlockers()
				if c := snap.GetContinuity(); c != nil {
					out.Continuity = &filingContinuity{
						Checked:                c.GetChecked(),
						PriorPeriodStart:       c.GetPriorPeriodStart(),
						PriorPeriodEnd:         c.GetPriorPeriodEnd(),
						PriorBulkClosingLAA:    c.GetPriorBulkClosingLaa(),
						BulkOpeningLAA:         c.GetBulkOpeningLaa(),
						BulkDiscrepancyLAA:     c.GetBulkDiscrepancyLaa(),
						PackagedDiscrepancyLAA: c.GetPackagedDiscrepancyLaa(),
						BackdatedEntryCount:    int32(len(c.GetBackdated())) + c.GetBackdatedTruncated(),
						BackdatedNetLAA:        c.GetBackdatedNetLaa(),
						Gap:                    c.GetGap(),
						GapNote:                c.GetGapNote(),
					}
				}
			}
		}

		// Losses awaiting a ruling, over the reviewed span.
		lossCtx, e := guard(ctx, user, "/stillhouse.v1.BulkService/ListLosses")
		if e != nil {
			return nil, nil, e
		}
		losses, e := d.Bulk.ListLosses(lossCtx, connect.NewRequest(&pb.ListLossesRequest{
			PeriodStart:      out.SuggestedPeriodStart,
			PeriodEnd:        out.SuggestedPeriodEnd,
			UnclassifiedOnly: true,
		}))
		if e != nil {
			return errResult(e), nil, nil
		}
		out.UnclassifiedLossCount = int32(len(losses.Msg.GetLosses()))
		out.UnclassifiedLossLAA = losses.Msg.GetUnclassifiedLaa()

		out.NextSteps, out.Note = filingNextSteps(&out)
		return jsonResultRaw(out), nil, nil
	})
}

// filingNextSteps orders the outstanding work. Ordering is the point: an
// operator told five things at once does none of them, and the five are
// not equally urgent or equally cheap.
//
// The order is deliberate. Losses come before generating, because
// generating before they are ruled on produces a return that has to be
// regenerated. Continuity comes last of the substantive steps because it
// concerns a return already filed and is the one that may need an
// amendment rather than an edit.
func filingNextSteps(o *filingReviewOutput) (steps []string, note string) {
	if o.UnclassifiedLossCount > 0 {
		steps = append(steps, fmt.Sprintf(
			"Rule on %d loss%s totalling %.4f LAA — each is either relieved or duty-payable (EDM3-4-1), and the return cannot be filed while any is unruled. Losses page in the web UI.",
			o.UnclassifiedLossCount, lossPlural(o.UnclassifiedLossCount), o.UnclassifiedLossLAA))
	}
	if !o.PeriodExists {
		steps = append(steps, fmt.Sprintf(
			"Generate the return for %s to %s. Nothing has been generated for this period yet, so there are no figures to check.",
			o.SuggestedPeriodStart, o.SuggestedPeriodEnd))
	}
	for _, b := range o.FilingBlockers {
		steps = append(steps, b)
	}
	if o.Overdue {
		steps = append(steps, fmt.Sprintf("This return was due %s and has not been submitted.", o.DueOn))
	}

	switch {
	case len(steps) == 0 && o.PeriodStatus == pb.B266Status_B266_STATUS_SUBMITTED.String():
		note = "Already submitted."
	case len(steps) == 0:
		note = "Nothing outstanding. That is not a promise the figures are right — only that nothing is missing. Check them against your own records before filing."
	default:
		note = "Stillhouse never files, and does not decide any of the above for you."
	}
	return steps, note
}

// lossPlural is the "es" ending that "loss" takes. Named for its ending
// rather than for pluralising in general — QA found a shared helper that
// only knew "es" appending it to "destruction", on a B266 filing blocker.
func lossPlural(n int32) string {
	if n == 1 {
		return ""
	}
	return "es"
}

// addListWorkOrders puts the job board on the MCP surface. PLAN J4.
//
// The read half of the work-order flow. Multi-row inputs are why most
// back-office writes stay in the web UI; a board is multi-row to READ,
// which chat handles perfectly well, and the one write it needs is a
// single field — see addSetWorkOrderStatus in tools_write.go.
func addListWorkOrders(s *mcpsdk.Server, d Deps, user sqlcgen.User) {
	type in struct {
		OpenOnly bool `json:"open_only,omitempty" jsonschema:"only jobs that are still planned or in progress"`
		Mine     bool `json:"mine,omitempty" jsonschema:"only jobs assigned to you"`
	}
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name: "list_work_orders",
		Description: "The job board: what is planned, what is in progress, who it is for and when it is scheduled. " +
			"Use open_only to see what is outstanding, mine to see your own.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx, err := guard(ctx, user, "/stillhouse.v1.WorkOrderService/ListWorkOrders")
		if err != nil {
			return nil, nil, err
		}
		req := &pb.ListWorkOrdersRequest{OpenOnly: args.OpenOnly}
		if args.Mine {
			req.AssignedTo = "me"
		}
		resp, err := d.WorkOrder.ListWorkOrders(ctx, connect.NewRequest(req))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}
