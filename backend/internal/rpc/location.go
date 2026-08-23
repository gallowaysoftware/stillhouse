package rpc

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

type LocationService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewLocationService(db *tenantdb.DB, logger *slog.Logger) *LocationService {
	return &LocationService{db: db, logger: logger}
}

func (s *LocationService) ListLocations(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListLocationsRequest],
) (*connect.Response[stillhousev1.ListLocationsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var rows []sqlcgen.ListLocationsRow
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListLocations(ctx, req.Msg.GetIncludeArchived())
		return e
	}); err != nil {
		s.logger.Error("ListLocations", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := make([]*stillhousev1.Location, 0, len(rows))
	for _, r := range rows {
		out = append(out, &stillhousev1.Location{
			Id: r.ID.String(), Name: r.Name, Address: r.Address,
			ExciseLicenceId: nullUUIDString(r.ExciseLicenceID),
			LicenceNumber:   r.LicenceNumber,
			RetailStore:     r.RetailStore, IsDefault: r.IsDefault,
			Notes: r.Notes, ContainerCount: r.ContainerCount,
			ArchivedAt: nullTimestamp(r.ArchivedAt),
		})
	}
	return connect.NewResponse(&stillhousev1.ListLocationsResponse{Locations: out}), nil
}

func (s *LocationService) SaveLocation(
	ctx context.Context,
	req *connect.Request[stillhousev1.SaveLocationRequest],
) (*connect.Response[stillhousev1.SaveLocationResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	name := strings.TrimSpace(in.GetName())
	if name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is required"))
	}
	var licenceID uuid.NullUUID
	if v := in.GetExciseLicenceId(); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid excise_licence_id"))
		}
		licenceID = uuid.NullUUID{UUID: id, Valid: true}
	}

	var row sqlcgen.Location
	err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		if in.GetId() == "" {
			row, e = q.CreateLocation(ctx, sqlcgen.CreateLocationParams{
				TenantID: u.TenantID, Name: name, Address: in.GetAddress(),
				ExciseLicenceID: licenceID, RetailStore: in.GetRetailStore(),
				Notes: in.GetNotes(),
			})
		} else {
			id, pe := uuid.Parse(in.GetId())
			if pe != nil {
				return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
			}
			var archived pgtype.Timestamptz
			if in.GetArchived() {
				archived = pgtype.Timestamptz{Valid: true, Time: time.Now().UTC()}
			}
			row, e = q.UpdateLocation(ctx, sqlcgen.UpdateLocationParams{
				ID: id, Name: name, Address: in.GetAddress(),
				ExciseLicenceID: licenceID, RetailStore: in.GetRetailStore(),
				Notes: in.GetNotes(), ArchivedAt: archived,
			})
		}
		if e != nil {
			return e
		}
		if in.GetMakeDefault() && !row.IsDefault {
			// Clearing the old default and setting the new one happen in
			// one transaction, so the partial unique index never sees two
			// and the tenant is never left with none.
			if e := q.ClearDefaultLocation(ctx); e != nil {
				return e
			}
			row, e = q.SetDefaultLocation(ctx, row.ID)
			if e != nil {
				return e
			}
		}
		if row.ArchivedAt.Valid && row.IsDefault {
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("this is the default location; make another one the default first"))
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "location", row.ID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"name": row.Name, "retail_store": row.RetailStore,
				"is_default": row.IsDefault, "archived": row.ArchivedAt.Valid,
			})
	})
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			return nil, ce
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("location not found"))
		}
		if ce := classifyWriteErr(err, "that licence no longer exists"); ce != nil {
			return nil, ce
		}
		s.logger.Error("SaveLocation", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.SaveLocationResponse{
		Location: &stillhousev1.Location{
			Id: row.ID.String(), Name: row.Name, Address: row.Address,
			ExciseLicenceId: nullUUIDString(row.ExciseLicenceID),
			RetailStore:     row.RetailStore, IsDefault: row.IsDefault,
			Notes: row.Notes, ArchivedAt: nullTimestamp(row.ArchivedAt),
		},
	}), nil
}

