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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/provincial"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

type ProvincialService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewProvincialService(db *tenantdb.DB, logger *slog.Logger) *ProvincialService {
	return &ProvincialService{db: db, logger: logger}
}

func (s *ProvincialService) fail(op string, err error) error {
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		return connectErr
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return connect.NewError(connect.CodeNotFound, errors.New("not found"))
	}
	s.logger.Error(op, "err", err)
	return connect.NewError(connect.CodeInternal, errors.New("internal error"))
}

func (s *ProvincialService) SaveProvincialRegistration(
	ctx context.Context,
	req *connect.Request[stillhousev1.SaveProvincialRegistrationRequest],
) (*connect.Response[stillhousev1.SaveProvincialRegistrationResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	jur := strings.ToUpper(strings.TrimSpace(in.GetJurisdiction()))
	if !validJurisdiction(jur) {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("jurisdiction must be an ISO subdivision code like CA-ON"))
	}
	registered, err := parseOptionalDate(in.GetRegisteredOn(), "registered_on")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	ended, err := parseOptionalDate(in.GetEndedOn(), "ended_on")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var out sqlcgen.ProvincialRegistration
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		out, e = q.SaveProvincialRegistration(ctx, sqlcgen.SaveProvincialRegistrationParams{
			TenantID: u.TenantID, Jurisdiction: jur,
			BoardName: in.GetBoardName(), RegistrationNo: in.GetRegistrationNo(),
			PortalUrl: in.GetPortalUrl(), Contact: in.GetContact(),
			RegisteredOn: registered, EndedOn: ended, Notes: in.GetNotes(),
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "provincial_registration",
			out.ID.String(), sqlcgen.AuditActionUpdate, map[string]any{
				"jurisdiction": jur,
				"board":        in.GetBoardName(),
			})
	})
	if err != nil {
		return nil, s.fail("SaveProvincialRegistration", err)
	}
	return connect.NewResponse(&stillhousev1.SaveProvincialRegistrationResponse{
		Registration: registrationToProto(out),
	}), nil
}

func (s *ProvincialService) ListProvincialRegistrations(
	ctx context.Context,
	_ *connect.Request[stillhousev1.ListProvincialRegistrationsRequest],
) (*connect.Response[stillhousev1.ListProvincialRegistrationsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var rows []sqlcgen.ProvincialRegistration
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListProvincialRegistrations(ctx)
		return e
	}); err != nil {
		return nil, s.fail("ListProvincialRegistrations", err)
	}
	out := make([]*stillhousev1.ProvincialRegistration, 0, len(rows))
	for _, r := range rows {
		out = append(out, registrationToProto(r))
	}
	return connect.NewResponse(&stillhousev1.ListProvincialRegistrationsResponse{
		Registrations: out,
	}), nil
}

func (s *ProvincialService) DeleteProvincialRegistration(
	ctx context.Context,
	req *connect.Request[stillhousev1.DeleteProvincialRegistrationRequest],
) (*connect.Response[stillhousev1.DeleteProvincialRegistrationResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if e := assertNoLegalHold(ctx, q, "a provincial registration"); e != nil {
			return e
		}
		if e := q.DeleteProvincialRegistration(ctx, id); e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "provincial_registration",
			id.String(), sqlcgen.AuditActionDelete, map[string]any{})
	}); err != nil {
		return nil, s.fail("DeleteProvincialRegistration", err)
	}
	return connect.NewResponse(&stillhousev1.DeleteProvincialRegistrationResponse{}), nil
}

