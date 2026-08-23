package rpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"connectrpc.com/connect"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/importer"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

// maxImportBytes bounds what one call will parse. Generous for a
// spreadsheet — a rackhouse of ten thousand casks is well under it — and
// small enough that a mistaken upload of a database dump is refused
// rather than held in memory.
const maxImportBytes = 8 << 20

type ImportService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewImportService(db *tenantdb.DB, logger *slog.Logger) *ImportService {
	return &ImportService{db: db, logger: logger}
}

// DescribeImport returns the columns a kind expects, which is what
// somebody needs before they can produce a file that imports.
func (s *ImportService) DescribeImport(
	ctx context.Context,
	req *connect.Request[stillhousev1.DescribeImportRequest],
) (*connect.Response[stillhousev1.DescribeImportResponse], error) {
	if _, ok := CurrentUser(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	kind, err := importKindToDomain(req.Msg.GetKind())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	cols := importer.ColumnsFor(kind)
	out := &stillhousev1.DescribeImportResponse{
		Help:        importer.KindHelp[kind],
		TemplateCsv: importer.Template(cols),
	}
	for _, c := range importer.Describe(cols) {
		out.Columns = append(out.Columns, &stillhousev1.ImportColumn{
			Name: c.Name, Required: c.Required, Help: c.Help,
		})
	}
	return connect.NewResponse(out), nil
}

// RunImport parses, validates, and — only when asked — writes.
//
// The commit path runs inside one WithTenantTx, so a row that fails on
// write aborts the whole transaction and nothing lands. That is what
// makes "rollback" a non-feature here: there is no half-imported state
// to roll back from.
func (s *ImportService) RunImport(
	ctx context.Context,
	req *connect.Request[stillhousev1.RunImportRequest],
) (*connect.Response[stillhousev1.RunImportResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	kind, err := importKindToDomain(req.Msg.GetKind())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	body := req.Msg.GetCsv()
	if strings.TrimSpace(body) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("no file content"))
	}
	if len(body) > maxImportBytes {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
			"the file is %d MB; the limit is %d MB. Split it, or check you meant to upload this",
			len(body)>>20, maxImportBytes>>20))
	}

	cols := importer.ColumnsFor(kind)
	rows, problems := importer.Parse(strings.NewReader(body), cols)
	if len(problems) > 0 {
		// A bad header or malformed CSV: report and stop, without
		// touching the database at all.
		return connect.NewResponse(problemsOnly(len(rows), problems)), nil
	}

	var result *importer.Result
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		result, e = importer.Load(ctx, q, u.TenantID, kind, rows, req.Msg.GetCommit())
		if e != nil {
			return e
		}
		if !result.Committed {
			// Either a dry run, or rows failed. Both roll the transaction
			// back by returning an error; the result travels out in the
			// closure and is reported normally.
			return errImportRolledBack
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "import", string(kind),
			sqlcgen.AuditActionCreate, map[string]any{
				"kind":          string(kind),
				"rows_read":     result.RowsRead,
				"rows_accepted": result.RowsAccepted,
			})
	})
	switch {
	case errors.Is(err, errImportRolledBack):
		// A deliberate rollback, not a failure.
	case err != nil:
		s.logger.Error("RunImport", "err", err, "kind", kind)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	out := &stillhousev1.RunImportResponse{
		RowsRead:     int32(result.RowsRead),
		RowsAccepted: int32(result.RowsAccepted),
		Notes:        result.Notes,
		Committed:    result.Committed,
	}
	for _, p := range result.Problems {
		out.Problems = append(out.Problems, &stillhousev1.ImportProblem{
			Row: int32(p.Row), Column: p.Column, Detail: p.Detail,
		})
	}
	return connect.NewResponse(out), nil
}

// errImportRolledBack rolls the transaction back on purpose — for a dry
// run, or for a file with a bad row in it.
//
// A dry run attempts every write and then abandons it, which is the only
// way to know a file would actually import rather than merely parse: a
// name that collides with a row already in the database is invisible to
// validation and obvious to an INSERT.
var errImportRolledBack = errors.New("import: rolling back")

func problemsOnly(rowsRead int, problems []importer.Problem) *stillhousev1.RunImportResponse {
	out := &stillhousev1.RunImportResponse{RowsRead: int32(rowsRead)}
	for _, p := range problems {
		out.Problems = append(out.Problems, &stillhousev1.ImportProblem{
			Row: int32(p.Row), Column: p.Column, Detail: p.Detail,
		})
	}
	return out
}

func importKindToDomain(k stillhousev1.ImportKind) (importer.Kind, error) {
	switch k {
	case stillhousev1.ImportKind_IMPORT_KIND_MATERIALS:
		return importer.KindMaterials, nil
	case stillhousev1.ImportKind_IMPORT_KIND_MATERIAL_LOTS:
		return importer.KindMaterialLots, nil
	case stillhousev1.ImportKind_IMPORT_KIND_PRODUCTS:
		return importer.KindProducts, nil
	case stillhousev1.ImportKind_IMPORT_KIND_CUSTOMERS:
		return importer.KindCustomers, nil
	case stillhousev1.ImportKind_IMPORT_KIND_BARRELS:
		return importer.KindBarrels, nil
	case stillhousev1.ImportKind_IMPORT_KIND_PACKAGED_INVENTORY:
		return importer.KindPackaged, nil
	}
	return "", errors.New("choose what the file contains")
}
