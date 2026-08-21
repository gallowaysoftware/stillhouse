package rpc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// External bulk movements — the rest of B266 page 3 against EDM10-1-7.
//
// The plan called this "B266 covers a subset of the form's lines", which
// undersold it. Nothing in the application ever created a
// transfer_in_bond, transfer_out_in_bond, destruction or loss_unaccounted
// movement: the report has had lines for all four since it was written and
// they were structurally always zero. A distillery receiving spirit in
// bond, shipping it out in bond, destroying spirit under CRA supervision
// or writing off an unaccounted loss had no path at all, and the return
// quietly said none of it happened.
//
// Marked special containers are deliberately absent: they are packaging,
// not bulk, and need their own model (PLAN B3).

// externalMovement describes one reportable kind: which ledger reason it
// writes, which way the alcohol goes, and what the operator has to supply.
type externalMovement struct {
	reason  sqlcgen.BulkMovementReason
	inbound bool
	label   string
	// isLoss marks the kinds that take alcohol out of the ledger without it
	// going anywhere, and so carry a duty treatment (EDM3-4-1).
	isLoss bool
	// needsCounterparty marks the kinds where EDM10-1-7 wants the other
	// party named, not just a quantity — an in-bond transfer is reportable
	// by both ends, and the counterparty is what ties them together.
	needsCounterparty bool
}

var externalMovements = map[stillhousev1.BulkExternalMovementKind]externalMovement{
	stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_IMPORT: {
		reason: sqlcgen.BulkMovementReasonImportReceived, inbound: true,
		label: "imported bulk spirits", needsCounterparty: true,
	},
	stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_IN_BOND_IN: {
		reason: sqlcgen.BulkMovementReasonTransferInBond, inbound: true,
		label: "received in bond", needsCounterparty: true,
	},
	stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_FROM_SPIRITS_LICENSEE: {
		reason: sqlcgen.BulkMovementReasonReceivedFromSpiritsLicensee, inbound: true,
		label: "received from another spirits licensee", needsCounterparty: true,
	},
	stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_FROM_LICENSED_USER: {
		reason: sqlcgen.BulkMovementReasonReceivedFromLicensedUser, inbound: true,
		label: "received from a licensed user", needsCounterparty: true,
	},
	stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_PACKAGED_RETURNED_TO_BULK: {
		reason: sqlcgen.BulkMovementReasonPackagedReturnedToBulk, inbound: true,
		label: "packaged spirits returned to bulk",
	},

	stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_IN_BOND_OUT: {
		reason: sqlcgen.BulkMovementReasonTransferOutInBond,
		label:  "transferred out in bond", needsCounterparty: true,
	},
	stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_TO_SPIRITS_LICENSEE: {
		reason: sqlcgen.BulkMovementReasonDeliveredToSpiritsLicensee,
		label:  "delivered to another spirits licensee", needsCounterparty: true,
	},
	stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_TO_LICENSED_USER: {
		reason: sqlcgen.BulkMovementReasonDeliveredToLicensedUser,
		label:  "delivered to a licensed user", needsCounterparty: true,
	},
	stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_EXPORT: {
		reason: sqlcgen.BulkMovementReasonExported,
		label:  "exported", needsCounterparty: true,
	},
	stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_DENATURED_DA: {
		reason: sqlcgen.BulkMovementReasonDenaturedDa, label: "denatured to DA",
	},
	stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_DENATURED_SDA: {
		reason: sqlcgen.BulkMovementReasonDenaturedSda, label: "denatured to SDA",
	},
	stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_RETURNED_TO_PRODUCTION: {
		reason: sqlcgen.BulkMovementReasonReturnedToProduction, label: "returned to production",
	},
	stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_DESTRUCTION: {
		reason: sqlcgen.BulkMovementReasonDestruction, isLoss: true, label: "destroyed",
	},
	stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_UNACCOUNTED_LOSS: {
		reason: sqlcgen.BulkMovementReasonLossUnaccounted, isLoss: true, label: "unaccounted loss",
	},
}

