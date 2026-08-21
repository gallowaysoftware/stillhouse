import { useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";

import { AdoptStockCard } from "@/components/AdoptStockCard";
import { ExternalMovementCard } from "@/components/ExternalMovementCard";
import { InventoryAdjustmentCard } from "@/components/InventoryAdjustmentCard";
import { ReductionCalculator } from "@/components/ReductionCalculator";
import { Shell } from "@/components/Shell";
import { bulkClient } from "@/lib/clients";
import {
  bulkContainerKindLabel,
  bulkMovementReasonLabel,
  formatLAA,
  formatQty,
} from "@/lib/format";

export function BulkContainerDetailPage() {
  const { id } = useParams();
  const detail = useQuery({
    queryKey: ["getBulkContainer", id],
    queryFn: () => bulkClient.getBulkContainer({ id: id! }),
    enabled: !!id,
  });

  if (!id) return <Shell><p>Missing id.</p></Shell>;
  if (detail.isLoading) return <Shell><p className="text-fg-muted">Loading…</p></Shell>;
  if (!detail.data?.container) return <Shell><p>Not found.</p></Shell>;

  const c = detail.data.container;

  return (
    <Shell>
      <header className="mb-6">
        <h1 className="text-3xl font-bold tracking-tight">{c.name}</h1>
        <p className="text-sm text-fg-muted">
          {bulkContainerKindLabel(c.kind)}
          {c.capacityLSet && <> · capacity {formatQty(c.capacityL)} L</>}
          {c.location && <> · {c.location}</>}
        </p>
      </header>

      <section className="mb-8 grid grid-cols-2 gap-4 sm:grid-cols-4">
        <Stat label="Current volume" value={`${formatQty(c.currentVolumeL)} L`} />
        <Stat label="Current ABV" value={c.currentAbvPctSet ? `${c.currentAbvPct.toFixed(2)}%` : "—"} />
        <Stat label="Current LAA" value={`${formatLAA(c.currentLaa)} L`} highlight />
        <Stat label="Movements" value={String(detail.data.movements.length)} />
      </section>

      {c.currentVolumeL === 0 && (
        <section className="mb-8 max-w-xl">
          <AdoptStockCard containerId={c.id} containerName={c.name} isBarrel={false} />
        </section>
      )}

      {/* Reconciling the book to a physical count. Available whether the
          vessel holds anything or not: a container the ledger says is
          empty and that turns out not to be is exactly the variance line D
          exists for. */}
      {/* Spirits arriving on or leaving the premises — the B266 page 3
          lines that had no path at all. */}
      <section className="mb-8 max-w-xl">
        <ExternalMovementCard containerId={c.id} containerName={c.name} />
      </section>

      <section className="mb-8 max-w-xl">
        <InventoryAdjustmentCard
          containerId={c.id}
          containerName={c.name}
          bookVolumeL={c.currentVolumeL}
          bookAbvPct={c.currentAbvPctSet ? c.currentAbvPct : null}
          bookLaa={c.currentLaa}
        />
      </section>

      {/* Prefilled from the tank in front of you — reducing this vessel is
          the reason you're on this page. */}
      {c.currentVolumeL > 0 && c.currentAbvPctSet && (
        <section className="mb-8 max-w-xl">
          <ReductionCalculator
            title={`Reduce ${c.name}`}
            defaultFromVolumeL={String(c.currentVolumeL)}
            defaultFromStrength={c.currentAbvPct.toFixed(2)}
          />
        </section>
      )}

      <h2 className="mb-3 text-sm font-semibold text-fg-muted">Movement history</h2>
      <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-3">When</th>
              <th className="px-4 py-3">Reason</th>
              <th className="px-4 py-3">Direction</th>
              <th className="px-4 py-3 text-right">Volume (L)</th>
              <th className="px-4 py-3 text-right">ABV</th>
              <th className="px-4 py-3 text-right">LAA</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {detail.data.movements.length === 0 && (
              <tr><td colSpan={6} className="px-4 py-3 text-fg-muted">No movements yet.</td></tr>
            )}
            {detail.data.movements.map((m) => {
              const isIn = m.destinationContainerId === c.id;
              return (
                <tr key={m.id}>
                  <td className="px-4 py-3 text-fg-muted">
                    {m.occurredAt ? new Date(Number(m.occurredAt.seconds) * 1000).toLocaleString() : ""}
                  </td>
                  <td className="px-4 py-3 text-fg-muted">{bulkMovementReasonLabel(m.reason)}</td>
                  <td className="px-4 py-3 text-fg-muted">
                    {isIn
                      ? `← ${m.sourceContainerName || "(new alcohol)"}`
                      : `→ ${m.destinationContainerName || "(loss)"}`}
                  </td>
                  <td className="px-4 py-3 text-right text-fg-muted">{formatQty(m.volumeL)}</td>
                  <td className="px-4 py-3 text-right text-fg-muted">{m.abvPct.toFixed(2)}%</td>
                  <td className={`px-4 py-3 text-right font-medium ${isIn ? "text-success-fg" : "text-fg-muted"}`}>
                    {isIn ? "+" : "−"}{formatLAA(m.laa)}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </Shell>
  );
}

function Stat({ label, value, highlight }: { label: string; value: string; highlight?: boolean }) {
  return (
    <div className="rounded-lg border border-border bg-surface-2 p-4 shadow-sm">
      <p className="text-xs text-fg-muted">{label}</p>
      <p className={`mt-1 text-xl font-semibold ${highlight ? "text-fg" : "text-fg"}`}>
        {value}
      </p>
    </div>
  );
}