func (s *ProvincialService) SaveProvincialReportDefinition(
	ctx context.Context,
	req *connect.Request[stillhousev1.SaveProvincialReportDefinitionRequest],
) (*connect.Response[stillhousev1.SaveProvincialReportDefinitionResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	id, err := parseOptionalUUID(in.GetId(), "id")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	regID, err := uuid.Parse(in.GetRegistrationId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("choose the jurisdiction this report is owed to"))
	}
	if strings.TrimSpace(in.GetName()) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("give the report a name — the one the board calls it"))
	}
	cadence, err := cadenceToDB(in.GetCadence())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	prov, err := requirementProvenanceToDB(in.GetProvenance())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	// A sourced requirement has to say what sourced it, or the flag is a
	// self-assessment nobody can check. The table's CHECK holds the same
	// line; this is the version with a sentence attached.
	if prov == sqlcgen.RequirementProvenanceSourced &&
		strings.TrimSpace(in.GetAuthority()) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("a sourced requirement has to cite its source — a URL, a "+
				"policy number, or the letter it came in"))
	}
	confirmed, err := parseOptionalDate(in.GetConfirmedOn(), "confirmed_on")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	dueDays := in.GetDueDaysAfterPeriodEnd()
	if dueDays < -1 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("due days cannot be negative; leave it unset if you do not know"))
	}

	var out sqlcgen.ProvincialReportDefinition
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if _, e := q.GetProvincialRegistration(ctx, regID); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return connect.NewError(connect.CodeNotFound,
					errors.New("that jurisdiction is not registered"))
			}
			return e
		}
		var e error
		out, e = q.SaveProvincialReportDefinition(ctx, sqlcgen.SaveProvincialReportDefinitionParams{
			ID: id, TenantID: u.TenantID, RegistrationID: regID,
			Name: in.GetName(), Cadence: cadence,
			DueDaysAfterPeriodEnd: dueDays,
			FollowsExciseClock:    in.GetFollowsExciseClock(),
			Provenance:            prov, Authority: in.GetAuthority(),
			ConfirmedOn: confirmed, Notes: in.GetNotes(), Archived: in.GetArchived(),
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "provincial_report_definition",
			out.ID.String(), sqlcgen.AuditActionUpdate, map[string]any{
				"name":       out.Name,
				"cadence":    string(cadence),
				"provenance": string(prov),
				"authority":  out.Authority,
			})
	})
	if err != nil {
		return nil, s.fail("SaveProvincialReportDefinition", err)
	}
	return connect.NewResponse(&stillhousev1.SaveProvincialReportDefinitionResponse{
		Definition: definitionToProto(out, "", ""),
	}), nil
}

func (s *ProvincialService) ListProvincialReportDefinitions(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListProvincialReportDefinitionsRequest],
) (*connect.Response[stillhousev1.ListProvincialReportDefinitionsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var rows []sqlcgen.ListProvincialReportDefinitionsRow
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListProvincialReportDefinitions(ctx, req.Msg.GetIncludeArchived())
		return e
	}); err != nil {
		return nil, s.fail("ListProvincialReportDefinitions", err)
	}
	out := make([]*stillhousev1.ProvincialReportDefinition, 0, len(rows))
	for _, r := range rows {
		out = append(out, definitionToProto(sqlcgen.ProvincialReportDefinition{
			ID: r.ID, RegistrationID: r.RegistrationID, Name: r.Name,
			Cadence: r.Cadence, DueDaysAfterPeriodEnd: r.DueDaysAfterPeriodEnd,
			FollowsExciseClock: r.FollowsExciseClock, Provenance: r.Provenance,
			Authority: r.Authority, ConfirmedOn: r.ConfirmedOn,
			Notes: r.Notes, Archived: r.Archived,
		}, r.Jurisdiction, r.BoardName))
	}
	return connect.NewResponse(&stillhousev1.ListProvincialReportDefinitionsResponse{
		Definitions: out,
	}), nil
}

// GenerateProvincialPeriods materialises the periods a definition implies.
//
// Idempotent: the upsert never duplicates a period, and never moves a due
// date already set. That second half matters for the same reason it does
// on the B266 — a change of definition must not silently restate when a
// past report was due.
func (s *ProvincialService) GenerateProvincialPeriods(
	ctx context.Context,
	req *connect.Request[stillhousev1.GenerateProvincialPeriodsRequest],
) (*connect.Response[stillhousev1.GenerateProvincialPeriodsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	defID, err := uuid.Parse(req.Msg.GetDefinitionId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid definition_id"))
	}
	from, err := parseDate(req.Msg.GetFrom(), "from")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	to, err := parseDate(req.Msg.GetTo(), "to")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	out := &stillhousev1.GenerateProvincialPeriodsResponse{}
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		def, e := q.GetProvincialReportDefinition(ctx, defID)
		if e != nil {
			return e
		}
		tenant, e := q.GetTenantByID(ctx, u.TenantID)
		if e != nil {
			return e
		}
		periods, e := provincial.Periods(
			cadenceToProvincial(def.Cadence),
			tenantFilingBasis(tenant),
			def.FollowsExciseClock,
			from, to,
			int(def.DueDaysAfterPeriodEnd),
		)
		if e != nil {
			return connect.NewError(connect.CodeFailedPrecondition, e)
		}
		for _, p := range periods {
			row, ie := q.UpsertProvincialReportPeriod(ctx, sqlcgen.UpsertProvincialReportPeriodParams{
				TenantID:     u.TenantID,
				DefinitionID: defID,
				PeriodStart:  pgtype.Date{Valid: true, Time: p.Start},
				PeriodEnd:    pgtype.Date{Valid: true, Time: p.End},
				DueOn:        pgtype.Date{Valid: !p.DueOn.IsZero(), Time: p.DueOn},
			})
			if ie != nil {
				return ie
			}
			out.Periods = append(out.Periods, periodToProto(row,
				def.Name, def.Jurisdiction, def.BoardName))
		}
		return nil
	})
	if err != nil {
		return nil, s.fail("GenerateProvincialPeriods", err)
	}
	return connect.NewResponse(out), nil
}

