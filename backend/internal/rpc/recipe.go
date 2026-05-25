package rpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/distilling"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

type RecipeService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewRecipeService(db *tenantdb.DB, logger *slog.Logger) *RecipeService {
	return &RecipeService{db: db, logger: logger}
}

func (s *RecipeService) CreateRecipe(
	ctx context.Context,
	req *connect.Request[stillhousev1.CreateRecipeRequest],
) (*connect.Response[stillhousev1.CreateRecipeResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	if in.GetName() == "" || in.GetSpiritKind() == stillhousev1.SpiritKind_SPIRIT_KIND_UNSPECIFIED {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name and spirit_kind are required"))
	}
	kind, err := spiritKindToDB(in.GetSpiritKind())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var r sqlcgen.Recipe
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var dbErr error
		r, dbErr = q.CreateRecipe(ctx, sqlcgen.CreateRecipeParams{
			TenantID:   u.TenantID,
			Name:       in.GetName(),
			SpiritKind: kind,
			Notes:      in.GetNotes(),
		})
		return dbErr
	})
	if err != nil {
		s.logger.Error("CreateRecipe", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.CreateRecipeResponse{Recipe: recipeToProto(r)}), nil
}

func (s *RecipeService) ListRecipes(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListRecipesRequest],
) (*connect.Response[stillhousev1.ListRecipesResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var rows []sqlcgen.Recipe
	err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var dbErr error
		rows, dbErr = q.ListRecipes(ctx, req.Msg.GetIncludeArchived())
		return dbErr
	})
	if err != nil {
		s.logger.Error("ListRecipes", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := make([]*stillhousev1.Recipe, 0, len(rows))
	for _, r := range rows {
		out = append(out, recipeToProto(r))
	}
	return connect.NewResponse(&stillhousev1.ListRecipesResponse{Recipes: out}), nil
}

func (s *RecipeService) GetRecipe(
	ctx context.Context,
	req *connect.Request[stillhousev1.GetRecipeRequest],
) (*connect.Response[stillhousev1.GetRecipeResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}

	resp := &stillhousev1.GetRecipeResponse{}
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		r, dbErr := q.GetRecipe(ctx, id)
		if dbErr != nil {
			return dbErr
		}
		resp.Recipe = recipeToProto(r)
		if !r.CurrentVersionID.Valid {
			return nil
		}
		v, dbErr := q.GetRecipeVersion(ctx, r.CurrentVersionID.UUID)
		if dbErr != nil {
			return dbErr
		}
		ingredients, dbErr := q.ListRecipeIngredients(ctx, v.ID)
		if dbErr != nil {
			return dbErr
		}
		// Sensory rows are optional — only present once the operator has
		// tasted this version. Each spirit family has its own table; load
		// whichever applies and ignore "no rows" as a benign signal.
		vp := recipeVersionToProto(v, ingredients)
		if r.SpiritKind == sqlcgen.SpiritKindGin {
			sensory, sensoryErr := q.GetRecipeVersionSensory(ctx, v.ID)
			if sensoryErr != nil && !errors.Is(sensoryErr, pgx.ErrNoRows) {
				return sensoryErr
			}
			if sensoryErr == nil {
				vp.Sensory = sensoryToProto(sensory)
			}
		}
		if isWhiskyKind(r.SpiritKind) {
			ws, wsErr := q.GetRecipeVersionWhiskySensory(ctx, v.ID)
			if wsErr != nil && !errors.Is(wsErr, pgx.ErrNoRows) {
				return wsErr
			}
			if wsErr == nil {
				vp.WhiskySensory = whiskySensoryToProto(ws)
			}
		}
		resp.CurrentVersion = vp
		resp.Projection = projectRecipeVersion(r.SpiritKind, v, ingredients)
		return nil
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("recipe not found"))
		}
		s.logger.Error("GetRecipe", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(resp), nil
}

func (s *RecipeService) ArchiveRecipe(
	ctx context.Context,
	req *connect.Request[stillhousev1.ArchiveRecipeRequest],
) (*connect.Response[stillhousev1.ArchiveRecipeResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	var r sqlcgen.Recipe
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var dbErr error
		r, dbErr = q.SetRecipeArchived(ctx, sqlcgen.SetRecipeArchivedParams{
			ID: id, Archived: req.Msg.GetArchived(),
		})
		return dbErr
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("recipe not found"))
		}
		s.logger.Error("ArchiveRecipe", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.ArchiveRecipeResponse{Recipe: recipeToProto(r)}), nil
}

