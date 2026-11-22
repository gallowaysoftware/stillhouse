package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	pb "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// registerWriteTools attaches the "things you do while standing at the
// still" tools — quick captures of an event that just happened. The
// underlying RPC services write audit-log rows so the journal still
// shows the actor, even when the call came via MCP.
//
// Figures the operator signs for (production gauges, bottling, removals,
// B266 generation) are deliberately not exposed here.
func registerWriteTools(s *registrar, d Deps, user sqlcgen.User) {
	addFillBarrel(s, d, user)
	addRegaugeBarrel(s, d, user)
	addDumpBarrel(s, d, user)
	addAddFermentationReading(s, d, user)
	addAddDistillationReading(s, d, user)
	addAddMashReading(s, d, user)
	addSaveRecipeVersionSensory(s, d, user)
	addSaveRecipeVersionWhiskySensory(s, d, user)
	addSetWorkOrderStatus(s, d, user)
	addStartMash(s, d, user)
	addAddMashIngredient(s, d, user)
	addSetMashStatus(s, d, user)
	addStartFermentation(s, d, user)
	addSetFermentationStatus(s, d, user)
	addStartDistillation(s, d, user)
	addSetDistillationStatus(s, d, user)
	addAddDistillationCharge(s, d, user)
	addAddDistillationCut(s, d, user)
	addRemoveEntry(s, d, user)
	addAddMaterial(s, d, user)
	addReceiveMaterial(s, d, user)
	addSaveRecipe(s, d, user)
}