func (s *ProvincialService) ListProvincialReportPeriods(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListProvincialReportPeriodsRequest],
) (*connect.Response[stillhousev1.ListProvincialReportPeriodsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var rows []sqlcgen.ListProvincialReportPeriodsRow
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListProvincialReportPeriods(ctx, req.Msg.GetUnfiledOnly())
		return e
	}); err != nil {
		return nil, s.fail("ListProvincialReportPeriods", err)
	}
	out := make([]*stillhousev1.ProvincialReportPeriod, 0, len(rows))
	for _, r := range rows {
		out = append(out, periodToProto(sqlcgen.ProvincialReportPeriod{
			ID: r.ID, DefinitionID: r.DefinitionID,
			PeriodStart: r.PeriodStart, PeriodEnd: r.PeriodEnd, DueOn: r.DueOn,
			FiledAt: r.FiledAt, Acknowledgement: r.Acknowledgement, Notes: r.Notes,
		}, r.DefinitionName, r.Jurisdiction, r.BoardName))
	}
	return connect.NewResponse(&stillhousev1.ListProvincialReportPeriodsResponse{
		Periods: out,
	}), nil
}

func (s *ProvincialService) MarkProvincialReportFiled(
	ctx context.Context,
	req *connect.Request[stillhousev1.MarkProvincialReportFiledRequest],
) (*connect.Response[stillhousev1.MarkProvincialReportFiledResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	// A period marked filed with nothing to show for it is a claim, not a
	// record — the same rule the B266's filing acknowledgement follows.
	ack := strings.TrimSpace(req.Msg.GetAcknowledgement())
	if ack == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("record what the board gave back — a confirmation number, "+
				"a receipt, or the date and who you spoke to"))
	}

	var out sqlcgen.ProvincialReportPeriod
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		out, e = q.MarkProvincialReportFiled(ctx, sqlcgen.MarkProvincialReportFiledParams{
			ID: id, FiledBy: uuid.NullUUID{UUID: u.ID, Valid: true},
			Acknowledgement: ack, Notes: req.Msg.GetNotes(),
		})
		if errors.Is(e, pgx.ErrNoRows) {
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("that period has already been filed"))
		}
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "provincial_report_period",
			out.ID.String(), sqlcgen.AuditActionUpdate, map[string]any{
				"period_start":    out.PeriodStart.Time.Format("2006-01-02"),
				"period_end":      out.PeriodEnd.Time.Format("2006-01-02"),
				"acknowledgement": ack,
			})
	})
	if err != nil {
		return nil, s.fail("MarkProvincialReportFiled", err)
	}
	return connect.NewResponse(&stillhousev1.MarkProvincialReportFiledResponse{
		Period: periodToProto(out, "", "", ""),
	}), nil
}

