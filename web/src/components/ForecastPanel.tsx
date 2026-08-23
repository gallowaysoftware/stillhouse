import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { Callout } from "@/components/Callout";
import { schedulingClient } from "@/lib/clients";
import { ForecastMethod } from "@/gen/stillhouse/v1/scheduling_pb";
import { OwnerOnly } from "@/lib/role";

/**
 * ForecastPanel — projected demand, beside the committed orders and never
 * added to them.
 *
 * Stage 185 built the production plan from actual demand and says so on
 * the page every time, because a plan built on an invented forecast looks
 * exactly as authoritative as one built on orders. This panel keeps that
 * true: two columns, never one, because a single number combining twelve
 * bottles somebody has paid for with forty somebody might buy cannot be
 * taken apart again.
 */
const methods: [ForecastMethod, string, string][] = [
  [
    ForecastMethod.TRAILING_AVERAGE,
    "Trailing average",
    "The mean of the last few complete months. Good where sales are steady, wrong where they are seasonal.",
  ],
  [
    ForecastMethod.SAME_PERIOD_LAST_YEAR,
    "Same month last year",
    "Good where sales are seasonal, wrong in a first year and wrong after a step change.",
  ],
  [
    ForecastMethod.MANUAL,
    "My own numbers",
    "The only method that can be right when there is a listing decision or a festival in the diary.",
  ],
];

function errText(e: unknown): string {
  return e instanceof ConnectError ? e.rawMessage : String(e);
}

export function ForecastPanel() {
  const qc = useQueryClient();
  const [err, setErr] = useState<string | null>(null);
  const f = useQuery({
    queryKey: ["demandForecast"],
    queryFn: () => schedulingClient.demandForecast({}),
  });

  const setMethod = useMutation({
    mutationFn: (m: ForecastMethod) =>
      schedulingClient.setForecastMethod({ method: m, trailingMonths: 3 }),
    onSuccess: () => {
      setErr(null);
      void qc.invalidateQueries({ queryKey: ["demandForecast"] });
    },
    onError: (e) => setErr(errText(e)),
  });

  const save = useMutation({
    mutationFn: (v: { productId: string; bottles: number; reason: string }) =>
      schedulingClient.saveDemandForecast({ ...v, month: d?.periodStart ?? "" }),
    onSuccess: () => {
      setErr(null);
      void qc.invalidateQueries({ queryKey: ["demandForecast"] });
    },
    onError: (e) => setErr(errText(e)),
  });

  const d = f.data;

  return (
    <section className="mt-8">
      <h2 className="mb-1 text-sm font-semibold text-fg-muted">
        Forecast{d && !d.refused && <> — {d.periodStart} to {d.periodEnd}</>}
      </h2>

      <OwnerOnly>
        <div className="mb-3 flex flex-wrap gap-2">
          {methods.map(([m, label, why]) => (
            <button
              key={m}
              title={why}
              onClick={() => setMethod.mutate(m)}
              className={`rounded border px-3 py-1 text-sm ${
                d?.method === m
                  ? "border-accent bg-accent/10 text-fg"
                  : "border-border text-fg-muted hover:text-fg"
              }`}
            >
              {label}
            </button>
          ))}
          {d?.method !== ForecastMethod.UNSPECIFIED && (
            <button
              onClick={() => setMethod.mutate(ForecastMethod.UNSPECIFIED)}
              className="rounded border border-border px-3 py-1 text-sm text-fg-muted hover:text-fg"
            >
              None
            </button>
          )}
        </div>
      </OwnerOnly>

      {err && <Callout tone="danger" title="That failed">{err}</Callout>}

      {d?.refused ? (
        <Callout tone="info" title="No forecast method chosen">
          {d.refused}
        </Callout>
      ) : d ? (
        <>
          <Callout tone="warning" title="Projected, not committed">
            {d.caution}
          </Callout>

          <div className="mt-3 overflow-x-auto rounded-lg border border-border bg-surface-2">
            <table className="min-w-full divide-y divide-border text-sm">
              <thead className="bg-surface-3 text-left text-xs text-fg-muted">
                <tr>
                  <th className="px-4 py-2">Product</th>
                  <th className="px-4 py-2 text-right">Committed</th>
                  <th className="px-4 py-2 text-right">Forecast</th>
                  <th className="px-4 py-2 text-right">On hand</th>
                  <th className="px-4 py-2">Basis</th>
                  <th className="px-4 py-2"></th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {d.lines.length === 0 && (
                  <tr><td colSpan={6} className="px-4 py-3 text-fg-muted">
                    Nothing ordered and nothing sold yet, so there is nothing to project.
                  </td></tr>
                )}
                {d.lines.map((l) => (
                  <tr key={l.productId}>
                    <td className="px-4 py-2">{l.productName}</td>
                    <td className="px-4 py-2 text-right font-medium tabular-nums">
                      {l.bottlesCommitted}
                    </td>
                    <td className="px-4 py-2 text-right tabular-nums">
                      {l.available ? (
                        <>
                          {l.bottlesForecast}
                          {l.overridden && (
                            <span className="ml-1 text-xs text-fg-muted">(entered)</span>
                          )}
                        </>
                      ) : (
                        <span className="text-fg-muted">—</span>
                      )}
                    </td>
                    <td className="px-4 py-2 text-right tabular-nums">{l.bottlesOnHand}</td>
                    <td className="px-4 py-2 text-xs text-fg-muted">
                      {l.available ? l.basis || l.overrideReason : l.missing}
                    </td>
                    <td className="px-4 py-2 text-right">
                      <OwnerOnly>
                        <button
                          onClick={() => {
                            const n = window.prompt(`Your own figure for ${l.productName}`);
                            if (n === null) return;
                            const reason = window.prompt(
                              "Why this number rather than the computed one?",
                            );
                            if (!reason) return;
                            save.mutate({
                              productId: l.productId,
                              bottles: Number(n),
                              reason,
                            });
                          }}
                          className="text-xs underline"
                        >
                          override
                        </button>
                      </OwnerOnly>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      ) : null}
    </section>
  );
}
