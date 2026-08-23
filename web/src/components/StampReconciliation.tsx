import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { exciseStampClient } from "@/lib/clients";
import { StampDispositionKind } from "@/gen/stillhouse/v1/excise_stamp_pb";
import { WriteOnly } from "@/lib/role";

const dispositionKinds: { v: StampDispositionKind; label: string; hint: string }[] = [
  { v: StampDispositionKind.SPOILED, label: "Spoiled", hint: "Damaged in application — the ordinary case." },
  { v: StampDispositionKind.DAMAGED, label: "Damaged", hint: "Damaged before application, in storage or transit." },
  { v: StampDispositionKind.LOST, label: "Lost", hint: "Cannot be located. Reportable." },
  { v: StampDispositionKind.STOLEN, label: "Stolen", hint: "Known to have been taken. Reportable." },
  { v: StampDispositionKind.DESTROYED, label: "Destroyed", hint: "Deliberately destroyed." },
  { v: StampDispositionKind.RETURNED, label: "Returned to CRA", hint: "" },
];

/**
 * Where every stamp in one order went.
 *
 * Three counters answer "how many are left". This answers the question
 * CRA actually asks — "where did stamp ONT00457 go" — by walking the
 * issued range end to end: applied to this run, disposed of for this
 * reason, or still on hand. Anything the account cannot close is stated
 * as a sentence rather than left as a number to notice.
 */
export function StampReconciliation({ orderId }: { orderId: string }) {
  const qc = useQueryClient();
  const [showForm, setShowForm] = useState(false);

  const rec = useQuery({
    queryKey: ["reconcileStampOrder", orderId],
    queryFn: () => exciseStampClient.reconcileStampOrder({ stampOrderId: orderId }),
  });
  const record = useMutation({
    mutationFn: (m: Parameters<typeof exciseStampClient.recordStampDisposition>[0]) =>
      exciseStampClient.recordStampDisposition(m),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["reconcileStampOrder", orderId] });
      qc.invalidateQueries({ queryKey: ["listStampOrders"] });
      setShowForm(false);
    },
  });

  const r = rec.data;
  if (!r) return null;

  return (
    <div className="mt-3 rounded-lg border border-border bg-surface p-4">
      <div className="mb-3 flex flex-wrap items-baseline justify-between gap-3">
        <p className="text-sm font-medium text-fg">
          Stamp account
          {r.serialRangeKnown && (
            <span className="ml-2 font-mono text-xs text-fg-muted">
              {r.serialStart}–{r.serialEnd}
            </span>
          )}
        </p>
        <p className="text-xs text-fg-muted">
          {r.receivedCount.toString()} received · {r.appliedCount.toString()} applied ·{" "}
          {r.disposedCount.toString()} disposed of
        </p>
      </div>

      {!r.serialRangeKnown && (
        <p className="mb-3 rounded border border-border bg-surface-2 px-3 py-2 text-xs text-fg-muted">
          No serial range was recorded for this order, so only the counts can be
          reconciled. Record the range when receiving to get a serial-level account.
        </p>
      )}

      {r.discrepancies.length > 0 && (
        <div className="mb-3 rounded border border-danger/40 bg-danger/10 px-3 py-2">
          <p className="text-sm font-medium text-danger-fg">This account does not close</p>
          <ul className="mt-1 space-y-1 text-sm text-fg-muted">
            {r.discrepancies.map((d, i) => <li key={i}>{d}</li>)}
          </ul>
        </div>
      )}

      <table className="min-w-full divide-y divide-border text-sm">
        <thead className="text-left text-xs text-fg-muted">
          <tr>
            <th className="px-2 py-1.5">Serials</th>
            <th className="px-2 py-1.5 text-right">Count</th>
            <th className="px-2 py-1.5">Went to</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-border">
          {r.allocations.map((a, i) => (
            <tr key={i} className={a.kind === "on_hand" ? "text-fg-muted" : ""}>
              <td className="px-2 py-1.5 font-mono text-xs">
                {a.unplaced ? (
                  <span className="text-warning-fg">not identified</span>
                ) : a.serialStart === a.serialEnd ? (
                  a.serialStart
                ) : (
                  `${a.serialStart}–${a.serialEnd}`
                )}
              </td>
              <td className="px-2 py-1.5 text-right">{a.count.toString()}</td>
              <td className="px-2 py-1.5">{a.purpose}</td>
            </tr>
          ))}
        </tbody>
      </table>

      <WriteOnly>
        {!showForm ? (
          <button
            onClick={() => setShowForm(true)}
            className="mt-3 text-xs text-fg-muted hover:text-fg"
          >
            Record stamps that never reached a bottle
          </button>
        ) : (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              const fd = new FormData(e.currentTarget);
              record.mutate({
                stampOrderId: orderId,
                kind: Number(fd.get("kind")) as StampDispositionKind,
                quantity: Number(fd.get("quantity") ?? 0),
                serialStart: fd.get("serial_start")?.toString() ?? "",
                serialEnd: fd.get("serial_end")?.toString() ?? "",
                occurredOn: fd.get("occurred_on")?.toString() ?? "",
                explanation: fd.get("explanation")?.toString() ?? "",
                reportedRef: fd.get("reported_ref")?.toString() ?? "",
              });
            }}
            className="mt-3 grid gap-3 border-t border-border pt-3 sm:grid-cols-3"
          >
            <div>
              <label className="mb-1 block text-xs text-fg-muted">What happened</label>
              <select name="kind" defaultValue={String(StampDispositionKind.SPOILED)}
                      className="w-full rounded border border-border-strong px-2 py-1.5 text-sm">
                {dispositionKinds.map((k) => (
                  <option key={k.v} value={k.v}>{k.label}</option>
                ))}
              </select>
            </div>
            <SmallField label="How many" name="quantity" type="number" required />
            <SmallField label="Date" name="occurred_on" type="date" />
            <SmallField label="First serial (if known)" name="serial_start" />
            <SmallField label="Last serial (if known)" name="serial_end" />
            <SmallField label="CRA report reference" name="reported_ref" />
            <div className="sm:col-span-3">
              <label className="mb-1 block text-xs text-fg-muted">
                What happened, in a sentence — a reason code alone doesn't answer an auditor
              </label>
              <input name="explanation" required
                     placeholder="Roll not on the shelf at the November count."
                     className="w-full rounded border border-border-strong px-2 py-1.5 text-sm" />
            </div>
            <div className="sm:col-span-3 flex items-center gap-3">
              <button type="submit" disabled={record.isPending}
                      className="rounded bg-accent px-3 py-1.5 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50">
                {record.isPending ? "Recording…" : "Record"}
              </button>
              <button type="button" onClick={() => setShowForm(false)}
                      className="text-sm text-fg-muted hover:text-fg">
                Cancel
              </button>
              {record.error && (
                <span className="text-sm text-danger-fg">
                  {record.error instanceof ConnectError
                    ? record.error.rawMessage
                    : String(record.error)}
                </span>
              )}
            </div>
          </form>
        )}
      </WriteOnly>
    </div>
  );
}

function SmallField({ label, name, type = "text", required }: {
  label: string; name: string; type?: string; required?: boolean;
}) {
  return (
    <div>
      <label className="mb-1 block text-xs text-fg-muted">{label}</label>
      <input name={name} type={type} required={required}
             className="w-full rounded border border-border-strong px-2 py-1.5 text-sm" />
    </div>
  );
}
