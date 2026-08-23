import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { bulkClient, redistillationClient } from "@/lib/clients";
import { RedistillationReason } from "@/gen/stillhouse/v1/redistillation_pb";
import { formatLAA } from "@/lib/format";
import { WriteOnly } from "@/lib/role";

const reasons: { v: RedistillationReason; label: string; hint: string }[] = [
  { v: RedistillationReason.OFF_SPEC, label: "Off spec", hint: "It wasn't what it should have been." },
  { v: RedistillationReason.FEINTS_RECOVERY, label: "Feints recovery", hint: "Heads and tails, for the alcohol in them." },
  { v: RedistillationReason.REPROCESSING, label: "Reprocessing", hint: "A deliberate second pass — gin from NGS, say." },
  { v: RedistillationReason.OTHER, label: "Other", hint: "" },
];
const reasonLabel = (r: RedistillationReason) => reasons.find((x) => x.v === r)?.label ?? "—";

/**
 * Spirit that went back into the still, and what came out.
 *
 * The withdrawal is already a reportable B266 movement. What this adds
 * is the other half: how much went in, how much came back, and the
 * difference — which is a loss like any other and has to be ruled
 * relieved or duty-payable. Without it that difference is not a loss
 * anybody classified, it is just a number that got smaller between two
 * periods.
 */
