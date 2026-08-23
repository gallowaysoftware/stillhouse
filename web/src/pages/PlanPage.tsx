import { useState } from "react";
import { ForecastPanel } from "@/components/ForecastPanel";
import { useQuery } from "@tanstack/react-query";

import { EmptyRow } from "@/components/EmptyState";
import { Shell } from "@/components/Shell";
import { schedulingClient } from "@/lib/clients";
import { formatLAA } from "@/lib/format";

export function PlanPage() {
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const plan = useQuery({
    queryKey: ["productionPlan", from, to],
    queryFn: () => schedulingClient.productionPlan({ from, to }),
  });
  const d = plan.data;

  return (
    <Shell>
      <div className="mb-6">
        <h1 className="text-3xl font-bold tracking-tight">Production plan</h1>
        <p className="text-sm text-fg-muted">
          What is owed, what is here, and whether the plant can close the gap.
        </p>
      </div>

      <div className="mb-4 flex flex-wrap items-end gap-3">
        <div>
          <label className="mb-1 block text-xs text-fg-muted">From</label>
          <input type="date" value={from} onChange={(e) => setFrom(e.target.value)}
                 className="rounded border border-border-strong px-2 py-1.5 text-sm" />
        </div>
        <div>
          <label className="mb-1 block text-xs text-fg-muted">To</label>
          <input type="date" value={to} onChange={(e) => setTo(e.target.value)}
                 className="rounded border border-border-strong px-2 py-1.5 text-sm" />
        </div>
        {d && (
          <span className="text-xs text-fg-subtle">
            {d.from} → {d.to} (blank uses the next four weeks)
          </span>
        )}
      </div>

      {d && (
        <>
          {/* The basis, on the page, every time. A plan built on an
              invented forecast looks exactly as authoritative as one
              built on orders. */}
          <p className="mb-4 text-xs text-fg-subtle">{d.basis}</p>

          {d.shortOfAlcohol && (
            <p className="mb-4 rounded border border-danger/40 bg-danger/10 px-3 py-2 text-sm text-fg">
              The shortfalls below need <strong>{formatLAA(d.shortfallLaa)} L</strong> of
              absolute alcohol and you hold <strong>{formatLAA(d.availableLaa)} L</strong> you
              could bottle. Worth knowing before promising a date.
            </p>
          )}

          {d.blindSpots.length > 0 && (
            <div className="mb-4 space-y-1">
              {d.blindSpots.map((b, i) => (
                <p key={i} className="rounded border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-fg">
                  {b}
                </p>
              ))}
            </div>
          )}

          <section className="mb-8">
            <h2 className="mb-2 text-sm font-semibold text-fg">What is owed</h2>
            <div className="overflow-hidden rounded-lg border border-border bg-surface-2">
              <table className="min-w-full divide-y divide-border text-sm">
                <thead className="bg-surface-3 text-left text-xs text-fg-muted">
                  <tr>
                    <th className="px-4 py-2">Product</th>
                    <th className="px-4 py-2">Wanted by</th>
                    <th className="px-4 py-2 text-right">Owed</th>
                    <th className="px-4 py-2 text-right">Free stock</th>
                    <th className="px-4 py-2 text-right">Short</th>
                    <th className="px-4 py-2 text-right">LAA needed</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-border">
                  {d.demand.length === 0 && (
                    <EmptyRow
                      colSpan={6}
                      title="Nothing on order"
                      message="Which is not the same as nothing to make — it is a statement that nothing has been ordered."
                    />
                  )}
                  {d.demand.map((l) => (
                    <tr key={l.productId} className={l.late ? "bg-danger-bg" : undefined}>
                      <td className="px-4 py-2 text-fg">
                        {l.productName}
                        <span className="ml-2 text-xs text-fg-subtle">
                          {l.bottleSizeMl} mL · {l.bottleAbvPct} %
                        </span>
                      </td>
                      <td className="px-4 py-2 text-fg-muted">
                        {l.earliestRequired || <span className="text-fg-subtle">no date given</span>}
                        {l.late && <span className="ml-2 text-xs text-danger-fg">late</span>}
                      </td>
                      <td className="px-4 py-2 text-right text-fg-muted">{l.bottlesOwed}</td>
                      <td className="px-4 py-2 text-right text-fg-muted">{l.bottlesAvailable}</td>
                      <td className={`px-4 py-2 text-right font-medium ${l.shortfall > 0 ? "text-fg" : "text-fg-subtle"}`}>
                        {l.shortfall || "—"}
                      </td>
                      <td className="px-4 py-2 text-right text-fg-muted">
                        {l.shortfall > 0 ? formatLAA(l.shortfallLaa) : "—"}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </section>

          <section>
            <h2 className="mb-2 text-sm font-semibold text-fg">The plant</h2>
            <div className="grid gap-3 sm:grid-cols-2">
              {d.equipment.length === 0 && (
                <p className="text-sm text-fg-muted">
                  Nothing in the equipment register yet. Add the still and this can
                  say something about capacity.
                </p>
              )}
              {d.equipment.map((e) => (
                <div
                  key={e.id}
                  className={`rounded-lg border p-4 ${
                    e.plannable ? "border-border bg-surface-2" : "border-warning/40 bg-warning/5"
                  }`}
                >
                  <div className="flex items-baseline justify-between gap-2">
                    <span className="text-sm font-medium text-fg">{e.name}</span>
                    <span className="text-xs text-fg-muted">
                      {e.capacityLSet ? `${e.capacityL} L` : "capacity unknown"}
                    </span>
                  </div>
                  {e.plannable ? (
                    <p className="mt-1 text-xs text-fg-muted">
                      {e.observedRuns > 0 ? (
                        <>
                          {e.observedMedianHours.toFixed(1)} h a run, observed across{" "}
                          {e.observedRuns} finished work order
                          {e.observedRuns === 1 ? "" : "s"}
                        </>
                      ) : (
                        <>{e.typicalRunHours} h a run, as recorded — nothing observed yet</>
                      )}
                      {e.scheduled.length > 0 && (
                        <> · {e.scheduled.length} scheduled, about {e.scheduledHours.toFixed(1)} h</>
                      )}
                    </p>
                  ) : (
                    <p className="mt-1 text-xs text-warning-fg">
                      Cannot be planned against: {e.whyNot}.
                    </p>
                  )}
                  {e.scheduled.length > 0 && (
                    <ul className="mt-2 space-y-0.5 text-xs text-fg-subtle">
                      {e.scheduled.map((w) => (
                        <li key={w.workOrderId}>
                          {w.scheduledFor} · #{w.workOrderNo} {w.title}
                        </li>
                      ))}
                    </ul>
                  )}
                </div>
              ))}
            </div>
          </section>
        </>
      )}
      <ForecastPanel />
    </Shell>
  );
}
