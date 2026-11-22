package mcp

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	pb "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// References are what somebody says across a room. A reference that fits
// more than one thing is an error naming them, never a choice made here.

type candidate struct {
	id    string
	label string
	names []string
}

func pick(what, ref string, all, open []candidate) (string, error) {
	ref = normalizeRef(ref)
	if ref == "" {
		switch len(open) {
		case 0:
			return "", fmt.Errorf("no %s is open", what)
		case 1:
			return open[0].id, nil
		}
		return "", ambiguous(what, open)
	}
	if _, err := uuid.Parse(ref); err == nil {
		return ref, nil
	}
	// A name is reused batch after batch, so an open run wins over a
	// finished one that went by the same name.
	for _, set := range [][]candidate{open, all} {
		var hits []candidate
		for _, c := range set {
			for _, n := range c.names {
				if normalizeRef(n) == ref {
					hits = append(hits, c)
					break
				}
			}
		}
		if len(hits) == 1 {
			return hits[0].id, nil
		}
		if len(hits) > 1 {
			return "", ambiguous(what, hits)
		}
	}
	if len(open) == 0 {
		return "", fmt.Errorf("no %s matches %q", what, ref)
	}
	return "", fmt.Errorf("no %s matches %q; there is %s", what, ref, labels(open))
}

func normalizeRef(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.ReplaceAll(s, "#", " "))), " ")
}

func labels(cs []candidate) string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.label)
	}
	return strings.Join(out, "; ")
}

func ambiguous(what string, cs []candidate) error {
	return fmt.Errorf("more than one %s fits — ask which: %s", what, labels(cs))
}

func resolveMash(ctx context.Context, d Deps, user sqlcgen.User, ref string) (string, error) {
	ctx, err := guard(ctx, user, "/stillhouse.v1.MashService/ListMashRuns")
	if err != nil {
		return "", err
	}
	resp, err := d.Mash.ListMashRuns(ctx, connect.NewRequest(&pb.ListMashRunsRequest{}))
	if err != nil {
		return "", err
	}
	var all, open []candidate
	for _, m := range resp.Msg.GetMashRuns() {
		c := candidate{m.GetId(), fmt.Sprintf("mash %d (%s, %s)", m.GetMashNo(), m.GetRecipeName(), m.GetMashDate()),
			numbered("mash", m.GetMashNo())}
		all = append(all, c)
		if mashOpen(m) {
			open = append(open, c)
		}
	}
	return pick("mash", ref, all, open)
}

func mashOpen(m *pb.MashRun) bool {
	return m.GetStatus() != pb.MashStatus_MASH_STATUS_DISTILLED && m.GetStatus() != pb.MashStatus_MASH_STATUS_CANCELLED
}

func resolveFermentation(ctx context.Context, d Deps, user sqlcgen.User, ref string) (string, error) {
	ctx, err := guard(ctx, user, "/stillhouse.v1.FermentationService/ListFermentationRuns")
	if err != nil {
		return "", err
	}
	resp, err := d.Fermentation.ListFermentationRuns(ctx, connect.NewRequest(&pb.ListFermentationRunsRequest{}))
	if err != nil {
		return "", err
	}
	var all, open []candidate
	for _, f := range resp.Msg.GetRuns() {
		c := candidate{f.GetId(), fmt.Sprintf("%s (mash %d, %s)", f.GetFermenterLabel(), f.GetMashNo(), f.GetRecipeName()),
			[]string{f.GetFermenterLabel(), fmt.Sprintf("mash %d", f.GetMashNo())}}
		all = append(all, c)
		if fermentationOpen(f) {
			open = append(open, c)
		}
	}
	return pick("fermentation", ref, all, open)
}

func fermentationOpen(f *pb.FermentationRun) bool {
	return f.GetStatus() != pb.FermentationStatus_FERMENTATION_STATUS_DISTILLED &&
		f.GetStatus() != pb.FermentationStatus_FERMENTATION_STATUS_CANCELLED
}

func resolveDistillation(ctx context.Context, d Deps, user sqlcgen.User, ref string) (string, error) {
	ctx, err := guard(ctx, user, "/stillhouse.v1.DistillationService/ListDistillationRuns")
	if err != nil {
		return "", err
	}
	resp, err := d.Distillation.ListDistillationRuns(ctx, connect.NewRequest(&pb.ListDistillationRunsRequest{}))
	if err != nil {
		return "", err
	}
	var all, open []candidate
	for _, r := range resp.Msg.GetRuns() {
		c := candidate{r.GetId(), fmt.Sprintf("run %d (%s, %s)", r.GetRunNo(), r.GetStillLabel(), r.GetRunDate()),
			append(numbered("run", r.GetRunNo()), r.GetStillLabel())}
		all = append(all, c)
		if distillationOpen(r) {
			open = append(open, c)
		}
	}
	return pick("still run", ref, all, open)
}

func distillationOpen(r *pb.DistillationRun) bool {
	return r.GetStatus() != pb.DistillationStatus_DISTILLATION_STATUS_GAUGED &&
		r.GetStatus() != pb.DistillationStatus_DISTILLATION_STATUS_CANCELLED &&
		r.GetVoidedAt() == nil
}

func numbered(what string, n int32) []string {
	return []string{strconv.Itoa(int(n)), fmt.Sprintf("%s %d", what, n)}
}