func (s *RecipeService) SaveRecipeVersion(
	ctx context.Context,
	req *connect.Request[stillhousev1.SaveRecipeVersionRequest],
) (*connect.Response[stillhousev1.SaveRecipeVersionResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	recipeID, err := uuid.Parse(in.GetRecipeId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid recipe_id"))
	}
	if len(in.GetIngredients()) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("at least one ingredient is required"))
	}
	if err := validateEfficiency("mash_efficiency_pct", in.GetMashEfficiencyPct()); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := validateEfficiency("ferment_efficiency_pct", in.GetFermentEfficiencyPct()); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := validateEfficiency("distillation_recovery_pct", in.GetDistillationRecoveryPct()); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	resp := &stillhousev1.SaveRecipeVersionResponse{}
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		// Confirm the recipe exists in this tenant.
		r, e := q.GetRecipe(ctx, recipeID)
		if e != nil {
			return e
		}
		nextNo, e := q.NextRecipeVersionNo(ctx, recipeID)
		if e != nil {
			return e
		}

		var waterL pgtype.Float8
		if in.GetTargetWaterLSet() {
			waterL = pgtype.Float8{Float64: in.GetTargetWaterL(), Valid: true}
		}
		var macerationHours pgtype.Float8
		if in.GetMacerationHoursSet() {
			macerationHours = pgtype.Float8{Float64: in.GetMacerationHours(), Valid: true}
		}
		var ngsInputL pgtype.Float8
		if in.GetGinNgsInputLSet() {
			ngsInputL = pgtype.Float8{Float64: in.GetGinNgsInputL(), Valid: true}
		}
		var ngsInputAbv pgtype.Float8
		if in.GetGinNgsInputAbvPctSet() {
			ngsInputAbv = pgtype.Float8{Float64: in.GetGinNgsInputAbvPct(), Valid: true}
		}

		v, e := q.CreateRecipeVersion(ctx, sqlcgen.CreateRecipeVersionParams{
			TenantID:                u.TenantID,
			RecipeID:                recipeID,
			VersionNo:               nextNo,
			Notes:                   in.GetNotes(),
			MashEfficiencyPct:       in.GetMashEfficiencyPct(),
			FermentEfficiencyPct:    in.GetFermentEfficiencyPct(),
			DistillationRecoveryPct: in.GetDistillationRecoveryPct(),
			TargetWaterL:            waterL,
			TastingNotes:            in.GetTastingNotes(),
			DistillationMethod:      distillationMethodToDB(in.GetDistillationMethod()),
			MacerationHours:         macerationHours,
			GinNgsInputL:            ngsInputL,
			GinNgsInputAbvPct:       ngsInputAbv,
		})
		if e != nil {
			return e
		}

		for _, ing := range in.GetIngredients() {
			matID, parseErr := uuid.Parse(ing.GetMaterialId())
			if parseErr != nil {
				return fmt.Errorf("ingredient: invalid material_id %q", ing.GetMaterialId())
			}
			if ing.GetQuantity() <= 0 {
				return fmt.Errorf("ingredient: quantity must be > 0")
			}
			if ing.GetUom() == "" {
				return fmt.Errorf("ingredient: uom is required")
			}
			if _, e := q.CreateRecipeIngredient(ctx, sqlcgen.CreateRecipeIngredientParams{
				TenantID:        u.TenantID,
				RecipeVersionID: v.ID,
				MaterialID:      matID,
				Quantity:        ing.GetQuantity(),
				Uom:             ing.GetUom(),
				Notes:           ing.GetNotes(),
				SortOrder:       ing.GetSortOrder(),
				BotanicalRole:   botanicalRoleToDB(ing.GetBotanicalRole()),
			}); e != nil {
				return e
			}
		}

		// Point the recipe at this new version.
		if e := q.SetRecipeCurrentVersion(ctx, sqlcgen.SetRecipeCurrentVersionParams{
			ID:               recipeID,
			CurrentVersionID: uuid.NullUUID{UUID: v.ID, Valid: true},
		}); e != nil {
			return e
		}

		ingredients, e := q.ListRecipeIngredients(ctx, v.ID)
		if e != nil {
			return e
		}
		resp.Version = recipeVersionToProto(v, ingredients)
		resp.Projection = projectRecipeVersion(r.SpiritKind, v, ingredients)
		return audit.Write(ctx, q, u.TenantID, u.ID, "recipe_version", v.ID.String(),
			sqlcgen.AuditActionCreate, map[string]any{
				"recipe_id":      recipeID.String(),
				"version_no":     v.VersionNo,
				"mash_eff":       v.MashEfficiencyPct,
				"ferment_eff":    v.FermentEfficiencyPct,
				"distill_recov":  v.DistillationRecoveryPct,
				"total_laa":      resp.Projection.GetTotalProjectedLaa(),
				"ingredient_n":   len(ingredients),
			})
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("recipe not found"))
		}
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return nil, connectErr
		}
		s.logger.Error("SaveRecipeVersion", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(resp), nil
}

