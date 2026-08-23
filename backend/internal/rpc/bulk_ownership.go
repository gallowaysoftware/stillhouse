package rpc

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// SetBulkContainerOwner records who owns what is in a container. An empty
// customer means the licensee does, which is the ordinary case and the
// default.
//
// Ownership decides whether the alcohol is an asset — the dashboard
// total, the inventory value, the cost of sales — and has nothing to do
// with the B266, which asks about possession. Setting it moves no
// alcohol and writes no movement: the spirits have not gone anywhere,
// and a container whose owner changed while it sat in the same rackhouse
// is on the same return either way.
func (s *BulkService) SetBulkContainerOwner(
	ctx context.Context,
	req *connect.Request[stillhousev1.SetBulkContainerOwnerRequest],
) (*connect.Response[stillhousev1.SetBulkContainerOwnerResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	ownerID, err := parseOptionalUUID(req.Msg.GetOwnerCustomerId(), "owner_customer_id")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var (
		out       sqlcgen.BulkContainer
		ownerName string
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		before, e := q.GetBulkContainer(ctx, id)
		if e != nil {
			return e
		}
		if ownerID.Valid {
			cust, ce := q.GetCustomer(ctx, ownerID.UUID)
			if ce != nil {
				if errors.Is(ce, pgx.ErrNoRows) {
					return connect.NewError(connect.CodeNotFound, errors.New("customer not found"))
				}
				return ce
			}
			ownerName = cust.Name
		}
		out, e = q.SetBulkContainerOwner(ctx, sqlcgen.SetBulkContainerOwnerParams{
			ID: id, OwnerCustomerID: ownerID,
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "bulk_container", id.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"container":  out.Name,
				"laa":        out.CurrentLaa,
				"owner_from": nullUUIDString(before.OwnerCustomerID),
				"owner_to":   nullUUIDString(out.OwnerCustomerID),
				"owner_name": ownerName,
			})
	})
	if err != nil {
		return nil, s.failBulk("SetBulkContainerOwner", err)
	}
	proto := bulkContainerToProto(out)
	proto.OwnerName = ownerName
	return connect.NewResponse(&stillhousev1.SetBulkContainerOwnerResponse{Container: proto}), nil
}

// SetBulkContainerPossession moves spirits on or off the premises without
// moving them between containers.
//
// This is the write the B266's bulk walk depends on. The closing balance
// sums containers we hold and then walks backwards through movements
// dated after the period end; a change of possession is a state
// transition on the container, which produces no movement of its own and
// so would be invisible to that walk. Two rules make it visible, and both
// are enforced here rather than described somewhere:
//
//  1. The change writes a bulk movement for the container's whole
//     balance, under an in-bond transfer reason. That is what the movement
//     actually is — spirits crossing a licensed boundary — and the B266
//     already has a line for it.
//
//  2. Nothing else may be recorded against a container held elsewhere.
//     You cannot gauge spirits you do not hold; whatever happens to them
//     is the holder's record, reconciled by a regauge on return.
//
// Given those two, the walk is correct with no change to its movement
// side. See the comment on SumBulkOnHandAsOf for the case analysis.
func (s *BulkService) SetBulkContainerPossession(
	ctx context.Context,
	req *connect.Request[stillhousev1.SetBulkContainerPossessionRequest],
) (*connect.Response[stillhousev1.SetBulkContainerPossessionResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	id, err := uuid.Parse(in.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	want, err := bulkPossessionToDB(in.GetPossession())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	holder := strings.TrimSpace(in.GetHeldByName())
	if want == sqlcgen.BulkPossessionHeldElsewhere && holder == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("name who is holding the spirits — a return that omits stock "+
				"has to be able to say where it went"))
	}
	occurredOn, err := parseDateOrToday(in.GetOccurredOn())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var (
		out      sqlcgen.BulkContainer
		movement sqlcgen.BulkMovement
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		// Locked, because the balance read here is the amount written onto
		// the movement — a concurrent fill would otherwise transfer a
		// figure that was never in the cask.
		before, e := q.GetBulkContainerForUpdate(ctx, id)
		if e != nil {
			return e
		}
		if before.Archived {
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("that container is archived"))
		}
		if before.Possession == want {
			return connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("%s is already %s", before.Name, possessionPhrase(want)))
		}
		// The movement lands on whichever return covers its date, so it
		// cannot be back-dated into a period already filed.
		if e := assertDateNotInLockedPeriod(ctx, q, occurredOn); e != nil {
			return e
		}
		if before.CurrentLaa <= 0 {
			// An empty container changing hands is bookkeeping, not a
			// movement of alcohol. Recording a zero-LAA in-bond transfer
			// would put a line on the return saying nothing crossed.
			out, e = q.SetBulkContainerPossession(ctx, sqlcgen.SetBulkContainerPossessionParams{
				ID: id, Possession: want, HeldByName: holder,
				HeldByLicenceNo: in.GetHeldByLicenceNo(),
			})
			if e != nil {
				return e
			}
			return audit.Write(ctx, q, u.TenantID, u.ID, "bulk_container", id.String(),
				sqlcgen.AuditActionUpdate, map[string]any{
					"container":  out.Name,
					"possession": string(want),
					"held_by":    holder,
					"laa":        0,
					"note":       "empty container; no movement written",
				})
		}

		reason := sqlcgen.BulkMovementReasonTransferOutInBond
		params := sqlcgen.InsertExternalBulkMovementParams{
			TenantID:          u.TenantID,
			SourceContainerID: uuid.NullUUID{UUID: id, Valid: true},
			VolumeL:           before.CurrentVolumeL,
			AbvPct:            before.CurrentAbvPct.Float64,
			Laa:               before.CurrentLaa,
			ReferenceType:     "possession_change",
			CounterpartyName:  holder,
			// The strength is the one already on the container. Nothing
			// was gauged — the spirits changed hands, they were not
			// measured — so the determination is recorded as uncorrected
			// rather than claiming a table lookup that did not happen.
			StrengthSource: sqlcgen.StrengthSourceUncorrected,
			RecordedBy:     uuid.NullUUID{UUID: u.ID, Valid: true},
			// Not a loss, so it carries no treatment. The column is an
			// enum with no empty member; leaving it zero would fail at
			// the insert rather than at review.
			LossDutyTreatment: sqlcgen.LossDutyTreatmentUnclassified,
		}
		if want == sqlcgen.BulkPossessionHeld {
			reason = sqlcgen.BulkMovementReasonTransferInBond
			params.SourceContainerID = uuid.NullUUID{}
			params.DestinationContainerID = uuid.NullUUID{UUID: id, Valid: true}
			// Coming back, the holder is the one we are receiving from.
			params.CounterpartyName = before.HeldByName
			params.CounterpartyLicenceNo = before.HeldByLicenceNo
		} else {
			params.CounterpartyLicenceNo = in.GetHeldByLicenceNo()
		}
		params.Reason = reason
		params.DocumentReference = in.GetDocumentReference()
		params.Notes = in.GetNotes()
		params.OccurredAt = pgtype.Timestamptz{Time: dayStart(occurredOn.Time), Valid: true}

		movement, e = q.InsertExternalBulkMovement(ctx, params)
		if e != nil {
			return e
		}
		// The container keeps its contents. The movement records that the
		// spirits crossed a licensed boundary; the balance records what is
		// in the cask, and both are true of a cask sitting in somebody
		// else's warehouse.
		out, e = q.SetBulkContainerPossession(ctx, sqlcgen.SetBulkContainerPossessionParams{
			ID: id, Possession: want, HeldByName: holder,
			HeldByLicenceNo: in.GetHeldByLicenceNo(),
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "bulk_container", id.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"container":   out.Name,
				"possession":  string(want),
				"held_by":     holder,
				"laa":         before.CurrentLaa,
				"movement_id": movement.ID.String(),
				"reason":      string(reason),
				"occurred_on": occurredOn.Time.Format("2006-01-02"),
			})
	})
	if err != nil {
		return nil, s.failBulk("SetBulkContainerPossession", err)
	}
	res := &stillhousev1.SetBulkContainerPossessionResponse{
		Container: bulkContainerToProto(out),
	}
	if movement.ID != uuid.Nil {
		res.Movement = bulkMovementToProto(movement)
	}
	return connect.NewResponse(res), nil
}

