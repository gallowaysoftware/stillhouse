import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { Button } from "@/components/Button";
import { Callout } from "@/components/Callout";
import { alcoholometryClient } from "@/lib/clients";
import { formatLAA, formatQty } from "@/lib/format";

/**
 * BlendPlanner — what comes out when parcels are vatted together.
 *
 * Two things a spreadsheet gets wrong here, both for the same reason:
 * ethanol and water contract on mixing, so parcels occupy less together
 * than apart. The blend's volume is therefore not the sum of its sources,
 * and its strength is not their volume-weighted mean — the alcohol ends
 * up concentrated into slightly less liquid.
 *
 * The arithmetic runs server-side against the same CRA tables the ledger
 * uses, so a plan can't disagree with what a gauge will later say.
 */

type Row = { containerId: string; volumeL: string; strengthPct: string };

export function BlendPlanner({
  containers,
}: {
  containers: { id: string; name: string; currentVolumeL: number; currentAbvPct: number; currentAbvPctSet: boolean }[];
}) {
  const [rows, setRows] = useState<Row[]>([
    { containerId: "", volumeL: "", strengthPct: "" },
    { containerId: "", volumeL: "", strengthPct: "" },
  ]);
  const [target, setTarget] = useState("");

  const plan = useMutation({
    mutationFn: () =>
      alcoholometryClient.planBlend({
        sources: rows
          .filter((r) => r.volumeL.trim() !== "" && r.strengthPct.trim() !== "")
          .map((r) => ({
            label: containers.find((c) => c.id === r.containerId)?.name ?? "",
            volumeL: Number(r.volumeL) || 0,
            strengthPct: Number(r.strengthPct) || 0,
          })),
        targetStrengthPct: target.trim() === "" ? 0 : Number(target) || 0,
      }),
  });

  // Picking a vessel fills in what it currently holds — the usual case is
  // vatting whole casks.
  function pick(i: number, containerId: string) {
    const c = containers.find((x) => x.id === containerId);
    setRows((rs) =>
      rs.map((r, idx) =>
        idx === i
          ? {
              containerId,
              volumeL: c ? String(c.currentVolumeL) : r.volumeL,
              strengthPct: c?.currentAbvPctSet ? c.currentAbvPct.toFixed(2) : r.strengthPct,
            }
          : r,
      ),
    );
  }

  const usable = rows.filter((r) => r.volumeL.trim() !== "" && r.strengthPct.trim() !== "").length;

  return (
    <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
      <header className="border-b border-border bg-surface-3 px-4 py-3">
        <h2 className="text-sm font-semibold text-fg-muted">Plan a vatting</h2>
      </header>

      <div className="space-y-3 p-4">
        {rows.map((r, i) => (
          <div key={i} className="grid grid-cols-[1fr_5rem_5rem_2rem] items-end gap-2">
            <div>
              {i === 0 && <label className="mb-1 block text-xs text-fg-muted">Parcel</label>}
              <select
                value={r.containerId}
                onChange={(e) => pick(i, e.target.value)}
                className="w-full rounded border border-border-strong px-2 py-2 text-sm"
              >
                <option value="">— pick a vessel or type below —</option>
                {containers
                  .filter((c) => c.currentVolumeL > 0)
                  .map((c) => (
                    <option key={c.id} value={c.id}>
                      {c.name} ({formatQty(c.currentVolumeL)} L
                      {c.currentAbvPctSet ? ` @ ${c.currentAbvPct.toFixed(1)}%` : ""})
                    </option>
                  ))}
              </select>
            </div>
            <Num
              label={i === 0 ? "Litres" : undefined}
              value={r.volumeL}
              onChange={(v) => setRows((rs) => rs.map((x, idx) => (idx === i ? { ...x, volumeL: v } : x)))}
            />
            <Num
              label={i === 0 ? "%" : undefined}
              value={r.strengthPct}
              onChange={(v) => setRows((rs) => rs.map((x, idx) => (idx === i ? { ...x, strengthPct: v } : x)))}
            />
            <button
              type="button"
              onClick={() => setRows((rs) => (rs.length <= 2 ? rs : rs.filter((_, idx) => idx !== i)))}
              disabled={rows.length <= 2}
              className="rounded px-2 py-2 text-xs text-fg-muted hover:text-danger-fg disabled:opacity-30"
              aria-label="Remove parcel"
            >
              ✕
            </button>
          </div>
        ))}

        <div className="flex flex-wrap items-end gap-3">
          <Button
            variant="secondary"
            size="sm"
            type="button"
            onClick={() => setRows((rs) => [...rs, { containerId: "", volumeL: "", strengthPct: "" }])}
          >
            Add parcel
          </Button>
          <Num label="Reduce to % (optional)" value={target} onChange={setTarget} wide />
          <Button type="button" onClick={() => plan.mutate()} disabled={usable < 2 || plan.isPending}>
            {plan.isPending ? "Calculating…" : "Plan"}
          </Button>
        </div>

        {plan.error && (
          <Callout tone="danger">
            {plan.error instanceof ConnectError ? plan.error.rawMessage : String(plan.error)}
          </Callout>
        )}

        {plan.data && (
          <div className="space-y-3">
            <div className="rounded-md border border-border bg-surface-3/60 p-3">
              <div className="flex items-baseline justify-between">
                <span className="text-xs font-medium text-fg-muted">The vatting</span>
                <span className="text-2xl font-bold tabular-nums text-fg">
                  {plan.data.blendStrengthPct.toFixed(2)}
                  <span className="ml-1 text-sm font-normal text-fg-muted">%</span>
                </span>
              </div>
              <dl className="mt-2 space-y-1 text-sm tabular-nums">
                <Row k="Volume">{formatQty(plan.data.blendVolumeL)} L</Row>
                <Row k="Weight">{formatQty(plan.data.totalMassKg)} kg</Row>
                <Row k="LAA">
                  <span className="font-semibold text-fg">{formatLAA(plan.data.totalLaa)} L</span>
                </Row>
              </dl>
            </div>

            {/* Contraction is maximal when mixing very different strengths
                and negligible when the parcels are similar; below a tenth
                of a litre it's interpolation noise, not physics. */}
            {plan.data.contractionL > 0.1 && (
              <Callout tone="info">
                Adding the parcels up gives {formatQty(plan.data.naiveVolumeL)} L, but the blend is{" "}
                {formatQty(plan.data.blendVolumeL)} L — {formatQty(plan.data.contractionL)} L less.
                Ethanol and water contract on mixing, which also puts the strength slightly above the
                volume-weighted average of the parts.
              </Callout>
            )}

            {plan.data.reductionSet && (
              <div className="rounded-md border border-success/30 bg-success/10 p-3">
                <p className="text-xs font-medium text-success-fg">Then reduce</p>
                <div className="mt-1 grid grid-cols-2 gap-3">
                  <div>
                    <p className="text-2xl font-bold tabular-nums text-success-fg">
                      {formatQty(plan.data.waterToAddKg)} kg
                    </p>
                    <p className="text-[11px] text-fg-muted">water by weight — exact</p>
                  </div>
                  <div>
                    <p className="text-xl font-semibold tabular-nums text-fg">
                      {formatQty(plan.data.waterToAddL)} L
                    </p>
                    <p className="text-[11px] text-fg-muted">
                      by volume · fill to {formatQty(plan.data.finalVolumeL)} L
                    </p>
                  </div>
                </div>
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

function Row({ k, children }: { k: string; children: React.ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-3">
      <dt className="text-xs text-fg-muted">{k}</dt>
      <dd className="text-fg-muted">{children}</dd>
    </div>
  );
}

function Num({
  label,
  value,
  onChange,
  wide,
}: {
  label?: string;
  value: string;
  onChange: (v: string) => void;
  wide?: boolean;
}) {
  return (
    <div>
      {label && <label className="mb-1 block text-xs text-fg-muted">{label}</label>}
      <input
        type="number"
        step="0.01"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className={`rounded border border-border-strong px-2 py-2 text-sm tabular-nums ${wide ? "w-40" : "w-full"}`}
      />
    </div>
  );
}