func (s *RecipeService) ListRecipeVersions(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListRecipeVersionsRequest],
) (*connect.Response[stillhousev1.ListRecipeVersionsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	recipeID, err := uuid.Parse(req.Msg.GetRecipeId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid recipe_id"))
	}

	var rows []sqlcgen.ListRecipeVersionsRow
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var dbErr error
		rows, dbErr = q.ListRecipeVersions(ctx, recipeID)
		return dbErr
	})
	if err != nil {
		s.logger.Error("ListRecipeVersions", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := make([]*stillhousev1.RecipeVersion, 0, len(rows))
	for _, r := range rows {
		// Reuse recipeVersionToProto by reconstructing the version row;
		// then layer sensory on top from the joined columns when present.
		// Ingredients aren't loaded by the list query — callers needing
		// the full breakdown call GetRecipe for the current version.
		v := sqlcgen.RecipeVersion{
			ID:                      r.ID,
			TenantID:                r.TenantID,
			RecipeID:                r.RecipeID,
			VersionNo:               r.VersionNo,
			Notes:                   r.Notes,
			MashEfficiencyPct:       r.MashEfficiencyPct,
			FermentEfficiencyPct:    r.FermentEfficiencyPct,
			DistillationRecoveryPct: r.DistillationRecoveryPct,
			TargetWaterL:            r.TargetWaterL,
			CreatedAt:               r.CreatedAt,
			TastingNotes:            r.TastingNotes,
			DistillationMethod:      r.DistillationMethod,
			MacerationHours:         r.MacerationHours,
			GinNgsInputL:            r.GinNgsInputL,
			GinNgsInputAbvPct:       r.GinNgsInputAbvPct,
		}
		vp := recipeVersionToProto(v, nil)
		if r.SensoryTastedAt.Valid {
			vp.Sensory = listRowSensoryToProto(r)
		}
		if r.WhiskyTastedAt.Valid {
			vp.WhiskySensory = listRowWhiskySensoryToProto(r)
		}
		out = append(out, vp)
	}
	return connect.NewResponse(&stillhousev1.ListRecipeVersionsResponse{Versions: out}), nil
}

// listRowSensoryToProto carries the LEFT JOIN columns from
// ListRecipeVersionsRow into the GinSensoryScores proto. Mirrors
// sensoryToProto but operates on the row's prefixed column names.
func listRowSensoryToProto(r sqlcgen.ListRecipeVersionsRow) *stillhousev1.GinSensoryScores {
	out := &stillhousev1.GinSensoryScores{
		TastingPanel: r.SensoryTastingPanel.String,
		TastedAt:     timestamppb.New(r.SensoryTastedAt.Time),
	}
	setI := func(dst *int32, dstSet *bool, src pgtype.Int2) {
		if src.Valid {
			*dst = int32(src.Int16)
			*dstSet = true
		}
	}
	setI(&out.Juniper, &out.JuniperSet, r.SensoryJuniper)
	setI(&out.Citrus, &out.CitrusSet, r.SensoryCitrus)
	setI(&out.Herbal, &out.HerbalSet, r.SensoryHerbal)
	setI(&out.Spice, &out.SpiceSet, r.SensorySpice)
	setI(&out.Floral, &out.FloralSet, r.SensoryFloral)
	setI(&out.Earth, &out.EarthSet, r.SensoryEarth)
	setI(&out.Body, &out.BodySet, r.SensoryBody)
	setI(&out.Heat, &out.HeatSet, r.SensoryHeat)
	setI(&out.Balance, &out.BalanceSet, r.SensoryBalance)
	setI(&out.Overall, &out.OverallSet, r.SensoryOverall)
	return out
}

// listRowWhiskySensoryToProto carries the whisky LEFT JOIN columns
// (SWRI Flavour Wheel primary classes + body / finish / overall).
func listRowWhiskySensoryToProto(r sqlcgen.ListRecipeVersionsRow) *stillhousev1.WhiskySensoryScores {
	out := &stillhousev1.WhiskySensoryScores{
		TastingPanel: r.WhiskyTastingPanel.String,
		TastedAt:     timestamppb.New(r.WhiskyTastedAt.Time),
	}
	setI := func(dst *int32, dstSet *bool, src pgtype.Int2) {
		if src.Valid {
			*dst = int32(src.Int16)
			*dstSet = true
		}
	}
	setI(&out.Cereal, &out.CerealSet, r.WhiskyCereal)
	setI(&out.Estery, &out.EsterySet, r.WhiskyEstery)
	setI(&out.Floral, &out.FloralSet, r.WhiskyFloral)
	setI(&out.Peaty, &out.PeatySet, r.WhiskyPeaty)
	setI(&out.Feinty, &out.FeintySet, r.WhiskyFeinty)
	setI(&out.Sulphury, &out.SulphurySet, r.WhiskySulphury)
	setI(&out.Woody, &out.WoodySet, r.WhiskyWoody)
	setI(&out.Winey, &out.WineySet, r.WhiskyWiney)
	setI(&out.Body, &out.BodySet, r.WhiskyBody)
	setI(&out.Finish, &out.FinishSet, r.WhiskyFinish)
	setI(&out.Overall, &out.OverallSet, r.WhiskyOverall)
	return out
}

// whiskySensoryToProto mirrors sensoryToProto for the whisky table row.
func whiskySensoryToProto(s sqlcgen.RecipeVersionWhiskySensory) *stillhousev1.WhiskySensoryScores {
	out := &stillhousev1.WhiskySensoryScores{
		TastingPanel: s.TastingPanel,
		TastedAt:     timestamppb.New(s.TastedAt.Time),
	}
	setI := func(dst *int32, dstSet *bool, src pgtype.Int2) {
		if src.Valid {
			*dst = int32(src.Int16)
			*dstSet = true
		}
	}
	setI(&out.Cereal, &out.CerealSet, s.Cereal)
	setI(&out.Estery, &out.EsterySet, s.Estery)
	setI(&out.Floral, &out.FloralSet, s.Floral)
	setI(&out.Peaty, &out.PeatySet, s.Peaty)
	setI(&out.Feinty, &out.FeintySet, s.Feinty)
	setI(&out.Sulphury, &out.SulphurySet, s.Sulphury)
	setI(&out.Woody, &out.WoodySet, s.Woody)
	setI(&out.Winey, &out.WineySet, s.Winey)
	setI(&out.Body, &out.BodySet, s.Body)
	setI(&out.Finish, &out.FinishSet, s.Finish)
	setI(&out.Overall, &out.OverallSet, s.Overall)
	return out
}

// isWhiskyKind returns true for spirit kinds that get the whisky tasting bench.
func isWhiskyKind(k sqlcgen.SpiritKind) bool {
	switch k {
	case sqlcgen.SpiritKindWhisky, sqlcgen.SpiritKindCanadianWhisky, sqlcgen.SpiritKindRyeWhisky:
		return true
	}
	return false
}

func (s *RecipeService) DuplicateRecipe(
	ctx context.Context,
	req *connect.Request[stillhousev1.DuplicateRecipeRequest],
) (*connect.Response[stillhousev1.DuplicateRecipeResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	sourceID, err := uuid.Parse(req.Msg.GetSourceRecipeId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid source_recipe_id"))
	}
	newName := req.Msg.GetNewName()
	if newName == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("new_name is required"))
	}

	var newRecipe sqlcgen.Recipe
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		src, e := q.GetRecipe(ctx, sourceID)
		if e != nil {
			return e
		}
		var dupErr error
		newRecipe, dupErr = q.CreateRecipe(ctx, sqlcgen.CreateRecipeParams{
			TenantID:   u.TenantID,
			Name:       newName,
			SpiritKind: src.SpiritKind,
			Notes:      src.Notes,
		})
		if dupErr != nil {
			return dupErr
		}

		// Carry over the latest version (if any) so the duplicate is
		// immediately usable — otherwise it'd be a name-only stub.
		if !src.CurrentVersionID.Valid {
			return audit.Write(ctx, q, u.TenantID, u.ID, "recipe", newRecipe.ID.String(),
				sqlcgen.AuditActionCreate, map[string]any{
					"source_recipe_id": sourceID.String(),
					"name":             newName,
					"copied_version":   false,
				})
		}
		srcVersion, e := q.GetRecipeVersion(ctx, src.CurrentVersionID.UUID)
		if e != nil {
			return e
		}
		srcIngredients, e := q.ListRecipeIngredients(ctx, srcVersion.ID)
		if e != nil {
			return e
		}
		nextNo, e := q.NextRecipeVersionNo(ctx, newRecipe.ID)
		if e != nil {
			return e
		}
		newVersion, e := q.CreateRecipeVersion(ctx, sqlcgen.CreateRecipeVersionParams{
			TenantID:                u.TenantID,
			RecipeID:                newRecipe.ID,
			VersionNo:               nextNo,
			Notes:                   srcVersion.Notes,
			MashEfficiencyPct:       srcVersion.MashEfficiencyPct,
			FermentEfficiencyPct:    srcVersion.FermentEfficiencyPct,
			DistillationRecoveryPct: srcVersion.DistillationRecoveryPct,
			TargetWaterL:            srcVersion.TargetWaterL,
			TastingNotes:            srcVersion.TastingNotes,
			DistillationMethod:      srcVersion.DistillationMethod,
			MacerationHours:         srcVersion.MacerationHours,
			GinNgsInputL:            srcVersion.GinNgsInputL,
			GinNgsInputAbvPct:       srcVersion.GinNgsInputAbvPct,
		})
		if e != nil {
			return e
		}
		for _, ing := range srcIngredients {
			if _, e := q.CreateRecipeIngredient(ctx, sqlcgen.CreateRecipeIngredientParams{
				TenantID:        u.TenantID,
				RecipeVersionID: newVersion.ID,
				MaterialID:      ing.MaterialID,
				Quantity:        ing.Quantity,
				Uom:             ing.Uom,
				Notes:           ing.Notes,
				SortOrder:       ing.SortOrder,
				BotanicalRole:   ing.BotanicalRole,
			}); e != nil {
				return e
			}
		}
		if e := q.SetRecipeCurrentVersion(ctx, sqlcgen.SetRecipeCurrentVersionParams{
			ID:               newRecipe.ID,
			CurrentVersionID: uuid.NullUUID{UUID: newVersion.ID, Valid: true},
		}); e != nil {
			return e
		}
		// Re-read so the returned recipe carries the new current_version_id.
		newRecipe, e = q.GetRecipe(ctx, newRecipe.ID)
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "recipe", newRecipe.ID.String(),
			sqlcgen.AuditActionCreate, map[string]any{
				"source_recipe_id": sourceID.String(),
				"name":             newName,
				"copied_version":   true,
				"ingredient_n":     len(srcIngredients),
			})
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("source recipe not found"))
		}
		s.logger.Error("DuplicateRecipe", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.DuplicateRecipeResponse{Recipe: recipeToProto(newRecipe)}), nil
}

