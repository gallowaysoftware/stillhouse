import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { Button } from "@/components/Button";
import { Callout } from "@/components/Callout";
import { alcoholometryClient } from "@/lib/clients";
import { formatLAA, formatQty } from "@/lib/format";

/**
 * ReductionCalculator — proofing down to bottling strength.
 *
 * Two things this does that a phone calculator doesn't:
 *
 * 1. The water figure accounts for volume contraction. Ethanol and water
 *    hydrogen-bond on mixing and the blend holds less than its parts did
 *    apart, so (final volume − starting volume) understates the water
 *    needed by 1–2 %. On a 1,000 L reduction that's ~17 L.
 *
 * 2. It gives the plan by weight, which has no such problem — mass is
 *    strictly additive. If the vessel is on a scale, those are the
 *    numbers to work to.
 *
 * The arithmetic runs server-side against the same CRA tables the rest of
 * the ledger uses, so a plan can't disagree with what a gauge would say.
 */

type Mode = "volume" | "mass";

export function ReductionCalculator({
  defaultFromStrength = "",
  defaultFromVolumeL = "",
  title = "Reduce to strength",
}: {
  defaultFromStrength?: string;
  defaultFromVolumeL?: string;
  title?: string;
}) {
  const [mode, setMode] = useState<Mode>("volume");
  const [volume, setVolume] = useState(defaultFromVolumeL);
  const [mass, setMass] = useState("");
  const [from, setFrom] = useState(defaultFromStrength);
  const [to, setTo] = useState("40");

  const plan = useMutation({
    mutationFn: () =>
      alcoholometryClient.planReduction({
        fromVolumeL: mode === "volume" ? Number(volume) || 0 : 0,
        fromMassKg: mode === "mass" ? Number(mass) || 0 : 0,
        fromMassKgSet: mode === "mass",
        fromStrengthPct: Number(from) || 0,
        toStrengthPct: Number(to) || 0,
      }),
  });

  const ready =
    (mode === "volume" ? volume.trim() !== "" : mass.trim() !== "") &&
    from.trim() !== "" &&
    to.trim() !== "";

  return (
    <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
      <header className="flex items-center justify-between border-b border-border bg-surface-3 px-4 py-3">
        <h2 className="text-sm font-semibold text-fg-muted">{title}</h2>
        <div className="flex overflow-hidden rounded border border-border-strong text-[11px]">
          {(
            [
              ["volume", "By volume"],
              ["mass", "By weight"],
            ] as const
          ).map(([m, label]) => (
            <button
              key={m}
              type="button"
              onClick={() => setMode(m)}
              className={`px-2 py-0.5 transition-colors ${
                mode === m
                  ? "bg-accent text-accent-fg font-medium"
                  : "text-fg-muted hover:bg-surface-3 hover:text-fg"
              }`}
            >
              {label}
            </button>
          ))}
        </div>
      </header>

      <div className="space-y-3 p-4">
        <div className="grid grid-cols-3 gap-3">
          {mode === "volume" ? (
            <Num label="Volume" suffix="L" value={volume} onChange={setVolume} />
          ) : (
            <Num label="Weight" suffix="kg" value={mass} onChange={setMass} />
          )}
          <Num label="From" suffix="%" value={from} onChange={setFrom} />
          <Num label="To" suffix="%" value={to} onChange={setTo} />
        </div>

        <Button type="button" onClick={() => plan.mutate()} disabled={!ready || plan.isPending}>
          {plan.isPending ? "Calculating…" : "Calculate"}
        </Button>

        {plan.error && (
          <Callout tone="danger">
            {plan.error instanceof ConnectError ? plan.error.rawMessage : String(plan.error)}
          </Callout>
        )}

        {plan.data && (
          <div className="space-y-3">
            <div className="rounded-md border border-success/30 bg-success/10 p-3">
              <p className="text-xs font-medium text-success-fg">Add water</p>
              <div className="mt-1 grid grid-cols-2 gap-3">
                <Figure
                  value={`${formatQty(plan.data.waterToAddKg)} kg`}
                  label="by weight — exact"
                  emphasis
                />
                <Figure value={`${formatQty(plan.data.waterToAddL)} L`} label="by volume" />
              </div>
            </div>

            <dl className="space-y-1 text-sm tabular-nums">
              <Row k="Fill to">{formatQty(plan.data.finalVolumeL)} L</Row>
              <Row k="Final weight">{formatQty(plan.data.finalMassKg)} kg</Row>
              <Row k="Starting weight">{formatQty(plan.data.fromMassKg)} kg</Row>
              <Row k="LAA (unchanged)">
                <span className="font-semibold text-fg">{formatLAA(plan.data.laa)} L</span>
              </Row>
            </dl>

            {plan.data.contractionL > 0 && (
              <Callout tone="info">
                A plain volume balance would say {formatQty(plan.data.naiveWaterL)} L. The blend
                contracts as the ethanol and water mix, so it actually takes{" "}
                {formatQty(plan.data.contractionL)} L more. Weighing avoids the problem — mass
                doesn't contract.
              </Callout>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

function Figure({ value, label, emphasis }: { value: string; label: string; emphasis?: boolean }) {
  return (
    <div>
      <p className={`tabular-nums ${emphasis ? "text-2xl font-bold text-success-fg" : "text-xl font-semibold text-fg"}`}>
        {value}
      </p>
      <p className="text-[11px] text-fg-muted">{label}</p>
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
  suffix,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  suffix?: string;
}) {
  return (
    <div>
      <label className="mb-1 block text-xs text-fg-muted">{label}</label>
      <div className="relative">
        <input
          type="number"
          step="0.01"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          className={`w-full rounded border border-border-strong px-3 py-2 text-sm tabular-nums ${suffix ? "pr-9" : ""}`}
        />
        {suffix && (
          <span className="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-xs text-fg-subtle">
            {suffix}
          </span>
        )}
      </div>
    </div>
  );
}
