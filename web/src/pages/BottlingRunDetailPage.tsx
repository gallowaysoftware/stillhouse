import { useState } from "react";
import { useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";

import { Shell } from "@/components/Shell";
import { bottlingClient, materialClient, traceabilityClient } from "@/lib/clients";
import { formatCAD, formatLAA, formatQty } from "@/lib/format";

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
  const cost = useQuery({
    queryKey: ["bottlingRunCost", id],
    queryFn: () => materialClient.bottlingRunCost({ bottlingRunId: id! }),
    enabled: !!id,
  });

  if (!id) return <Shell><p>Missing id.</p></Shell>;
  if (run.isLoading) return <Shell><p className="text-fg-muted">Loading…</p></Shell>;
  if (!run.data?.run) return <Shell><p>Bottling run not found.</p></Shell>;

  const r = run.data.run;

  return (
    <Shell>
      <header className="mb-6 flex items-start justify-between">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">Bottling run #{r.runNo}</h1>
          <p className="text-sm text-fg-muted">
            {r.bottlingDate} · {r.productName} · lot {r.lotCode} · → {r.destinationJurisdiction}
          </p>
          {r.notes && <p className="mt-2 text-sm text-fg">{r.notes}</p>}
        </div>
        <button
          onClick={() => setTraceOpen((s) => !s)}
          className="rounded border border-border-strong px-3 py-2 text-sm font-medium text-fg hover:bg-surface-3"
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

      <section className="mb-8 rounded-lg border border-border bg-surface-2 p-5 shadow-sm">
        <h2 className="mb-3 text-sm font-semibold text-fg-muted">Tank gauge</h2>
        <dl className="grid grid-cols-3 gap-3 text-sm">
          <Row k="Volume drawn">{formatQty(r.tankGaugeVolumeL)} L</Row>
          <Row k="ABV at gauge">{r.tankGaugeAbvPct.toFixed(2)}%</Row>
          <Row k="LAA">{formatLAA(r.tankGaugeLaa)} L</Row>
          {r.bottlingLossL > 0 && <Row k="Bottling loss">{formatQty(r.bottlingLossL)} L</Row>}
        </dl>
      </section>

      {/* A licensee without an excise warehouse licence cannot hold packaged
          spirits non-duty-paid, so duty crystallises here rather than at the
          removal months later — and this run is the document behind that
          line on the return. Shown only when this run is the duty event;
          for a warehouse licensee the duty sits on the removal instead. */}
      {r.dutyPaidAtPackaging && (
        <section className="mb-8 rounded-lg border border-border bg-surface-2 p-5 shadow-sm">
          <h2 className="mb-3 text-sm font-semibold text-fg-muted">Excise duty — payable at packaging</h2>
          <dl className="grid grid-cols-3 gap-3 text-sm">
            {r.dutyRatePerLaa > 0 ? (
              <Row k="Rate">${r.dutyRatePerLaa.toFixed(3)} / LAA</Row>
            ) : (
              <Row k="Rate">per litre of product (≤7% ABV)</Row>
            )}
            <Row k="Duty">{formatCAD(r.dutyAmountCad)}</Row>
            {r.dutyRateSource && <Row k="Rate source">{r.dutyRateSource}</Row>}
          </dl>
          <p className="mt-3 text-xs text-fg-muted">
            Charged on the sealed bottles, not on what was drawn from the tank —
            the packaging loss never became packaged spirits. Removals of this
            lot carry no further duty.
          </p>
        </section>
      )}

      {cost.data && cost.data.lines.length > 0 && (
        <section className="mb-8 rounded-lg border border-border bg-surface-2 p-5 shadow-sm">
          <h2 className="mb-3 text-sm font-semibold text-fg-muted">Material cost</h2>
          <div className="mb-3 grid grid-cols-2 gap-4 sm:grid-cols-3">
            <Stat label="Total materials" value={formatCAD(cost.data.totalMaterialCostCad)} />
            <Stat
              label="Per bottle"
              value={formatCAD(cost.data.materialCostPerBottleCad)}
              highlight
            />
          </div>
          <table className="min-w-full divide-y divide-border text-sm">
            <thead className="text-left text-xs text-fg-muted">
              <tr>
                <th className="px-3 py-2">Material / Lot</th>
                <th className="px-3 py-2 text-right">Qty</th>
                <th className="px-3 py-2 text-right">Unit cost</th>
                <th className="px-3 py-2 text-right">Line cost</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {cost.data.lines.map((l, i) => (
                <tr key={`${l.materialName}-${l.supplierLot}-${i}`}>
                  <td className="px-3 py-2">
                    <div className="text-fg">{l.materialName}</div>
                    {l.supplierLot && <div className="text-xs text-fg-muted">lot {l.supplierLot}</div>}
                  </td>
                  <td className="px-3 py-2 text-right text-fg-muted">{formatQty(l.quantityUsed)} {l.uom}</td>
                  <td className="px-3 py-2 text-right text-fg-muted">
                    {/* Unit costs keep three decimals: a closure or a
                        label is priced in fractions of a cent, and
                        rounding to two loses the difference. */}
                    {l.unitCostCad > 0 ? `$${l.unitCostCad.toFixed(3)}` : <span className="text-warning-fg">no price</span>}
                  </td>
                  <td className="px-3 py-2 text-right font-medium text-fg">
                    {l.lineCostCad > 0 ? formatCAD(l.lineCostCad) : "—"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          <p className="mt-3 text-xs text-fg-muted">
            Materials only. Doesn't include labour, energy, packaging, excise duty, or overhead.
            Lines without a recorded unit price are dropped from the per-bottle figure.
          </p>
        </section>
      )}

      {traceOpen && (
        <section className="mb-8 rounded-lg border border-border bg-surface-2 p-5 shadow-sm">
          <h2 className="mb-3 text-sm font-semibold text-fg-muted">Grain-to-glass trace</h2>
          {trace.isLoading && <p className="text-sm text-fg-muted">Loading trace…</p>}
          {trace.error && <p className="text-sm text-danger-fg">{String(trace.error)}</p>}
          {trace.data && (
            <ol className="space-y-1 font-mono text-xs">
              {trace.data.nodes.map((n) => (
                <li key={`${n.kind}-${n.id}-${n.headline}`} className="text-fg">
                  <span className="text-fg">{n.headline}</span>
                  {n.detail && <span className="block pl-4 text-fg-muted">{n.detail}</span>}
                </li>
              ))}
            </ol>
          )}
          <p className="mt-3 text-xs text-fg-muted">
            Walks bottling → source-container feeds (last year) → production gauges → distillation → every charge's
            fermentation → yeast lot → mash → material lots → recipe, plus barrel dumps where applicable.
          </p>
        </section>
      )}

      <h2 className="mb-3 text-sm font-semibold text-fg-muted">Excise stamps applied</h2>
      <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-3">Jurisdiction</th>
              <th className="px-4 py-3">Serial range</th>
              <th className="px-4 py-3 text-right">Bottles</th>
              <th className="px-4 py-3 text-right">Voids</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {r.stampUsage.length === 0 && (
              <tr><td colSpan={4} className="px-4 py-3 text-fg-muted">No stamp usage recorded.</td></tr>
            )}
            {r.stampUsage.map((u) => (
              <tr key={u.id}>
                <td className="px-4 py-3 text-fg">{u.jurisdiction}</td>
                <td className="px-4 py-3 font-mono text-fg">
                  {u.serialStart && u.serialEnd
                    ? `${u.serialStart} – ${u.serialEnd}`
                    : <span className="text-fg-subtle">(no serials recorded for this order)</span>}
                </td>
                <td className="px-4 py-3 text-right text-fg-muted">{u.bottleCount.toLocaleString()}</td>
                <td className="px-4 py-3 text-right text-fg-muted">{u.voids}</td>
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
    <div className={`rounded-lg border bg-surface-2 p-4 shadow-sm ${highlight ? "border-success/30" : "border-border"}`}>
      <p className="text-xs text-fg-muted">{label}</p>
      <p className={`mt-1 text-xl font-semibold ${highlight ? "text-success-fg" : "text-fg"}`}>{value}</p>
    </div>
  );
}

function Row({ k, children }: { k: string; children: React.ReactNode }) {
  return (
    <div>
      <dt className="text-xs text-fg-muted">{k}</dt>
      <dd className="mt-1 text-fg">{children}</dd>
    </div>
  );
}