// SaveRecipeVersionSensory upserts the per-version tasting scores. The
// recipe development workflow is: distill a small test batch, taste,
// score, tweak, save a new version, repeat. Scoring overwrites — every
// call replaces the prior scores for that version.
func (s *RecipeService) SaveRecipeVersionSensory(
	ctx context.Context,
	req *connect.Request[stillhousev1.SaveRecipeVersionSensoryRequest],
) (*connect.Response[stillhousev1.SaveRecipeVersionSensoryResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	versionID, err := uuid.Parse(req.Msg.GetRecipeVersionId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid recipe_version_id"))
	}
	in := req.Msg.GetScores()
	if in == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("scores is required"))
	}

	score := func(v int32, set bool) (pgtype.Int2, error) {
		if !set {
			return pgtype.Int2{}, nil
		}
		if v < 0 || v > 10 {
			return pgtype.Int2{}, fmt.Errorf("score must be in [0, 10], got %d", v)
		}
		return pgtype.Int2{Int16: int16(v), Valid: true}, nil
	}
	type axis struct {
		name string
		v    int32
		set  bool
	}
	axes := []axis{
		{"juniper", in.GetJuniper(), in.GetJuniperSet()},
		{"citrus", in.GetCitrus(), in.GetCitrusSet()},
		{"herbal", in.GetHerbal(), in.GetHerbalSet()},
		{"spice", in.GetSpice(), in.GetSpiceSet()},
		{"floral", in.GetFloral(), in.GetFloralSet()},
		{"earth", in.GetEarth(), in.GetEarthSet()},
		{"body", in.GetBody(), in.GetBodySet()},
		{"heat", in.GetHeat(), in.GetHeatSet()},
		{"balance", in.GetBalance(), in.GetBalanceSet()},
		{"overall", in.GetOverall(), in.GetOverallSet()},
	}
	vals := make([]pgtype.Int2, len(axes))
	for i, a := range axes {
		val, err := score(a.v, a.set)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%s: %w", a.name, err))
		}
		vals[i] = val
	}

	tastedAt := timestampOrNow(in.GetTastedAt())
	var saved sqlcgen.RecipeVersionSensory
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		// Confirm the version exists in this tenant before upserting.
		v, e := q.GetRecipeVersion(ctx, versionID)
		if e != nil {
			return e
		}
		// Gate to gin recipes — the 10 axes (juniper, citrus, herbal,
		// spice, floral, earth, body, heat, balance, overall) are
		// gin-shaped. A whisky tasting bench would need different axes
		// (caramel, vanilla, oak, smoke, …); whoever wants that should
		// add a separate RPC rather than overloading this one.
		r, e := q.GetRecipe(ctx, v.RecipeID)
		if e != nil {
			return e
		}
		if r.SpiritKind != sqlcgen.SpiritKindGin {
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("sensory bench is gin-only; this recipe is " + string(r.SpiritKind)))
		}
		saved, e = q.UpsertRecipeVersionSensory(ctx, sqlcgen.UpsertRecipeVersionSensoryParams{
			RecipeVersionID: versionID,
			TenantID:        u.TenantID,
			Juniper:         vals[0],
			Citrus:          vals[1],
			Herbal:          vals[2],
			Spice:           vals[3],
			Floral:          vals[4],
			Earth:           vals[5],
			Body:            vals[6],
			Heat:            vals[7],
			Balance:         vals[8],
			Overall:         vals[9],
			TastingPanel:    in.GetTastingPanel(),
			TastedAt:        tastedAt,
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "recipe_version_sensory", versionID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"tasting_panel": saved.TastingPanel,
				"overall":       in.GetOverall(),
			})
	})
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			return nil, ce
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("recipe version not found"))
		}
		s.logger.Error("SaveRecipeVersionSensory", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.SaveRecipeVersionSensoryResponse{
		Scores: sensoryToProto(saved),
	}), nil
}

