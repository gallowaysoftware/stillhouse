import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { Callout } from "@/components/Callout";
import { EmptyRow } from "@/components/EmptyState";
import { costingClient } from "@/lib/clients";
import { WIPChargeBasis } from "@/gen/stillhouse/v1/costing_pb";
import { formatCAD, formatLAA } from "@/lib/format";
import { OwnerOnly } from "@/lib/role";

/**
 * WIPProductionTab — spirit gauged into work in progress, valued by
 * walking forward from the mashes behind it.
 *
 * The interesting part of this screen is the state it is in before the
 * licensee has stated a charge basis. It refuses, by name, and says why:
 * apportioning a fermentation's cost across the stills it fed is an
 * accounting policy, and a figure produced on a convention nobody chose
 * would reconcile and never be questioned. That refusal is the feature,
 * not an unfinished state to be tidied away with a default.
 */
export function WIPProductionTab() {
  const today = new Date();
  const first = new Date(today.getFullYear(), today.getMonth(), 1);
  const [start, setStart] = useState(iso(first));
  const [end, setEnd] = useState(iso(today));

  const basis = useQuery({
    queryKey: ["wipChargeBasis"],
    queryFn: () => costingClient.getWIPChargeBasis({}),
  });
  const wip = useQuery({
    queryKey: ["wipProduction", start, end],
    queryFn: () => costingClient.wIPProduction({ periodStart: start, periodEnd: end }),
  });

  return (
    <div className="space-y-4">
      <BasisPicker current={basis.data?.basis ?? WIPChargeBasis.WIP_CHARGE_BASIS_UNSPECIFIED} />

      <div className="flex flex-wrap items-end gap-3">
        <label className="text-sm">
          <span className="mb-1 block text-fg-muted">From</span>
          <input type="date" value={start} onChange={(e) => setStart(e.target.value)}
                 className="rounded border border-border bg-bg px-2 py-1" />
        </label>
        <label className="text-sm">
          <span className="mb-1 block text-fg-muted">To</span>
          <input type="date" value={end} onChange={(e) => setEnd(e.target.value)}
                 className="rounded border border-border bg-bg px-2 py-1" />
        </label>
      </div>

      {wip.isLoading && <p className="text-sm text-fg-muted">Loading…</p>}
      {wip.error && <Callout tone="danger" title="Could not value production">
        {errText(wip.error)}
      </Callout>}

      {wip.data?.refused ? (
        <Callout tone="warning" title="No charge basis set">
          <p>{wip.data.refused}</p>
        </Callout>
      ) : wip.data ? (
        <>
          <div className="grid gap-3 sm:grid-cols-3">
            <Stat label="Valued into WIP" value={formatCAD(wip.data.totalCad)} />
            <Stat label="Gauges valued"
                  value={`${wip.data.valuedCount} of ${wip.data.gauges.length}`} />
            <Stat label="LAA valued"
                  value={`${formatLAA(wip.data.valuedLaa)} of ${formatLAA(wip.data.totalLaa)}`} />
          </div>

          {!wip.data.complete && wip.data.gauges.length > 0 && (
            <Callout tone="warning" title="Some gauges could not be valued">
              The total above is over the gauges that could be. It is not the
              period's work in progress, and the counts beside it are there so
              the difference is visible rather than assumed away.
            </Callout>
          )}

          <div className="overflow-x-auto rounded border border-border">
            <table className="w-full text-sm">
              <thead className="bg-bg-subtle text-left text-xs uppercase text-fg-muted">
                <tr>
                  <th className="px-3 py-2 font-medium">Gauged</th>
                  <th className="px-3 py-2 font-medium">Into</th>
                  <th className="px-3 py-2 text-right font-medium">LAA</th>
                  <th className="px-3 py-2 text-right font-medium">Charges</th>
                  <th className="px-3 py-2 text-right font-medium">Value</th>
                </tr>
              </thead>
              <tbody>
                {wip.data.gauges.length === 0 && (
                  <EmptyRow colSpan={5} title="No spirit gauged into bulk in this period." />
                )}
                {wip.data.gauges.map((g) => (
                  <tr key={g.id} className="border-t border-border align-top">
                    <td className="px-3 py-2 tabular-nums">{g.gaugeDate}</td>
                    <td className="px-3 py-2">{g.containerName}</td>
                    <td className="px-3 py-2 text-right tabular-nums">{formatLAA(g.laa)}</td>
                    <td className="px-3 py-2 text-right tabular-nums">{g.chargeCount}</td>
                    <td className="px-3 py-2 text-right">
                      {g.available ? (
                        <span className="tabular-nums">{formatCAD(g.amountCad)}</span>
                      ) : (
                        <span className="text-fg-muted">
                          <span className="font-medium">unvalued</span>
                          <span className="mt-1 block max-w-md text-left text-xs">{g.missing}</span>
                        </span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      ) : null}
    </div>
  );
}

function BasisPicker({ current }: { current: WIPChargeBasis }) {
  const qc = useQueryClient();
  const [err, setErr] = useState<string | null>(null);
  const save = useMutation({
    mutationFn: (basis: WIPChargeBasis) => costingClient.setWIPChargeBasis({ basis }),
    onSuccess: () => {
      setErr(null);
      void qc.invalidateQueries({ queryKey: ["wipChargeBasis"] });
      void qc.invalidateQueries({ queryKey: ["wipProduction"] });
    },
    onError: (e) => setErr(errText(e)),
  });

  return (
    <div className="rounded border border-border p-3">
      <h3 className="text-sm font-semibold">Charge basis</h3>
      <p className="mt-1 text-sm text-fg-muted">
        How a fermentation's cost follows the stills it was charged to. A
        low-wines run and a spirit run drawing the same litres do not carry
        the same alcohol, so which of these is right is your accounting
        policy. Stillhouse refuses to value production until you say.
      </p>
      <OwnerOnly>
        <div className="mt-2 flex flex-wrap gap-2">
          {([
            [WIPChargeBasis.WIP_CHARGE_BASIS_CHARGED_VOLUME, "Litres charged"],
            [WIPChargeBasis.WIP_CHARGE_BASIS_CHARGED_LAA, "LAA charged"],
            [WIPChargeBasis.WIP_CHARGE_BASIS_UNSPECIFIED, "Not stated"],
          ] as const).map(([v, label]) => (
            <button
              key={v}
              onClick={() => save.mutate(v)}
              disabled={save.isPending}
              className={`rounded border px-3 py-1 text-sm ${
                current === v ? "border-accent bg-accent/10 text-fg" : "border-border text-fg-muted hover:text-fg"
              }`}
            >
              {label}
            </button>
          ))}
        </div>
      </OwnerOnly>
      {err && <p className="mt-2 text-sm text-danger">{err}</p>}
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded border border-border p-3">
      <div className="text-xs uppercase text-fg-muted">{label}</div>
      <div className="mt-1 text-lg font-semibold tabular-nums">{value}</div>
    </div>
  );
}

function iso(d: Date): string {
  return d.toISOString().slice(0, 10);
}

function errText(e: unknown): string {
  return e instanceof ConnectError ? e.rawMessage : String(e);
}
