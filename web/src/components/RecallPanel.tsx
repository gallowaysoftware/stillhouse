import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { Callout } from "@/components/Callout";
import { traceabilityClient } from "@/lib/clients";
import { RecallOrigin } from "@/gen/stillhouse/v1/traceability_pb";

/**
 * RecallPanel — forward from a material lot to everything that might
 * carry it and everyone who received it.
 *
 * The screen is built around one distinction and would be actively
 * dangerous without it. Everything up to the production gauge is exact:
 * recorded links, nothing inferred. Everything past it is possible
 * contact, because spirit gets blended and vatted and "which mash is in
 * this tank" stops being a fact anyone holds.
 *
 * Both errors here are expensive and they point opposite ways. Treat
 * possible contact as certainty and you destroy good stock; ignore it and
 * you leave bad stock on a shelf. So the two sets are rendered apart,
 * never summed, and the decision is left where it belongs.
 *
 * It simulates. Nothing is held, blocked or notified.
 */
export function RecallPanel({ lotId, lotLabel }: { lotId: string; lotLabel: string }) {
  const [open, setOpen] = useState(false);
  const r = useQuery({
    queryKey: ["simulateRecall", lotId],
    queryFn: () =>
      traceabilityClient.simulateRecall({
        origin: RecallOrigin.MATERIAL_LOT,
        originId: lotId,
      }),
    enabled: open,
  });

  if (!open) {
    return (
      <button
        onClick={() => setOpen(true)}
        className="rounded border border-border-strong px-2 py-1 text-xs hover:bg-surface-3"
      >
        Simulate recall
      </button>
    );
  }

  const d = r.data;
  return (
    <div className="mt-2 rounded border border-border bg-surface-2 p-3 text-sm">
      <div className="mb-2 flex items-center justify-between">
        <h4 className="font-semibold">Recall simulation — lot {lotLabel}</h4>
        <button onClick={() => setOpen(false)} className="text-xs text-fg-muted hover:text-fg">
          close
        </button>
      </div>

      {r.isLoading && <p className="text-fg-muted">Walking the chain…</p>}
      {r.error && (
        <Callout tone="danger" title="Could not run the simulation">
          {r.error instanceof ConnectError ? r.error.rawMessage : String(r.error)}
        </Callout>
      )}

      {d && (
        <>
          {d.note ? (
            <Callout tone="info" title="Nothing to recall">{d.note}</Callout>
          ) : (
            <>
              <div className="mb-3 grid gap-2 sm:grid-cols-3">
                <Stat label="Bottles possibly affected" value={String(d.bottlesPackaged)} />
                <Stat label="Still on hand" value={String(d.bottlesOnHand)} hint="can be held" />
                <Stat label="Already removed" value={String(d.bottlesRemoved)} hint="has to be chased" />
              </div>

              <Callout tone="warning" title="Exact to the gauge, possible contact after it">
                {d.exactnessNote}
              </Callout>

              <Section title="One up — where it came from">
                <p>
                  {d.materialName || "—"}
                  {d.supplierName && <> from <strong>{d.supplierName}</strong></>}
                  {d.supplierLot && <>, supplier lot <strong>{d.supplierLot}</strong></>}
                </p>
              </Section>

              <Section title={`Exact — mashes (${d.mashes.length}) and gauges (${d.gauges.length})`}>
                <ul className="list-disc space-y-1 pl-5">
                  {d.mashes.map((m) => (
                    <li key={m.mashRunId}>
                      Mash {m.mashNo} on {m.mashDate} — {m.quantityUsed} {m.uom}
                    </li>
                  ))}
                  {d.gauges.map((g) => (
                    <li key={g.productionGaugeId}>
                      Run {g.distillationRunNo} gauged {g.gaugeDate} into {g.containerName}
                      {g.voided && <span className="ml-1 text-fg-muted">(voided — not on any shelf)</span>}
                    </li>
                  ))}
                </ul>
              </Section>

              <Section title={`Possible contact — packaged lots (${d.packagedLots.length})`}>
                {d.packagedLots.length === 0 ? (
                  <p className="text-fg-muted">Nothing has been bottled from the affected spirit.</p>
                ) : (
                  <table className="w-full text-sm">
                    <thead className="text-left text-xs uppercase text-fg-muted">
                      <tr>
                        <th className="py-1 pr-3">Lot</th>
                        <th className="py-1 pr-3">Product</th>
                        <th className="py-1 pr-3">Bottled</th>
                        <th className="py-1 pr-3 text-right">Packaged</th>
                        <th className="py-1 pr-3 text-right">On hand</th>
                        <th className="py-1 text-right">Removed</th>
                      </tr>
                    </thead>
                    <tbody>
                      {d.packagedLots.map((l) => (
                        <tr key={l.packagedInventoryId} className="border-t border-border">
                          <td className="py-1 pr-3 font-medium">{l.lotCode}</td>
                          <td className="py-1 pr-3">{l.productName}</td>
                          <td className="py-1 pr-3 tabular-nums">{l.bottledOn}</td>
                          <td className="py-1 pr-3 text-right tabular-nums">{l.bottlesPackaged}</td>
                          <td className="py-1 pr-3 text-right tabular-nums">{l.bottlesOnHand}</td>
                          <td className="py-1 text-right tabular-nums">{l.bottlesRemoved}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                )}
              </Section>

              <Section title={`One down — who received it (${d.removals.length})`}>
                {d.removals.length === 0 ? (
                  <p className="text-fg-muted">None of the affected stock has left.</p>
                ) : (
                  <table className="w-full text-sm">
                    <thead className="text-left text-xs uppercase text-fg-muted">
                      <tr>
                        <th className="py-1 pr-3">Date</th>
                        <th className="py-1 pr-3">Lot</th>
                        <th className="py-1 pr-3">Received by</th>
                        <th className="py-1 text-right">Bottles</th>
                      </tr>
                    </thead>
                    <tbody>
                      {d.removals.map((rm) => (
                        <tr key={rm.id} className="border-t border-border">
                          <td className="py-1 pr-3 tabular-nums">{rm.removalDate}</td>
                          <td className="py-1 pr-3">{rm.lotCode}</td>
                          <td className="py-1 pr-3">
                            {rm.customerName || rm.destinationName || "—"}
                            {rm.voided && <span className="ml-1 text-fg-muted">(voided)</span>}
                          </td>
                          <td className="py-1 text-right tabular-nums">{rm.bottles}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                )}
                {d.voidedRemovals > 0 && (
                  <p className="mt-2 text-xs text-fg-muted">
                    {d.voidedRemovals} removal{d.voidedRemovals === 1 ? "" : "s"} in this list
                    {d.voidedRemovals === 1 ? " was" : " were"} voided — the stock did not leave, but a
                    voided removal is not the same as one that never happened.
                  </p>
                )}
              </Section>

              <p className="mt-3 text-xs text-fg-muted">
                This is a simulation. Nothing has been held, blocked or notified.
              </p>
            </>
          )}
        </>
      )}
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="mt-3">
      <h5 className="mb-1 text-xs font-semibold uppercase text-fg-muted">{title}</h5>
      {children}
    </div>
  );
}

function Stat({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <div className="rounded border border-border p-2">
      <div className="text-xs uppercase text-fg-muted">{label}</div>
      <div className="text-lg font-semibold tabular-nums">{value}</div>
      {hint && <div className="text-xs text-fg-muted">{hint}</div>}
    </div>
  );
}
