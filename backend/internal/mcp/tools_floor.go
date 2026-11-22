package mcp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	pb "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

type floorRun struct {
	ID      string `json:"id"`
	Ref     string `json:"ref"`
	Recipe  string `json:"recipe,omitempty"`
	Date    string `json:"date,omitempty"`
	Started string `json:"started,omitempty"`
	Status  string `json:"status"`
}

type floorLot struct {
	Lot    string  `json:"lot"`
	OnHand float64 `json:"on_hand"`
}

type floorMaterial struct {
	Name string     `json:"name"`
	Kind string     `json:"kind"`
	Uom  string     `json:"uom"`
	Lots []floorLot `json:"lots"`
}

type floorOutput struct {
	NowUTC        string          `json:"now_utc"`
	Mashes        []floorRun      `json:"open_mashes"`
	Fermentations []floorRun      `json:"open_fermentations"`
	StillRuns     []floorRun      `json:"open_still_runs"`
	Recipes       []string        `json:"recipes"`
	Materials     []floorMaterial `json:"materials"`
}

func addGetFloor(s *registrar, d Deps, user sqlcgen.User) {
	type in struct{}
	addTool(s, &mcpsdk.Tool{
		Name:        "get_floor",
		Description: "What is open right now and what it is called: mashes, fermentations and still runs in progress, the recipes, and the materials with their lots on hand. Call this first.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ in) (*mcpsdk.CallToolResult, any, error) {
		ctx, err := guard(ctx, user, "/stillhouse.v1.MashService/ListMashRuns")
		if err != nil {
			return nil, nil, err
		}
		out := floorOutput{
			NowUTC: time.Now().UTC().Format(time.RFC3339),
			Mashes: []floorRun{}, Fermentations: []floorRun{}, StillRuns: []floorRun{},
			Recipes: []string{}, Materials: []floorMaterial{},
		}
		mashes, err := d.Mash.ListMashRuns(ctx, connect.NewRequest(&pb.ListMashRunsRequest{}))
		if err != nil {
			return errResult(err), nil, nil
		}
		for _, m := range mashes.Msg.GetMashRuns() {
			if mashOpen(m) {
				out.Mashes = append(out.Mashes, floorRun{ID: m.GetId(), Ref: fmt.Sprintf("mash %d", m.GetMashNo()),
					Recipe: m.GetRecipeName(), Date: m.GetMashDate(), Status: m.GetStatus().String()})
			}
		}
		if ctx, err = guard(ctx, user, "/stillhouse.v1.FermentationService/ListFermentationRuns"); err != nil {
			return nil, nil, err
		}
		ferments, err := d.Fermentation.ListFermentationRuns(ctx, connect.NewRequest(&pb.ListFermentationRunsRequest{}))
		if err != nil {
			return errResult(err), nil, nil
		}
		for _, f := range ferments.Msg.GetRuns() {
			if fermentationOpen(f) {
				out.Fermentations = append(out.Fermentations, floorRun{ID: f.GetId(), Ref: f.GetFermenterLabel(),
					Recipe:  fmt.Sprintf("%s (mash %d)", f.GetRecipeName(), f.GetMashNo()),
					Started: f.GetPitchAt().AsTime().Format(time.RFC3339), Status: f.GetStatus().String()})
			}
		}
		if ctx, err = guard(ctx, user, "/stillhouse.v1.DistillationService/ListDistillationRuns"); err != nil {
			return nil, nil, err
		}
		runs, err := d.Distillation.ListDistillationRuns(ctx, connect.NewRequest(&pb.ListDistillationRunsRequest{}))
		if err != nil {
			return errResult(err), nil, nil
		}
		for _, r := range runs.Msg.GetRuns() {
			if distillationOpen(r) {
				out.StillRuns = append(out.StillRuns, floorRun{ID: r.GetId(), Ref: fmt.Sprintf("run %d (%s)", r.GetRunNo(), r.GetStillLabel()),
					Date: r.GetRunDate(), Status: r.GetStatus().String()})
			}
		}
		if ctx, err = guard(ctx, user, "/stillhouse.v1.RecipeService/ListRecipes"); err != nil {
			return nil, nil, err
		}
		recipes, err := d.Recipe.ListRecipes(ctx, connect.NewRequest(&pb.ListRecipesRequest{}))
		if err != nil {
			return errResult(err), nil, nil
		}
		for _, r := range recipes.Msg.GetRecipes() {
			if r.GetCurrentVersionId() != "" {
				out.Recipes = append(out.Recipes, r.GetName())
			}
		}
		if ctx, err = guard(ctx, user, "/stillhouse.v1.MaterialService/ListMaterials"); err != nil {
			return nil, nil, err
		}
		materials, err := d.Material.ListMaterials(ctx, connect.NewRequest(&pb.ListMaterialsRequest{}))
		if err != nil {
			return errResult(err), nil, nil
		}
		if ctx, err = guard(ctx, user, "/stillhouse.v1.MaterialService/ListMaterialLots"); err != nil {
			return nil, nil, err
		}
		lots, err := d.Material.ListMaterialLots(ctx, connect.NewRequest(&pb.ListMaterialLotsRequest{OnHandOnly: true}))
		if err != nil {
			return errResult(err), nil, nil
		}
		byMaterial := map[string][]floorLot{}
		for _, l := range lots.Msg.GetLots() {
			byMaterial[l.GetMaterialId()] = append(byMaterial[l.GetMaterialId()], floorLot{l.GetSupplierLot(), l.GetQuantityOnHand()})
		}
		for _, m := range materials.Msg.GetMaterials() {
			out.Materials = append(out.Materials, floorMaterial{m.GetName(), m.GetKind().String(), m.GetUom(), byMaterial[m.GetId()]})
		}
		return jsonResultRaw(out), nil, nil
	})
}

