package rpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/labelcode"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

type LabelService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewLabelService(db *tenantdb.DB, logger *slog.Logger) *LabelService {
	return &LabelService{db: db, logger: logger}
}

// ResolveLabel turns whatever came off the scanner back into the row it
// names.
//
// A wedge scanner is a keyboard, so this is also what an operator typing
// gets, and it accepts the four things that are plausibly in front of
// them: a Stillhouse label code, a lot code from a bottling record, a
// cask name or burn-in serial, and a GTIN off a retail case. Only the
// first is unambiguous by construction; the rest are matched exactly and
// the caller is told when more than one row answers.
func (s *LabelService) ResolveLabel(
	ctx context.Context,
	req *connect.Request[stillhousev1.ResolveLabelRequest],
) (*connect.Response[stillhousev1.ResolveLabelResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	scanned := strings.TrimSpace(req.Msg.GetScanned())
	if scanned == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("nothing was scanned"))
	}

	var found []*stillhousev1.LabelTarget
	err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		found, e = resolveScan(ctx, q, scanned)
		return e
	})
	if err != nil {
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return nil, connectErr
		}
		s.logger.Error("ResolveLabel", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	// A caller that said what it wanted gets a refusal it can act on
	// rather than being sent somewhere else. Scanning a cask tag at the
	// pick screen is a mistake worth naming.
	if want := req.Msg.GetExpect(); want != stillhousev1.LabelKind_LABEL_KIND_UNSPECIFIED {
		kept := found[:0]
		var wrong *stillhousev1.LabelTarget
		for _, t := range found {
			if t.GetKind() == want {
				kept = append(kept, t)
			} else if wrong == nil {
				wrong = t
			}
		}
		if len(kept) == 0 && wrong != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"that is a %s (%s), not a %s",
				labelKindNoun(wrong.GetKind()), wrong.GetTitle(), labelKindNoun(want)))
		}
		found = kept
	}

	switch len(found) {
	case 0:
		return nil, connect.NewError(connect.CodeNotFound,
			fmt.Errorf("nothing here is labelled %q", scanned))
	case 1:
		return connect.NewResponse(&stillhousev1.ResolveLabelResponse{Target: found[0]}), nil
	default:
		// Guessing which cask the operator meant is worse than asking.
		return connect.NewResponse(&stillhousev1.ResolveLabelResponse{Ambiguous: found}), nil
	}
}

func resolveScan(
	ctx context.Context, q *sqlcgen.Queries, scanned string,
) ([]*stillhousev1.LabelTarget, error) {
	// A label code first, because it is the only input that says what kind
	// of thing it is and so cannot be confused with anything else.
	if kind, prefix, err := labelcode.Decode(scanned); err == nil {
		return resolveByPrefix(ctx, q, kind, prefix)
	}

	var out []*stillhousev1.LabelTarget
	lots, err := q.FindPackagedInventoryByLotCode(ctx, scanned)
	if err != nil {
		return nil, err
	}
	for _, l := range lots {
		out = append(out, lotTarget(l.ID, l.LotCode, l.Jurisdiction, l.BottlesOnHand, l.ProductName))
	}
	casks, err := q.FindBulkContainersByName(ctx, scanned)
	if err != nil {
		return nil, err
	}
	for _, c := range casks {
		out = append(out, containerTarget(c.ID, c.Name, c.Kind, c.Location,
			c.CurrentVolumeL, c.CurrentAbvPct, c.CurrentLaa))
	}
	prods, err := q.FindProductByGTIN(ctx, scanned)
	if err != nil {
		return nil, err
	}
	for _, p := range prods {
		out = append(out, productTarget(p))
	}
	return out, nil
}