// RecordBulkExternalMovement records bulk spirits arriving on or leaving
// the premises.
func (s *BulkService) RecordBulkExternalMovement(
	ctx context.Context,
	req *connect.Request[stillhousev1.RecordBulkExternalMovementRequest],
) (*connect.Response[stillhousev1.RecordBulkExternalMovementResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	containerID, err := uuid.Parse(in.GetContainerId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid container_id"))
	}
	spec, ok := externalMovements[in.GetKind()]
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("kind is required and must be one of the reportable B266 page 3 movements"))
	}
	counterparty := strings.TrimSpace(in.GetCounterpartyName())
	// A quantity with no counterparty cannot be reconciled against the
	// other party's return, which is the point of these lines being
	// reportable at both ends.
	if spec.needsCounterparty && counterparty == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
			"counterparty_name is required for spirits %s — the line is reportable at both ends and the quantity alone cannot be reconciled",
			spec.label))
	}
	if in.GetVolumeL() <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("volume_l must be > 0"))
	}
	if in.GetAbvPct() < 0 || in.GetAbvPct() > 100 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("abv_pct must be in [0, 100]"))
	}

	// A loss or a destruction can be classified where it is recorded: an
	// operator destroying spirits under a CRA approval has the approval in
	// front of them, and making them come back for it is how a period ends
	// up full of unclassified losses.
	lossTreatment := sqlcgen.LossDutyTreatmentUnclassified
	lossAuthority := ""
	if spec.isLoss {
		if in.GetLossDutyTreatment() != stillhousev1.LossDutyTreatment_LOSS_DUTY_TREATMENT_UNSPECIFIED {
			var lerr error
			lossTreatment, lossAuthority, lerr = lossTreatmentToDB(
				in.GetLossDutyTreatment(), in.GetLossTreatmentAuthority())
			if lerr != nil {
				return nil, connect.NewError(connect.CodeInvalidArgument, lerr)
			}
		}
	} else if in.GetLossDutyTreatment() != stillhousev1.LossDutyTreatment_LOSS_DUTY_TREATMENT_UNSPECIFIED {
		// Silently dropping it would leave the operator believing they had
		// classified something.
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
			"a duty treatment does not apply to spirits %s — it is only meaningful for a loss or a destruction",
			spec.label))
	}

	// Receiving spirit in bond means gauging it, and that gauge lands on a
	// return, so it goes through the same 20 °C correction as any other.
	measured, err := resolveStrength(strengthInput{
		ObservedVolumeL: in.GetVolumeL(),
		AbvPct:          in.GetAbvPct(),
		DensityKgM3:     in.GetDensityKgM3(),
		DensityIsSet:    in.GetDensityKgM3Set(),
		TemperatureC:    in.GetTemperatureC(),
		TemperatureSet:  in.GetTemperatureCSet(),
	})
	if err != nil {
		return nil, alcoholometryError(err)
	}
	occurredAt := timestampOrNow(in.GetOccurredAt())

	var (
		movement  sqlcgen.BulkMovement
		container sqlcgen.BulkContainer
		warnings  []string
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if e := assertDateNotInLockedPeriod(ctx, q, pgtype.Date{Valid: true, Time: occurredAt.Time}); e != nil {
			return e
		}
		locked, e := lockContainers(ctx, q, containerID)
		if e != nil {
			return e
		}
		vessel := locked[containerID]

		instr, e := checkInstruments(ctx, q, in.GetInstruments(), occurredAt.Time)
		if e != nil {
			return e
		}
		warnings = instr.warnings

		volume, abv, laa := measured.VolumeL20C, measured.StrengthPct20C, measured.LAA()

		// Packaged spirits returned to bulk take their quantity from the
		// bottles, not from a typed volume: the alcohol is whatever those
		// bottles hold, and letting the two disagree would credit bulk
		// with more than packaged gave up.
		var (
			pkgID    uuid.NullUUID
			unbottle pgtype.Int4
		)
		if spec.reason == sqlcgen.BulkMovementReasonPackagedReturnedToBulk {
			pkgID, unbottle, volume, abv, laa, e = unpackageToBulk(ctx, q, in)
			if e != nil {
				return e
			}
		}

		if spec.inbound {
			if vessel.CapacityL.Valid && vessel.CurrentVolumeL+volume > vessel.CapacityL.Float64+1e-6 {
				return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
					"%.4f L would overfill %s (%.4f L on hand, %.4f L capacity)",
					volume, vessel.Name, vessel.CurrentVolumeL, vessel.CapacityL.Float64))
			}
		} else {
			// You cannot ship, denature or destroy alcohol that is not
			// there. The CHECK would catch the negative; the operator
			// deserves the sentence.
			if vessel.CurrentLaa+1e-6 < laa {
				return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
					"%s holds %.4f LAA but this movement takes %.4f",
					vessel.Name, vessel.CurrentLaa, laa))
			}
			if vessel.CurrentVolumeL+1e-6 < volume {
				return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
					"%s holds %.4f L but this movement takes %.4f",
					vessel.Name, vessel.CurrentVolumeL, volume))
			}
		}

		src, dst := uuid.NullUUID{UUID: containerID, Valid: true}, uuid.NullUUID{}
		if spec.inbound {
			src, dst = uuid.NullUUID{}, uuid.NullUUID{UUID: containerID, Valid: true}
		}
		movement, e = q.InsertExternalBulkMovement(ctx, sqlcgen.InsertExternalBulkMovementParams{
			TenantID:                u.TenantID,
			SourceContainerID:       src,
			DestinationContainerID:  dst,
			VolumeL:                 volume,
			AbvPct:                  abv,
			Laa:                     laa,
			Reason:                  spec.reason,
			ReferenceType:           "external_movement",
			Notes:                   in.GetNotes(),
			OccurredAt:              occurredAt,
			CounterpartyName:        counterparty,
			CounterpartyLicenceNo:   strings.TrimSpace(in.GetCounterpartyLicenceNo()),
			DocumentReference:       strings.TrimSpace(in.GetDocumentReference()),
			TemperatureC:            optionalFloat(in.GetTemperatureCSet(), in.GetTemperatureC()),
			ObservedVolumeL:         optionalFloat(true, in.GetVolumeL()),
			ObservedDensityKgM3:     optionalFloat(in.GetDensityKgM3Set(), in.GetDensityKgM3()),
			VolumeFactorC:           measured.VolumeFactorC,
			StrengthSource:          strengthSourceToDB(measured.Source),
			VolumeInstrumentID:      instr.volume,
			StrengthInstrumentID:    instr.strength,
			TemperatureInstrumentID: instr.temperature,
			RecordedBy:              uuid.NullUUID{UUID: u.ID, Valid: true},
			LossDutyTreatment:       lossTreatment,
			LossTreatmentAuthority:  lossAuthority,
			PackagedInventoryID:     pkgID,
			BottlesUnpackaged:       unbottle,
		})
		if e != nil {
			return e
		}

		newVol, newLAA := vessel.CurrentVolumeL+volume, vessel.CurrentLaa+laa
		if !spec.inbound {
			newVol, newLAA = vessel.CurrentVolumeL-volume, vessel.CurrentLaa-laa
		}
		newABV := pgtype.Float8{}
		if newVol > 1e-9 {
			newABV = pgtype.Float8{Float64: newLAA / newVol * 100, Valid: true}
		} else {
			newVol, newLAA = 0, 0
		}
		container, e = q.UpdateBulkContainerBalance(ctx, sqlcgen.UpdateBulkContainerBalanceParams{
			ID: containerID, CurrentVolumeL: newVol, CurrentAbvPct: newABV, CurrentLaa: newLAA,
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "bulk_movement", movement.ID.String(),
			sqlcgen.AuditActionCreate, map[string]any{
				"kind":               spec.label,
				"reason":             string(spec.reason),
				"container":          vessel.Name,
				"laa":                laa,
				"counterparty":       counterparty,
				"document_reference": movement.DocumentReference,
			})
	})
	if err != nil {
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return nil, connectErr
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("container not found"))
		}
		if ce := classifyWriteErr(err, "container not found"); ce != nil {
			return nil, ce
		}
		s.logger.Error("RecordBulkExternalMovement", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	out := bulkMovementToProto(movement)
	if externalMovements[in.GetKind()].inbound {
		out.DestinationContainerName = container.Name
	} else {
		out.SourceContainerName = container.Name
	}
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		out.Instruments = newInstrumentCache(q, time.Now()).refs(ctx,
			movement.VolumeInstrumentID, movement.StrengthInstrumentID, movement.TemperatureInstrumentID)
		return nil
	}); err != nil {
		s.logger.Warn("RecordBulkExternalMovement: resolve instruments", "err", err)
	}
	return connect.NewResponse(&stillhousev1.RecordBulkExternalMovementResponse{
		Movement:  out,
		Container: bulkContainerToProto(container),
		Warnings:  warnings,
	}), nil
}

