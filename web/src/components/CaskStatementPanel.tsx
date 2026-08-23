import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { Callout } from "@/components/Callout";
import { barrelClient } from "@/lib/clients";
import { formatCAD, formatLAA } from "@/lib/format";

/**
 * CaskStatementPanel — what a cask owner is entitled to know.
 *
 * Every figure here is a recorded gauge or arithmetic over recorded
 * gauges. Where one cannot be computed from what was actually written
 * down — a fill with no strength, a duty rate that cannot be cited for
 * today — it says so in place of the number.
 *
 * That matters more here than anywhere else in the app. This document
 * leaves the building, gets kept, and is read by somebody with no way to
 * check it, so a plausible-looking invented figure would never be caught.
 */
export function CaskStatementPanel({ containerId }: { containerId: string }) {
  const [open, setOpen] = useState(false);
  const s = useQuery({
    queryKey: ["caskStatement", containerId],
    queryFn: () => barrelClient.caskStatement({ containerId }),
    enabled: open,
  });

  if (!open) {
    return (
      <button
        onClick={() => setOpen(true)}
        className="rounded border border-border-strong px-3 py-2 text-sm hover:bg-surface-3"
      >
        Owner's statement
      </button>
    );
  }

  const d = s.data;
  return (
    <section className="rounded-lg border border-border bg-surface-2 p-4">
      <div className="mb-3 flex items-start justify-between">
        <div>
          <h2 className="text-sm font-semibold">Cask statement</h2>
          {d && (
            <p className="text-xs text-fg-muted">
              {d.ownerName ? <>for {d.ownerName}</> : "this distillery's own cask"} · as at{" "}
              {d.generatedAt}
            </p>
          )}
        </div>
        <button onClick={() => setOpen(false)} className="text-xs text-fg-muted hover:text-fg">
          close
        </button>
      </div>

      {s.isLoading && <p className="text-sm text-fg-muted">Loading…</p>}
      {s.error && (
        <Callout tone="danger" title="Could not build the statement">
          {s.error instanceof ConnectError ? s.error.rawMessage : String(s.error)}
        </Callout>
      )}

      {d && (
        <>
          <div className="mb-4 grid gap-3 sm:grid-cols-2">
            <Field label="Cask" value={d.caskName} />
            <Field label="Filled" value={d.fillDate || "—"} />
            <Field
              label="Wood"
              value={[d.woodSpecies, d.priorUse, d.charLevel ? `char ${d.charLevel}` : ""]
                .filter(Boolean)
                .join(" · ") || "—"}
            />
            <Field label="Cooperage" value={d.cooperageSupplier || "—"} />
            <Field label="Position" value={d.rickhouse || "—"} />
            <Field label="Days in wood" value={d.daysInWood ? String(d.daysInWood) : "—"} />
          </div>

          <table className="mb-4 w-full text-sm">
            <thead className="text-left text-xs uppercase text-fg-muted">
              <tr>
                <th className="py-1"></th>
                <th className="py-1 text-right">Volume</th>
                <th className="py-1 text-right">Strength</th>
                <th className="py-1 text-right">LAA</th>
              </tr>
            </thead>
            <tbody>
              <tr className="border-t border-border">
                <td className="py-1">At fill</td>
                <td className="py-1 text-right tabular-nums">{d.filledVolumeL ? `${d.filledVolumeL} L` : "—"}</td>
                <td className="py-1 text-right tabular-nums">{d.filledAbvPct ? `${d.filledAbvPct}%` : "—"}</td>
                <td className="py-1 text-right tabular-nums">{d.filledLaa ? formatLAA(d.filledLaa) : "—"}</td>
              </tr>
              <tr className="border-t border-border font-medium">
                <td className="py-1">Today</td>
                <td className="py-1 text-right tabular-nums">{d.currentVolumeL} L</td>
                <td className="py-1 text-right tabular-nums">{d.currentAbvPct}%</td>
                <td className="py-1 text-right tabular-nums">{formatLAA(d.currentLaa)}</td>
              </tr>
            </tbody>
          </table>

          <div className="mb-4">
            <h3 className="mb-1 text-xs font-semibold uppercase text-fg-muted">Angel's share</h3>
            {d.angelsShareKnown ? (
              <p className="text-sm">
                <strong>{formatLAA(d.angelsShareLaa)}</strong> lost since fill —{" "}
                {d.angelsSharePctPerYear.toFixed(2)}% of the original alcohol per year.
              </p>
            ) : (
              <p className="text-sm text-fg-muted">Not available: {d.angelsShareMissing}</p>
            )}
          </div>

          <div className="mb-4">
            <h3 className="mb-1 text-xs font-semibold uppercase text-fg-muted">
              Duty if bottled today
            </h3>
            {d.dutyKnown ? (
              <p className="text-sm">
                <strong>{formatCAD(d.dutyIfBottledTodayCad)}</strong> at{" "}
                {formatCAD(d.dutyRatePerLaa)}/LAA. An estimate at today's rate on today's
                contents — both move.
              </p>
            ) : (
              <p className="text-sm text-fg-muted">Not available: {d.dutyMissing}</p>
            )}
          </div>

          <h3 className="mb-1 text-xs font-semibold uppercase text-fg-muted">Gauges</h3>
          <table className="w-full text-sm">
            <thead className="text-left text-xs uppercase text-fg-muted">
              <tr>
                <th className="py-1">Date</th>
                <th className="py-1">Kind</th>
                <th className="py-1 text-right">Volume</th>
                <th className="py-1 text-right">Strength</th>
                <th className="py-1 text-right">LAA</th>
                <th className="py-1">Gauged by</th>
              </tr>
            </thead>
            <tbody>
              {d.gauges.length === 0 && (
                <tr><td colSpan={6} className="py-2 text-fg-muted">No gauges recorded.</td></tr>
              )}
              {d.gauges.map((g, i) => (
                <tr key={i} className="border-t border-border">
                  <td className="py-1 tabular-nums">{g.date}</td>
                  <td className="py-1">{g.kind}</td>
                  <td className="py-1 text-right tabular-nums">{g.volumeL || "—"}</td>
                  <td className="py-1 text-right tabular-nums">{g.abvPct || "—"}</td>
                  <td className="py-1 text-right tabular-nums">{g.laa ? formatLAA(g.laa) : "—"}</td>
                  <td className="py-1 text-fg-muted">{g.gaugedBy || "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>

          <p className="mt-4 border-t border-border pt-3 text-xs text-fg-muted">{d.basis}</p>
        </>
      )}
    </section>
  );
}

function Field({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-xs uppercase text-fg-muted">{label}</div>
      <div className="text-sm">{value}</div>
    </div>
  );
}