func resolveMaterial(ctx context.Context, d Deps, user sqlcgen.User, ref string) (string, error) {
	if normalizeRef(ref) == "" {
		return "", errors.New("say which material")
	}
	ctx, err := guard(ctx, user, "/stillhouse.v1.MaterialService/ListMaterials")
	if err != nil {
		return "", err
	}
	resp, err := d.Material.ListMaterials(ctx, connect.NewRequest(&pb.ListMaterialsRequest{}))
	if err != nil {
		return "", err
	}
	var all []candidate
	for _, m := range resp.Msg.GetMaterials() {
		all = append(all, candidate{m.GetId(), m.GetName(), []string{m.GetName()}})
	}
	return pick("material", ref, all, all)
}

func resolveLot(ctx context.Context, d Deps, user sqlcgen.User, materialID, ref string) (string, error) {
	if normalizeRef(ref) == "" {
		return "", nil
	}
	ctx, err := guard(ctx, user, "/stillhouse.v1.MaterialService/ListMaterialLots")
	if err != nil {
		return "", err
	}
	resp, err := d.Material.ListMaterialLots(ctx, connect.NewRequest(&pb.ListMaterialLotsRequest{MaterialId: materialID}))
	if err != nil {
		return "", err
	}
	var all, open []candidate
	for _, l := range resp.Msg.GetLots() {
		c := candidate{l.GetId(), fmt.Sprintf("lot %s (%g on hand)", l.GetSupplierLot(), l.GetQuantityOnHand()),
			[]string{l.GetSupplierLot(), "lot " + l.GetSupplierLot(), l.GetId()}}
		all = append(all, c)
		if l.GetQuantityOnHand() > 0 {
			open = append(open, c)
		}
	}
	// Checked against this material even when given as a UUID: the debit
	// itself does not check that the lot is one of the material's.
	if _, err := uuid.Parse(ref); err == nil {
		for _, c := range all {
			if c.id == strings.ToLower(ref) {
				return c.id, nil
			}
		}
		return "", errors.New("that lot is not a lot of that material")
	}
	return pick("lot of that material", ref, all, open)
}

func resolveRecipeVersion(ctx context.Context, d Deps, user sqlcgen.User, ref string) (string, error) {
	if normalizeRef(ref) == "" {
		return "", errors.New("say which recipe")
	}
	ctx, err := guard(ctx, user, "/stillhouse.v1.RecipeService/ListRecipes")
	if err != nil {
		return "", err
	}
	resp, err := d.Recipe.ListRecipes(ctx, connect.NewRequest(&pb.ListRecipesRequest{}))
	if err != nil {
		return "", err
	}
	var all []candidate
	for _, r := range resp.Msg.GetRecipes() {
		if r.GetCurrentVersionId() != "" {
			all = append(all, candidate{r.GetCurrentVersionId(), r.GetName(), []string{r.GetName(), r.GetId()}})
		}
	}
	if _, err := uuid.Parse(ref); err == nil {
		for _, c := range all {
			if c.names[1] == strings.ToLower(ref) {
				return c.id, nil
			}
		}
	}
	return pick("recipe", ref, all, all)
}

const (
	// The slack between a phone's clock and the server's.
	clockSkew = 2 * time.Minute
	// A model that gets the date wrong gets it wrong by months. Anything
	// older than this is entered in the web UI, where the date is a picker.
	oldestEntry = 30 * 24 * time.Hour
)

// observedAt returns nil for "now", which the services fill in themselves.
func observedAt(at string, minutesAgo *float64, now time.Time) (*timestamppb.Timestamp, error) {
	var t time.Time
	switch {
	case at != "" && minutesAgo != nil:
		return nil, errors.New("give at or minutes_ago, not both")
	case at != "":
		var err error
		if t, err = time.Parse(time.RFC3339, at); err != nil {
			return nil, errors.New("at must be RFC 3339 with an offset, like 2026-01-31T14:05:00-04:00")
		}
	case minutesAgo != nil:
		if math.IsNaN(*minutesAgo) || *minutesAgo < 0 || *minutesAgo > oldestEntry.Minutes() {
			return nil, errors.New("minutes_ago must be between 0 and 30 days")
		}
		t = now.Add(-time.Duration(*minutesAgo * float64(time.Minute)))
	default:
		return nil, nil
	}
	if t.After(now.Add(clockSkew)) {
		return nil, fmt.Errorf("%s is in the future; it is now %s", t.Format(time.RFC3339), now.UTC().Format(time.RFC3339))
	}
	if t.Before(now.Add(-oldestEntry)) {
		return nil, fmt.Errorf("%s is more than 30 days ago; it is now %s", t.Format(time.RFC3339), now.UTC().Format(time.RFC3339))
	}
	return timestamppb.New(t), nil
}

type protoEnum interface {
	~int32
	protoreflect.Enum
}

// enumFromName maps the words an operator would say. There is no default:
// an unrecognised status must not quietly move a run somewhere.
func enumFromName[E protoEnum](s string) (E, error) {
	var zero E
	values := zero.Descriptor().Values()
	prefix := strings.TrimSuffix(string(values.ByNumber(0).Name()), "UNSPECIFIED")
	want := strings.ReplaceAll(strings.ToUpper(strings.TrimSpace(s)), " ", "_")
	if want == "CANCELED" {
		want = "CANCELLED"
	}
	var names []string
	for i := range values.Len() {
		v := values.Get(i)
		if v.Number() == 0 {
			continue
		}
		name := strings.TrimPrefix(string(v.Name()), prefix)
		if name == want {
			return E(v.Number()), nil
		}
		names = append(names, strings.ToLower(name))
	}
	return zero, fmt.Errorf("%q is not one of: %s", s, strings.Join(names, ", "))
}