func addFillBarrel(s *registrar, d Deps, user sqlcgen.User) {
	type in struct {
		BarrelID          string   `json:"barrel_id" jsonschema:"UUID of the barrel being filled"`
		SourceContainerID string   `json:"source_container_id" jsonschema:"UUID of the bulk container the spirit is coming from"`
		VolumeL           float64  `json:"volume_l" jsonschema:"volume transferred, in litres"`
		AbvPct            float64  `json:"abv_pct" jsonschema:"strength of the spirit going into the barrel as a percentage (0-100), at 20 °C. Ignored when density_kg_m3 is supplied"`
		Notes             string   `json:"notes,omitempty" jsonschema:"optional free-text notes"`
		TemperatureC      *float64 `json:"temperature_c,omitempty" jsonschema:"temperature the reading was taken at, in °C. Strength is only defined at 20 °C — supply this and Stillhouse resolves the reading through the Canadian Alcoholometric Tables 1980"`
		DensityKgM3       *float64 `json:"density_kg_m3,omitempty" jsonschema:"hydrometer indication in kg/m³ (CRA's approved instrument reads density, not %ABV). When supplied with temperature_c this determines the strength and abv_pct is ignored"`
	}
	addTool(s, &mcpsdk.Tool{
		Name:        "fill_barrel",
		Description: "Record a fill from a bulk container into a barrel. Updates both balances, writes a barrel event, sets the barrel's fill_date if this is its first fill (starting the maturation clock).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx, err := guard(ctx, user, "/stillhouse.v1.BarrelService/FillBarrel")
		if err != nil {
			return nil, nil, err
		}
		resp, err := d.Barrel.FillBarrel(ctx, connect.NewRequest(&pb.FillBarrelRequest{
			BarrelId:          args.BarrelID,
			SourceContainerId: args.SourceContainerID,
			VolumeL:           args.VolumeL,
			AbvPct:            args.AbvPct,
			Notes:             args.Notes,
			TemperatureC:      derefFloat(args.TemperatureC),
			TemperatureCSet:   args.TemperatureC != nil,
			DensityKgM3:       derefFloat(args.DensityKgM3),
			DensityKgM3Set:    args.DensityKgM3 != nil,
		}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

func addRegaugeBarrel(s *registrar, d Deps, user sqlcgen.User) {
	type in struct {
		BarrelID     string   `json:"barrel_id" jsonschema:"UUID of the barrel"`
		NewVolumeL   float64  `json:"new_volume_l" jsonschema:"measured current volume in litres; must be ≤ recorded current volume. Cannot be 0 if the barrel is non-empty — use dump_barrel for transfers"`
		NewAbvPct    float64  `json:"new_abv_pct" jsonschema:"measured current strength as a percentage (0-100), at 20 °C; resulting LAA must be ≤ recorded LAA. Ignored when density_kg_m3 is supplied"`
		Notes        string   `json:"notes,omitempty" jsonschema:"optional free-text notes"`
		TemperatureC *float64 `json:"temperature_c,omitempty" jsonschema:"temperature the reading was taken at, in °C. A warehouse is rarely at 20 °C — supply this and Stillhouse resolves the reading through the Canadian Alcoholometric Tables 1980"`
		DensityKgM3  *float64 `json:"density_kg_m3,omitempty" jsonschema:"hydrometer indication in kg/m³. When supplied with temperature_c this determines the strength and new_abv_pct is ignored"`
	}
	addTool(s, &mcpsdk.Tool{
		Name:        "regauge_barrel",
		Description: "Record actual barrel contents on inspection (e.g. after sampling or evaporation loss). The lost LAA is written to the journal as a loss_evaporation movement. Regauges can only record losses, not gains, and cannot zero out a non-empty barrel — use dump_barrel for that.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx, err := guard(ctx, user, "/stillhouse.v1.BarrelService/RegaugeBarrel")
		if err != nil {
			return nil, nil, err
		}
		resp, err := d.Barrel.RegaugeBarrel(ctx, connect.NewRequest(&pb.RegaugeBarrelRequest{
			BarrelId:        args.BarrelID,
			NewVolumeL:      args.NewVolumeL,
			NewAbvPct:       args.NewAbvPct,
			Notes:           args.Notes,
			TemperatureC:    derefFloat(args.TemperatureC),
			TemperatureCSet: args.TemperatureC != nil,
			DensityKgM3:     derefFloat(args.DensityKgM3),
			DensityKgM3Set:  args.DensityKgM3 != nil,
		}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

func addDumpBarrel(s *registrar, d Deps, user sqlcgen.User) {
	type in struct {
		BarrelID               string   `json:"barrel_id" jsonschema:"UUID of the barrel being dumped"`
		DestinationContainerID string   `json:"destination_container_id" jsonschema:"UUID of the bulk container (tank, blend tank, etc.) receiving the spirit"`
		VolumeL                float64  `json:"volume_l" jsonschema:"measured volume coming out of the barrel, in litres"`
		AbvPct                 float64  `json:"abv_pct" jsonschema:"measured strength at dump as a percentage (0-100), at 20 °C. Ignored when density_kg_m3 is supplied"`
		Notes                  string   `json:"notes,omitempty" jsonschema:"optional free-text notes"`
		TemperatureC           *float64 `json:"temperature_c,omitempty" jsonschema:"temperature the reading was taken at, in °C. Supply this and Stillhouse resolves the reading through the Canadian Alcoholometric Tables 1980"`
		DensityKgM3            *float64 `json:"density_kg_m3,omitempty" jsonschema:"hydrometer indication in kg/m³. When supplied with temperature_c this determines the strength and abv_pct is ignored"`
	}
	addTool(s, &mcpsdk.Tool{
		Name:        "dump_barrel",
		Description: "Empty a barrel into a destination bulk container. Captures days aged on the barrel attributes for downstream Canadian Whisky eligibility checks.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx, err := guard(ctx, user, "/stillhouse.v1.BarrelService/DumpBarrel")
		if err != nil {
			return nil, nil, err
		}
		resp, err := d.Barrel.DumpBarrel(ctx, connect.NewRequest(&pb.DumpBarrelRequest{
			BarrelId:               args.BarrelID,
			DestinationContainerId: args.DestinationContainerID,
			VolumeL:                args.VolumeL,
			AbvPct:                 args.AbvPct,
			Notes:                  args.Notes,
			TemperatureC:           derefFloat(args.TemperatureC),
			TemperatureCSet:        args.TemperatureC != nil,
			DensityKgM3:            derefFloat(args.DensityKgM3),
			DensityKgM3Set:         args.DensityKgM3 != nil,
		}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

func addAddFermentationReading(s *registrar, d Deps, user sqlcgen.User) {
	// Pointer fields so we can tell "operator measured 0" from "operator
	// didn't measure this." A literal 0 °C wort is a real (cold-crash)
	// reading; pH=0 is theoretical but possible; SG can't realistically
	// be 0 but the same shape applies uniformly.
	type in struct {
		FermentationRunID string   `json:"fermentation_run_id,omitempty" jsonschema:"the fermentation, by fermenter name, by 'mash 3', or UUID; empty when only one is open"`
		SpecificGravity   *float64 `json:"specific_gravity,omitempty" jsonschema:"e.g. 1.052 — omit if not taken"`
		PH                *float64 `json:"ph,omitempty" jsonschema:"e.g. 4.3 — omit if not taken"`
		TemperatureC      *float64 `json:"temperature_c,omitempty" jsonschema:"degrees Celsius — omit if not taken (0 is a valid reading)"`
		Notes             string   `json:"notes,omitempty" jsonschema:"optional free-text notes"`
		At                string   `json:"at,omitempty" jsonschema:"when it happened, RFC 3339 with offset; omit for now"`
		MinutesAgo        *float64 `json:"minutes_ago,omitempty" jsonschema:"how long ago it happened, in minutes; omit for now"`
	}
	addTool(s, &mcpsdk.Tool{
		Name:        "add_fermentation_reading",
		Description: "Log a point-in-time reading on an active fermentation: SG, pH, and/or temperature. Any field can be omitted if not measured; a literal 0 is treated as a measurement, not as 'omitted'.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx, err := guard(ctx, user, "/stillhouse.v1.FermentationService/AddFermentationLog")
		if err != nil {
			return nil, nil, err
		}
		fermentationID, err := resolveFermentation(ctx, d, user, args.FermentationRunID)
		if err != nil {
			return errResult(err), nil, nil
		}
		req := &pb.AddFermentationLogRequest{
			FermentationRunId: fermentationID,
			Notes:             args.Notes,
		}
		if args.SpecificGravity != nil {
			req.SpecificGravity = *args.SpecificGravity
			req.SpecificGravitySet = true
		}
		if args.PH != nil {
			req.Ph = *args.PH
			req.PhSet = true
		}
		if args.TemperatureC != nil {
			req.TemperatureC = *args.TemperatureC
			req.TemperatureCSet = true
		}
		observedAt, err := observedAt(args.At, args.MinutesAgo, time.Now())
		if err != nil {
			return errResult(err), nil, nil
		}
		req.ObservedAt = observedAt
		resp, err := d.Fermentation.AddFermentationLog(ctx, connect.NewRequest(req))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

func addAddMashReading(s *registrar, d Deps, user sqlcgen.User) {
	type in struct {
		MashRunID  string   `json:"mash_run_id,omitempty" jsonschema:"the mash, by number or UUID; empty when only one is open"`
		Kind       string   `json:"kind" jsonschema:"one of: original_gravity, mash_ph, mash_temp_c, water_volume_l, wash_volume_l, strike_temp_c, other"`
		Value      float64  `json:"value" jsonschema:"numeric reading; units implied by kind (or set via unit for 'other')"`
		Unit       string   `json:"unit,omitempty" jsonschema:"optional unit label, primarily for kind=other"`
		Notes      string   `json:"notes,omitempty" jsonschema:"optional free-text notes"`
		At         string   `json:"at,omitempty" jsonschema:"when it happened, RFC 3339 with offset; omit for now"`
		MinutesAgo *float64 `json:"minutes_ago,omitempty" jsonschema:"how long ago it happened, in minutes; omit for now"`
	}
	addTool(s, &mcpsdk.Tool{
		Name:        "add_mash_reading",
		Description: "Log a metric on an active mash: OG, pH, temperature, water volume, wash volume, strike temp, or other. Set kind to one of: original_gravity, mash_ph, mash_temp_c, water_volume_l, wash_volume_l, strike_temp_c, other. (value is required; 0 is a valid reading.) OG plus a water or wash volume unlocks the conversion-efficiency figure on get_mash.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx, err := guard(ctx, user, "/stillhouse.v1.MashService/AddMashMetric")
		if err != nil {
			return nil, nil, err
		}
		mashID, err := resolveMash(ctx, d, user, args.MashRunID)
		if err != nil {
			return errResult(err), nil, nil
		}
		kind, err := parseMashMetricKind(args.Kind)
		if err != nil {
			return errResult(err), nil, nil
		}
		observedAt, err := observedAt(args.At, args.MinutesAgo, time.Now())
		if err != nil {
			return errResult(err), nil, nil
		}
		resp, err := d.Mash.AddMashMetric(ctx, connect.NewRequest(&pb.AddMashMetricRequest{
			MashRunId:  mashID,
			Kind:       kind,
			Value:      args.Value,
			Unit:       args.Unit,
			Notes:      args.Notes,
			ObservedAt: observedAt,
		}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

// addSaveRecipeVersionSensory wires the gin tasting-bench write. Every
// pointer-typed score uses nil = "not measured on this axis", so a
// literal `0` is treated as a real reading (you can score floral=0 on
// a gin that has none and have it persist). Upsert semantics: the
// underlying RPC replaces the prior score row.
func addSaveRecipeVersionSensory(s *registrar, d Deps, user sqlcgen.User) {
	type in struct {
		RecipeVersionID string `json:"recipe_version_id" jsonschema:"UUID of the recipe version you just tasted"`
		Juniper         *int32 `json:"juniper,omitempty" jsonschema:"0-10; omit if not scored on this axis"`
		Citrus          *int32 `json:"citrus,omitempty" jsonschema:"0-10"`
		Herbal          *int32 `json:"herbal,omitempty" jsonschema:"0-10"`
		Spice           *int32 `json:"spice,omitempty" jsonschema:"0-10"`
		Floral          *int32 `json:"floral,omitempty" jsonschema:"0-10"`
		Earth           *int32 `json:"earth,omitempty" jsonschema:"0-10 (root / earthy notes)"`
		Body            *int32 `json:"body,omitempty" jsonschema:"0-10 (mouthfeel / weight)"`
		Heat            *int32 `json:"heat,omitempty" jsonschema:"0-10 (perceived ABV burn)"`
		Balance         *int32 `json:"balance,omitempty" jsonschema:"0-10 (how well the whole hangs together)"`
		Overall         *int32 `json:"overall,omitempty" jsonschema:"0-10 (gut-call rating)"`
		TastingPanel    string `json:"tasting_panel,omitempty" jsonschema:"who tasted — 'self', 'Kyle + Jane', etc."`
		Notes           string `json:"notes,omitempty" jsonschema:"optional — appended to the recipe version's tasting notes by future iterations; for now serves as a panel comment"`
	}
	addTool(s, &mcpsdk.Tool{
		Name:        "save_recipe_version_sensory",
		Description: "Score a gin recipe version on the 10-axis tasting bench (juniper / citrus / herbal / spice / floral / earth / body / heat / balance / overall, each 0-10). Upsert — running this replaces any prior scores for the same version. Use after distilling a small test batch and tasting; combine with get_recipe + list_recipe_versions to iterate.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx, err := guard(ctx, user, "/stillhouse.v1.RecipeService/SaveRecipeVersionSensory")
		if err != nil {
			return nil, nil, err
		}
		scores := &pb.GinSensoryScores{TastingPanel: args.TastingPanel}
		if args.Juniper != nil {
			scores.Juniper = *args.Juniper
			scores.JuniperSet = true
		}
		if args.Citrus != nil {
			scores.Citrus = *args.Citrus
			scores.CitrusSet = true
		}
		if args.Herbal != nil {
			scores.Herbal = *args.Herbal
			scores.HerbalSet = true
		}
		if args.Spice != nil {
			scores.Spice = *args.Spice
			scores.SpiceSet = true
		}
		if args.Floral != nil {
			scores.Floral = *args.Floral
			scores.FloralSet = true
		}
		if args.Earth != nil {
			scores.Earth = *args.Earth
			scores.EarthSet = true
		}
		if args.Body != nil {
			scores.Body = *args.Body
			scores.BodySet = true
		}
		if args.Heat != nil {
			scores.Heat = *args.Heat
			scores.HeatSet = true
		}
		if args.Balance != nil {
			scores.Balance = *args.Balance
			scores.BalanceSet = true
		}
		if args.Overall != nil {
			scores.Overall = *args.Overall
			scores.OverallSet = true
		}
		resp, err := d.Recipe.SaveRecipeVersionSensory(ctx, connect.NewRequest(&pb.SaveRecipeVersionSensoryRequest{
			RecipeVersionId: args.RecipeVersionID,
			Scores:          scores,
		}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

// addSaveRecipeVersionWhiskySensory — parallel to the gin tool, but
// the axes are the 8 SWRI Flavour Wheel primary classes plus body /
// finish / overall. Sulphury is primarily an off-note class (low score
// = clean spirit). Pointer-typed scores so a literal 0 means "scored
// zero," not "didn't score."
func addSaveRecipeVersionWhiskySensory(s *registrar, d Deps, user sqlcgen.User) {
	type in struct {
		RecipeVersionID string `json:"recipe_version_id" jsonschema:"UUID of the whisky-family recipe version you just tasted"`
		Cereal          *int32 `json:"cereal,omitempty"   jsonschema:"0-10 — porridge / husky / malt / biscuit / cracker"`
		Estery          *int32 `json:"estery,omitempty"   jsonschema:"0-10 — fruity esters: banana / pear-drop / apple / pineapple / citrus / dried fruit"`
		Floral          *int32 `json:"floral,omitempty"   jsonschema:"0-10 — geranium / rose / fragrant / honey"`
		Peaty           *int32 `json:"peaty,omitempty"    jsonschema:"0-10 — phenolic / smoky / medicinal / iodine / bonfire"`
		Feinty          *int32 `json:"feinty,omitempty"   jsonschema:"0-10 — leather / tobacco / honey-tobacco / horse / cheesy"`
		Sulphury        *int32 `json:"sulphury,omitempty" jsonschema:"0-10 — OFF-note class: rubbery / vegetative / sandy / gunflint / DMS. Low score = clean spirit"`
		Woody           *int32 `json:"woody,omitempty"    jsonschema:"0-10 — vanilla / toasted oak / resinous / coconut / sawdust"`
		Winey           *int32 `json:"winey,omitempty"    jsonschema:"0-10 — sherry / port / brandy notes (from finishing casks)"`
		Body            *int32 `json:"body,omitempty"     jsonschema:"0-10 — mouthfeel / weight / texture"`
		Finish          *int32 `json:"finish,omitempty"   jsonschema:"0-10 — length / persistence / dryness"`
		Overall         *int32 `json:"overall,omitempty"  jsonschema:"0-10 — gut-call quality / hedonic"`
		TastingPanel    string `json:"tasting_panel,omitempty" jsonschema:"who tasted — 'self', 'Kyle + Jane', etc."`
	}
	addTool(s, &mcpsdk.Tool{
		Name:        "save_recipe_version_whisky_sensory",
		Description: "Score a whisky-family (whisky / canadian_whisky / rye_whisky) recipe version on the 11-axis tasting bench. Axes are the 8 SWRI Flavour Wheel primary classes (cereal / estery / floral / peaty / feinty / sulphury / woody / winey) plus body / finish / overall from the standard panel scorecard. Each 0-10. Upsert with partial-update semantics — sending a single axis preserves the others. Sulphury is primarily an off-note class (low = clean).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx, err := guard(ctx, user, "/stillhouse.v1.RecipeService/SaveRecipeVersionWhiskySensory")
		if err != nil {
			return nil, nil, err
		}
		scores := &pb.WhiskySensoryScores{TastingPanel: args.TastingPanel}
		setInt := func(field *int32, src *int32, setFlag *bool) {
			if src != nil {
				*field = *src
				*setFlag = true
			}
		}
		setInt(&scores.Cereal, args.Cereal, &scores.CerealSet)
		setInt(&scores.Estery, args.Estery, &scores.EsterySet)
		setInt(&scores.Floral, args.Floral, &scores.FloralSet)
		setInt(&scores.Peaty, args.Peaty, &scores.PeatySet)
		setInt(&scores.Feinty, args.Feinty, &scores.FeintySet)
		setInt(&scores.Sulphury, args.Sulphury, &scores.SulphurySet)
		setInt(&scores.Woody, args.Woody, &scores.WoodySet)
		setInt(&scores.Winey, args.Winey, &scores.WineySet)
		setInt(&scores.Body, args.Body, &scores.BodySet)
		setInt(&scores.Finish, args.Finish, &scores.FinishSet)
		setInt(&scores.Overall, args.Overall, &scores.OverallSet)
		resp, err := d.Recipe.SaveRecipeVersionWhiskySensory(ctx, connect.NewRequest(&pb.SaveRecipeVersionWhiskySensoryRequest{
			RecipeVersionId: args.RecipeVersionID,
			Scores:          scores,
		}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

func parseMashMetricKind(s string) (pb.MashMetricKind, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "original_gravity", "og":
		return pb.MashMetricKind_MASH_METRIC_KIND_ORIGINAL_GRAVITY, nil
	case "mash_ph", "ph":
		return pb.MashMetricKind_MASH_METRIC_KIND_MASH_PH, nil
	case "mash_temp_c", "temp", "temp_c":
		return pb.MashMetricKind_MASH_METRIC_KIND_MASH_TEMP_C, nil
	case "water_volume_l", "water":
		return pb.MashMetricKind_MASH_METRIC_KIND_WATER_VOLUME_L, nil
	case "wash_volume_l", "wash":
		return pb.MashMetricKind_MASH_METRIC_KIND_WASH_VOLUME_L, nil
	case "strike_temp_c", "strike":
		return pb.MashMetricKind_MASH_METRIC_KIND_STRIKE_TEMP_C, nil
	case "other":
		return pb.MashMetricKind_MASH_METRIC_KIND_OTHER, nil
	}
	return 0, fmt.Errorf("unknown mash metric kind %q", s)
}

// derefFloat unwraps an optional numeric argument. The write tools use
// pointers so "not measured" stays distinguishable from a measured 0 —
// see addAddFermentationReading for why that distinction matters.
func derefFloat(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

// addSetWorkOrderStatus is the write half of PLAN J4's work-order flow.
//
// It is on this surface where SaveWorkOrder is not, and the difference is
// the shape of the input rather than the risk. Raising a job means a
// title, a kind, an assignee, a date and a location — five fields that a
// form asks for at once and a chat asks for one at a time, badly. Moving
// one along is a single field, and it is the half an operator does with
// wet hands: started this, finished that.
//
// Cancelling is the exception and takes a reason, because a cancelled job
// with no reason is indistinguishable next month from one nobody got to.
func addSetWorkOrderStatus(s *registrar, d Deps, user sqlcgen.User) {
	type in struct {
		WorkOrderID  string `json:"work_order_id" jsonschema:"UUID of the work order"`
		Status       string `json:"status" jsonschema:"one of: planned, in_progress, done, cancelled"`
		CancelReason string `json:"cancel_reason,omitempty" jsonschema:"why it was cancelled; required when status is cancelled"`
	}
	addTool(s, &mcpsdk.Tool{
		Name: "set_work_order_status",
		Description: "Move a job along: planned, in_progress, done, or cancelled. " +
			"Cancelling needs a reason — a cancelled job with none reads next month like one nobody got to.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx, err := guard(ctx, user, "/stillhouse.v1.WorkOrderService/SetWorkOrderStatus")
		if err != nil {
			return nil, nil, err
		}
		status, err := workOrderStatusFromName(args.Status)
		if err != nil {
			return errResult(err), nil, nil
		}
		resp, err := d.WorkOrder.SetWorkOrderStatus(ctx, connect.NewRequest(&pb.SetWorkOrderStatusRequest{
			Id:           args.WorkOrderID,
			Status:       status,
			CancelReason: args.CancelReason,
		}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

// workOrderStatusFromName maps the words an operator would say.
//
// Deliberately no default: an unrecognised status must not quietly become
// "planned", which would move a finished job backwards.
func workOrderStatusFromName(s string) (pb.WorkOrderStatus, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "planned":
		return pb.WorkOrderStatus_WORK_ORDER_STATUS_PLANNED, nil
	case "in_progress", "in progress", "started":
		return pb.WorkOrderStatus_WORK_ORDER_STATUS_IN_PROGRESS, nil
	case "done", "finished", "complete", "completed":
		return pb.WorkOrderStatus_WORK_ORDER_STATUS_DONE, nil
	case "cancelled", "canceled":
		return pb.WorkOrderStatus_WORK_ORDER_STATUS_CANCELLED, nil
	}
	return pb.WorkOrderStatus_WORK_ORDER_STATUS_UNSPECIFIED,
		fmt.Errorf("%q is not a work-order status; use planned, in_progress, done or cancelled", s)
}

func addAddDistillationReading(s *registrar, d Deps, user sqlcgen.User) {
	// Pointer fields for the same reason as the fermentation reading: a
	// literal 0 is a measurement, an absent field is one not taken.
	type in struct {
		DistillationRunID      string   `json:"distillation_run_id,omitempty" jsonschema:"the still run, by number or UUID; empty when only one is open"`
		DistillateAbvPct       *float64 `json:"distillate_abv_pct,omitempty" jsonschema:"strength at the parrot as read, percent (0-100), at whatever temperature the parrot is at — not corrected to 20 °C"`
		DistillateTemperatureC *float64 `json:"distillate_temperature_c,omitempty" jsonschema:"temperature of the spirit at the parrot, °C"`
		HeatSettingPct         *float64 `json:"heat_setting_pct,omitempty" jsonschema:"the element dial or jacket valve as a percentage of full (0-100)"`
		PotTemperatureC        *float64 `json:"pot_temperature_c,omitempty" jsonschema:"pot thermometer, °C"`
		VapourTemperatureC     *float64 `json:"vapour_temperature_c,omitempty" jsonschema:"vapour (head or neck) thermometer, °C"`
		Notes                  string   `json:"notes,omitempty" jsonschema:"optional free-text notes — what the glass says, a cut taken"`
		At                     string   `json:"at,omitempty" jsonschema:"when it happened, RFC 3339 with offset; omit for now"`
		MinutesAgo             *float64 `json:"minutes_ago,omitempty" jsonschema:"how long ago it happened, in minutes; omit for now"`
	}
	addTool(s, &mcpsdk.Tool{
		Name:        "add_distillation_reading",
		Description: "Log a point-in-time reading during a distillation run: strength at the parrot as read, its temperature, the heat setting, pot and vapour temperatures, notes. Any field can be omitted if not measured; a literal 0 is a measurement. These readings are the run's log and what calibrates the still's planner.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx, err := guard(ctx, user, "/stillhouse.v1.DistillationService/AddDistillationReading")
		if err != nil {
			return nil, nil, err
		}
		distillationID, err := resolveDistillation(ctx, d, user, args.DistillationRunID)
		if err != nil {
			return errResult(err), nil, nil
		}
		req := &pb.AddDistillationReadingRequest{
			DistillationRunId: distillationID,
			Notes:             args.Notes,
		}
		if args.DistillateAbvPct != nil {
			req.DistillateAbvPct, req.DistillateAbvPctSet = *args.DistillateAbvPct, true
		}
		if args.DistillateTemperatureC != nil {
			req.DistillateTemperatureC, req.DistillateTemperatureCSet = *args.DistillateTemperatureC, true
		}
		if args.HeatSettingPct != nil {
			req.HeatSettingPct, req.HeatSettingPctSet = *args.HeatSettingPct, true
		}
		if args.PotTemperatureC != nil {
			req.PotTemperatureC, req.PotTemperatureCSet = *args.PotTemperatureC, true
		}
		if args.VapourTemperatureC != nil {
			req.VapourTemperatureC, req.VapourTemperatureCSet = *args.VapourTemperatureC, true
		}
		observedAt, err := observedAt(args.At, args.MinutesAgo, time.Now())
		if err != nil {
			return errResult(err), nil, nil
		}
		req.ObservedAt = observedAt
		resp, err := d.Distillation.AddDistillationReading(ctx, connect.NewRequest(req))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

func addStartMash(s *registrar, d Deps, user sqlcgen.User) {
	type in struct {
		Recipe   string `json:"recipe" jsonschema:"the recipe, by name (its current version) or a recipe version UUID"`
		MashDate string `json:"mash_date" jsonschema:"the local date, YYYY-MM-DD"`
		Notes    string `json:"notes,omitempty" jsonschema:"optional free-text notes"`
	}
	addTool(s, &mcpsdk.Tool{
		Name:        "start_mash",
		Description: "Open a mash run against a saved recipe version. It starts planned and empty: add the grain actually weighed with add_mash_ingredient, then move it along with set_mash_status.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx, err := guard(ctx, user, "/stillhouse.v1.MashService/CreateMashRun")
		if err != nil {
			return nil, nil, err
		}
		if args.MashDate == "" {
			return errResult(errors.New("mash_date is required: the server's own date is UTC, which is already tomorrow on an evening in Canada")), nil, nil
		}
		versionID, err := resolveRecipeVersion(ctx, d, user, args.Recipe)
		if err != nil {
			return errResult(err), nil, nil
		}
		resp, err := d.Mash.CreateMashRun(ctx, connect.NewRequest(&pb.CreateMashRunRequest{
			RecipeVersionId: versionID,
			MashDate:        args.MashDate,
			Notes:           args.Notes,
		}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

func addAddMashIngredient(s *registrar, d Deps, user sqlcgen.User) {
	type in struct {
		MashRunID    string  `json:"mash_run_id,omitempty" jsonschema:"the mash, by number or UUID; empty when only one is open"`
		Material     string  `json:"material" jsonschema:"the material, by name or UUID"`
		QuantityUsed float64 `json:"quantity_used" jsonschema:"quantity actually used, in uom"`
		Uom          string  `json:"uom" jsonschema:"unit of the quantity; use the material's own unit, since the lot is debited by this number as given"`
		Lot          string  `json:"lot,omitempty" jsonschema:"the lot it came from, by supplier lot number or UUID; omit only if no lot was received"`
		Notes        string  `json:"notes,omitempty" jsonschema:"optional free-text notes"`
	}
	addTool(s, &mcpsdk.Tool{
		Name:        "add_mash_ingredient",
		Description: "Record material that went into a mash, as weighed. Name a lot and its on-hand quantity is debited in the same step; remove_entry undoes both.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx, err := guard(ctx, user, "/stillhouse.v1.MashService/AddMashIngredient")
		if err != nil {
			return nil, nil, err
		}
		mashID, err := resolveMash(ctx, d, user, args.MashRunID)
		if err != nil {
			return errResult(err), nil, nil
		}
		materialID, err := resolveMaterial(ctx, d, user, args.Material)
		if err != nil {
			return errResult(err), nil, nil
		}
		lotID, err := resolveLot(ctx, d, user, materialID, args.Lot)
		if err != nil {
			return errResult(err), nil, nil
		}
		if lotID != "" {
			if err := requireOwnUnit(ctx, d, user, materialID, args.Uom); err != nil {
				return errResult(err), nil, nil
			}
		}
		resp, err := d.Mash.AddMashIngredient(ctx, connect.NewRequest(&pb.AddMashIngredientRequest{
			MashRunId:     mashID,
			MaterialId:    materialID,
			QuantityUsed:  args.QuantityUsed,
			Uom:           args.Uom,
			MaterialLotId: lotID,
			Notes:         args.Notes,
		}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

func addSetMashStatus(s *registrar, d Deps, user sqlcgen.User) {
	type in struct {
		MashRunID string `json:"mash_run_id,omitempty" jsonschema:"the mash, by number or UUID; empty when only one is open"`
		Status    string `json:"status" jsonschema:"one of: planned, in_progress, fermenting, distilled, cancelled"`
	}
	addTool(s, &mcpsdk.Tool{
		Name:        "set_mash_status",
		Description: "Move a mash along: planned, in_progress, fermenting, distilled, or cancelled.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx, err := guard(ctx, user, "/stillhouse.v1.MashService/UpdateMashStatus")
		if err != nil {
			return nil, nil, err
		}
		mashID, err := resolveMash(ctx, d, user, args.MashRunID)
		if err != nil {
			return errResult(err), nil, nil
		}
		status, err := enumFromName[pb.MashStatus](args.Status)
		if err != nil {
			return errResult(err), nil, nil
		}
		resp, err := d.Mash.UpdateMashStatus(ctx, connect.NewRequest(&pb.UpdateMashStatusRequest{
			Id:     mashID,
			Status: status,
		}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

func addStartFermentation(s *registrar, d Deps, user sqlcgen.User) {
	type in struct {
		MashRunID          string   `json:"mash_run_id,omitempty" jsonschema:"the mash, by number or UUID; empty when only one is open"`
		FermenterLabel     string   `json:"fermenter_label" jsonschema:"the fermenter, as it is labelled on the floor"`
		InitialVolumeL     *float64 `json:"initial_volume_l,omitempty" jsonschema:"volume in the fermenter in litres — omit if not measured"`
		Yeast              string   `json:"yeast,omitempty" jsonschema:"the yeast material, by name or UUID"`
		YeastLot           string   `json:"yeast_lot,omitempty" jsonschema:"the yeast lot, by supplier lot number or UUID"`
		YeastNotes         string   `json:"yeast_notes,omitempty" jsonschema:"pitch rate, rehydration, anything about the yeast"`
		TargetFinalGravity *float64 `json:"target_final_gravity,omitempty" jsonschema:"e.g. 0.998 — omit if none"`
		Notes              string   `json:"notes,omitempty" jsonschema:"optional free-text notes"`
		At                 string   `json:"at,omitempty" jsonschema:"when it happened, RFC 3339 with offset; omit for now"`
		MinutesAgo         *float64 `json:"minutes_ago,omitempty" jsonschema:"how long ago it happened, in minutes; omit for now"`
	}
	addTool(s, &mcpsdk.Tool{
		Name:        "start_fermentation",
		Description: "Pitch a fermenter from a mash: opens a fermentation run, pitched now unless at or minutes_ago says otherwise. Naming a yeast lot records it for traceability; it does not debit the lot.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx, err := guard(ctx, user, "/stillhouse.v1.FermentationService/CreateFermentationRun")
		if err != nil {
			return nil, nil, err
		}
		mashID, err := resolveMash(ctx, d, user, args.MashRunID)
		if err != nil {
			return errResult(err), nil, nil
		}
		var yeastID, yeastLotID string
		if args.Yeast != "" {
			if yeastID, err = resolveMaterial(ctx, d, user, args.Yeast); err != nil {
				return errResult(err), nil, nil
			}
		}
		if args.YeastLot != "" {
			if yeastID == "" {
				return errResult(errors.New("name the yeast as well as its lot")), nil, nil
			}
			if yeastLotID, err = resolveLot(ctx, d, user, yeastID, args.YeastLot); err != nil {
				return errResult(err), nil, nil
			}
		}
		pitchAt, err := observedAt(args.At, args.MinutesAgo, time.Now())
		if err != nil {
			return errResult(err), nil, nil
		}
		resp, err := d.Fermentation.CreateFermentationRun(ctx, connect.NewRequest(&pb.CreateFermentationRunRequest{
			MashRunId:             mashID,
			FermenterLabel:        args.FermenterLabel,
			InitialVolumeL:        derefFloat(args.InitialVolumeL),
			InitialVolumeLSet:     args.InitialVolumeL != nil,
			YeastMaterialId:       yeastID,
			YeastLotId:            yeastLotID,
			YeastNotes:            args.YeastNotes,
			TargetFinalGravity:    derefFloat(args.TargetFinalGravity),
			TargetFinalGravitySet: args.TargetFinalGravity != nil,
			Notes:                 args.Notes,
			PitchAt:               pitchAt,
		}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

func addSetFermentationStatus(s *registrar, d Deps, user sqlcgen.User) {
	type in struct {
		FermentationRunID string `json:"fermentation_run_id,omitempty" jsonschema:"the fermentation, by fermenter name, by 'mash 3', or UUID; empty when only one is open"`
		Status            string `json:"status" jsonschema:"one of: pitched, active, finished, distilled, cancelled"`
	}
	addTool(s, &mcpsdk.Tool{
		Name:        "set_fermentation_status",
		Description: "Move a fermentation along: pitched, active, finished, distilled, or cancelled.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx, err := guard(ctx, user, "/stillhouse.v1.FermentationService/UpdateFermentationStatus")
		if err != nil {
			return nil, nil, err
		}
		fermentationID, err := resolveFermentation(ctx, d, user, args.FermentationRunID)
		if err != nil {
			return errResult(err), nil, nil
		}
		status, err := enumFromName[pb.FermentationStatus](args.Status)
		if err != nil {
			return errResult(err), nil, nil
		}
		resp, err := d.Fermentation.UpdateFermentationStatus(ctx, connect.NewRequest(&pb.UpdateFermentationStatusRequest{
			Id:     fermentationID,
			Status: status,
		}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}