func resolveByPrefix(
	ctx context.Context, q *sqlcgen.Queries, kind labelcode.Kind, prefix uint64,
) ([]*stillhousev1.LabelTarget, error) {
	hex := fmt.Sprintf("%016x", prefix)
	var out []*stillhousev1.LabelTarget
	switch kind {
	case labelcode.KindBarrel, labelcode.KindContainer:
		rows, err := q.FindBulkContainersByIDPrefix(ctx, hex)
		if err != nil {
			return nil, err
		}
		for _, c := range rows {
			// The kind letter distinguishes a cask tag from a tank label,
			// so a code that says barrel must not resolve to a tank.
			isBarrel := c.Kind == sqlcgen.BulkContainerKindBarrel
			if (kind == labelcode.KindBarrel) != isBarrel {
				continue
			}
			out = append(out, containerTarget(c.ID, c.Name, c.Kind, c.Location,
				c.CurrentVolumeL, c.CurrentAbvPct, c.CurrentLaa))
		}
	case labelcode.KindLot:
		rows, err := q.FindPackagedInventoryByIDPrefix(ctx, hex)
		if err != nil {
			return nil, err
		}
		for _, l := range rows {
			out = append(out, lotTarget(l.ID, l.LotCode, l.Jurisdiction, l.BottlesOnHand, l.ProductName))
		}
	case labelcode.KindShipment:
		rows, err := q.FindShipmentsByIDPrefix(ctx, hex)
		if err != nil {
			return nil, err
		}
		for _, sh := range rows {
			out = append(out, &stillhousev1.LabelTarget{
				Kind:     stillhousev1.LabelKind_LABEL_KIND_SHIPMENT,
				Id:       sh.ID.String(),
				Code:     labelcode.Encode(labelcode.KindShipment, sh.ID),
				Title:    fmt.Sprintf("Shipment %d", sh.ShipmentNo),
				Subtitle: sh.CustomerName,
			})
		}
	case labelcode.KindProduct:
		rows, err := q.FindProductsByIDPrefix(ctx, hex)
		if err != nil {
			return nil, err
		}
		for _, p := range rows {
			out = append(out, productTarget(p))
		}
	}
	return out, nil
}

// The row types coming back from the label lookups are all flattened
// joins, so these take the fields rather than a struct — one label shape,
// however the row was fetched.
func containerTarget(
	id uuid.UUID, name string, k sqlcgen.BulkContainerKind, location string,
	volumeL float64, abv pgtype.Float8, laa float64,
) *stillhousev1.LabelTarget {
	kind := labelcode.KindContainer
	pk := stillhousev1.LabelKind_LABEL_KIND_CONTAINER
	if k == sqlcgen.BulkContainerKindBarrel {
		kind, pk = labelcode.KindBarrel, stillhousev1.LabelKind_LABEL_KIND_BARREL
	}
	sub := bulkContainerKindNoun(k)
	if location != "" {
		sub += " · " + location
	}
	return &stillhousev1.LabelTarget{
		Kind:     pk,
		Id:       id.String(),
		Code:     labelcode.Encode(kind, id),
		Title:    name,
		Subtitle: sub,
		VolumeL:  volumeL,
		AbvPct:   abv.Float64,
		Laa:      laa,
	}
}

func lotTarget(
	id uuid.UUID, lotCode, jurisdiction string, bottles int32, productName string,
) *stillhousev1.LabelTarget {
	return &stillhousev1.LabelTarget{
		Kind:         stillhousev1.LabelKind_LABEL_KIND_LOT,
		Id:           id.String(),
		Code:         labelcode.Encode(labelcode.KindLot, id),
		Title:        lotCode,
		Subtitle:     productName,
		Bottles:      bottles,
		Jurisdiction: jurisdiction,
	}
}

// daysBetween is whole days from t to now, floored at zero — a fill date
// in the future is somebody's typo, not a negative age.
func daysBetween(t time.Time) int32 {
	d := int32(time.Since(t).Hours() / 24)
	if d < 0 {
		return 0
	}
	return d
}

func productTarget(p sqlcgen.Product) *stillhousev1.LabelTarget {
	return &stillhousev1.LabelTarget{
		Kind:     stillhousev1.LabelKind_LABEL_KIND_PRODUCT,
		Id:       p.ID.String(),
		Code:     labelcode.Encode(labelcode.KindProduct, p.ID),
		Title:    p.Name,
		Subtitle: fmt.Sprintf("%d mL · %.1f %%", p.BottleSizeMl, p.TargetAbvPct),
		AbvPct:   p.TargetAbvPct,
		Gtin:     p.Gtin,
	}
}