// unpackageToBulk takes bottles out of packaged inventory and returns the
// quantity of alcohol they held.
//
// The quantity comes from the bottles rather than from a typed volume:
// the alcohol going back into bulk is whatever those bottles held, and
// letting the operator type a different figure would credit bulk with more
// than packaged gave up — LAA created out of a keystroke.
//
// The duty consequence is not handled here. For an at-packaging licensee
// this stock was dutied when it was bottled, and getting that duty back is
// a B256 refund application — a separate form, and PLAN A9. Reporting the
// movement without inventing a refund is the correct behaviour, not an
// omission.
func unpackageToBulk(
	ctx context.Context,
	q *sqlcgen.Queries,
	in *stillhousev1.RecordBulkExternalMovementRequest,
) (uuid.NullUUID, pgtype.Int4, float64, float64, float64, error) {
	var (
		nilID  uuid.NullUUID
		nilInt pgtype.Int4
	)
	piID, err := uuid.Parse(in.GetPackagedInventoryId())
	if err != nil {
		return nilID, nilInt, 0, 0, 0, connect.NewError(connect.CodeInvalidArgument,
			errors.New("packaged_inventory_id is required to return packaged spirits to bulk"))
	}
	bottles := in.GetBottlesUnpackaged()
	if bottles <= 0 {
		return nilID, nilInt, 0, 0, 0, connect.NewError(connect.CodeInvalidArgument,
			errors.New("bottles_unpackaged must be > 0"))
	}
	// Locked before the check, same as a removal: without it two
	// unpackagings against one lot both read the same count and both
	// decrement (stage 140).
	lot, err := q.GetPackagedInventoryForUpdate(ctx, piID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nilID, nilInt, 0, 0, 0, connect.NewError(connect.CodeNotFound,
				errors.New("packaged inventory not found"))
		}
		return nilID, nilInt, 0, 0, 0, err
	}
	if lot.BottlesOnHand < bottles {
		return nilID, nilInt, 0, 0, 0, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("only %d bottles on hand", lot.BottlesOnHand))
	}
	product, err := q.GetProduct(ctx, lot.ProductID)
	if err != nil {
		return nilID, nilInt, 0, 0, 0, err
	}
	if _, err := q.DecrementPackagedOnHand(ctx, sqlcgen.DecrementPackagedOnHandParams{
		ID: piID, BottlesOnHand: bottles,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nilID, nilInt, 0, 0, 0, connect.NewError(connect.CodeFailedPrecondition,
				errors.New("those bottles are no longer on hand — reload and try again"))
		}
		return nilID, nilInt, 0, 0, 0, err
	}

	volume := float64(bottles) * float64(product.BottleSizeMl) / 1000
	abv := product.TargetAbvPct
	return uuid.NullUUID{UUID: piID, Valid: true},
		pgtype.Int4{Int32: bottles, Valid: true},
		volume, abv, volume * abv / 100, nil
}