// SaveRecipeVersionWhiskySensory upserts the per-version whisky
// tasting scores. Parallel to SaveRecipeVersionSensory but for whisky-
// family recipes (whisky / canadian_whisky / rye_whisky) using the
// SWRI Flavour Wheel axes. Partial upsert via COALESCE — sending a
// single axis preserves the other 10.
func (s *RecipeService) SaveRecipeVersionWhiskySensory(
	ctx context.Context,
	req *connect.Request[stillhousev1.SaveRecipeVersionWhiskySensoryRequest],
) (*connect.Response[stillhousev1.SaveRecipeVersionWhiskySensoryResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	versionID, err := uuid.Parse(req.Msg.GetRecipeVersionId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid recipe_version_id"))
	}
	in := req.Msg.GetScores()
	if in == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("scores is required"))
	}

	score := func(v int32, set bool) (pgtype.Int2, error) {
		if !set {
			return pgtype.Int2{}, nil
		}
		if v < 0 || v > 10 {
			return pgtype.Int2{}, fmt.Errorf("score must be in [0, 10], got %d", v)
		}
		return pgtype.Int2{Int16: int16(v), Valid: true}, nil
	}
	type axis struct {
		name string
		v    int32
		set  bool
	}
	axes := []axis{
		{"cereal", in.GetCereal(), in.GetCerealSet()},
		{"estery", in.GetEstery(), in.GetEsterySet()},
		{"floral", in.GetFloral(), in.GetFloralSet()},
		{"peaty", in.GetPeaty(), in.GetPeatySet()},
		{"feinty", in.GetFeinty(), in.GetFeintySet()},
		{"sulphury", in.GetSulphury(), in.GetSulphurySet()},
		{"woody", in.GetWoody(), in.GetWoodySet()},
		{"winey", in.GetWiney(), in.GetWineySet()},
		{"body", in.GetBody(), in.GetBodySet()},
		{"finish", in.GetFinish(), in.GetFinishSet()},
		{"overall", in.GetOverall(), in.GetOverallSet()},
	}
	vals := make([]pgtype.Int2, len(axes))
	for i, a := range axes {
		val, err := score(a.v, a.set)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%s: %w", a.name, err))
		}
		vals[i] = val
	}

	tastedAt := timestampOrNow(in.GetTastedAt())
	var saved sqlcgen.RecipeVersionWhiskySensory
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		v, e := q.GetRecipeVersion(ctx, versionID)
		if e != nil {
			return e
		}
		// Whisky-family gate — symmetric with the gin gate on
		// SaveRecipeVersionSensory. The SWRI axes are whisky-shaped;
		// using them on a vodka or rum recipe would be a category
		// error (those spirits would need their own benches).
		r, e := q.GetRecipe(ctx, v.RecipeID)
		if e != nil {
			return e
		}
		if !isWhiskyKind(r.SpiritKind) {
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("whisky sensory bench is for whisky-family recipes only; this recipe is "+string(r.SpiritKind)))
		}
		saved, e = q.UpsertRecipeVersionWhiskySensory(ctx, sqlcgen.UpsertRecipeVersionWhiskySensoryParams{
			RecipeVersionID: versionID,
			TenantID:        u.TenantID,
			Cereal:          vals[0],
			Estery:          vals[1],
			Floral:          vals[2],
			Peaty:           vals[3],
			Feinty:          vals[4],
			Sulphury:        vals[5],
			Woody:           vals[6],
			Winey:           vals[7],
			Body:            vals[8],
			Finish:          vals[9],
			Overall:         vals[10],
			TastingPanel:    in.GetTastingPanel(),
			TastedAt:        tastedAt,
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "recipe_version_whisky_sensory", versionID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"tasting_panel": saved.TastingPanel,
				"overall":       in.GetOverall(),
			})
	})
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			return nil, ce
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("recipe version not found"))
		}
		s.logger.Error("SaveRecipeVersionWhiskySensory", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.SaveRecipeVersionWhiskySensoryResponse{
		Scores: whiskySensoryToProto(saved),
	}), nil
}

