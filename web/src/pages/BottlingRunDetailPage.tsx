import { useState } from "react";
import { useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";

import { Shell } from "@/components/Shell";
import { bottlingClient, traceabilityClient } from "@/lib/clients";
import { formatLAA, formatQty } from "@/lib/format";

export function BottlingRunDetailPage() {
  const { id } = useParams();
  const run = useQuery({
    queryKey: ["getBottlingRun", id],
    queryFn: () => bottlingClient.getBottlingRun({ id: id! }),
    enabled: !!id,
  });
  const [traceOpen, setTraceOpen] = useState(false);
  const trace = useQuery({
    queryKey: ["traceBottlingRun", id],
    queryFn: () => traceabilityClient.traceBottlingRun({ bottlingRunId: id! }),
    enabled: traceOpen && !!id,
  });

  if (!id) return <Shell><p>Missing id.</p></Shell>;
  if (run.isLoading) return <Shell><p className="text-stone-500">Loading…</p></Shell>;
  if (!run.data?.run) return <Shell><p>Bottling run not found.</p></Shell>;

  const r = run.data.run;

  return (
    <Shell>
      <header className="mb-6 flex items-start justify-between">
        <div>
          <h1 className="text-2xl font-semibold">Bottling run #{r.runNo}</h1>
          <p className="text-sm text-stone-500">
            {r.bottlingDate} · {r.productName} · lot {r.lotCode} · → {r.destinationJurisdiction}
          </p>
          {r.notes && <p className="mt-2 text-sm text-stone-700">{r.notes}</p>}
        </div>
        <button
          onClick={() => setTraceOpen((s) => !s)}
          className="rounded border border-stone-300 px-3 py-2 text-sm font-medium text-stone-700 hover:bg-stone-100"
        >
          {traceOpen ? "Hide trace" : "Trace to grain"}
        </button>
      </header>

      <section className="mb-8 grid grid-cols-2 gap-4 sm:grid-cols-4">
        <Stat label="Bottles" value={r.bottleCount.toLocaleString()} />
        <Stat label="Bottle size" value={`${r.productBottleSizeMl} mL`} />
        <Stat label="ABV" value={`${r.tankGaugeAbvPct.toFixed(2)}%`} />
        <Stat label="LAA" value={`${formatLAA(r.tankGaugeLaa)} L`} highlight />
      </section>

      <section className="mb-8 rounded-lg border border-stone-200 bg-white p-5 shadow-sm">
        <h2 className="mb-3 text-sm font-semibold uppercase text-stone-500">Tank gauge</h2>
        <dl className="grid grid-cols-3 gap-3 text-sm">
          <Row k="Volume drawn">{formatQty(r.tankGaugeVolumeL)} L</Row>
          <Row k="ABV at gauge">{r.tankGaugeAbvPct.toFixed(2)}%</Row>
          <Row k="LAA">{formatLAA(r.tankGaugeLaa)} L</Row>
          {r.bottlingLossL > 0 && <Row k="Bottling loss">{formatQty(r.bottlingLossL)} L</Row>}
        </dl>
      </section>

      {traceOpen && (
        <section className="mb-8 rounded-lg border border-stone-200 bg-white p-5 shadow-sm">
          <h2 className="mb-3 text-sm font-semibold uppercase text-stone-500">Grain-to-glass trace</h2>
          {trace.isLoading && <p className="text-sm text-stone-500">Loading trace…</p>}
          {trace.error && <p className="text-sm text-red-600">{String(trace.error)}</p>}
          {trace.data && (
            <ol className="space-y-1 font-mono text-xs">
              {trace.data.nodes.map((n) => (
                <li key={`${n.kind}-${n.id}-${n.headline}`} className="text-stone-700">
                  <span className="text-stone-900">{n.headline}</span>
                  {n.detail && <span className="block pl-4 text-stone-500">{n.detail}</span>}
                </li>
              ))}
            </ol>
          )}
          <p className="mt-3 text-xs text-stone-500">
            Walks bottling → source-container feeds (last year) → production gauges → distillation → fermentation → mash →
            material lots (when recorded) → recipe, and barrel dumps where applicable. Single-charge-per-distillation
            simplification; multi-charge fans need follow-up per row.
          </p>
        </section>
      )}

      <h2 className="mb-3 text-sm font-semibold uppercase text-stone-500">Excise stamps applied</h2>
      <div className="overflow-hidden rounded-lg border border-stone-200 bg-white shadow-sm">
        <table className="min-w-full divide-y divide-stone-200 text-sm">
          <thead className="bg-stone-50 text-left text-xs uppercase text-stone-500">
            <tr>
              <th className="px-4 py-3">Jurisdiction</th>
              <th className="px-4 py-3">Serial range</th>
              <th className="px-4 py-3 text-right">Bottles</th>
              <th className="px-4 py-3 text-right">Voids</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-stone-100">
            {r.stampUsage.length === 0 && (
              <tr><td colSpan={4} className="px-4 py-3 text-stone-500">No stamp usage recorded.</td></tr>
            )}
            {r.stampUsage.map((u) => (
              <tr key={u.id}>
                <td className="px-4 py-3 text-stone-900">{u.jurisdiction}</td>
                <td className="px-4 py-3 font-mono text-stone-700">
                  {u.serialStart && u.serialEnd
                    ? `${u.serialStart} – ${u.serialEnd}`
                    : <span className="text-stone-400">(no serials recorded for this order)</span>}
                </td>
                <td className="px-4 py-3 text-right text-stone-600">{u.bottleCount.toLocaleString()}</td>
                <td className="px-4 py-3 text-right text-stone-600">{u.voids}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Shell>
  );
}

function Stat({ label, value, highlight }: { label: string; value: string; highlight?: boolean }) {
  return (
    <div className={`rounded-lg border bg-white p-4 shadow-sm ${highlight ? "border-emerald-200" : "border-stone-200"}`}>
      <p className="text-xs uppercase text-stone-500">{label}</p>
      <p className={`mt-1 text-xl font-semibold ${highlight ? "text-emerald-700" : "text-stone-900"}`}>{value}</p>
    </div>
  );
}

function Row({ k, children }: { k: string; children: React.ReactNode }) {
  return (
    <div>
      <dt className="text-xs uppercase text-stone-500">{k}</dt>
      <dd className="mt-1 text-stone-900">{children}</dd>
    </div>
  );
}
