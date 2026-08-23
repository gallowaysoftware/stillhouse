package rpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/journal"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

type JournalService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewJournalService(db *tenantdb.DB, logger *slog.Logger) *JournalService {
	return &JournalService{db: db, logger: logger}
}

func (s *JournalService) PreviewJournal(
	ctx context.Context,
	req *connect.Request[stillhousev1.PreviewJournalRequest],
) (*connect.Response[stillhousev1.PreviewJournalResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	start, end, err := parseJournalPeriod(req.Msg.GetPeriodStart(), req.Msg.GetPeriodEnd())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var j *journal.Journal
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		j, e = journal.Build(ctx, q, start, end)
		return e
	}); err != nil {
		s.logger.Error("PreviewJournal", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(journalToProto(j)), nil
}

func (s *JournalService) ListJournalAccounts(
	ctx context.Context,
	_ *connect.Request[stillhousev1.ListJournalAccountsRequest],
) (*connect.Response[stillhousev1.ListJournalAccountsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var rows []sqlcgen.JournalAccount
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListJournalAccounts(ctx)
		return e
	}); err != nil {
		s.logger.Error("ListJournalAccounts", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := make([]*stillhousev1.JournalAccountMapping, 0, len(rows))
	for _, r := range rows {
		out = append(out, &stillhousev1.JournalAccountMapping{
			Kind:          journalKindToProto(r.Kind),
			DebitAccount:  r.DebitAccount,
			CreditAccount: r.CreditAccount,
			DebitName:     r.DebitName,
			CreditName:    r.CreditName,
			MemoPrefix:    r.MemoPrefix,
		})
	}
	return connect.NewResponse(&stillhousev1.ListJournalAccountsResponse{Mappings: out}), nil
}

func (s *JournalService) SetJournalAccount(
	ctx context.Context,
	req *connect.Request[stillhousev1.SetJournalAccountRequest],
) (*connect.Response[stillhousev1.SetJournalAccountResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	m := req.Msg.GetMapping()
	kind, err := journalKindToDB(m.GetKind())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	var row sqlcgen.JournalAccount
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		row, e = q.UpsertJournalAccount(ctx, sqlcgen.UpsertJournalAccountParams{
			TenantID:      u.TenantID,
			Kind:          kind,
			DebitAccount:  m.GetDebitAccount(),
			CreditAccount: m.GetCreditAccount(),
			DebitName:     m.GetDebitName(),
			CreditName:    m.GetCreditName(),
			MemoPrefix:    m.GetMemoPrefix(),
		})
		return e
	}); err != nil {
		s.logger.Error("SetJournalAccount", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.SetJournalAccountResponse{
		Mapping: &stillhousev1.JournalAccountMapping{
			Kind:          journalKindToProto(row.Kind),
			DebitAccount:  row.DebitAccount,
			CreditAccount: row.CreditAccount,
			DebitName:     row.DebitName,
			CreditName:    row.CreditName,
			MemoPrefix:    row.MemoPrefix,
		},
	}), nil
}

func journalToProto(j *journal.Journal) *stillhousev1.PreviewJournalResponse {
	out := &stillhousev1.PreviewJournalResponse{}
	counts := map[sqlcgen.JournalEventKind]int32{}
	for _, l := range j.Lines {
		counts[l.Kind]++
		out.Lines = append(out.Lines, &stillhousev1.JournalLine{
			Date:          l.Date.Format("2006-01-02"),
			Kind:          journalKindToProto(l.Kind),
			Description:   l.Description,
			Reference:     l.Reference,
			AmountCad:     l.AmountCAD,
			DebitAccount:  l.Debit,
			DebitName:     l.DebitName,
			CreditAccount: l.Credit,
			CreditName:    l.CreditName,
			Memo:          l.Memo,
			Basis:         l.Basis,
		})
	}
	for kind, total := range j.TotalsByKind {
		out.Totals = append(out.Totals, &stillhousev1.JournalKindTotal{
			Kind:      journalKindToProto(kind),
			AmountCad: total,
			LineCount: counts[kind],
		})
	}
	for _, w := range j.Warnings {
		out.Warnings = append(out.Warnings, &stillhousev1.JournalWarning{
			Kind: w.Kind, Detail: w.Detail,
		})
	}
	return out
}

func parseJournalPeriod(startStr, endStr string) (time.Time, time.Time, error) {
	if startStr == "" || endStr == "" {
		return time.Time{}, time.Time{}, errors.New("period_start and period_end are required")
	}
	start, err := time.Parse("2006-01-02", startStr)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("period_start must be YYYY-MM-DD")
	}
	end, err := time.Parse("2006-01-02", endStr)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("period_end must be YYYY-MM-DD")
	}
	if end.Before(start) {
		return time.Time{}, time.Time{}, errors.New("period_end is before period_start")
	}
	return start, end, nil
}

func journalKindToProto(k sqlcgen.JournalEventKind) stillhousev1.JournalEventKind {
	switch k {
	case sqlcgen.JournalEventKindDutyPayable:
		return stillhousev1.JournalEventKind_JOURNAL_EVENT_KIND_DUTY_PAYABLE
	case sqlcgen.JournalEventKindMaterialReceipt:
		return stillhousev1.JournalEventKind_JOURNAL_EVENT_KIND_MATERIAL_RECEIPT
	case sqlcgen.JournalEventKindMaterialConsumption:
		return stillhousev1.JournalEventKind_JOURNAL_EVENT_KIND_MATERIAL_CONSUMPTION
	case sqlcgen.JournalEventKindCogsOnRemoval:
		return stillhousev1.JournalEventKind_JOURNAL_EVENT_KIND_COGS_ON_REMOVAL
	}
	return stillhousev1.JournalEventKind_JOURNAL_EVENT_KIND_UNSPECIFIED
}

func journalKindToDB(k stillhousev1.JournalEventKind) (sqlcgen.JournalEventKind, error) {
	switch k {
	case stillhousev1.JournalEventKind_JOURNAL_EVENT_KIND_DUTY_PAYABLE:
		return sqlcgen.JournalEventKindDutyPayable, nil
	case stillhousev1.JournalEventKind_JOURNAL_EVENT_KIND_MATERIAL_RECEIPT:
		return sqlcgen.JournalEventKindMaterialReceipt, nil
	case stillhousev1.JournalEventKind_JOURNAL_EVENT_KIND_MATERIAL_CONSUMPTION:
		return sqlcgen.JournalEventKindMaterialConsumption, nil
	case stillhousev1.JournalEventKind_JOURNAL_EVENT_KIND_COGS_ON_REMOVAL:
		return sqlcgen.JournalEventKindCogsOnRemoval, nil
	}
	return "", fmt.Errorf("unknown journal event kind")
}
