package mcp

import (
	"context"
	"fmt"
	"strings"

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
// More complex writes (distillation runs with cut arrays, bottling
// runs, removals, B266 generation) are deliberately not exposed here —
// they're better done at a laptop with the web UI.
func registerWriteTools(s *mcpsdk.Server, d Deps, user sqlcgen.User) {
	addFillBarrel(s, d, user)
	addRegaugeBarrel(s, d, user)
	addDumpBarrel(s, d, user)
	addAddFermentationReading(s, d, user)
	addAddMashReading(s, d, user)
	addSaveRecipeVersionSensory(s, d, user)
}

func addFillBarrel(s *mcpsdk.Server, d Deps, user sqlcgen.User) {
	type in struct {
		BarrelID          string  `json:"barrel_id" jsonschema:"UUID of the barrel being filled"`
		SourceContainerID string  `json:"source_container_id" jsonschema:"UUID of the bulk container the spirit is coming from"`
		VolumeL           float64 `json:"volume_l" jsonschema:"volume transferred, in litres"`
		AbvPct            float64 `json:"abv_pct" jsonschema:"ABV of the spirit going into the barrel, as a percentage (0-100)"`
		Notes             string  `json:"notes,omitempty" jsonschema:"optional free-text notes"`
	}
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "fill_barrel",
		Description: "Record a fill from a bulk container into a barrel. Updates both balances, writes a barrel event, sets the barrel's fill_date if this is its first fill (starting the maturation clock).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx = withUser(ctx, user)
		resp, err := d.Barrel.FillBarrel(ctx, connect.NewRequest(&pb.FillBarrelRequest{
			BarrelId:          args.BarrelID,
			SourceContainerId: args.SourceContainerID,
			VolumeL:           args.VolumeL,
			AbvPct:            args.AbvPct,
			Notes:             args.Notes,
		}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

func addRegaugeBarrel(s *mcpsdk.Server, d Deps, user sqlcgen.User) {
	type in struct {
		BarrelID    string  `json:"barrel_id" jsonschema:"UUID of the barrel"`
		NewVolumeL  float64 `json:"new_volume_l" jsonschema:"measured current volume in litres; must be ≤ recorded current volume. Cannot be 0 if the barrel is non-empty — use dump_barrel for transfers"`
		NewAbvPct   float64 `json:"new_abv_pct" jsonschema:"measured current ABV percentage (0-100); resulting LAA must be ≤ recorded LAA"`
		Notes       string  `json:"notes,omitempty" jsonschema:"optional free-text notes"`
	}
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "regauge_barrel",
		Description: "Record actual barrel contents on inspection (e.g. after sampling or evaporation loss). The lost LAA is written to the journal as a loss_evaporation movement. Regauges can only record losses, not gains, and cannot zero out a non-empty barrel — use dump_barrel for that.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx = withUser(ctx, user)
		resp, err := d.Barrel.RegaugeBarrel(ctx, connect.NewRequest(&pb.RegaugeBarrelRequest{
			BarrelId:   args.BarrelID,
			NewVolumeL: args.NewVolumeL,
			NewAbvPct:  args.NewAbvPct,
			Notes:      args.Notes,
		}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

func addDumpBarrel(s *mcpsdk.Server, d Deps, user sqlcgen.User) {
	type in struct {
		BarrelID               string  `json:"barrel_id" jsonschema:"UUID of the barrel being dumped"`
		DestinationContainerID string  `json:"destination_container_id" jsonschema:"UUID of the bulk container (tank, blend tank, etc.) receiving the spirit"`
		VolumeL                float64 `json:"volume_l" jsonschema:"measured volume coming out of the barrel, in litres"`
		AbvPct                 float64 `json:"abv_pct" jsonschema:"measured ABV at dump, percentage (0-100)"`
		Notes                  string  `json:"notes,omitempty" jsonschema:"optional free-text notes"`
	}
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "dump_barrel",
		Description: "Empty a barrel into a destination bulk container. Captures days aged on the barrel attributes for downstream Canadian Whisky eligibility checks.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx = withUser(ctx, user)
		resp, err := d.Barrel.DumpBarrel(ctx, connect.NewRequest(&pb.DumpBarrelRequest{
			BarrelId:               args.BarrelID,
			DestinationContainerId: args.DestinationContainerID,
			VolumeL:                args.VolumeL,
			AbvPct:                 args.AbvPct,
			Notes:                  args.Notes,
		}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

func addAddFermentationReading(s *mcpsdk.Server, d Deps, user sqlcgen.User) {
	// Pointer fields so we can tell "operator measured 0" from "operator
	// didn't measure this." A literal 0 °C wort is a real (cold-crash)
	// reading; pH=0 is theoretical but possible; SG can't realistically
	// be 0 but the same shape applies uniformly.
	type in struct {
		FermentationRunID string   `json:"fermentation_run_id" jsonschema:"UUID of the active fermentation run"`
		SpecificGravity   *float64 `json:"specific_gravity,omitempty" jsonschema:"e.g. 1.052 — omit if not taken"`
		PH                *float64 `json:"ph,omitempty" jsonschema:"e.g. 4.3 — omit if not taken"`
		TemperatureC      *float64 `json:"temperature_c,omitempty" jsonschema:"degrees Celsius — omit if not taken (0 is a valid reading)"`
		Notes             string   `json:"notes,omitempty" jsonschema:"optional free-text notes"`
	}
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "add_fermentation_reading",
		Description: "Log a point-in-time reading on an active fermentation: SG, pH, and/or temperature. Any field can be omitted if not measured; a literal 0 is treated as a measurement, not as 'omitted'.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx = withUser(ctx, user)
		req := &pb.AddFermentationLogRequest{
			FermentationRunId: args.FermentationRunID,
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
		resp, err := d.Fermentation.AddFermentationLog(ctx, connect.NewRequest(req))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

func addAddMashReading(s *mcpsdk.Server, d Deps, user sqlcgen.User) {
	type in struct {
		MashRunID string  `json:"mash_run_id" jsonschema:"UUID of the active mash run"`
		Kind      string  `json:"kind" jsonschema:"one of: original_gravity, mash_ph, mash_temp_c, water_volume_l, strike_temp_c, other"`
		Value     float64 `json:"value" jsonschema:"numeric reading; units implied by kind (or set via unit for 'other')"`
		Unit      string  `json:"unit,omitempty" jsonschema:"optional unit label, primarily for kind=other"`
		Notes     string  `json:"notes,omitempty" jsonschema:"optional free-text notes"`
	}
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "add_mash_reading",
		Description: "Log a metric on an active mash: OG, pH, temperature, water volume, strike temp, or other. Set kind to one of: original_gravity, mash_ph, mash_temp_c, water_volume_l, strike_temp_c, other. (value is required; 0 is a valid reading.)",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx = withUser(ctx, user)
		kind, err := parseMashMetricKind(args.Kind)
		if err != nil {
			return errResult(err), nil, nil
		}
		resp, err := d.Mash.AddMashMetric(ctx, connect.NewRequest(&pb.AddMashMetricRequest{
			MashRunId: args.MashRunID,
			Kind:      kind,
			Value:     args.Value,
			Unit:      args.Unit,
			Notes:     args.Notes,
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
func addSaveRecipeVersionSensory(s *mcpsdk.Server, d Deps, user sqlcgen.User) {
	type in struct {
		RecipeVersionID string   `json:"recipe_version_id" jsonschema:"UUID of the recipe version you just tasted"`
		Juniper         *int32   `json:"juniper,omitempty" jsonschema:"0-10; omit if not scored on this axis"`
		Citrus          *int32   `json:"citrus,omitempty" jsonschema:"0-10"`
		Herbal          *int32   `json:"herbal,omitempty" jsonschema:"0-10"`
		Spice           *int32   `json:"spice,omitempty" jsonschema:"0-10"`
		Floral          *int32   `json:"floral,omitempty" jsonschema:"0-10"`
		Earth           *int32   `json:"earth,omitempty" jsonschema:"0-10 (root / earthy notes)"`
		Body            *int32   `json:"body,omitempty" jsonschema:"0-10 (mouthfeel / weight)"`
		Heat            *int32   `json:"heat,omitempty" jsonschema:"0-10 (perceived ABV burn)"`
		Balance         *int32   `json:"balance,omitempty" jsonschema:"0-10 (how well the whole hangs together)"`
		Overall         *int32   `json:"overall,omitempty" jsonschema:"0-10 (gut-call rating)"`
		TastingPanel    string   `json:"tasting_panel,omitempty" jsonschema:"who tasted — 'self', 'Kyle + Jane', etc."`
		Notes           string   `json:"notes,omitempty" jsonschema:"optional — appended to the recipe version's tasting notes by future iterations; for now serves as a panel comment"`
	}
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name: "save_recipe_version_sensory",
		Description: "Score a gin recipe version on the 10-axis tasting bench (juniper / citrus / herbal / spice / floral / earth / body / heat / balance / overall, each 0-10). Upsert — running this replaces any prior scores for the same version. Use after distilling a small test batch and tasting; combine with get_recipe + list_recipe_versions to iterate.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx = withUser(ctx, user)
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
	case "strike_temp_c", "strike":
		return pb.MashMetricKind_MASH_METRIC_KIND_STRIKE_TEMP_C, nil
	case "other":
		return pb.MashMetricKind_MASH_METRIC_KIND_OTHER, nil
	}
	return 0, fmt.Errorf("unknown mash metric kind %q", s)
}
