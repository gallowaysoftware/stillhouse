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

// ListExciseLicences returns the register, ceased entries included.
//
// Nothing is hidden: a return filed under a licence that has since been
// surrendered still has to be explicable years later, and a register
// that quietly drops what it no longer needs is not a register.
func (s *TenantService) ListExciseLicences(
	ctx context.Context,
	_ *connect.Request[stillhousev1.ListExciseLicencesRequest],
) (*connect.Response[stillhousev1.ListExciseLicencesResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var (
		rows    []sqlcgen.ExciseLicence
		missing int32
	)
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListExciseLicences(ctx)
		if e != nil {
			return e
		}
		missing, e = q.CountLicencesMissingExpiry(ctx)
		return e
	}); err != nil {
		s.logger.Error("ListExciseLicences", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := make([]*stillhousev1.ExciseLicence, 0, len(rows))
	for _, r := range rows {
		out = append(out, licenceToProto(r))
	}
	return connect.NewResponse(&stillhousev1.ListExciseLicencesResponse{
		Licences:           out,
		MissingExpiryCount: missing,
	}), nil
}

// SaveExciseLicence creates or updates one entry. Audited, because which
// licences are held decides which returns exist and where duty falls —
// changing the register changes what the system believes it is filing.
func (s *TenantService) SaveExciseLicence(
	ctx context.Context,
	req *connect.Request[stillhousev1.SaveExciseLicenceRequest],
) (*connect.Response[stillhousev1.SaveExciseLicenceResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	kind, err := licenceKindToDB(in.GetKind())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	number := strings.TrimSpace(in.GetLicenceNumber())
	if number == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("licence_number is required"))
	}
	effectiveFrom, err := parseRequiredDate(in.GetEffectiveFrom(), "effective_from")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	expiresOn, err := parseOptionalDate(in.GetExpiresOn(), "expires_on")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	securityExpires, err := parseOptionalDate(in.GetSecurityExpiresOn(), "security_expires_on")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	ceasedOn, err := parseOptionalDate(in.GetCeasedOn(), "ceased_on")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if expiresOn.Valid && expiresOn.Time.Before(effectiveFrom.Time) {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("expires_on is before effective_from"))
	}
	var security pgtype.Numeric
	if v := strings.TrimSpace(in.GetSecurityAmountCad()); v != "" {
		if err := security.Scan(v); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				errors.New("security_amount_cad must be a decimal amount, e.g. 5000.00"))
		}
	}

	var row sqlcgen.ExciseLicence
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		if in.GetId() == "" {
			row, e = q.CreateExciseLicence(ctx, sqlcgen.CreateExciseLicenceParams{
				TenantID: u.TenantID, Kind: kind, LicenceNumber: number,
				EffectiveFrom: effectiveFrom, ExpiresOn: expiresOn,
				Premises: in.GetPremises(), SecurityAmountCad: security,
				SecurityExpiresOn: securityExpires, Notes: in.GetNotes(),
			})
		} else {
			id, pe := uuid.Parse(in.GetId())
			if pe != nil {
				return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
			}
			row, e = q.UpdateExciseLicence(ctx, sqlcgen.UpdateExciseLicenceParams{
				ID: id, Kind: kind, LicenceNumber: number,
				EffectiveFrom: effectiveFrom, ExpiresOn: expiresOn,
				Premises: in.GetPremises(), SecurityAmountCad: security,
				SecurityExpiresOn: securityExpires, Notes: in.GetNotes(),
				CeasedOn: ceasedOn,
			})
		}
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "excise_licence", row.ID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"kind":           string(row.Kind),
				"licence_number": row.LicenceNumber,
				"expires_on":     formatDate(row.ExpiresOn),
				"ceased":         row.CeasedOn.Valid,
			})
	})
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			return nil, ce
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("licence not found"))
		}
		if ce := classifyWriteErr(err, "the tenant no longer exists"); ce != nil {
			return nil, ce
		}
		s.logger.Error("SaveExciseLicence", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.SaveExciseLicenceResponse{
		Licence: licenceToProto(row),
	}), nil
}

