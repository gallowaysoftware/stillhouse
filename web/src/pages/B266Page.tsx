import { FormEvent, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { Shell } from "@/components/Shell";
import { b266Client } from "@/lib/clients";
import { useCurrentUser } from "@/lib/role";
import {
  B266Report,
  B266Status,
  GenerateB266RequestSchema,
  SubmitB266RequestSchema,
} from "@/gen/stillhouse/v1/b266_pb";
import { formatLAA, formatQty } from "@/lib/format";
import { WriteOnly, OwnerOnly } from "@/lib/role";


function todayISO(): string {
  return new Date().toISOString().slice(0, 10);
}

function firstOfThisMonth(): string {
  const d = new Date();
  return new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), 1)).toISOString().slice(0, 10);
}

function lastOfThisMonth(): string {
  const d = new Date();
  return new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth() + 1, 0)).toISOString().slice(0, 10);
}

export function B266Page() {
  const qc = useQueryClient();
  const periods = useQuery({
    queryKey: ["listB266Periods"],
    queryFn: () => b266Client.listB266Periods({}),
  });

  const [periodStart, setPeriodStart] = useState(firstOfThisMonth());
  const [periodEnd, setPeriodEnd] = useState(lastOfThisMonth());
  const [openPeriodId, setOpenPeriodId] = useState<string>("");

  const openPeriod = useQuery({
    queryKey: ["getB266Period", openPeriodId],
    queryFn: () => b266Client.getB266Period({ id: openPeriodId }),
    enabled: !!openPeriodId,
  });
  // Invalidate qc on submit so the list reflects updated status. (qc unused
  // until we wire it; kept for future use.)
  void qc;

  const generate = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof GenerateB266RequestSchema>>) =>
      b266Client.generateB266(msg),
  });
  const submit = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof SubmitB266RequestSchema>>) =>
      b266Client.submitB266(msg),
    onSuccess: () => {
      periods.refetch();
    },
  });

  function generateNow(e: FormEvent) {
    e.preventDefault();
    generate.mutate(
      create(GenerateB266RequestSchema, { periodStart, periodEnd }),
    );
  }

  const result = generate.data;
  const submitted = submit.data;

  return (
    <Shell>
      <div className="mb-6">
        <h1 className="text-3xl font-bold tracking-tight">CRA Form B266</h1>
        <p className="text-sm text-fg-muted">
          Monthly Excise Duty Return — Spirits Licensee. Pick a period, generate the
          values, copy them into the My Business Account return.
          Generated today {todayISO()}; rates effective April 1, 2026.
        </p>
      </div>

      <form
        data-print-hide
        onSubmit={generateNow}
        className="mb-8 flex flex-wrap items-end gap-3 rounded-lg border border-border bg-surface-2 p-5 shadow-sm"
      >
        <div>
          <label className="mb-2 block text-sm font-medium text-fg-muted">Period start</label>
          <input type="date" value={periodStart} onChange={(e) => setPeriodStart(e.target.value)} required className="rounded border border-border-strong px-3 py-2 text-sm" />
        </div>
        <div>
          <label className="mb-2 block text-sm font-medium text-fg-muted">Period end</label>
          <input type="date" value={periodEnd} onChange={(e) => setPeriodEnd(e.target.value)} required className="rounded border border-border-strong px-3 py-2 text-sm" />
        </div>
        <WriteOnly>
          <button
            type="submit"
            disabled={generate.isPending}
            className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
          >
            {generate.isPending ? "Generating…" : "Generate"}
          </button>
        </WriteOnly>
        {generate.error && (
          <span className="text-sm text-red-400">
            {generate.error instanceof ConnectError ? generate.error.rawMessage : String(generate.error)}
          </span>
        )}
      </form>

      {result?.report && (
        <ReportView
          report={result.report}
          period={result.period}
          onSubmit={() => submit.mutate(create(SubmitB266RequestSchema, { periodId: result.period!.id }))}
          submitting={submit.isPending}
          submitError={submit.error}
          submittedStatus={submitted?.period?.status ?? result.period?.status}
        />
      )}

      <h2 data-print-hide className="mb-3 mt-10 text-sm font-semibold text-fg-muted">Past returns</h2>
      <div data-print-hide className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-3">Period</th>
              <th className="px-4 py-3">Status</th>
              <th className="px-4 py-3">Submitted</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {periods.data?.periods.length === 0 && (
              <tr><td colSpan={3} className="px-4 py-3 text-fg-muted">No periods yet.</td></tr>
            )}
            {periods.data?.periods.map((p) => (
              <tr
                key={p.id}
                className={`cursor-pointer hover:bg-surface-3 ${openPeriodId === p.id ? "bg-surface-3" : ""}`}
                onClick={() => setOpenPeriodId(openPeriodId === p.id ? "" : p.id)}
              >
                <td className="px-4 py-3 font-medium text-fg">{p.periodStart} → {p.periodEnd}</td>
                <td className="px-4 py-3 text-fg-muted">{p.status === B266Status.SUBMITTED ? "Submitted" : "Draft"}</td>
                <td className="px-4 py-3 text-fg-muted">
                  {p.submittedAt ? new Date(Number(p.submittedAt.seconds) * 1000).toLocaleString() : "—"}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {openPeriodId && (
        <section className="mt-6">
          <h2 className="mb-3 text-sm font-semibold text-fg-muted">
            Snapshot · {openPeriod.data?.period?.periodStart} → {openPeriod.data?.period?.periodEnd}
          </h2>
          {openPeriod.isLoading && <p className="text-fg-muted">Loading…</p>}
          {openPeriod.data && !openPeriod.data.snapshot && (
            <p className="text-sm text-fg-muted">
              This period was never submitted — no frozen snapshot exists. Re-generate it above
              with the same period bounds to recompute.
            </p>
          )}
          {openPeriod.data?.snapshot && (
            <ReportView
              report={openPeriod.data.snapshot}
              period={openPeriod.data.period}
              onSubmit={() => {}}
              submitting={false}
              submitError={null}
              submittedStatus={openPeriod.data.period?.status}
            />
          )}
        </section>
      )}
    </Shell>
  );
}

function ReportView({
  report,
  period,
  onSubmit,
  submitting,
  submitError,
  submittedStatus,
}: {
  report: B266Report;
  period: { id: string; periodStart?: string; periodEnd?: string; submittedAt?: { seconds: bigint } } | undefined;
  onSubmit: () => void;
  submitting: boolean;
  submitError: Error | null;
  submittedStatus: B266Status | undefined;
}) {
  const me = useCurrentUser();
  const tenantName = me.data?.tenant?.name ?? "";
  const periodStart = period?.periodStart ?? report.periodStart;
  const periodEnd = period?.periodEnd ?? report.periodEnd;
  return (
    <section className="space-y-6">
      <div data-print-only className="border-b border-border-strong pb-4">
        <p className="text-xs text-fg-muted">CRA Form B266 — Excise Duty Return, Spirits Licensee</p>
        <h2 className="mt-1 text-xl font-semibold">{tenantName || "Distillery"}</h2>
        <p className="mt-1 text-sm">
          Period {periodStart} → {periodEnd}
          {submittedStatus === B266Status.SUBMITTED
            ? ` · submitted ${period?.submittedAt ? new Date(Number(period.submittedAt.seconds) * 1000).toLocaleString() : ""}`
            : ` · DRAFT — printed ${new Date().toLocaleString()}`}
        </p>
      </div>
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        <Card title="Bulk spirits (LAA)">
          <Row k="Opening on hand" v={formatLAA(report.bulkOpeningLaa)} />
          <Row k="Production"             v={formatLAA(report.bulkProductionLaa)} />
          <Row k="Received in bond"       v={formatLAA(report.bulkReceivedInBondLaa)} />
          <Row k="Blend in"               v={formatLAA(report.bulkBlendInLaa)} />
          <Row k="Transferred to packaging" v={formatLAA(report.bulkTransferredToPackagingLaa)} dim />
          <Row k="Transferred out in bond" v={formatLAA(report.bulkTransferredOutInBondLaa)} dim />
          <Row k="Losses (evap + unaccounted)" v={formatLAA(report.bulkLossesLaa)} dim />
          <Row k="Destroyed"              v={formatLAA(report.bulkDestroyedLaa)} dim />
          <Row k="Closing on hand"        v={formatLAA(report.bulkClosingLaa)} bold />
        </Card>
        <Card title="Packaged spirits (LAA)">
          <Row k="Opening on hand"        v={formatLAA(report.packagedOpeningLaa)} />
          <Row k="Packaged this period"   v={`${formatLAA(report.packagedPackagedLaa)} (${report.packagedPackagedBottles.toLocaleString()} bottles)`} />
          <Row k="Removed duty-paid"      v={`${formatLAA(report.packagedRemovedDutyPaidLaa)} (${report.packagedRemovedDutyPaidBottles.toLocaleString()} bottles)`} dim />
          <Row k="Closing on hand"        v={`${formatLAA(report.packagedClosingLaa)} (${report.packagedClosingBottles.toLocaleString()} bottles)`} bold />
        </Card>
      </div>

      <Card title="Duty payable">
        <Row k="Removed LAA (duty-paid)" v={formatLAA(report.packagedRemovedDutyPaidLaa)} />
        <Row k="Rate (CAD / LAA, >7%)"  v={`$${report.dutyRatePerLaa.toFixed(3)}`} />
        <Row k="Duty payable (CAD)"     v={`$${formatQty(report.dutyPayableCad)}`} bold highlight />
      </Card>

      <div data-print-hide className="flex items-center gap-3">
        <button
          onClick={() => window.print()}
          className="rounded border border-border-strong px-3 py-2 text-sm text-fg hover:bg-surface-3"
        >
          Print / Save as PDF
        </button>
      </div>

      {period && submittedStatus !== B266Status.SUBMITTED && (
        <OwnerOnly>
        <div data-print-hide className="flex items-center gap-3">
          <button
            onClick={onSubmit}
            disabled={submitting}
            className="rounded bg-emerald-700 px-3 py-2 text-sm font-medium text-white hover:bg-emerald-800 disabled:bg-accent/50"
          >
            {submitting ? "Submitting…" : "Mark submitted (freeze snapshot)"}
          </button>
          {submitError && (
            <span className="text-sm text-red-400">
              {submitError instanceof ConnectError ? submitError.rawMessage : String(submitError)}
            </span>
          )}
          <p className="text-xs text-fg-muted">
            Marking submitted freezes the values for audit. Make sure you've entered these into the CRA portal first.
          </p>
        </div>
        </OwnerOnly>
      )}
      {submittedStatus === B266Status.SUBMITTED && period && (
        <OwnerOnly>
          <ReopenPanel periodId={period.id} />
        </OwnerOnly>
      )}
    </section>
  );
}

// ReopenPanel — owner-only escape hatch when a filed return genuinely
// needs to be corrected. Flips status back to draft so backdated voids
// / inserts pass the period-lock guard. Audit-logged with the reason.
function ReopenPanel({ periodId }: { periodId: string }) {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState("");
  const reopen = useMutation({
    mutationFn: () => b266Client.reopenB266Period({ id: periodId, reason }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["listB266Periods"] });
      qc.invalidateQueries({ queryKey: ["getB266Period", periodId] });
      setOpen(false);
      setReason("");
    },
  });
  return (
    <div data-print-hide className="space-y-3">
      <p className="rounded bg-emerald-500/10 px-4 py-2 text-sm text-emerald-400">
        This return is submitted; the snapshot is frozen.
      </p>
      {!open ? (
        <button
          onClick={() => setOpen(true)}
          className="rounded border border-amber-500/40 px-3 py-2 text-sm text-amber-400 hover:bg-amber-500/10"
        >
          Reopen for correction…
        </button>
      ) : (
        <div className="space-y-3 rounded border border-amber-500/40 bg-amber-500/5 p-4">
          <p className="text-sm text-fg">
            Reopening flips this period back to <b>draft</b> so backdated voids and inserts pass the
            period-lock guard. The snapshot stays for audit, but live numbers may drift from what
            you filed with CRA — make sure you can square the two before submitting again.
          </p>
          <input
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            placeholder="Reason for reopening (required, audit-logged)"
            className="w-full rounded border border-border-strong bg-surface px-3 py-2 text-sm text-fg"
          />
          {reopen.error && (
            <p className="text-sm text-red-400">
              {reopen.error instanceof ConnectError ? reopen.error.rawMessage : String(reopen.error)}
            </p>
          )}
          <div className="flex gap-2">
            <button
              onClick={() => reopen.mutate()}
              disabled={!reason.trim() || reopen.isPending}
              className="rounded bg-amber-500 px-3 py-2 text-sm font-medium text-zinc-900 hover:bg-amber-400 disabled:opacity-50"
            >
              {reopen.isPending ? "Reopening…" : "Reopen period"}
            </button>
            <button
              onClick={() => { setOpen(false); setReason(""); }}
              className="rounded border border-border-strong px-3 py-2 text-sm text-fg hover:bg-surface-3"
            >
              Cancel
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

function Card({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
      <header className="border-b border-border bg-surface-3 px-4 py-3">
        <h2 className="text-sm font-semibold text-fg-muted">{title}</h2>
      </header>
      <dl className="divide-y divide-border text-sm">{children}</dl>
    </div>
  );
}

function Row({
  k, v, bold, dim, highlight,
}: { k: string; v: string; bold?: boolean; dim?: boolean; highlight?: boolean }) {
  return (
    <div className={`flex items-center justify-between px-4 py-2 ${highlight ? "bg-emerald-500/10" : ""}`}>
      <dt className={`text-fg-muted ${dim ? "text-fg-subtle" : ""}`}>{k}</dt>
      <dd className={`font-mono ${bold ? "font-semibold text-fg" : "text-fg"} ${highlight ? "text-emerald-400" : ""}`}>{v}</dd>
    </div>
  );
}