// ListThirdPartySpirits is everything that is not simply ours-and-here:
// the list to read before signing a return or valuing an inventory.
func (s *BulkService) ListThirdPartySpirits(
	ctx context.Context,
	_ *connect.Request[stillhousev1.ListThirdPartySpiritsRequest],
) (*connect.Response[stillhousev1.ListThirdPartySpiritsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var rows []sqlcgen.ListThirdPartyBulkContainersRow
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListThirdPartyBulkContainers(ctx)
		return e
	}); err != nil {
		return nil, s.failBulk("ListThirdPartySpirits", err)
	}
	out := &stillhousev1.ListThirdPartySpiritsResponse{}
	for _, r := range rows {
		c := bulkContainerToProto(sqlcgen.BulkContainer{
			ID: r.ID, TenantID: r.TenantID, Name: r.Name, Kind: r.Kind,
			CapacityL: r.CapacityL, Location: r.Location, Notes: r.Notes,
			Archived: r.Archived, CurrentVolumeL: r.CurrentVolumeL,
			CurrentAbvPct: r.CurrentAbvPct, CurrentLaa: r.CurrentLaa,
			CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			LocationID: r.LocationID, OwnerCustomerID: r.OwnerCustomerID,
			Possession: r.Possession, HeldByName: r.HeldByName,
			HeldByLicenceNo:     r.HeldByLicenceNo,
			PossessionChangedAt: r.PossessionChangedAt,
		})
		c.OwnerName = r.OwnerName
		out.Containers = append(out.Containers, c)
		switch {
		case r.Possession == sqlcgen.BulkPossessionHeldElsewhere && !r.OwnerCustomerID.Valid:
			out.HeldElsewhereLaa += r.CurrentLaa
		case r.Possession == sqlcgen.BulkPossessionHeld && r.OwnerCustomerID.Valid:
			out.HeldForOthersLaa += r.CurrentLaa
		}
	}
	return connect.NewResponse(out), nil
}

func possessionPhrase(p sqlcgen.BulkPossession) string {
	if p == sqlcgen.BulkPossessionHeldElsewhere {
		return "recorded as held elsewhere"
	}
	return "recorded as held here"
}

func (s *BulkService) failBulk(op string, err error) error {
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		return connectErr
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return connect.NewError(connect.CodeNotFound, errors.New("container not found"))
	}
	s.logger.Error(op, "err", err)
	return connect.NewError(connect.CodeInternal, errors.New("internal error"))
}