// ProvincialSalesReport is the figures a provincial report is built from.
func (s *ProvincialService) ProvincialSalesReport(
	ctx context.Context,
	req *connect.Request[stillhousev1.ProvincialSalesReportRequest],
) (*connect.Response[stillhousev1.ProvincialSalesReportResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	start, err := parseDate(req.Msg.GetPeriodStart(), "period_start")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	end, err := parseDate(req.Msg.GetPeriodEnd(), "period_end")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if end.Before(start) {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("the period ends before it starts"))
	}
	jur := strings.ToUpper(strings.TrimSpace(req.Msg.GetJurisdiction()))
	var jurArg pgtype.Text
	if jur != "" {
		if !validJurisdiction(jur) {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				errors.New("jurisdiction must be an ISO subdivision code like CA-ON"))
		}
		jurArg = pgtype.Text{String: jur, Valid: true}
	}

	var (
		rows         []sqlcgen.ProvincialSalesInPeriodRow
		unattributed sqlcgen.ProvincialSalesUnattributedRow
	)
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ProvincialSalesInPeriod(ctx, sqlcgen.ProvincialSalesInPeriodParams{
			PeriodStart:  pgtype.Date{Valid: true, Time: start},
			PeriodEnd:    pgtype.Date{Valid: true, Time: end},
			Jurisdiction: jurArg,
		})
		if e != nil {
			return e
		}
		unattributed, e = q.ProvincialSalesUnattributed(ctx,
			sqlcgen.ProvincialSalesUnattributedParams{
				PeriodStart: pgtype.Date{Valid: true, Time: start},
				PeriodEnd:   pgtype.Date{Valid: true, Time: end},
			})
		return e
	}); err != nil {
		return nil, s.fail("ProvincialSalesReport", err)
	}

	out := &stillhousev1.ProvincialSalesReportResponse{
		Jurisdiction: jur,
		PeriodStart:  start.Format("2006-01-02"),
		PeriodEnd:    end.Format("2006-01-02"),
		Basis: "Removals to a customer whose jurisdiction is this one, by the date " +
			"they left. The jurisdiction is the buyer's, not the stamps' — a case " +
			"stamped for one province and sold into another belongs to the second. " +
			"Voided removals are excluded. Duty is what Stillhouse charged, which " +
			"is federal excise and not any provincial levy.",
		UnattributedBottles:  unattributed.Bottles,
		UnattributedLaa:      unattributed.Laa,
		UnattributedRemovals: unattributed.Removals,
	}
	for _, r := range rows {
		out.Lines = append(out.Lines, &stillhousev1.ProvincialSalesLine{
			Jurisdiction: r.Jurisdiction,
			ProductId:    r.ProductID.String(), ProductName: r.ProductName,
			Gtin: r.Gtin, BottleSizeMl: r.BottleSizeMl, BottleAbvPct: r.TargetAbvPct,
			Bottles: r.Bottles, Litres: r.Litres, Laa: r.Laa,
			DutyCad: r.DutyCad, Removals: r.Removals,
		})
		out.TotalBottles += r.Bottles
		out.TotalLitres += r.Litres
		out.TotalLaa += r.Laa
		out.TotalDutyCad += r.DutyCad
	}
	return connect.NewResponse(out), nil
}

// --- converters ---

