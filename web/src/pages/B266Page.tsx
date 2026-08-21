import { FormEvent, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { Callout } from "@/components/Callout";
import { LossClassificationCard } from "@/components/LossClassificationCard";
import { Shell } from "@/components/Shell";
import { b266Client } from "@/lib/clients";
import { useCurrentUser } from "@/lib/role";
import {
  B266Report,
  B266Status,
  GenerateB266RequestSchema,
  SubmitB266RequestSchema,
} from "@/gen/stillhouse/v1/b266_pb";
import { DutyPoint } from "@/gen/stillhouse/v1/tenant_pb";
import { formatCAD, formatLAA } from "@/lib/format";
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
          <span className="text-sm text-danger-fg">
            {generate.error instanceof ConnectError ? generate.error.rawMessage : String(generate.error)}
          </span>
        )}
      </form>

      {/* What has to be resolved before this period can be filed, with the
          means to resolve it right here rather than three pages away. */}
      {result?.report && (
        <LossClassificationCard
          periodStart={result.report.periodStart}
          periodEnd={result.report.periodEnd}
        />
      )}

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
  const craLicence = me.data?.tenant?.craSpiritsLicenceNumber ?? "";
  const periodStart = period?.periodStart ?? report.periodStart;
  const periodEnd = period?.periodEnd ?? report.periodEnd;
  const isSubmitted = submittedStatus === B266Status.SUBMITTED;
  return (
    <section className="space-y-6">
      <div data-print-only className="border-b-2 border-black pb-4">
        <div className="flex items-start justify-between">
          <div>
            <p className="text-xs">CRA Form B266 — Excise Duty Return, Spirits Licensee</p>
            <h2 className="mt-1 text-xl font-semibold">{tenantName || "Distillery"}</h2>
            {craLicence && <p className="mt-0.5 text-xs">Licence {craLicence}</p>}
            <p className="mt-1 text-sm">Period {periodStart} → {periodEnd}</p>
          </div>
          <div
            // Raw ink colours on purpose: this is the stamp on a printed
            // CRA return, and the print rules force the sheet to white
            // regardless of the operator's theme. A semantic token would
            // follow the theme and could come out invisible on paper.
            className={`rounded border-2 px-3 py-1 text-sm font-bold uppercase tracking-wider ${
              isSubmitted ? "border-black text-black" : "border-red-600 text-red-600"
            }`}
          >
            {isSubmitted ? "Submitted" : "Draft"}
          </div>
        </div>
        <p className="mt-2 text-xs text-fg-muted">
          {isSubmitted && period?.submittedAt
            ? `Snapshot frozen ${new Date(Number(period.submittedAt.seconds) * 1000).toLocaleString()}`
            : `Printed ${new Date().toLocaleString()} — not yet filed with CRA`}
        </p>
      </div>
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        <Card title="Bulk spirits (LAA)">
          <Row k="Opening on hand" v={formatLAA(report.bulkOpeningLaa)} />
          {report.bulkOpeningInventoryAdoptedLaa > 0 && (
            /* Already inside the opening balance above, not added to it.
               Shown so an adopted balance is never invisible on the return
               it affects. */
            <Row
              k="  of which adopted into Stillhouse"
              v={formatLAA(report.bulkOpeningInventoryAdoptedLaa)}
              dim
            />
          )}
          <Row k="Production"             v={formatLAA(report.bulkProductionLaa)} />
          <Row k="Received in bond"       v={formatLAA(report.bulkReceivedInBondLaa)} />
          {/* The rest of EDM10-1-7 page 3. Shown only when there is
              something on the line: a craft distiller who never imports,
              never denatures and never contract-fills should not read
              eleven zeroes to find the two numbers that matter. */}
          {report.bulkImportedLaa > 0 && (
            <Row k="Imported" v={formatLAA(report.bulkImportedLaa)} />
          )}
          {report.bulkReceivedFromLicenseeLaa > 0 && (
            <Row k="Received from spirits licensee" v={formatLAA(report.bulkReceivedFromLicenseeLaa)} />
          )}
          {report.bulkReceivedFromLicensedUserLaa > 0 && (
            <Row k="Received from licensed user" v={formatLAA(report.bulkReceivedFromLicensedUserLaa)} />
          )}
          {report.bulkPackagedReturnedToBulkLaa > 0 && (
            <Row k="Packaged returned to bulk" v={formatLAA(report.bulkPackagedReturnedToBulkLaa)} />
          )}
          <Row k="Blend in"               v={formatLAA(report.bulkBlendInLaa)} />
          <Row k="Transferred to packaging" v={formatLAA(report.bulkTransferredToPackagingLaa)} dim />
          <Row k="Transferred out in bond" v={formatLAA(report.bulkTransferredOutInBondLaa)} dim />
          {report.bulkDeliveredToLicenseeLaa > 0 && (
            <Row k="Delivered to spirits licensee" v={formatLAA(report.bulkDeliveredToLicenseeLaa)} dim />
          )}
          {report.bulkDeliveredToLicensedUserLaa > 0 && (
            <Row k="Delivered to licensed user" v={formatLAA(report.bulkDeliveredToLicensedUserLaa)} dim />
          )}
          {report.bulkExportedLaa > 0 && (
            <Row k="Exported" v={formatLAA(report.bulkExportedLaa)} dim />
          )}
          {report.bulkDenaturedDaLaa > 0 && (
            <Row k="Denatured to DA" v={formatLAA(report.bulkDenaturedDaLaa)} dim />
          )}
          {report.bulkDenaturedSdaLaa > 0 && (
            <Row k="Denatured to SDA" v={formatLAA(report.bulkDenaturedSdaLaa)} dim />
          )}
          {report.bulkReturnedToProductionLaa > 0 && (
            <Row k="Returned to production" v={formatLAA(report.bulkReturnedToProductionLaa)} dim />
          )}
          <Row k="Losses (evap + unaccounted)" v={formatLAA(report.bulkLossesLaa)} dim />
          {/* EDM3-4-1: a relieved loss and one that cannot be accounted for
              are charged differently, so the total alone is not a filable
              figure. Shown only when there is a loss to split. */}
          {report.bulkLossesLaa > 0 && (
            <>
              {report.bulkLossesRelievedLaa > 0 && (
                <Row k="  relieved" v={formatLAA(report.bulkLossesRelievedLaa)} dim />
              )}
              {report.bulkLossesDutiableLaa > 0 && (
                <Row k="  duty payable" v={formatLAA(report.bulkLossesDutiableLaa)} dim />
              )}
              {report.bulkLossesUnclassifiedLaa > 0 && (
                <Row k="  not yet classified" v={formatLAA(report.bulkLossesUnclassifiedLaa)} dim />
              )}
            </>
          )}
          <Row k="Destroyed"              v={formatLAA(report.bulkDestroyedLaa)} dim />
          {/* Line D. Both directions when there were any, because a period
              that found alcohol in one tank and lost it in another nets to
              zero, and a line showing only the net would say nothing
              happened. */}
          {report.bulkAdjustmentsCount > 0 && (
            <>
              <Row
                k={`Adjustments (${report.bulkAdjustmentsCount})`}
                v={`${report.bulkAdjustmentsLaa > 0 ? "+" : ""}${formatLAA(report.bulkAdjustmentsLaa)}`}
              />
              {report.bulkAdjustmentsIncreaseLaa > 0 && (
                <Row k="  increases" v={formatLAA(report.bulkAdjustmentsIncreaseLaa)} dim />
              )}
              {report.bulkAdjustmentsDecreaseLaa > 0 && (
                <Row k="  decreases" v={formatLAA(report.bulkAdjustmentsDecreaseLaa)} dim />
              )}
            </>
          )}
          <Row k="Closing on hand"        v={formatLAA(report.bulkClosingLaa)} bold />
        </Card>
        <Card title="Packaged spirits (LAA)">
          <Row k="Opening on hand"        v={formatLAA(report.packagedOpeningLaa)} />
          <Row k="Packaged this period"   v={`${formatLAA(report.packagedPackagedLaa)} (${report.packagedPackagedBottles.toLocaleString()} bottles)`} />
          {/* Of what was packaged, how much went in duty-paid. An
              at-packaging licensee cannot hold packaged spirits
              non-duty-paid at all, so for most craft distillers this is all
              of it. */}
          {report.packagedDutyPaidBottles > 0 && (
            <Row
              k="  of which duty-paid"
              v={`${formatLAA(report.packagedDutyPaidLaa)} (${report.packagedDutyPaidBottles.toLocaleString()} bottles)`}
              dim
            />
          )}
          {report.packagedNonDutyPaidBottles > 0 && (
            <Row
              k="  of which non-duty-paid"
              v={`${formatLAA(report.packagedNonDutyPaidLaa)} (${report.packagedNonDutyPaidBottles.toLocaleString()} bottles)`}
              dim
            />
          )}
          <Row k="Removed duty-paid"      v={`${formatLAA(report.packagedRemovedDutyPaidLaa)} (${report.packagedRemovedDutyPaidBottles.toLocaleString()} bottles)`} dim />
          <Row k="Closing on hand"        v={`${formatLAA(report.packagedClosingLaa)} (${report.packagedClosingBottles.toLocaleString()} bottles)`} bold />
        </Card>
      </div>

      {report.filingBlockers.length > 0 && (
        <Callout tone="warning" title="This period isn't ready to file">
          <ul className="list-disc space-y-1 pl-5">
            {report.filingBlockers.map((b) => (
              <li key={b}>{b}</li>
            ))}
          </ul>
          {/* An empty list is not a promise the figures are right — only
              that nothing is outstanding. Stillhouse never files, and a
              green light it can't honestly give would be worse than none. */}
          <p className="mt-2 text-xs">
            Resolving these does not mean the return is correct — only that nothing
            is missing. Check the figures against your own records before filing.
          </p>
        </Callout>
      )}

      <Card title="Duty payable">
        {/* Which event crystallised the duty. Without it the figures below
            can't be checked: the same LAA produces the same duty at either
            event, but in different months. */}
        <Row
          k="Duty point"
          v={
            report.dutyPoint === DutyPoint.AT_PACKAGING
              ? "At packaging (no excise warehouse licence)"
              : report.dutyPoint === DutyPoint.AT_REMOVAL
                ? "At removal (excise warehouse licence held)"
                : "—"
          }
        />
        {report.dutyPointEffectiveFrom && (
          <Row k="  on this basis since" v={report.dutyPointEffectiveFrom} dim />
        )}

        {/* Both halves are shown whenever either is non-zero. Only a period
            containing a change of basis carries both — stock packaged
            before the change is still dutied on its way out — and that is
            precisely the period where showing one and hiding the other
            would make the total look wrong. */}
        {(report.packagedDutiedOver7DutyCad > 0 || report.packagedDutiedUnder7DutyCad > 0) && (
          <>
            <Row k="Packaged >7% (LAA)" v={formatLAA(report.packagedDutiedOver7Laa)} dim />
            <Row k="  duty at packaging" v={formatCAD(report.packagedDutiedOver7DutyCad)} dim />
            {report.packagedDutiedUnder7DutyCad > 0 && (
              <>
                <Row k="Packaged ≤7% (litres)" v={report.packagedDutiedUnder7Litres.toFixed(2)} dim />
                <Row k="  duty at packaging" v={formatCAD(report.packagedDutiedUnder7DutyCad)} dim />
              </>
            )}
          </>
        )}
        {(report.packagedRemovedOver7DutyCad > 0 || report.packagedRemovedUnder7DutyCad > 0) && (
          <>
            <Row k="Removed >7% (LAA)" v={formatLAA(report.packagedRemovedOver7Laa)} dim />
            <Row k="  duty at removal" v={formatCAD(report.packagedRemovedOver7DutyCad)} dim />
            {report.packagedRemovedUnder7DutyCad > 0 && (
              <>
                <Row k="Removed ≤7% (litres)" v={report.packagedRemovedUnder7Litres.toFixed(2)} dim />
                <Row k="  duty at removal" v={formatCAD(report.packagedRemovedUnder7DutyCad)} dim />
              </>
            )}
          </>
        )}
        {report.dutyOnLossesCad > 0 && (
          <Row k="Duty on losses" v={formatCAD(report.dutyOnLossesCad)} dim />
        )}
        <Row k="Rate (CAD / LAA, >7%)"  v={`$${report.dutyRatePerLaa.toFixed(3)}`} />
        <Row k="Rate (CAD / litre, ≤7%)" v={`$${report.dutyRatePerLitreUnder7.toFixed(3)}`} dim />
        <Row k="Duty payable (CAD)"     v={`${formatCAD(report.dutyPayableCad)}`} bold highlight />
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
        <div data-print-hide className="space-y-3">
          <Callout tone="warning" title="Stillhouse does NOT file with CRA">
            Marking submitted only freezes this snapshot inside Stillhouse for your audit
            trail. The actual return has to be entered into{" "}
            <a
              href="https://www.canada.ca/en/revenue-agency/services/e-services/digital-services-businesses/business-account.html"
              target="_blank"
              rel="noreferrer"
              className="underline"
            >
              CRA My Business Account
            </a>
            {" "}separately. Confirm the numbers above match what you filed before clicking
            the button.
          </Callout>
          <div className="flex items-center gap-3">
            <button
              onClick={onSubmit}
              disabled={submitting}
              className="rounded bg-success px-3 py-2 text-sm font-medium text-white hover:bg-success/80 disabled:opacity-50"
            >
              {submitting ? "Freezing…" : "I've filed in CRA — freeze the snapshot"}
            </button>
            {submitError && (
              <span className="text-sm text-danger-fg">
                {submitError instanceof ConnectError ? submitError.rawMessage : String(submitError)}
              </span>
            )}
          </div>
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
      <Callout tone="success" title="Snapshot frozen in Stillhouse">
        Stillhouse has locked the period for backdated changes. The CRA filing itself
        lives in My Business Account — if you haven't entered it there yet, this lock
        doesn't change that.
      </Callout>
      {!open ? (
        <button
          onClick={() => setOpen(true)}
          className="rounded border border-warning/40 px-3 py-2 text-sm text-warning-fg hover:bg-warning/10"
        >
          Reopen for correction…
        </button>
      ) : (
        <div className="space-y-3 rounded border border-warning/40 bg-warning/5 p-4">
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
            <p className="text-sm text-danger-fg">
              {reopen.error instanceof ConnectError ? reopen.error.rawMessage : String(reopen.error)}
            </p>
          )}
          <div className="flex gap-2">
            <button
              onClick={() => reopen.mutate()}
              disabled={!reason.trim() || reopen.isPending}
              className="rounded bg-accent px-3 py-2 text-sm font-medium text-accent-fg hover:bg-accent-hover disabled:opacity-50"
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
  // break-inside-avoid keeps a card together on a printed page — splitting
  // "Closing on hand" across two sheets is exactly the wrong place for the
  // pagebreak to land.
  return (
    <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm print:break-inside-avoid">
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
    <div className={`flex items-center justify-between px-4 py-2 ${highlight ? "bg-success/10" : ""}`}>
      <dt className={`text-fg-muted ${dim ? "text-fg-subtle" : ""}`}>{k}</dt>
      <dd className={`font-mono ${bold ? "font-semibold text-fg" : "text-fg"} ${highlight ? "text-success-fg" : ""}`}>{v}</dd>
    </div>
  );
}