// SetContainerLocation moves a cask or tank between premises.
//
// It records where the vessel is, not a movement of alcohol: the LAA
// does not change and no bulk movement is written, because none
// happened. A transfer that also changes the contents is a transfer, and
// goes through the bulk path.
func (s *LocationService) SetContainerLocation(
	ctx context.Context,
	req *connect.Request[stillhousev1.SetContainerLocationRequest],
) (*connect.Response[stillhousev1.SetContainerLocationResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	containerID, err := uuid.Parse(req.Msg.GetContainerId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid container_id"))
	}
	var locationID uuid.NullUUID
	if v := req.Msg.GetLocationId(); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid location_id"))
		}
		locationID = uuid.NullUUID{UUID: id, Valid: true}
	}

	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		row, e := q.SetBulkContainerLocation(ctx, sqlcgen.SetBulkContainerLocationParams{
			ID: containerID, LocationID: locationID,
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "bulk_container", containerID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"event": "location_changed", "name": row.Name,
				"location_id": nullUUIDString(locationID),
				// Stated so the trail cannot be read as alcohol moving.
				"laa_unchanged": true,
			})
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("container not found"))
		}
		if ce := classifyWriteErr(err, "that location no longer exists"); ce != nil {
			return nil, ce
		}
		s.logger.Error("SetContainerLocation", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.SetContainerLocationResponse{
		ContainerId: containerID.String(), LocationId: req.Msg.GetLocationId(),
	}), nil
}

// RetailSupplyReport is the licensee's own side of the 30%
// single-retail-store supply rule (EDM8-1-1 ¶20).
//
// What it deliberately does not do is report a percentage against the
// store's whole stock. The rule turns on how much of a store's stock
// came from this licensee, and Stillhouse cannot see what else the store
// bought — producing that ratio would be inventing the number the rule
// is about. So it reports what it does know, each location's share of
// this licensee's own removals, and says plainly what the figure is not.
func (s *LocationService) RetailSupplyReport(
	ctx context.Context,
	req *connect.Request[stillhousev1.RetailSupplyReportRequest],
) (*connect.Response[stillhousev1.RetailSupplyReportResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	start, end, err := parseJournalPeriod(req.Msg.GetPeriodStart(), req.Msg.GetPeriodEnd())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var rows []sqlcgen.RetailSupplyByLocationRow
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.RetailSupplyByLocation(ctx, sqlcgen.RetailSupplyByLocationParams{
			PeriodStart: pgtype.Date{Valid: true, Time: start},
			PeriodEnd:   pgtype.Date{Valid: true, Time: end},
		})
		return e
	}); err != nil {
		s.logger.Error("RetailSupplyReport", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	out := &stillhousev1.RetailSupplyReportResponse{
		Caveat: "This is your own side of the rule: each location's share of everything " +
			"you removed in the period. The 30% test is against the retail store's whole " +
			"stock, which Stillhouse cannot see — it does not know what else the store " +
			"bought. Do not read these percentages as the rule's figure.",
	}
	for _, r := range rows {
		out.TotalLaaRemoved += r.LaaRemoved
	}
	for _, r := range rows {
		line := &stillhousev1.RetailSupplyLine{
			LocationId: r.LocationID.String(), LocationName: r.LocationName,
			RetailStore: r.RetailStore, LaaRemoved: r.LaaRemoved,
			BottlesRemoved: r.BottlesRemoved,
		}
		if out.TotalLaaRemoved > 0 {
			line.ShareOfTotalPct = r.LaaRemoved / out.TotalLaaRemoved * 100
		}
		out.Lines = append(out.Lines, line)
	}
	return connect.NewResponse(out), nil
}

func nullTimestamp(t pgtype.Timestamptz) *timestamppb.Timestamp {
	if !t.Valid {
		return nil
	}
	return timestamppb.New(t.Time)
}