// ListLabelTargets is what a print sheet is built from: everything of one
// kind, with the figures that belong on its label already resolved.
func (s *LabelService) ListLabelTargets(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListLabelTargetsRequest],
) (*connect.Response[stillhousev1.ListLabelTargetsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	out := &stillhousev1.ListLabelTargetsResponse{}
	err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		switch req.Msg.GetKind() {
		case stillhousev1.LabelKind_LABEL_KIND_BARREL:
			rows, e := q.ListBarrels(ctx, req.Msg.GetIncludeArchived())
			if e != nil {
				return e
			}
			for _, b := range rows {
				t := &stillhousev1.LabelTarget{
					Kind:     stillhousev1.LabelKind_LABEL_KIND_BARREL,
					Id:       b.ID.String(),
					Code:     labelcode.Encode(labelcode.KindBarrel, b.ID),
					Title:    b.Name,
					Subtitle: barrelSubtitle(b),
					VolumeL:  b.CurrentVolumeL,
					AbvPct:   b.CurrentAbvPct.Float64,
					Laa:      b.CurrentLaa,
				}
				if b.FillDate.Valid {
					t.FillDate = b.FillDate.Time.Format("2006-01-02")
					t.DaysAged = daysBetween(b.FillDate.Time)
				}
				out.Targets = append(out.Targets, t)
			}
		case stillhousev1.LabelKind_LABEL_KIND_CONTAINER:
			rows, e := q.ListBulkContainers(ctx, req.Msg.GetIncludeArchived())
			if e != nil {
				return e
			}
			for _, c := range rows {
				out.Targets = append(out.Targets, containerTarget(c.ID, c.Name, c.Kind,
					c.Location, c.CurrentVolumeL, c.CurrentAbvPct, c.CurrentLaa))
			}
		case stillhousev1.LabelKind_LABEL_KIND_LOT:
			rows, e := q.ListPackagedInventory(ctx, false)
			if e != nil {
				return e
			}
			for _, l := range rows {
				if l.BottlesOnHand <= 0 {
					continue
				}
				t := lotTarget(l.ID, l.LotCode, l.Jurisdiction, l.BottlesOnHand, l.ProductName)
				t.AbvPct = l.TargetAbvPct
				out.Targets = append(out.Targets, t)
			}
		case stillhousev1.LabelKind_LABEL_KIND_PRODUCT:
			rows, e := q.ListProducts(ctx, false)
			if e != nil {
				return e
			}
			for _, p := range rows {
				out.Targets = append(out.Targets, productTarget(p))
			}
		default:
			return connect.NewError(connect.CodeInvalidArgument,
				errors.New("say what kind of label to print"))
		}
		return nil
	})
	if err != nil {
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return nil, connectErr
		}
		s.logger.Error("ListLabelTargets", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(out), nil
}

func labelKindNoun(k stillhousev1.LabelKind) string {
	switch k {
	case stillhousev1.LabelKind_LABEL_KIND_BARREL:
		return "cask"
	case stillhousev1.LabelKind_LABEL_KIND_CONTAINER:
		return "vessel"
	case stillhousev1.LabelKind_LABEL_KIND_LOT:
		return "packaged lot"
	case stillhousev1.LabelKind_LABEL_KIND_SHIPMENT:
		return "shipment"
	case stillhousev1.LabelKind_LABEL_KIND_PRODUCT:
		return "product"
	default:
		return "thing"
	}
}

func bulkContainerKindNoun(k sqlcgen.BulkContainerKind) string {
	switch k {
	case sqlcgen.BulkContainerKindBarrel:
		return "cask"
	case sqlcgen.BulkContainerKindTank:
		return "tank"
	default:
		return string(k)
	}
}

func barrelSubtitle(b sqlcgen.ListBarrelsRow) string {
	parts := make([]string, 0, 3)
	if b.CooperageSupplier != "" {
		parts = append(parts, b.CooperageSupplier)
	}
	if b.PriorUse != "" {
		parts = append(parts, b.PriorUse)
	}
	if b.Rickhouse != "" {
		parts = append(parts, b.Rickhouse)
	}
	if len(parts) == 0 {
		return "cask"
	}
	return strings.Join(parts, " · ")
}