export function RedistillationPanel() {
  const qc = useQueryClient();
  const [showForm, setShowForm] = useState(false);
  const [closing, setClosing] = useState<string | null>(null);

  const list = useQuery({
    queryKey: ["listRedistillations"],
    queryFn: () => redistillationClient.listRedistillations({}),
  });
  const containers = useQuery({
    queryKey: ["listBulkContainers"],
    queryFn: () => bulkClient.listBulkContainers({}),
  });
  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["listRedistillations"] });
    qc.invalidateQueries({ queryKey: ["listBulkContainers"] });
    qc.invalidateQueries({ queryKey: ["listAlerts"] });
  };
  const start = useMutation({
    mutationFn: (m: Parameters<typeof redistillationClient.startRedistillation>[0]) =>
      redistillationClient.startRedistillation(m),
    onSuccess: () => { setShowForm(false); invalidate(); },
  });
  const close = useMutation({
    mutationFn: (m: Parameters<typeof redistillationClient.recordRedistillationOutput>[0]) =>
      redistillationClient.recordRedistillationOutput(m),
    onSuccess: () => { setClosing(null); invalidate(); },
  });

  const rows = list.data?.redistillations ?? [];

  return (
    <section className="mt-8">
      <div className="mb-3 flex items-baseline justify-between">
        <h2 className="text-sm font-semibold text-fg-muted">Back through the still</h2>
        <WriteOnly>
          <button
            onClick={() => setShowForm((v) => !v)}
            className="text-xs text-fg-muted hover:text-fg"
          >
            {showForm ? "Cancel" : "Send spirit back"}
          </button>
        </WriteOnly>
      </div>

      <div className="rounded-lg border border-border bg-surface-2 p-5 shadow-sm">
        <p className="mb-4 text-sm text-fg-muted">
          Spirit going back into production leaves stock as a reportable movement. What
          comes out is recorded against it, and the difference is a loss that still needs
          ruling on — relieved or duty-payable.
        </p>

        {showForm && (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              const fd = new FormData(e.currentTarget);
              start.mutate({
                sourceContainerId: fd.get("container")?.toString() ?? "",
                reason: Number(fd.get("reason")) as RedistillationReason,
                volumeL: Number(fd.get("volume") ?? 0),
                abvPct: Number(fd.get("abv") ?? 0),
                takenOn: fd.get("taken_on")?.toString() ?? "",
                notes: fd.get("notes")?.toString() ?? "",
              });
            }}
            className="mb-4 grid gap-3 border-b border-border pb-4 sm:grid-cols-3"
          >
            <div className="sm:col-span-2">
              <label className="mb-1 block text-xs text-fg-muted">From</label>
              <select name="container" required
                      className="w-full rounded border border-border-strong px-2 py-1.5 text-sm">
                <option value="">— choose —</option>
                {containers.data?.containers
                  .filter((c) => c.currentLaa > 0)
                  .map((c) => (
                    <option key={c.id} value={c.id}>
                      {c.name} — {formatLAA(c.currentLaa)} LAA at {c.currentAbvPct?.toFixed(1)}%
                    </option>
                  ))}
              </select>
            </div>
            <div>
              <label className="mb-1 block text-xs text-fg-muted">Why</label>
              <select name="reason" defaultValue={String(RedistillationReason.OFF_SPEC)}
                      className="w-full rounded border border-border-strong px-2 py-1.5 text-sm">
                {reasons.map((r) => <option key={r.v} value={r.v}>{r.label}</option>)}
              </select>
            </div>
            <RField label="Volume (L)" name="volume" type="number" step="0.01" required />
            <RField label="Strength (%)" name="abv" type="number" step="0.01" required />
            <RField label="Taken on" name="taken_on" type="date" />
            <RField label="Notes" name="notes" className="sm:col-span-3" />
            <div className="sm:col-span-3 flex items-center gap-3">
              <button type="submit" disabled={start.isPending}
                      className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50">
                {start.isPending ? "Recording…" : "Take it out of stock"}
              </button>
              {start.error && (
                <span className="text-sm text-danger-fg">
                  {start.error instanceof ConnectError ? start.error.rawMessage : String(start.error)}
                </span>
              )}
            </div>
          </form>
        )}

        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="text-left text-xs text-fg-muted">
            <tr>
              <th className="px-2 py-2">Taken</th>
              <th className="px-2 py-2">From</th>
              <th className="px-2 py-2">Why</th>
              <th className="px-2 py-2 text-right">In</th>
              <th className="px-2 py-2 text-right">Out</th>
              <th className="px-2 py-2 text-right">Lost</th>
              <th className="px-2 py-2 text-right"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {rows.length === 0 && (
              <tr><td colSpan={7} className="px-2 py-3 text-fg-muted">Nothing has gone back through.</td></tr>
            )}
            {rows.map((r) => (
              <tr key={r.id} className={!r.laaProducedSet ? "bg-warning/5" : ""}>
                <td className="px-2 py-2 text-fg-muted">{r.takenOn}</td>
                <td className="px-2 py-2 text-fg">{r.sourceContainerName}</td>
                <td className="px-2 py-2 text-fg-muted">{reasonLabel(r.reason)}</td>
                <td className="px-2 py-2 text-right text-fg-muted">{formatLAA(r.laaTaken)}</td>
                <td className="px-2 py-2 text-right text-fg-muted">
                  {r.laaProducedSet ? formatLAA(r.laaProduced) : <span className="text-warning-fg">still out</span>}
                </td>
                <td className="px-2 py-2 text-right">
                  {r.lossLaaSet ? (
                    <span className={r.lossLaa > 0 && !r.lossClassified ? "text-warning-fg" : "text-fg-muted"}>
                      {formatLAA(r.lossLaa)}
                    </span>
                  ) : "—"}
                </td>
                <td className="px-2 py-2 text-right">
                  {!r.laaProducedSet && (
                    <WriteOnly>
                      <button
                        onClick={() => setClosing(closing === r.id ? null : r.id)}
                        className="text-xs text-accent hover:underline"
                      >
                        {closing === r.id ? "Cancel" : "Record what came out"}
                      </button>
                    </WriteOnly>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>

        {closing && (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              const fd = new FormData(e.currentTarget);
              close.mutate({
                id: closing,
                laaProduced: Number(fd.get("laa") ?? 0),
                producedOn: fd.get("produced_on")?.toString() ?? "",
              });
            }}
            className="mt-4 flex flex-wrap items-end gap-3 border-t border-border pt-4"
          >
            <RField label="LAA recovered" name="laa" type="number" step="0.01" required />
            <RField label="Produced on" name="produced_on" type="date" />
            <button type="submit" disabled={close.isPending}
                    className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50">
              {close.isPending ? "Recording…" : "Record"}
            </button>
            {close.error && (
              <span className="text-sm text-danger-fg">
                {close.error instanceof ConnectError ? close.error.rawMessage : String(close.error)}
              </span>
            )}
          </form>
        )}

        {close.isSuccess && close.data.needsLossClassification && (
          <p className="mt-3 rounded border border-warning/40 bg-warning/10 px-3 py-2 text-sm text-fg-muted">
            {formatLAA(close.data.lossLaa)} LAA went in and did not come out. That's a loss
            like any other — rule it relieved or duty-payable on the losses screen, or it
            sits unclassified on the return.
          </p>
        )}
      </div>
    </section>
  );
}

function RField({ label, name, type = "text", step, required, className }: {
  label: string; name: string; type?: string; step?: string;
  required?: boolean; className?: string;
}) {
  return (
    <div className={className}>
      <label className="mb-1 block text-xs text-fg-muted">{label}</label>
      <input name={name} type={type} step={step} required={required}
             className="w-full rounded border border-border-strong px-2 py-1.5 text-sm" />
    </div>
  );
}
