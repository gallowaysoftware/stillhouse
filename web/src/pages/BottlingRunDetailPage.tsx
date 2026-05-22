import { useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";

import { Shell } from "@/components/Shell";
import { bottlingClient } from "@/lib/clients";
import { formatLAA, formatQty } from "@/lib/format";

export function BottlingRunDetailPage() {
  const { id } = useParams();
  const run = useQuery({
    queryKey: ["getBottlingRun", id],
    queryFn: () => bottlingClient.getBottlingRun({ id: id! }),
    enabled: !!id,
  });

  if (!id) return <Shell><p>Missing id.</p></Shell>;
  if (run.isLoading) return <Shell><p className="text-stone-500">Loading…</p></Shell>;
  if (!run.data?.run) return <Shell><p>Bottling run not found.</p></Shell>;

  const r = run.data.run;

  return (
    <Shell>
      <header className="mb-6">
        <h1 className="text-2xl font-semibold">Bottling run #{r.runNo}</h1>
        <p className="text-sm text-stone-500">
          {r.bottlingDate} · {r.productName} · lot {r.lotCode} · → {r.destinationJurisdiction}
        </p>
        {r.notes && <p className="mt-2 text-sm text-stone-700">{r.notes}</p>}
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
