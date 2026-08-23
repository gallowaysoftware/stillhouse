package rpc

import (
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

func userToProto(u sqlcgen.User) *stillhousev1.User {
	return &stillhousev1.User{
		Id:          u.ID.String(),
		TenantId:    u.TenantID.String(),
		Email:       u.Email,
		DisplayName: u.DisplayName,
		Role:        roleToProto(u.Role),
		CreatedAt:   timestamppb.New(u.CreatedAt.Time),
		UpdatedAt:   timestamppb.New(u.UpdatedAt.Time),
	}
}

func tenantToProto(t sqlcgen.Tenant) *stillhousev1.Tenant {
	out := &stillhousev1.Tenant{
		Id:                      t.ID.String(),
		Name:                    t.Name,
		CraSpiritsLicenceNumber: t.CraSpiritsLicenceNumber,
		DefaultJurisdiction:     t.DefaultJurisdiction,
		CreatedAt:               timestamppb.New(t.CreatedAt.Time),
		UpdatedAt:               timestamppb.New(t.UpdatedAt.Time),
		// Derived from the warehouse licence in the database, so it can
		// never disagree with it. Read-only over the wire for the same
		// reason.
		DutyPoint:              dutyPointToProto(t.DutyPoint),
		DutyPointEffectiveFrom: t.DutyPointEffectiveFrom.Time.Format("2006-01-02"),

		FilingFrequency:                 filingFrequencyToProto(t.FilingFrequency),
		FiscalMonthBasis:                fiscalMonthBasisToProto(t.FiscalMonthBasis),
		FiscalMonthNotificationRef:      t.FiscalMonthNotificationRef,
		FilingFrequencyAuthorizationRef: t.FilingFrequencyAuthorizationRef,
	}
	if t.FiscalMonthEndDay.Valid {
		out.FiscalMonthEndDay = t.FiscalMonthEndDay.Int32
	}
	if t.ExciseWarehouseLicenceNumber.Valid {
		out.ExciseWarehouseLicenceNumber = t.ExciseWarehouseLicenceNumber.String
	}
	return out
}

func roleToProto(r sqlcgen.UserRole) stillhousev1.UserRole {
	switch r {
	case sqlcgen.UserRoleOwner:
		return stillhousev1.UserRole_USER_ROLE_OWNER
	case sqlcgen.UserRoleOperator:
		return stillhousev1.UserRole_USER_ROLE_OPERATOR
	case sqlcgen.UserRoleViewer:
		return stillhousev1.UserRole_USER_ROLE_VIEWER
	case sqlcgen.UserRoleAccountant:
		return stillhousev1.UserRole_USER_ROLE_ACCOUNTANT
	}
	return stillhousev1.UserRole_USER_ROLE_UNSPECIFIED
}