// --- helpers ---

func validateEfficiency(name string, v float64) error {
	if v <= 0 || v > 1 {
		return fmt.Errorf("%s must be in (0, 1], got %v", name, v)
	}
	return nil
}

func recipeToProto(r sqlcgen.Recipe) *stillhousev1.Recipe {
	out := &stillhousev1.Recipe{
		Id:         r.ID.String(),
		TenantId:   r.TenantID.String(),
		Name:       r.Name,
		SpiritKind: spiritKindToProto(r.SpiritKind),
		Archived:   r.Archived,
		Notes:      r.Notes,
		CreatedAt:  timestamppb.New(r.CreatedAt.Time),
		UpdatedAt:  timestamppb.New(r.UpdatedAt.Time),
	}
	if r.CurrentVersionID.Valid {
		out.CurrentVersionId = r.CurrentVersionID.UUID.String()
	}
	return out
}

func recipeVersionToProto(v sqlcgen.RecipeVersion, ingredients []sqlcgen.ListRecipeIngredientsRow) *stillhousev1.RecipeVersion {
	out := &stillhousev1.RecipeVersion{
		Id:                      v.ID.String(),
		TenantId:                v.TenantID.String(),
		RecipeId:                v.RecipeID.String(),
		VersionNo:               v.VersionNo,
		Notes:                   v.Notes,
		MashEfficiencyPct:       v.MashEfficiencyPct,
		FermentEfficiencyPct:    v.FermentEfficiencyPct,
		DistillationRecoveryPct: v.DistillationRecoveryPct,
		CreatedAt:               timestamppb.New(v.CreatedAt.Time),
		TastingNotes:            v.TastingNotes,
		DistillationMethod:      distillationMethodToProto(v.DistillationMethod),
	}
	if v.TargetWaterL.Valid {
		out.TargetWaterL = v.TargetWaterL.Float64
		out.TargetWaterLSet = true
	}
	if v.MacerationHours.Valid {
		out.MacerationHours = v.MacerationHours.Float64
		out.MacerationHoursSet = true
	}
	if v.GinNgsInputL.Valid {
		out.GinNgsInputL = v.GinNgsInputL.Float64
		out.GinNgsInputLSet = true
	}
	if v.GinNgsInputAbvPct.Valid {
		out.GinNgsInputAbvPct = v.GinNgsInputAbvPct.Float64
		out.GinNgsInputAbvPctSet = true
	}
	for _, ing := range ingredients {
		out.Ingredients = append(out.Ingredients, recipeIngredientRowToProto(ing))
	}
	return out
}

func recipeIngredientRowToProto(r sqlcgen.ListRecipeIngredientsRow) *stillhousev1.RecipeIngredient {
	out := &stillhousev1.RecipeIngredient{
		Id:              r.ID.String(),
		RecipeVersionId: r.RecipeVersionID.String(),
		MaterialId:      r.MaterialID.String(),
		MaterialName:    r.MaterialName,
		MaterialKind:    materialKindToProto(r.MaterialKind),
		Quantity:        r.Quantity,
		Uom:             r.Uom,
		Notes:           r.Notes,
		SortOrder:       r.SortOrder,
		BotanicalRole:   botanicalRoleToProto(r.BotanicalRole),
	}
	if r.MaterialExtractPct.Valid {
		out.MaterialExtractPct = r.MaterialExtractPct.Float64
		out.MaterialExtractPctSet = true
	}
	return out
}

