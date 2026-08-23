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
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

// formatTimestamp renders a nullable timestamp as an ISO date, empty
// when unset — the timestamp counterpart of formatDate.
func formatTimestamp(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return t.Time.Format("2006-01-02")
}

// smallWoodMaxL is the ceiling for "small wood" in FDR B.02.020, which
// the maturation clock already uses. A cask above it matures spirits but
// does not make them Canadian Whisky, and it cannot support an age claim
// that depends on small wood.
const smallWoodMaxL = 700

type CertificateService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewCertificateService(db *tenantdb.DB, logger *slog.Logger) *CertificateService {
	return &CertificateService{db: db, logger: logger}
}

// AgeCertificate assembles the evidence behind an age and origin claim.
//
// It does not produce a certificate. A certificate of age and origin is
// signed by a Canadian official (EDM3-1-1 ¶43–46); what Stillhouse can do
// is set out what its own records support, name every cask it cannot
// account for, and let a person decide what to put their name to.
//
// The age claimed is the youngest cask in the run, because a blend is
// only as old as its youngest component. Where a cask's age is not
// recorded the claim is not quietly made over the gap — it is refused and
// the cask is named.
func (s *CertificateService) AgeCertificate(
	ctx context.Context,
	req *connect.Request[stillhousev1.AgeCertificateRequest],
) (*connect.Response[stillhousev1.AgeCertificateResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	runID, err := uuid.Parse(req.Msg.GetBottlingRunId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("say which bottling run the exported spirits came from"))
	}

	out := &stillhousev1.AgeCertificateResponse{
		BottlingRunId:      runID.String(),
		Consignee:          req.Msg.GetConsignee(),
		DestinationCountry: req.Msg.GetDestinationCountry(),
		Reference:          req.Msg.GetReference(),
		Basis: "Stillhouse does not certify anything. A certificate of age and " +
			"origin is signed by a Canadian official (EDM3-1-1 ¶43–46); this is " +
			"the evidence behind one. Age is taken from each cask's days in wood " +
			"as recorded when it was emptied, and the claim is the youngest cask " +
			"in the run, because a blend is only as old as its youngest part.",
	}

	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		run, e := q.GetBottlingRun(ctx, runID)
		if e != nil {
			return e
		}
		if run.VoidedAt.Valid {
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("that run was voided"))
		}
		product, e := q.GetProduct(ctx, run.ProductID)
		if e != nil {
			return e
		}
		tenant, e := q.GetTenantByID(ctx, u.TenantID)
		if e != nil {
			return e
		}
		out.LotCode = run.LotCode
		out.ProductName = product.Name
		out.BottleAbvPct = product.TargetAbvPct
		out.BottleCount = run.BottleCount
		out.BottledOn = formatDate(run.BottlingDate)
		out.ProducerName = tenant.Name
		out.ProducerLicenceNo = tenant.CraSpiritsLicenceNumber

		// A day's grace on the cutoff, the same as the costing walk: a
		// dump recorded the same evening as the bottling it fed is part
		// of that chain.
		cutoff := run.BottlingDate.Time.Add(24 * time.Hour)
		casks, e := q.AgeEvidenceForBottlingRun(ctx, sqlcgen.AgeEvidenceForBottlingRunParams{
			SourceContainerID: run.SourceContainerID,
			Before:            pgtype.Timestamptz{Valid: true, Time: cutoff},
		})
		if e != nil {
			return e
		}

		youngest := int32(-1)
		unknown := 0
		for _, c := range casks {
			small := c.CapacityL.Valid && c.CapacityL.Float64 <= smallWoodMaxL
			item := &stillhousev1.AgeEvidenceCask{
				ContainerId: c.ContainerID.String(), CaskName: c.CaskName,
				SerialBurnin: c.SerialBurnin, SmallWood: small,
				DumpedOn:    formatTimestamp(c.DumpedOn),
				WoodSpecies: c.WoodSpecies, PriorUse: c.PriorUse,
			}
			if c.CapacityL.Valid {
				item.CapacityL = c.CapacityL.Float64
			}
			if c.DumpedLaa.Valid {
				item.DumpedLaa = c.DumpedLaa.Float64
			}
			switch {
			case !c.DaysAgedAtDump.Valid:
				item.WhyNot = "no age was recorded when it was emptied, so nothing " +
					"here can say how long it was in wood"
				unknown++
			case !small:
				item.WhyNot = fmt.Sprintf(
					"at %.0f L it is not small wood, so time in it does not support "+
						"an age claim that depends on small wood", item.GetCapacityL())
				unknown++
			default:
				item.DaysAged = c.DaysAgedAtDump.Int32
				item.DaysAgedKnown = true
				if youngest < 0 || item.DaysAged < youngest {
					youngest = item.DaysAged
				}
			}
			// Age resets on redistillation, so a cask whose spirits went
			// back through the still cannot carry its original clock.
			redist, re := q.RedistillationsTouchingContainer(ctx, c.ContainerID)
			if re != nil {
				return re
			}
			if len(redist) > 0 {
				item.DaysAgedKnown = false
				item.WhyNot = fmt.Sprintf(
					"spirits from this cask were put back through the still on %s — "+
						"age resets on redistillation",
					redist[0].TakenOn.Time.Format("2006-01-02"))
				unknown++
			}
			out.Casks = append(out.Casks, item)
		}

		switch {
		case len(casks) == 0:
			out.Caveats = append(out.Caveats,
				"No cask fed this run's source vessel within the chain Stillhouse "+
					"can walk, so there is no age evidence at all. Spirits bottled "+
					"from a tank that was never filled from wood cannot carry an age.")
		case unknown > 0:
			out.Caveats = append(out.Caveats, fmt.Sprintf(
				"%d of the %d casks behind this run cannot support an age claim — "+
					"see the reason on each. The age below is not made over that gap.",
				unknown, len(casks)))
		}
		if youngest >= 0 && unknown == 0 {
			out.SupportableAgeYears = youngest / 365
			out.AgeSupportable = out.SupportableAgeYears > 0
			if !out.AgeSupportable {
				out.Caveats = append(out.Caveats, fmt.Sprintf(
					"The youngest cask was %d days in wood, which is under a year.",
					youngest))
			}
		} else if youngest >= 0 {
			out.Caveats = append(out.Caveats, fmt.Sprintf(
				"The oldest supportable figure across the casks that do have one is "+
					"%d years, but it is not offered as the run's age while any cask "+
					"is unaccounted for.", youngest/365))
		}
		if strings.TrimSpace(tenant.CraSpiritsLicenceNumber) == "" {
			out.Caveats = append(out.Caveats,
				"No CRA spirits licence number is on file, and a certificate has to "+
					"name it.")
		}
		return nil
	})
	if err != nil {
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return nil, connectErr
		}
		s.logger.Error("AgeCertificate", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(out), nil
}
