package rpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

type TraceabilityService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewTraceabilityService(db *tenantdb.DB, logger *slog.Logger) *TraceabilityService {
	return &TraceabilityService{db: db, logger: logger}
}

// TraceBottlingRun walks backward from a bottling_run and surfaces every
// meaningful upstream event as a flat list of TraceabilityNode entries.
// Depth indicated by leading "↳" characters in the headline so the caller
// can render without an extra field.
//
// The walk is intentionally bounded (recent feeds only, single-charge per
// distillation, no fan-out across multiple parent mashes in a single
// distillation). Useful for the common "what's behind this lot?" question;
// for recall-grade full-fanout traceability, follow per-charge from the
// nodes returned here.
func (s *TraceabilityService) TraceBottlingRun(
	ctx context.Context,
	req *connect.Request[stillhousev1.TraceBottlingRunRequest],
) (*connect.Response[stillhousev1.TraceBottlingRunResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	runID, err := uuid.Parse(req.Msg.GetBottlingRunId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid bottling_run_id"))
	}

	resp := &stillhousev1.TraceBottlingRunResponse{
		BottlingRunId: runID.String(),
	}

	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		run, e := q.GetBottlingRun(ctx, runID)
		if e != nil {
			return e
		}
		resp.LotCode = run.LotCode

		product, e := q.GetProduct(ctx, run.ProductID)
		if e != nil {
			return e
		}
		resp.Nodes = append(resp.Nodes, &stillhousev1.TraceabilityNode{
			Kind:        "bottling_run",
			Id:          run.ID.String(),
			Headline:    fmt.Sprintf("Bottling #%d — %s (lot %s)", run.RunNo, product.Name, run.LotCode),
			Detail:      fmt.Sprintf("%d × %d mL @ %.1f%% to %s · %s", run.BottleCount, product.BottleSizeMl, run.TankGaugeAbvPct, run.DestinationJurisdiction, formatDate(run.BottlingDate)),
			OccurredAt:  timestamppb.New(run.BottlingDate.Time),
		})

		// Walk recent feeds into the source container. bottling_date is
		// a pgtype.Date (midnight UTC); bump by 24h so same-day production
		// gauges are included.
		feedCutoff := run.BottlingDate.Time.Add(24 * time.Hour)
		feeds, e := q.BottlingRunChainFeeds(ctx, sqlcgen.BottlingRunChainFeedsParams{
			DestinationContainerID: uuid.NullUUID{UUID: run.SourceContainerID, Valid: true},
			OccurredAt:             pgtype.Timestamptz{Time: feedCutoff, Valid: true},
		})
		if e != nil {
			return e
		}
		// Limit to a reasonable number of feeds to keep the tree readable.
		if len(feeds) > 20 {
			feeds = feeds[:20]
		}
		for _, fd := range feeds {
			indent := "↳ "
			node := &stillhousev1.TraceabilityNode{
				Id:         fd.ID.String(),
				Headline:   fmt.Sprintf("%sFeed: %s → %s", indent, fd.SourceName.String, fd.DestinationName.String),
				Detail:     fmt.Sprintf("reason=%s · vol=%.2f L · abv=%.2f%% · LAA=%.4f", fd.Reason, fd.VolumeL, fd.AbvPct, fd.Laa),
				OccurredAt: timestamppb.New(fd.OccurredAt.Time),
				Kind:       "bulk_movement",
			}
			resp.Nodes = append(resp.Nodes, node)

			switch fd.Reason {
			case sqlcgen.BulkMovementReasonProductionGauge:
				// Walk into the distillation chain.
				chain, ce := q.DistillationChainFromGauge(ctx, fd.ID)
				if errors.Is(ce, pgx.ErrNoRows) {
					continue
				}
				if ce != nil {
					return ce
				}
				resp.Nodes = append(resp.Nodes,
					&stillhousev1.TraceabilityNode{
						Kind: "distillation_run", Id: chain.DistillationRunID.String(),
						Headline: fmt.Sprintf("  ↳ Distillation #%d (%s)", chain.DistillationRunNo, chain.StillLabel),
					},
					&stillhousev1.TraceabilityNode{
						Kind: "fermentation_run", Id: nullUUIDString(chain.FermentationRunID),
						Headline: fmt.Sprintf("    ↳ Fermentation %s", chain.FermenterLabel.String),
					},
					&stillhousev1.TraceabilityNode{
						Kind: "mash_run", Id: nullUUIDString(chain.MashRunID),
						Headline: fmt.Sprintf("      ↳ Mash #%d on %s", chain.MashNo.Int32, formatDate(chain.MashDate)),
					},
					&stillhousev1.TraceabilityNode{
						Kind: "recipe_version", Id: nullUUIDString(chain.RecipeVersionID),
						Headline: fmt.Sprintf("        ↳ Recipe: %s v%d", chain.RecipeName.String, chain.RecipeVersionNo.Int32),
					},
				)
			case sqlcgen.BulkMovementReasonInterTankTransfer:
				// May be a barrel dump.
				dump, de := q.BarrelDumpsForContainerFill(ctx, fd.ID)
				if de != nil {
					return de
				}
				for _, b := range dump {
					resp.Nodes = append(resp.Nodes, &stillhousev1.TraceabilityNode{
						Kind: "barrel", Id: b.BarrelID.String(),
						Headline: fmt.Sprintf("  ↳ Dumped from barrel: %s (%s)", b.BarrelName, b.CooperageSupplier.String),
						Detail:   fmt.Sprintf("aged %d days", b.DaysAgedAtDump.Int32),
					})
				}
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("bottling run not found"))
		}
		s.logger.Error("TraceBottlingRun", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(resp), nil
}
