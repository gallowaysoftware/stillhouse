import { useState } from "react";
import { useQuery } from "@tanstack/react-query";

import { Callout } from "@/components/Callout";
import { provincialClient } from "@/lib/clients";
import { formatCAD } from "@/lib/format";

/**
 * DepositReportPanel — containers into each market, and the deposit they
 * imply.
 *
 * The distinction this screen exists to preserve is between a count
 * Stillhouse is sure of and a rate it is not. The counts come from
 * removals. The rates come from `internal/pricing`, which grades every
 * one of them, and a rate marked indicative produces a planning figure
 * rather than a remittance — quoting an aggregator's number to a
 * stewardship programme is the same kind of mistake as quoting an uncited
 * excise rate to CRA.
 *
 * So the total is shown with what it is, and one indicative line is
 * enough to make the whole thing a plan: a total nobody decomposes is
 * only as good as its worst line.
 */
export function DepositReportPanel() {
  const now = new Date();
  const [from, setFrom] = useState(
    new Date(now.getFullYear(), now.getMonth(), 1).toISOString().slice(0, 10),
  );
  const [to, setTo] = useState(
    new Date(now.getFullYear(), now.getMonth() + 1, 0).toISOString().slice(0, 10),
  );
  const r = useQuery({
    queryKey: ["containerDeposits", from, to],
    queryFn: () =>
      provincialClient.containerDepositReport({ periodStart: from, periodEnd: to }),
  });
  const d = r.data;

  return (
    <section className="mt-8">
      <h2 className="mb-1 text-sm font-semibold text-fg-muted">Container deposits</h2>
      <p className="mb-3 text-sm text-fg-muted">
        Containers that entered each market during the period, and the deposit
        owed on them.
      </p>

      <div className="mb-3 flex flex-wrap items-end gap-3">
        <label className="text-sm">
          <span className="mb-1 block text-fg-muted">From</span>
          <input type="date" value={from} onChange={(e) => setFrom(e.target.value)}
                 className="rounded border border-border-strong px-2 py-1" />
        </label>
        <label className="text-sm">
          <span className="mb-1 block text-fg-muted">To</span>
          <input type="date" value={to} onChange={(e) => setTo(e.target.value)}
                 className="rounded border border-border-strong px-2 py-1" />
        </label>
      </div>

      {d && (
        <>
          {d.remittable ? (
            <Callout tone="success" title="Every rate on this report is sourced">
              {formatCAD(d.totalDepositCad)} over {d.totalContainersNet} containers.
            </Callout>
          ) : (
            <Callout tone="warning" title="A planning figure, not a remittance">
              <p>
                {formatCAD(d.totalDepositCad)} over {d.totalContainersNet} containers —
                but {d.needsASourcedRate.join(", ") || "one or more jurisdictions"} still
                need a rate from the programme's own material before this is a number to
                pay against.
              </p>
              <p className="mt-2 text-xs">{d.caution}</p>
            </Callout>
          )}

          <div className="mt-3 overflow-x-auto rounded-lg border border-border bg-surface-2">
            <table className="min-w-full divide-y divide-border text-sm">
              <thead className="bg-surface-3 text-left text-xs text-fg-muted">
                <tr>
                  <th className="px-4 py-2">Market</th>
                  <th className="px-4 py-2 text-right">Size</th>
                  <th className="px-4 py-2 text-right">Out</th>
                  <th className="px-4 py-2 text-right">Back</th>
                  <th className="px-4 py-2 text-right">Net</th>
                  <th className="px-4 py-2 text-right">Rate</th>
                  <th className="px-4 py-2 text-right">Deposit</th>
                  <th className="px-4 py-2">Rate came from</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {d.lines.length === 0 && (
                  <tr><td colSpan={8} className="px-4 py-3 text-fg-muted">
                    Nothing entered a market in this period.
                  </td></tr>
                )}
                {d.lines.map((l, i) => (
                  <tr key={i}>
                    <td className="px-4 py-2">{l.jurisdiction}</td>
                    <td className="px-4 py-2 text-right tabular-nums">{l.bottleSizeMl} mL</td>
                    <td className="px-4 py-2 text-right tabular-nums">{l.containersOut}</td>
                    <td className="px-4 py-2 text-right tabular-nums">{l.containersReturned}</td>
                    <td className="px-4 py-2 text-right font-medium tabular-nums">{l.containersNet}</td>
                    <td className="px-4 py-2 text-right tabular-nums">
                      {l.amountAvailable ? formatCAD(l.depositPerContainerCad) : "—"}
                    </td>
                    <td className="px-4 py-2 text-right tabular-nums">
                      {l.amountAvailable ? formatCAD(l.depositTotalCad) : "—"}
                    </td>
                    <td className="px-4 py-2 text-xs text-fg-muted">
                      {l.amountAvailable ? (
                        <>
                          <span className={l.rateProvenance === "sourced" ? "" : "text-warning-fg"}>
                            {l.rateProvenance}
                          </span>
                          {l.rateSource && <span className="block">{l.rateSource}</span>}
                          {l.rateNote && <span className="block">{l.rateNote}</span>}
                        </>
                      ) : (
                        l.amountMissing
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
    </section>
  );
}