func addStartDistillation(s *registrar, d Deps, user sqlcgen.User) {
	type in struct {
		StillLabel string `json:"still_label" jsonschema:"the still, as it is labelled on the floor"`
		RunDate    string `json:"run_date" jsonschema:"the local date, YYYY-MM-DD"`
		Notes      string `json:"notes,omitempty" jsonschema:"optional free-text notes"`
	}
	addTool(s, &mcpsdk.Tool{
		Name:        "start_distillation",
		Description: "Open a still run. Charge it with add_distillation_charge, log it with add_distillation_reading, and take cuts with add_distillation_cut. The production gauge that puts the spirit in the ledger is recorded in the web UI.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx, err := guard(ctx, user, "/stillhouse.v1.DistillationService/CreateDistillationRun")
		if err != nil {
			return nil, nil, err
		}
		if args.RunDate == "" {
			return errResult(errors.New("run_date is required: the server's own date is UTC, which is already tomorrow on an evening in Canada")), nil, nil
		}
		resp, err := d.Distillation.CreateDistillationRun(ctx, connect.NewRequest(&pb.CreateDistillationRunRequest{
			StillLabel: args.StillLabel, RunDate: args.RunDate, Notes: args.Notes,
		}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

func addSetDistillationStatus(s *registrar, d Deps, user sqlcgen.User) {
	type in struct {
		DistillationRunID string `json:"distillation_run_id,omitempty" jsonschema:"the still run, by number or UUID; empty when only one is open"`
		Status            string `json:"status" jsonschema:"one of: planned, charging, distilling, cancelled"`
	}
	addTool(s, &mcpsdk.Tool{
		Name:        "set_distillation_status",
		Description: "Move a still run along: planned, charging, distilling, or cancelled. It becomes gauged only by recording the production gauge in the web UI.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx, err := guard(ctx, user, "/stillhouse.v1.DistillationService/UpdateDistillationStatus")
		if err != nil {
			return nil, nil, err
		}
		status, err := enumFromName[pb.DistillationStatus](args.Status)
		if err != nil {
			return errResult(err), nil, nil
		}
		if status == pb.DistillationStatus_DISTILLATION_STATUS_GAUGED {
			return errResult(errors.New("a run is gauged by recording its production gauge in the web UI")), nil, nil
		}
		id, err := resolveDistillation(ctx, d, user, args.DistillationRunID)
		if err != nil {
			return errResult(err), nil, nil
		}
		// A gauged run moved back to distilling could be gauged a second
		// time, so a run that has one does not move from here.
		if err := requireUngauged(ctx, d, user, id); err != nil {
			return errResult(err), nil, nil
		}
		resp, err := d.Distillation.UpdateDistillationStatus(ctx, connect.NewRequest(&pb.UpdateDistillationStatusRequest{Id: id, Status: status}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

func readDistillation(ctx context.Context, d Deps, user sqlcgen.User, id string) (*pb.DistillationRun, error) {
	ctx, err := guard(ctx, user, "/stillhouse.v1.DistillationService/GetDistillationRun")
	if err != nil {
		return nil, err
	}
	resp, err := d.Distillation.GetDistillationRun(ctx, connect.NewRequest(&pb.GetDistillationRunRequest{Id: id}))
	if err != nil {
		return nil, err
	}
	return resp.Msg.GetRun(), nil
}

func requireUngauged(ctx context.Context, d Deps, user sqlcgen.User, id string) error {
	run, err := readDistillation(ctx, d, user, id)
	if err != nil {
		return err
	}
	if run.GetGauge() != nil || run.GetStatus() == pb.DistillationStatus_DISTILLATION_STATUS_GAUGED {
		return errors.New("that run has been gauged; change it in the web UI")
	}
	return nil
}

func addAddDistillationCharge(s *registrar, d Deps, user sqlcgen.User) {
	type in struct {
		DistillationRunID string  `json:"distillation_run_id,omitempty" jsonschema:"the still run, by number or UUID; empty when only one is open"`
		FermentationRunID string  `json:"fermentation_run_id,omitempty" jsonschema:"the fermentation charged, by fermenter name, by 'mash 3', or UUID; empty when only one is open"`
		VolumeL           float64 `json:"volume_l" jsonschema:"litres charged"`
		AbvPct            float64 `json:"abv_pct" jsonschema:"strength of the wash, percent (0-100)"`
		Notes             string  `json:"notes,omitempty" jsonschema:"optional free-text notes"`
	}
	addTool(s, &mcpsdk.Tool{
		Name:        "add_distillation_charge",
		Description: "Record wash charged to the still from a fermentation.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx, err := guard(ctx, user, "/stillhouse.v1.DistillationService/AddDistillationCharge")
		if err != nil {
			return nil, nil, err
		}
		runID, err := resolveDistillation(ctx, d, user, args.DistillationRunID)
		if err != nil {
			return errResult(err), nil, nil
		}
		fermentationID, err := resolveFermentation(ctx, d, user, args.FermentationRunID)
		if err != nil {
			return errResult(err), nil, nil
		}
		if err := requireUngauged(ctx, d, user, runID); err != nil {
			return errResult(err), nil, nil
		}
		run, err := readDistillation(ctx, d, user, runID)
		if err != nil {
			return errResult(err), nil, nil
		}
		resp, err := d.Distillation.AddDistillationCharge(ctx, connect.NewRequest(&pb.AddDistillationChargeRequest{
			DistillationRunId: runID, FermentationRunId: fermentationID,
			VolumeChargedL: args.VolumeL, AbvPct: args.AbvPct,
			ChargeOrder: int32(len(run.GetCharges()) + 1), Notes: args.Notes,
		}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

func addAddDistillationCut(s *registrar, d Deps, user sqlcgen.User) {
	type in struct {
		DistillationRunID string   `json:"distillation_run_id,omitempty" jsonschema:"the still run, by number or UUID; empty when only one is open"`
		Kind              string   `json:"kind" jsonschema:"one of: foreshots, heads, hearts, tails, feints_saved"`
		VolumeL           float64  `json:"volume_l" jsonschema:"litres collected"`
		AbvPct            float64  `json:"abv_pct" jsonschema:"strength of the fraction, percent (0-100)"`
		Notes             string   `json:"notes,omitempty" jsonschema:"optional free-text notes"`
		At                string   `json:"at,omitempty" jsonschema:"when it happened, RFC 3339 with offset; omit for now"`
		MinutesAgo        *float64 `json:"minutes_ago,omitempty" jsonschema:"how long ago it happened, in minutes; omit for now"`
	}
	addTool(s, &mcpsdk.Tool{
		Name:        "add_distillation_cut",
		Description: "Record a fraction collected, after it is collected: its kind, litres and strength. This is the run's record of its cuts; it does not put spirit in the ledger.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx, err := guard(ctx, user, "/stillhouse.v1.DistillationService/AddDistillationCut")
		if err != nil {
			return nil, nil, err
		}
		kind, err := enumFromName[pb.DistillationCutKind](args.Kind)
		if err != nil {
			return errResult(err), nil, nil
		}
		if args.VolumeL <= 0 || args.AbvPct < 0 || args.AbvPct > 100 {
			return errResult(errors.New("volume_l must be above 0 and abv_pct between 0 and 100")), nil, nil
		}
		observed, err := observedAt(args.At, args.MinutesAgo, time.Now())
		if err != nil {
			return errResult(err), nil, nil
		}
		runID, err := resolveDistillation(ctx, d, user, args.DistillationRunID)
		if err != nil {
			return errResult(err), nil, nil
		}
		run, err := readDistillation(ctx, d, user, runID)
		if err != nil {
			return errResult(err), nil, nil
		}
		resp, err := d.Distillation.AddDistillationCut(ctx, connect.NewRequest(&pb.AddDistillationCutRequest{
			DistillationRunId: runID, Kind: kind, VolumeL: args.VolumeL, AbvPct: args.AbvPct,
			CutOrder: int32(len(run.GetCuts()) + 1), ObservedAt: observed, Notes: args.Notes,
		}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

func addRemoveEntry(s *registrar, d Deps, user sqlcgen.User) {
	type in struct {
		Kind string `json:"kind" jsonschema:"one of: mash_ingredient, mash_reading, fermentation_reading, distillation_charge, distillation_reading, distillation_cut"`
		ID   string `json:"id" jsonschema:"UUID of the entry, as returned when it was recorded"`
	}
	addTool(s, &mcpsdk.Tool{
		Name:        "remove_entry",
		Description: "Remove one entry that was recorded wrongly, so it can be recorded again. The audit log keeps what was removed. A mash ingredient goes back on its lot.",
		Annotations: &mcpsdk.ToolAnnotations{DestructiveHint: ptr(true)},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		var err error
		switch args.Kind {
		case "mash_ingredient":
			if ctx, err = guard(ctx, user, "/stillhouse.v1.MashService/DeleteMashIngredient"); err == nil {
				_, err = d.Mash.DeleteMashIngredient(ctx, connect.NewRequest(&pb.DeleteMashIngredientRequest{Id: args.ID}))
			}
		case "mash_reading":
			if ctx, err = guard(ctx, user, "/stillhouse.v1.MashService/DeleteMashMetric"); err == nil {
				_, err = d.Mash.DeleteMashMetric(ctx, connect.NewRequest(&pb.DeleteMashMetricRequest{Id: args.ID}))
			}
		case "fermentation_reading":
			if ctx, err = guard(ctx, user, "/stillhouse.v1.FermentationService/DeleteFermentationLog"); err == nil {
				_, err = d.Fermentation.DeleteFermentationLog(ctx, connect.NewRequest(&pb.DeleteFermentationLogRequest{Id: args.ID}))
			}
		case "distillation_charge":
			if ctx, err = guard(ctx, user, "/stillhouse.v1.DistillationService/DeleteDistillationCharge"); err == nil {
				_, err = d.Distillation.DeleteDistillationCharge(ctx, connect.NewRequest(&pb.DeleteDistillationChargeRequest{Id: args.ID}))
			}
		case "distillation_reading":
			if ctx, err = guard(ctx, user, "/stillhouse.v1.DistillationService/DeleteDistillationReading"); err == nil {
				_, err = d.Distillation.DeleteDistillationReading(ctx, connect.NewRequest(&pb.DeleteDistillationReadingRequest{Id: args.ID}))
			}
		case "distillation_cut":
			if ctx, err = guard(ctx, user, "/stillhouse.v1.DistillationService/DeleteDistillationCut"); err == nil {
				_, err = d.Distillation.DeleteDistillationCut(ctx, connect.NewRequest(&pb.DeleteDistillationCutRequest{Id: args.ID}))
			}
		default:
			err = fmt.Errorf("%q is not a kind of entry", args.Kind)
		}
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResultRaw(map[string]string{"removed": args.Kind, "id": args.ID}), nil, nil
	})
}

func ptr[T any](v T) *T { return &v }

func addAddMaterial(s *registrar, d Deps, user sqlcgen.User) {
	type in struct {
		Name            string   `json:"name" jsonschema:"what it is called"`
		Kind            string   `json:"kind" jsonschema:"one of: grain, malt, yeast, water, ngs, botanical, packaging, other"`
		Uom             string   `json:"uom" jsonschema:"the unit it is counted in: kg for grain and malt"`
		Cereal          string   `json:"cereal,omitempty" jsonschema:"for grain and malt: barley, wheat, rye, maize, rice, oat, other. Drives mash temperature guidance; omit if not stated"`
		ExtractFraction *float64 `json:"extract_fraction,omitempty" jsonschema:"extract as a fraction, 0 to 1, from the malt analysis — 80.5 percent is 0.805. Omit if not stated; never guess it"`
		Supplier        string   `json:"supplier,omitempty" jsonschema:"who it comes from"`
		Notes           string   `json:"notes,omitempty" jsonschema:"optional free-text notes"`
	}
	addTool(s, &mcpsdk.Tool{
		Name:        "add_material",
		Description: "Define a raw material the distillery has not used before. Check get_floor first so one material is not defined twice.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx, err := guard(ctx, user, "/stillhouse.v1.MaterialService/CreateMaterial")
		if err != nil {
			return nil, nil, err
		}
		kind, err := enumFromName[pb.MaterialKind](args.Kind)
		if err != nil {
			return errResult(err), nil, nil
		}
		var cereal pb.Cereal
		if args.Cereal != "" {
			if cereal, err = enumFromName[pb.Cereal](args.Cereal); err != nil {
				return errResult(err), nil, nil
			}
		}
		listCtx, err := guard(ctx, user, "/stillhouse.v1.MaterialService/ListMaterials")
		if err != nil {
			return nil, nil, err
		}
		existing, err := d.Material.ListMaterials(listCtx, connect.NewRequest(&pb.ListMaterialsRequest{}))
		if err != nil {
			return errResult(err), nil, nil
		}
		for _, m := range existing.Msg.GetMaterials() {
			if normalizeRef(m.GetName()) == normalizeRef(args.Name) {
				return errResult(fmt.Errorf("there is already a material called %s", m.GetName())), nil, nil
			}
		}
		resp, err := d.Material.CreateMaterial(ctx, connect.NewRequest(&pb.CreateMaterialRequest{
			Name: args.Name, Kind: kind, Uom: args.Uom, Cereal: cereal, Supplier: args.Supplier, Notes: args.Notes,
			ExtractFraction: derefFloat(args.ExtractFraction), ExtractFractionSet: args.ExtractFraction != nil,
		}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

func addReceiveMaterial(s *registrar, d Deps, user sqlcgen.User) {
	type in struct {
		Material    string   `json:"material" jsonschema:"the material, by name or UUID"`
		Quantity    float64  `json:"quantity" jsonschema:"quantity received, in the material's own unit"`
		SupplierLot string   `json:"supplier_lot" jsonschema:"the lot number on the bag or the certificate"`
		UnitCostCAD *float64 `json:"unit_cost_cad,omitempty" jsonschema:"cost per unit in CAD — omit if not stated"`
		Notes       string   `json:"notes,omitempty" jsonschema:"optional free-text notes"`
		At          string   `json:"at,omitempty" jsonschema:"when it arrived, RFC 3339 with offset; omit for now"`
		MinutesAgo  *float64 `json:"minutes_ago,omitempty" jsonschema:"how long ago it arrived, in minutes; omit for now"`
	}
	addTool(s, &mcpsdk.Tool{
		Name:        "receive_material",
		Description: "Record a delivered lot of material. The quantity goes on hand and the lot becomes traceable into the mashes that use it. There is no undo here; correct a wrong receipt in the web UI.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx, err := guard(ctx, user, "/stillhouse.v1.MaterialService/RecordMaterialReceipt")
		if err != nil {
			return nil, nil, err
		}
		received, err := observedAt(args.At, args.MinutesAgo, time.Now())
		if err != nil {
			return errResult(err), nil, nil
		}
		materialID, err := resolveMaterial(ctx, d, user, args.Material)
		if err != nil {
			return errResult(err), nil, nil
		}
		resp, err := d.Material.RecordMaterialReceipt(ctx, connect.NewRequest(&pb.RecordMaterialReceiptRequest{
			MaterialId: materialID, SupplierLot: args.SupplierLot, QuantityReceived: args.Quantity,
			ReceivedAt: received, Notes: args.Notes,
			UnitCostCad: derefFloat(args.UnitCostCAD), UnitCostCadSet: args.UnitCostCAD != nil,
		}))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}

func addSaveRecipe(s *registrar, d Deps, user sqlcgen.User) {
	type ingredient struct {
		Material string  `json:"material" jsonschema:"the material, by name or UUID"`
		Quantity float64 `json:"quantity" jsonschema:"quantity per batch"`
		Uom      string  `json:"uom" jsonschema:"unit; kg for anything fermentable"`
	}
	type in struct {
		Name                 string       `json:"name" jsonschema:"the recipe. An existing name saves a new version of it; a new name creates it"`
		SpiritKind           string       `json:"spirit_kind,omitempty" jsonschema:"needed for a new recipe: whisky, canadian_whisky, rye_whisky, gin, vodka, rum, brandy, liqueur, other"`
		Ingredients          []ingredient `json:"ingredients" jsonschema:"the whole bill"`
		MashEfficiency       float64      `json:"mash_efficiency_fraction" jsonschema:"fraction of extract converted, 0 to 1; ask the operator"`
		FermentEfficiency    float64      `json:"ferment_efficiency_fraction" jsonschema:"fraction of sugar fermented, 0 to 1; ask the operator"`
		DistillationRecovery float64      `json:"distillation_recovery_fraction" jsonschema:"fraction of alcohol recovered, 0 to 1; ask the operator"`
		WaterL               *float64     `json:"water_l,omitempty" jsonschema:"mash liquor in litres"`
		Notes                string       `json:"notes,omitempty" jsonschema:"what is different about this version"`
	}
	addTool(s, &mcpsdk.Tool{
		Name:        "save_recipe",
		Description: "Save a grain recipe: its bill and the efficiencies its yield is projected with. Returns the projection. Gin recipes with a botanical bill are edited in the web UI.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args in) (*mcpsdk.CallToolResult, any, error) {
		ctx, err := guard(ctx, user, "/stillhouse.v1.RecipeService/SaveRecipeVersion")
		if err != nil {
			return nil, nil, err
		}
		// Everything that can be refused is checked before the recipe is
		// created, so a bad bill does not leave an empty recipe behind.
		if normalizeRef(args.Name) == "" || len(args.Ingredients) == 0 {
			return errResult(errors.New("a recipe needs a name and at least one ingredient")), nil, nil
		}
		for _, f := range []float64{args.MashEfficiency, args.FermentEfficiency, args.DistillationRecovery} {
			if f <= 0 || f > 1 {
				return errResult(errors.New("the three efficiencies are fractions above 0 and up to 1; ask the operator for them")), nil, nil
			}
		}
		req := &pb.SaveRecipeVersionRequest{
			Notes:                        args.Notes,
			MashEfficiencyFraction:       args.MashEfficiency,
			FermentEfficiencyFraction:    args.FermentEfficiency,
			DistillationRecoveryFraction: args.DistillationRecovery,
			TargetWaterL:                 derefFloat(args.WaterL),
			TargetWaterLSet:              args.WaterL != nil,
		}
		for i, ing := range args.Ingredients {
			id, err := resolveMaterial(ctx, d, user, ing.Material)
			if err != nil {
				return errResult(err), nil, nil
			}
			if ing.Quantity <= 0 || ing.Uom == "" {
				return errResult(fmt.Errorf("%s needs a quantity above 0 and a unit", ing.Material)), nil, nil
			}
			req.Ingredients = append(req.Ingredients, &pb.RecipeIngredientInput{
				MaterialId: id, Quantity: ing.Quantity, Uom: ing.Uom, SortOrder: int32(i + 1),
			})
		}
		listCtx, err := guard(ctx, user, "/stillhouse.v1.RecipeService/ListRecipes")
		if err != nil {
			return nil, nil, err
		}
		recipes, err := d.Recipe.ListRecipes(listCtx, connect.NewRequest(&pb.ListRecipesRequest{}))
		if err != nil {
			return errResult(err), nil, nil
		}
		for _, r := range recipes.Msg.GetRecipes() {
			if normalizeRef(r.GetName()) != normalizeRef(args.Name) {
				continue
			}
			if r.GetSpiritKind() == pb.SpiritKind_SPIRIT_KIND_GIN {
				return errResult(errors.New("gin recipes are edited in the web UI")), nil, nil
			}
			req.RecipeId = r.GetId()
		}
		if req.RecipeId == "" {
			kind, err := enumFromName[pb.SpiritKind](args.SpiritKind)
			if err != nil {
				return errResult(err), nil, nil
			}
			if kind == pb.SpiritKind_SPIRIT_KIND_GIN {
				return errResult(errors.New("gin recipes are edited in the web UI")), nil, nil
			}
			createCtx, err := guard(ctx, user, "/stillhouse.v1.RecipeService/CreateRecipe")
			if err != nil {
				return nil, nil, err
			}
			created, err := d.Recipe.CreateRecipe(createCtx, connect.NewRequest(&pb.CreateRecipeRequest{Name: args.Name, SpiritKind: kind}))
			if err != nil {
				return errResult(err), nil, nil
			}
			req.RecipeId = created.Msg.GetRecipe().GetId()
		}
		resp, err := d.Recipe.SaveRecipeVersion(ctx, connect.NewRequest(req))
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(resp.Msg), nil, nil
	})
}