func licenceToProto(l sqlcgen.ExciseLicence) *stillhousev1.ExciseLicence {
	return &stillhousev1.ExciseLicence{
		Id:                l.ID.String(),
		Kind:              licenceKindToProto(l.Kind),
		LicenceNumber:     l.LicenceNumber,
		EffectiveFrom:     formatDate(l.EffectiveFrom),
		ExpiresOn:         formatDate(l.ExpiresOn),
		Premises:          l.Premises,
		SecurityAmountCad: numericToDecimalString(l.SecurityAmountCad),
		SecurityExpiresOn: formatDate(l.SecurityExpiresOn),
		Notes:             l.Notes,
		CeasedOn:          formatDate(l.CeasedOn),
	}
}

func licenceKindToDB(k stillhousev1.ExciseLicenceKind) (sqlcgen.ExciseLicenceKind, error) {
	switch k {
	case stillhousev1.ExciseLicenceKind_EXCISE_LICENCE_KIND_SPIRITS:
		return sqlcgen.ExciseLicenceKindSpirits, nil
	case stillhousev1.ExciseLicenceKind_EXCISE_LICENCE_KIND_EXCISE_WAREHOUSE:
		return sqlcgen.ExciseLicenceKindExciseWarehouse, nil
	case stillhousev1.ExciseLicenceKind_EXCISE_LICENCE_KIND_USERS:
		return sqlcgen.ExciseLicenceKindUsers, nil
	case stillhousev1.ExciseLicenceKind_EXCISE_LICENCE_KIND_WINE:
		return sqlcgen.ExciseLicenceKindWine, nil
	case stillhousev1.ExciseLicenceKind_EXCISE_LICENCE_KIND_OTHER:
		return sqlcgen.ExciseLicenceKindOther, nil
	}
	return "", fmt.Errorf("kind is required")
}

func licenceKindToProto(k sqlcgen.ExciseLicenceKind) stillhousev1.ExciseLicenceKind {
	switch k {
	case sqlcgen.ExciseLicenceKindSpirits:
		return stillhousev1.ExciseLicenceKind_EXCISE_LICENCE_KIND_SPIRITS
	case sqlcgen.ExciseLicenceKindExciseWarehouse:
		return stillhousev1.ExciseLicenceKind_EXCISE_LICENCE_KIND_EXCISE_WAREHOUSE
	case sqlcgen.ExciseLicenceKindUsers:
		return stillhousev1.ExciseLicenceKind_EXCISE_LICENCE_KIND_USERS
	case sqlcgen.ExciseLicenceKindWine:
		return stillhousev1.ExciseLicenceKind_EXCISE_LICENCE_KIND_WINE
	case sqlcgen.ExciseLicenceKindOther:
		return stillhousev1.ExciseLicenceKind_EXCISE_LICENCE_KIND_OTHER
	}
	return stillhousev1.ExciseLicenceKind_EXCISE_LICENCE_KIND_UNSPECIFIED
}

func parseRequiredDate(v, field string) (pgtype.Date, error) {
	if strings.TrimSpace(v) == "" {
		return pgtype.Date{}, fmt.Errorf("%s is required", field)
	}
	t, err := time.Parse("2006-01-02", v)
	if err != nil {
		return pgtype.Date{}, fmt.Errorf("%s must be YYYY-MM-DD", field)
	}
	return pgtype.Date{Valid: true, Time: t}, nil
}

func parseOptionalDate(v, field string) (pgtype.Date, error) {
	if strings.TrimSpace(v) == "" {
		return pgtype.Date{}, nil
	}
	t, err := time.Parse("2006-01-02", v)
	if err != nil {
		return pgtype.Date{}, fmt.Errorf("%s must be YYYY-MM-DD", field)
	}
	return pgtype.Date{Valid: true, Time: t}, nil
}