// validJurisdiction accepts the ISO 3166-2 subdivision codes the rest of
// Stillhouse already uses. Deliberately a shape check rather than a list:
// a shipped list of provinces would be one more thing to keep current,
// and the codes are stable enough that the shape is the useful part.
func validJurisdiction(s string) bool {
	if len(s) != 5 || s[:3] != "CA-" {
		return false
	}
	for _, r := range s[3:] {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

func parseDate(v, field string) (time.Time, error) {
	t, err := time.Parse("2006-01-02", strings.TrimSpace(v))
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be a date like 2026-01-31", field)
	}
	return t, nil
}

func registrationToProto(r sqlcgen.ProvincialRegistration) *stillhousev1.ProvincialRegistration {
	return &stillhousev1.ProvincialRegistration{
		Id: r.ID.String(), Jurisdiction: r.Jurisdiction, BoardName: r.BoardName,
		RegistrationNo: r.RegistrationNo, PortalUrl: r.PortalUrl, Contact: r.Contact,
		RegisteredOn: formatDate(r.RegisteredOn), EndedOn: formatDate(r.EndedOn),
		Notes: r.Notes,
	}
}

func definitionToProto(
	d sqlcgen.ProvincialReportDefinition, jurisdiction, board string,
) *stillhousev1.ProvincialReportDefinition {
	return &stillhousev1.ProvincialReportDefinition{
		Id: d.ID.String(), RegistrationId: d.RegistrationID.String(),
		Jurisdiction: jurisdiction, BoardName: board,
		Name: d.Name, Cadence: cadenceToProto(d.Cadence),
		DueDaysAfterPeriodEnd: d.DueDaysAfterPeriodEnd,
		FollowsExciseClock:    d.FollowsExciseClock,
		Provenance:            requirementProvenanceToProto(d.Provenance),
		Authority:             d.Authority,
		ConfirmedOn:           formatDate(d.ConfirmedOn),
		Notes:                 d.Notes, Archived: d.Archived,
	}
}

func periodToProto(
	p sqlcgen.ProvincialReportPeriod, defName, jurisdiction, board string,
) *stillhousev1.ProvincialReportPeriod {
	out := &stillhousev1.ProvincialReportPeriod{
		Id: p.ID.String(), DefinitionId: p.DefinitionID.String(),
		DefinitionName: defName, Jurisdiction: jurisdiction, BoardName: board,
		PeriodStart: formatDate(p.PeriodStart), PeriodEnd: formatDate(p.PeriodEnd),
		DueOn: formatDate(p.DueOn), Acknowledgement: p.Acknowledgement, Notes: p.Notes,
	}
	if p.FiledAt.Valid {
		out.FiledAt = timestamppb.New(p.FiledAt.Time)
	}
	// A period with no due date is never overdue. Inventing the deadline
	// would be worse than having none, because it would look like one.
	if p.DueOn.Valid && !p.FiledAt.Valid {
		days := int32(time.Until(p.DueOn.Time).Hours() / 24)
		out.DaysUntilDue = days
		out.Overdue = days < 0
	}
	return out
}

func cadenceToProto(c sqlcgen.ReportingCadence) stillhousev1.ReportingCadence {
	switch c {
	case sqlcgen.ReportingCadenceMonthly:
		return stillhousev1.ReportingCadence_REPORTING_CADENCE_MONTHLY
	case sqlcgen.ReportingCadenceQuarterly:
		return stillhousev1.ReportingCadence_REPORTING_CADENCE_QUARTERLY
	case sqlcgen.ReportingCadenceSemiAnnual:
		return stillhousev1.ReportingCadence_REPORTING_CADENCE_SEMI_ANNUAL
	case sqlcgen.ReportingCadenceAnnual:
		return stillhousev1.ReportingCadence_REPORTING_CADENCE_ANNUAL
	case sqlcgen.ReportingCadencePerShipment:
		return stillhousev1.ReportingCadence_REPORTING_CADENCE_PER_SHIPMENT
	default:
		return stillhousev1.ReportingCadence_REPORTING_CADENCE_OTHER
	}
}

func cadenceToDB(c stillhousev1.ReportingCadence) (sqlcgen.ReportingCadence, error) {
	switch c {
	case stillhousev1.ReportingCadence_REPORTING_CADENCE_MONTHLY:
		return sqlcgen.ReportingCadenceMonthly, nil
	case stillhousev1.ReportingCadence_REPORTING_CADENCE_QUARTERLY:
		return sqlcgen.ReportingCadenceQuarterly, nil
	case stillhousev1.ReportingCadence_REPORTING_CADENCE_SEMI_ANNUAL:
		return sqlcgen.ReportingCadenceSemiAnnual, nil
	case stillhousev1.ReportingCadence_REPORTING_CADENCE_ANNUAL:
		return sqlcgen.ReportingCadenceAnnual, nil
	case stillhousev1.ReportingCadence_REPORTING_CADENCE_PER_SHIPMENT:
		return sqlcgen.ReportingCadencePerShipment, nil
	case stillhousev1.ReportingCadence_REPORTING_CADENCE_OTHER:
		return sqlcgen.ReportingCadenceOther, nil
	default:
		return "", errors.New("say how often this report is owed")
	}
}

func cadenceToProvincial(c sqlcgen.ReportingCadence) provincial.Cadence {
	switch c {
	case sqlcgen.ReportingCadenceMonthly:
		return provincial.Monthly
	case sqlcgen.ReportingCadenceQuarterly:
		return provincial.Quarterly
	case sqlcgen.ReportingCadenceSemiAnnual:
		return provincial.SemiAnnual
	case sqlcgen.ReportingCadenceAnnual:
		return provincial.Annual
	case sqlcgen.ReportingCadencePerShipment:
		return provincial.PerShipment
	default:
		return provincial.Other
	}
}

func requirementProvenanceToProto(p sqlcgen.RequirementProvenance) stillhousev1.RequirementProvenance {
	switch p {
	case sqlcgen.RequirementProvenanceIndicative:
		return stillhousev1.RequirementProvenance_REQUIREMENT_PROVENANCE_INDICATIVE
	case sqlcgen.RequirementProvenanceSourced:
		return stillhousev1.RequirementProvenance_REQUIREMENT_PROVENANCE_SOURCED
	default:
		return stillhousev1.RequirementProvenance_REQUIREMENT_PROVENANCE_UNKNOWN
	}
}

func requirementProvenanceToDB(p stillhousev1.RequirementProvenance) (sqlcgen.RequirementProvenance, error) {
	switch p {
	case stillhousev1.RequirementProvenance_REQUIREMENT_PROVENANCE_INDICATIVE:
		return sqlcgen.RequirementProvenanceIndicative, nil
	case stillhousev1.RequirementProvenance_REQUIREMENT_PROVENANCE_SOURCED:
		return sqlcgen.RequirementProvenanceSourced, nil
	case stillhousev1.RequirementProvenance_REQUIREMENT_PROVENANCE_UNSPECIFIED,
		stillhousev1.RequirementProvenance_REQUIREMENT_PROVENANCE_UNKNOWN:
		return sqlcgen.RequirementProvenanceUnknown, nil
	default:
		return "", errors.New("unknown provenance")
	}
}