func sensoryToProto(s sqlcgen.RecipeVersionSensory) *stillhousev1.GinSensoryScores {
	out := &stillhousev1.GinSensoryScores{
		TastingPanel: s.TastingPanel,
		TastedAt:     timestamppb.New(s.TastedAt.Time),
	}
	setI := func(dst *int32, dstSet *bool, src pgtype.Int2) {
		if src.Valid {
			*dst = int32(src.Int16)
			*dstSet = true
		}
	}
	setI(&out.Juniper, &out.JuniperSet, s.Juniper)
	setI(&out.Citrus, &out.CitrusSet, s.Citrus)
	setI(&out.Herbal, &out.HerbalSet, s.Herbal)
	setI(&out.Spice, &out.SpiceSet, s.Spice)
	setI(&out.Floral, &out.FloralSet, s.Floral)
	setI(&out.Earth, &out.EarthSet, s.Earth)
	setI(&out.Body, &out.BodySet, s.Body)
	setI(&out.Heat, &out.HeatSet, s.Heat)
	setI(&out.Balance, &out.BalanceSet, s.Balance)
	setI(&out.Overall, &out.OverallSet, s.Overall)
	return out
}

func botanicalRoleToDB(r stillhousev1.BotanicalRole) string {
	switch r {
	case stillhousev1.BotanicalRole_BOTANICAL_ROLE_JUNIPER:
		return "juniper"
	case stillhousev1.BotanicalRole_BOTANICAL_ROLE_CITRUS:
		return "citrus"
	case stillhousev1.BotanicalRole_BOTANICAL_ROLE_HERBAL:
		return "herbal"
	case stillhousev1.BotanicalRole_BOTANICAL_ROLE_SPICE:
		return "spice"
	case stillhousev1.BotanicalRole_BOTANICAL_ROLE_FLORAL:
		return "floral"
	case stillhousev1.BotanicalRole_BOTANICAL_ROLE_ROOT:
		return "root"
	case stillhousev1.BotanicalRole_BOTANICAL_ROLE_OTHER:
		return "other"
	}
	return ""
}

func botanicalRoleToProto(s string) stillhousev1.BotanicalRole {
	switch s {
	case "juniper":
		return stillhousev1.BotanicalRole_BOTANICAL_ROLE_JUNIPER
	case "citrus":
		return stillhousev1.BotanicalRole_BOTANICAL_ROLE_CITRUS
	case "herbal":
		return stillhousev1.BotanicalRole_BOTANICAL_ROLE_HERBAL
	case "spice":
		return stillhousev1.BotanicalRole_BOTANICAL_ROLE_SPICE
	case "floral":
		return stillhousev1.BotanicalRole_BOTANICAL_ROLE_FLORAL
	case "root":
		return stillhousev1.BotanicalRole_BOTANICAL_ROLE_ROOT
	case "other":
		return stillhousev1.BotanicalRole_BOTANICAL_ROLE_OTHER
	}
	return stillhousev1.BotanicalRole_BOTANICAL_ROLE_UNSPECIFIED
}

func distillationMethodToDB(m stillhousev1.DistillationMethod) string {
	switch m {
	case stillhousev1.DistillationMethod_DISTILLATION_METHOD_POT:
		return "pot"
	case stillhousev1.DistillationMethod_DISTILLATION_METHOD_VAPOR:
		return "vapor"
	case stillhousev1.DistillationMethod_DISTILLATION_METHOD_COMBINED:
		return "combined"
	}
	return ""
}

func distillationMethodToProto(s string) stillhousev1.DistillationMethod {
	switch s {
	case "pot":
		return stillhousev1.DistillationMethod_DISTILLATION_METHOD_POT
	case "vapor":
		return stillhousev1.DistillationMethod_DISTILLATION_METHOD_VAPOR
	case "combined":
		return stillhousev1.DistillationMethod_DISTILLATION_METHOD_COMBINED
	}
	return stillhousev1.DistillationMethod_DISTILLATION_METHOD_UNSPECIFIED
}

func projectRecipeVersion(spiritKind sqlcgen.SpiritKind, v sqlcgen.RecipeVersion, ingredients []sqlcgen.ListRecipeIngredientsRow) *stillhousev1.RecipeProjection {
	// Gin runs a different math: the recipe IS the botanical bill +
	// per-version NGS input. LAA comes entirely from the NGS carrier,
	// passed through the redistillation recovery. Botanicals contribute
	// flavor, not alcohol — they appear as zero-LAA projection lines.
	if spiritKind == sqlcgen.SpiritKindGin {
		return projectGinRecipe(v, ingredients)
	}

	// Whisky-style: walk fermentable grains/malt through mash → ferment
	// → distill. Anything without an extract_pct (water, yeast,
	// botanicals) passes through as a zero-LAA line.
	inputs := make([]distilling.Ingredient, 0, len(ingredients))
	for _, ing := range ingredients {
		if !ing.MaterialExtractPct.Valid {
			continue
		}
		// Only count grain/malt masses. uom 'kg' is assumed for fermentables.
		// (Future: convert lb→kg here when we add imperial uoms.)
		if ing.Uom != "kg" {
			continue
		}
		inputs = append(inputs, distilling.Ingredient{
			Name:       ing.MaterialName,
			MassKg:     ing.Quantity,
			ExtractPct: ing.MaterialExtractPct.Float64,
		})
	}
	eff := distilling.Efficiencies{
		Mash:                 v.MashEfficiencyPct,
		Ferment:              v.FermentEfficiencyPct,
		DistillationRecovery: v.DistillationRecoveryPct,
	}
	batch := distilling.ProjectBatch(inputs, eff)

	// Build the projection lines mirroring every ingredient (even non-fermentables
	// — they appear with zero LAA so the caller sees the full list).
	lines := make([]*stillhousev1.RecipeProjectionLine, 0, len(ingredients))
	idx := 0
	for _, ing := range ingredients {
		line := &stillhousev1.RecipeProjectionLine{
			MaterialId:   ing.MaterialID.String(),
			MaterialName: ing.MaterialName,
			Quantity:     ing.Quantity,
			Uom:          ing.Uom,
		}
		if ing.MaterialExtractPct.Valid && ing.Uom == "kg" && idx < len(batch.PerIngredient) {
			r := batch.PerIngredient[idx]
			line.FermentableKg = r.FermentableKg
			line.ExtractFreedKg = r.ExtractFreedKg
			line.EthanolMassKg = r.EthanolMassKg
			line.EthanolVolumeL = r.EthanolVolumeL
			line.ProjectedLaa = r.ProjectedLAA
			idx++
		}
		lines = append(lines, line)
	}

	out := &stillhousev1.RecipeProjection{
		Lines:             lines,
		TotalProjectedLaa: batch.TotalProjectedLAA,
	}
	if v.TargetWaterL.Valid {
		w := distilling.ProjectWash(inputs, eff, v.TargetWaterL.Float64)
		out.ProjectedWashVolumeL = w.VolumeL
		out.ProjectedWashAbvPct = w.ABVPct
	}
	return out
}

