package rpc

import (
	"context"
	"errors"
	"sort"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// copyNote is on the response, because the difference between a copy and
// a link is exactly what somebody will assume wrongly.
const copyNote = "Copied, not linked. These are now this distillery's own definitions and editing them here changes nothing at the distillery they came from — which is the point: two licences are two filers, and each has to own the definitions its own figures were computed from."

// CopyReferenceData carries materials or suppliers between distilleries
// the caller holds accounts at. PLAN H7.
//
// It copies rather than shares, and that is the design rather than a
// shortcut. Shared mutable reference data across licences means one
// licensee's edit changing another's records, and a material's extract
// fraction feeds a conversion efficiency which feeds a yield — the kind
// of figure that gets defended to CRA. Each licence has to own what its
// own numbers were computed from, including the right to have it differ.
func (s *TenantService) CopyReferenceData(
	ctx context.Context,
	req *connect.Request[stillhousev1.CopyReferenceDataRequest],
) (*connect.Response[stillhousev1.CopyReferenceDataResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	fromID, err := uuid.Parse(req.Msg.GetFromTenantId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid from_tenant_id"))
	}
	if fromID == u.TenantID {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("that is this distillery"))
	}

	// The caller must hold an account at the SOURCE as well. Without this
	// the endpoint would read another licensee's material list on the
	// strength of knowing a tenant id, which is the whole reason group
	// membership is "holds an account" and not a table.
	accounts, err := s.q.ListUsersByEmail(ctx, u.Email)
	if err != nil {
		s.logger.Error("CopyReferenceData: accounts", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	var holdsSource bool
	for _, a := range accounts {
		if a.TenantID == fromID {
			holdsSource = true
			break
		}
	}
	if !holdsSource {
		// Deliberately the same answer as a tenant that does not exist:
		// otherwise this distinguishes real licences from invented ones
		// for anybody with a session.
		return nil, connect.NewError(connect.CodeNotFound,
			errors.New("no distillery of yours with that id"))
	}

	want := map[stillhousev1.CopyableReference]bool{}
	for _, w := range req.Msg.GetWhat() {
		want[w] = true
	}
	if len(want) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("say what to copy: materials, suppliers, or both"))
	}

	out := &stillhousev1.CopyReferenceDataResponse{Note: copyNote}
	skipped := map[string]bool{}

	// Read the source in ITS OWN tenant context, so RLS is what bounds
	// the read rather than a WHERE clause somebody could get wrong.
	var (
		materials []sqlcgen.ListMaterialsForCopyRow
		suppliers []sqlcgen.ListSuppliersForCopyRow
	)
	if err := s.db.WithTenantTx(ctx, fromID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		if want[stillhousev1.CopyableReference_COPYABLE_REFERENCE_MATERIALS] {
			if materials, e = q.ListMaterialsForCopy(ctx); e != nil {
				return e
			}
		}
		if want[stillhousev1.CopyableReference_COPYABLE_REFERENCE_SUPPLIERS] {
			if suppliers, e = q.ListSuppliersForCopy(ctx); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		s.logger.Error("CopyReferenceData: read source", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	// And write in this one.
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if len(materials) > 0 {
			have, e := q.MaterialNamesInUse(ctx)
			if e != nil {
				return e
			}
			existing := lowerSet(have)
			for _, m := range materials {
				if existing[strings.ToLower(m.Name)] {
					// Not overwritten. The definition already here is
					// what this licensee's own figures were computed
					// from, and replacing it would restate them.
					skipped[m.Name] = true
					continue
				}
				if e := q.InsertCopiedMaterial(ctx, sqlcgen.InsertCopiedMaterialParams{
					TenantID: u.TenantID, Name: m.Name, Kind: m.Kind, Uom: m.Uom,
					ExtractFraction: m.ExtractFraction, MoistureFraction: m.MoistureFraction,
					Cereal: m.Cereal, Notes: m.Notes,
				}); e != nil {
					return e
				}
				out.MaterialsCopied++
			}
		}
		if len(suppliers) > 0 {
			have, e := q.SupplierNamesInUse(ctx)
			if e != nil {
				return e
			}
			existing := lowerSet(have)
			for _, sup := range suppliers {
				if existing[strings.ToLower(sup.Name)] {
					skipped[sup.Name] = true
					continue
				}
				if e := q.InsertCopiedSupplier(ctx, sqlcgen.InsertCopiedSupplierParams{
					TenantID: u.TenantID, Name: sup.Name,
					AccountReference: sup.AccountReference, ContactName: sup.ContactName,
					Email: sup.Email, Phone: sup.Phone, Address: sup.Address,
					PaymentTermsDays: sup.PaymentTermsDays, Country: sup.Country,
					Notes: sup.Notes,
				}); e != nil {
					return e
				}
				out.SuppliersCopied++
			}
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "tenant", u.TenantID.String(),
			sqlcgen.AuditActionCreate, map[string]any{
				"copied_from": fromID.String(),
				"materials":   out.MaterialsCopied,
				"suppliers":   out.SuppliersCopied,
			})
	}); err != nil {
		s.logger.Error("CopyReferenceData: write", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	for n := range skipped {
		out.Skipped = append(out.Skipped, n)
	}
	sort.Strings(out.Skipped)
	return connect.NewResponse(out), nil
}

// lowerSet folds names for comparison. Case-insensitive because "Rye" and
// "rye" are one grain, and creating both is how a materials list becomes
// two lists.
func lowerSet(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[strings.ToLower(n)] = true
	}
	return out
}
