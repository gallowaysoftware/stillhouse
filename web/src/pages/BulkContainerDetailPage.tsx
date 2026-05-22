import { useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";

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
  if (detail.isLoading) return <Shell><p className="text-stone-500">Loading…</p></Shell>;
  if (!detail.data?.container) return <Shell><p>Not found.</p></Shell>;

  const c = detail.data.container;

  return (
    <Shell>
      <header className="mb-6">
        <h1 className="text-2xl font-semibold">{c.name}</h1>
        <p className="text-sm text-stone-500">
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

      <h2 className="mb-3 text-sm font-semibold uppercase text-stone-500">Movement history</h2>
      <div className="overflow-hidden rounded-lg border border-stone-200 bg-white shadow-sm">
        <table className="min-w-full divide-y divide-stone-200 text-sm">
          <thead className="bg-stone-50 text-left text-xs uppercase text-stone-500">
            <tr>
              <th className="px-4 py-3">When</th>
              <th className="px-4 py-3">Reason</th>
              <th className="px-4 py-3">Direction</th>
              <th className="px-4 py-3 text-right">Volume (L)</th>
              <th className="px-4 py-3 text-right">ABV</th>
              <th className="px-4 py-3 text-right">LAA</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-stone-100">
            {detail.data.movements.length === 0 && (
              <tr><td colSpan={6} className="px-4 py-3 text-stone-500">No movements yet.</td></tr>
            )}
            {detail.data.movements.map((m) => {
              const isIn = m.destinationContainerId === c.id;
              return (
                <tr key={m.id}>
                  <td className="px-4 py-3 text-stone-600">
                    {m.occurredAt ? new Date(Number(m.occurredAt.seconds) * 1000).toLocaleString() : ""}
                  </td>
                  <td className="px-4 py-3 text-stone-600">{bulkMovementReasonLabel(m.reason)}</td>
                  <td className="px-4 py-3 text-stone-600">
                    {isIn
                      ? `← ${m.sourceContainerName || "(new alcohol)"}`
                      : `→ ${m.destinationContainerName || "(loss)"}`}
                  </td>
                  <td className="px-4 py-3 text-right text-stone-600">{formatQty(m.volumeL)}</td>
                  <td className="px-4 py-3 text-right text-stone-600">{m.abvPct.toFixed(2)}%</td>
                  <td className={`px-4 py-3 text-right font-medium ${isIn ? "text-green-700" : "text-stone-600"}`}>
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
    <div className="rounded-lg border border-stone-200 bg-white p-4 shadow-sm">
      <p className="text-xs uppercase text-stone-500">{label}</p>
      <p className={`mt-1 text-xl font-semibold ${highlight ? "text-stone-900" : "text-stone-900"}`}>
        {value}
      </p>
    </div>
  );
}