// projectGinRecipe computes the projection for a gin recipe. The LAA
// math collapses to NGS_LAA * distillation_recovery. Each ingredient
// gets a zero-LAA line; the carrier (the NGS volume + ABV stored on
// the version itself) is what produces the projected output.
func projectGinRecipe(v sqlcgen.RecipeVersion, ingredients []sqlcgen.ListRecipeIngredientsRow) *stillhousev1.RecipeProjection {
	lines := make([]*stillhousev1.RecipeProjectionLine, 0, len(ingredients))
	for _, ing := range ingredients {
		lines = append(lines, &stillhousev1.RecipeProjectionLine{
			MaterialId:   ing.MaterialID.String(),
			MaterialName: ing.MaterialName,
			Quantity:     ing.Quantity,
			Uom:          ing.Uom,
		})
	}
	out := &stillhousev1.RecipeProjection{Lines: lines}
	if v.GinNgsInputL.Valid && v.GinNgsInputAbvPct.Valid {
		ngsLAA := v.GinNgsInputL.Float64 * v.GinNgsInputAbvPct.Float64 / 100
		out.TotalProjectedLaa = ngsLAA * v.DistillationRecoveryPct
	}
	return out
}

func spiritKindToDB(k stillhousev1.SpiritKind) (sqlcgen.SpiritKind, error) {
	switch k {
	case stillhousev1.SpiritKind_SPIRIT_KIND_WHISKY:
		return sqlcgen.SpiritKindWhisky, nil
	case stillhousev1.SpiritKind_SPIRIT_KIND_CANADIAN_WHISKY:
		return sqlcgen.SpiritKindCanadianWhisky, nil
	case stillhousev1.SpiritKind_SPIRIT_KIND_RYE_WHISKY:
		return sqlcgen.SpiritKindRyeWhisky, nil
	case stillhousev1.SpiritKind_SPIRIT_KIND_GIN:
		return sqlcgen.SpiritKindGin, nil
	case stillhousev1.SpiritKind_SPIRIT_KIND_VODKA:
		return sqlcgen.SpiritKindVodka, nil
	case stillhousev1.SpiritKind_SPIRIT_KIND_RUM:
		return sqlcgen.SpiritKindRum, nil
	case stillhousev1.SpiritKind_SPIRIT_KIND_BRANDY:
		return sqlcgen.SpiritKindBrandy, nil
	case stillhousev1.SpiritKind_SPIRIT_KIND_LIQUEUR:
		return sqlcgen.SpiritKindLiqueur, nil
	case stillhousev1.SpiritKind_SPIRIT_KIND_OTHER:
		return sqlcgen.SpiritKindOther, nil
	}
	return "", errors.New("invalid spirit kind")
}

func spiritKindToProto(k sqlcgen.SpiritKind) stillhousev1.SpiritKind {
	switch k {
	case sqlcgen.SpiritKindWhisky:
		return stillhousev1.SpiritKind_SPIRIT_KIND_WHISKY
	case sqlcgen.SpiritKindCanadianWhisky:
		return stillhousev1.SpiritKind_SPIRIT_KIND_CANADIAN_WHISKY
	case sqlcgen.SpiritKindRyeWhisky:
		return stillhousev1.SpiritKind_SPIRIT_KIND_RYE_WHISKY
	case sqlcgen.SpiritKindGin:
		return stillhousev1.SpiritKind_SPIRIT_KIND_GIN
	case sqlcgen.SpiritKindVodka:
		return stillhousev1.SpiritKind_SPIRIT_KIND_VODKA
	case sqlcgen.SpiritKindRum:
		return stillhousev1.SpiritKind_SPIRIT_KIND_RUM
	case sqlcgen.SpiritKindBrandy:
		return stillhousev1.SpiritKind_SPIRIT_KIND_BRANDY
	case sqlcgen.SpiritKindLiqueur:
		return stillhousev1.SpiritKind_SPIRIT_KIND_LIQUEUR
	case sqlcgen.SpiritKindOther:
		return stillhousev1.SpiritKind_SPIRIT_KIND_OTHER
	}
	return stillhousev1.SpiritKind_SPIRIT_KIND_UNSPECIFIED
}
