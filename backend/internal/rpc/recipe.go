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
		vp := recipeVersionToProto(v, ingredients)
		resp.CurrentVersion = vp
		resp.Projection = projectRecipeVersion(v, ingredients)
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
		if _, e := q.GetRecipe(ctx, recipeID); e != nil {
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

		v, e := q.CreateRecipeVersion(ctx, sqlcgen.CreateRecipeVersionParams{
			TenantID:                u.TenantID,
			RecipeID:                recipeID,
			VersionNo:               nextNo,
			Notes:                   in.GetNotes(),
			MashEfficiencyPct:       in.GetMashEfficiencyPct(),
			FermentEfficiencyPct:    in.GetFermentEfficiencyPct(),
			DistillationRecoveryPct: in.GetDistillationRecoveryPct(),
			TargetWaterL:            waterL,
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
		resp.Projection = projectRecipeVersion(v, ingredients)
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

	var versions []sqlcgen.RecipeVersion
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var dbErr error
		versions, dbErr = q.ListRecipeVersions(ctx, recipeID)
		return dbErr
	})
	if err != nil {
		s.logger.Error("ListRecipeVersions", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := make([]*stillhousev1.RecipeVersion, 0, len(versions))
	for _, v := range versions {
		// We don't load ingredients here for list view — clients call GetRecipe
		// or a future GetRecipeVersion if they need the full breakdown.
		out = append(out, recipeVersionToProto(v, nil))
	}
	return connect.NewResponse(&stillhousev1.ListRecipeVersionsResponse{Versions: out}), nil
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
	}
	if v.TargetWaterL.Valid {
		out.TargetWaterL = v.TargetWaterL.Float64
		out.TargetWaterLSet = true
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
	}
	if r.MaterialExtractPct.Valid {
		out.MaterialExtractPct = r.MaterialExtractPct.Float64
		out.MaterialExtractPctSet = true
	}
	return out
}

func projectRecipeVersion(v sqlcgen.RecipeVersion, ingredients []sqlcgen.ListRecipeIngredientsRow) *stillhousev1.RecipeProjection {
	// Only fermentable sources (those with a non-null extract_pct) contribute
	// to projected LAA. Water, yeast, botanicals etc. are tracked in the
	// recipe but pass through the projection as zero-LAA lines.
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
