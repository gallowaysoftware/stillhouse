package importer

import (
	"fmt"
	"strings"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
)

// Parsing the words a person types into the enums the database holds.
//
// These are deliberately forgiving about spacing, case, hyphens and
// underscores, and deliberately strict about meaning: "spirits licensee"
// and "licensee" are different customers with different duty
// consequences, so a near-miss is rejected with the list rather than
// guessed at.

func normalise(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.NewReplacer("-", " ", "_", " ", "  ", " ").Replace(s)
}

func materialKind(s string) (sqlcgen.MaterialKind, error) {
	switch normalise(s) {
	case "grain":
		return sqlcgen.MaterialKindGrain, nil
	case "malt":
		return sqlcgen.MaterialKindMalt, nil
	case "yeast":
		return sqlcgen.MaterialKindYeast, nil
	case "ngs", "neutral grain spirit", "neutral spirit":
		return sqlcgen.MaterialKindNgs, nil
	case "botanical":
		return sqlcgen.MaterialKindBotanical, nil
	case "water":
		return sqlcgen.MaterialKindWater, nil
	case "packaging":
		return sqlcgen.MaterialKindPackaging, nil
	case "other", "":
		return sqlcgen.MaterialKindOther, nil
	}
	return "", fmt.Errorf("%q is not a material kind — use grain, malt, yeast, ngs, "+
		"botanical, water, packaging or other", s)
}

func spiritKind(s string) (sqlcgen.SpiritKind, error) {
	switch normalise(s) {
	case "canadian whisky", "canadian whiskey":
		return sqlcgen.SpiritKindCanadianWhisky, nil
	case "rye whisky", "rye whiskey", "rye":
		return sqlcgen.SpiritKindRyeWhisky, nil
	case "whisky", "whiskey":
		return sqlcgen.SpiritKindWhisky, nil
	case "gin":
		return sqlcgen.SpiritKindGin, nil
	case "vodka":
		return sqlcgen.SpiritKindVodka, nil
	case "rum":
		return sqlcgen.SpiritKindRum, nil
	case "brandy":
		return sqlcgen.SpiritKindBrandy, nil
	case "liqueur", "liquor":
		return sqlcgen.SpiritKindLiqueur, nil
	case "other", "":
		return sqlcgen.SpiritKindOther, nil
	}
	return "", fmt.Errorf("%q is not a spirit kind — use canadian whisky, rye whisky, whisky, "+
		"gin, vodka, rum, brandy, liqueur or other", s)
}

// customerKind returns both the customer kind and the excise
// consequence that follows from it, because they are one decision. A
// removal to a provincial board is duty-paid and a removal to another
// spirits licensee is not, and that follows from who they are rather
// than from anything on the movement.
func customerKind(s string) (sqlcgen.CustomerKind, string, error) {
	switch normalise(s) {
	case "provincial board", "board", "lcbo", "liquor board":
		return sqlcgen.CustomerKindProvincialBoard, "duty_paid_customer", nil
	case "licensee", "bar", "restaurant":
		return sqlcgen.CustomerKindLicensee, "duty_paid_customer", nil
	case "private retail", "private store", "retail":
		return sqlcgen.CustomerKindPrivateRetail, "duty_paid_customer", nil
	case "spirits licensee", "excise licensee", "distillery":
		return sqlcgen.CustomerKindSpiritsLicensee, "transfer_out_in_bond", nil
	case "export":
		return sqlcgen.CustomerKindExport, "export", nil
	case "on site retail", "on site", "tasting room", "shop":
		return sqlcgen.CustomerKindOnSiteRetail, "duty_paid_customer", nil
	case "other", "":
		return sqlcgen.CustomerKindOther, "other", nil
	}
	return "", "", fmt.Errorf("%q is not a customer kind — use provincial board, licensee, "+
		"private retail, spirits licensee, export, on-site retail or other", s)
}
