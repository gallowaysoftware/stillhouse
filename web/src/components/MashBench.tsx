import { useState } from "react";
import { useMutation } from "@tanstack/react-query";

import { Button } from "@/components/Button";
import { mashClient } from "@/lib/clients";
import type { MashBench as MashBenchMsg, MashFinding } from "@/gen/stillhouse/v1/mash_pb";
import { MashFindingSeverity } from "@/gen/stillhouse/v1/mash_pb";
import { formatQty } from "@/lib/format";

/**
 * MashBench — what the grain bill and the readings say about this mash,
 * while the tun is still hot.
 *
 * The centrepiece is the temperature strip: a single axis showing where
 * this bill's starch gelatinises against where the amylases actually
 * work. When those two bands overlap you mash in one rest; when they
 * don't — a maize bill needs 80 °C and the enzymes are dead by then —
 * the picture makes the reason obvious before the words do.
 *
 * Every figure traces to the IBD/CIBD distilling curriculum; see
 * backend/internal/mashing.
 */
export function MashBench({ bench }: { bench: MashBenchMsg }) {
  const gel = bench.gelatinisationC;
  const conv = bench.conversionC;

  return (
    <section className="mb-8 overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
      <header className="flex items-center justify-between border-b border-border bg-surface-3 px-4 py-3">
        <h2 className="text-sm font-semibold text-fg-muted">Mash bench</h2>
        <span className="text-xs text-fg-subtle">
          {formatQty(bench.totalGrainKg)} kg grain
        </span>
      </header>

      <div className="space-y-5 p-4">
        {gel && conv && gel.maxC > 0 && (
          <TemperatureStrip
            gelMin={gel.minC}
            gelMax={gel.maxC}
            convMin={conv.minC}
            convMax={conv.maxC}
            cerealCook={bench.cerealCookRequired}
            known={bench.gelatinisationKnown}
          />
        )}

        <div className="grid grid-cols-2 gap-4 sm:grid-cols-3">
          {bench.thicknessLPerKgSet && (
            <Tile
              label="Mash thickness"
              value={`${bench.thicknessLPerKg.toFixed(2)} L/kg`}
              band="usual 2.5–3.5"
              ok={bench.thicknessLPerKg >= 2.5 && bench.thicknessLPerKg <= 3.5}
            />
          )}
          {bench.efficiencySet && bench.efficiency && (
            <>
              <Tile
                label="Conversion efficiency"
                value={`${bench.efficiency.pct.toFixed(0)} %`}
                band={`${bench.efficiency.extractMeasuredKg.toFixed(1)} of ${bench.efficiency.extractAvailableKg.toFixed(1)} kg extract`}
                ok={bench.efficiency.pct >= 75 && bench.efficiency.pct <= 100}
              />
              <Tile
                label="Original gravity"
                value={bench.efficiency.originalGravity.toFixed(3)}
                band={`${bench.efficiency.plato.toFixed(1)} °P in ${formatQty(bench.efficiency.washVolumeL)} L${
                  bench.efficiency.washVolumeEstimated ? " (est.)" : ""
                }`}
              />
            </>
          )}
        </div>

        {bench.findings.length > 0 && (
          <ul className="space-y-2">
            {bench.findings.map((f, i) => (
              <FindingRow key={`${f.code}-${i}`} finding={f} />
            ))}
          </ul>
        )}

        <StrikeCalculator
          defaultTargetC={conv ? (conv.minC + conv.maxC) / 2 : 64}
          defaultThickness={bench.thicknessLPerKgSet ? bench.thicknessLPerKg : 3}
          grainKg={bench.totalGrainKg}
        />
      </div>
    </section>
  );
}

/**
 * TemperatureStrip draws both bands on one 40–100 °C axis. The overlap —
 * or the gap — is the whole message, so it's shown as geometry rather
 * than as two numbers the reader has to compare in their head.
 */
function TemperatureStrip({
  gelMin,
  gelMax,
  convMin,
  convMax,
  cerealCook,
  known,
}: {
  gelMin: number;
  gelMax: number;
  convMin: number;
  convMax: number;
  cerealCook: boolean;
  known: boolean;
}) {
  const AXIS_MIN = 40;
  const AXIS_MAX = 100;
  const pct = (c: number) => ((c - AXIS_MIN) / (AXIS_MAX - AXIS_MIN)) * 100;
  const ticks = [40, 50, 60, 70, 80, 90, 100];

  return (
    <div>
      <div className="mb-2 flex items-baseline justify-between">
        <span className="text-xs font-medium text-fg-muted">Temperature</span>
        <span className={`text-xs ${cerealCook ? "text-warning-fg" : "text-success-fg"}`}>
          {cerealCook ? "Separate cereal cook required" : "Single rest works"}
        </span>
      </div>

      <div className="relative h-16">
        {/* Gelatinisation band — what the starch needs. */}
        <Band
          left={pct(gelMin)}
          width={pct(gelMax) - pct(gelMin)}
          top="0"
          className="bg-warning/30 border-warning/60"
          label={`Gelatinisation ${gelMin.toFixed(0)}–${gelMax.toFixed(0)} °C${known ? "" : " (partial)"}`}
        />
        {/* Conversion band — where the amylases survive and work. */}
        <Band
          left={pct(convMin)}
          width={pct(convMax) - pct(convMin)}
          top="1.75rem"
          className="bg-success/30 border-success/60"
          label={`Conversion ${convMin.toFixed(0)}–${convMax.toFixed(0)} °C`}
        />
        {/* The hard ceiling: above this the enzymes are gone. */}
        <div
          className="absolute top-0 bottom-4 w-px bg-danger/70"
          style={{ left: `${pct(80)}%` }}
          title="80 °C — enzymes denatured"
        />
        <span
          className="absolute bottom-0 -translate-x-1/2 text-[10px] text-danger-fg"
          style={{ left: `${pct(80)}%` }}
        >
          80 °C enzymes die
        </span>
      </div>

      <div className="relative mt-1 h-4 border-t border-border">
        {ticks.map((t) => (
          <span
            key={t}
            className="absolute -translate-x-1/2 text-[10px] text-fg-subtle"
            style={{ left: `${pct(t)}%` }}
          >
            {t}
          </span>
        ))}
      </div>
    </div>
  );
}

function Band({
  left,
  width,
  top,
  className,
  label,
}: {
  left: number;
  width: number;
  top: string;
  className: string;
  label: string;
}) {
  return (
    <div
      className={`absolute flex h-6 items-center justify-center rounded border px-1 ${className}`}
      style={{ left: `${left}%`, width: `${Math.max(width, 2)}%`, top }}
      title={label}
    >
      <span className="truncate whitespace-nowrap text-[10px] text-fg">{label}</span>
    </div>
  );
}

function FindingRow({ finding }: { finding: MashFinding }) {
  const tone = severityTone(finding.severity);
  return (
    <li className={`rounded-md border border-l-4 px-3 py-2 ${tone.box}`}>
      <p className={`text-sm font-medium ${tone.title}`}>{finding.title}</p>
      {finding.detail && <p className="mt-0.5 text-xs text-fg-muted">{finding.detail}</p>}
    </li>
  );
}

function severityTone(s: MashFindingSeverity): { box: string; title: string } {
  switch (s) {
    case MashFindingSeverity.PROBLEM:
      return { box: "border-danger/40 border-l-danger bg-danger/10", title: "text-danger-fg" };
    case MashFindingSeverity.WARNING:
      return { box: "border-warning/40 border-l-warning bg-warning/10", title: "text-warning-fg" };
    default:
      return { box: "border-border border-l-border-strong bg-surface-3/50", title: "text-fg" };
  }
}

function Tile({
  label,
  value,
  band,
  ok,
}: {
  label: string;
  value: string;
  band?: string;
  ok?: boolean;
}) {
  return (
    <div className={`rounded-lg border p-3 ${ok === false ? "border-warning/40 bg-warning/5" : "border-border bg-surface-3/40"}`}>
      <p className="text-xs text-fg-muted">{label}</p>
      <p className={`mt-1 text-lg font-semibold tabular-nums ${ok === false ? "text-warning-fg" : "text-fg"}`}>
        {value}
      </p>
      {band && <p className="mt-0.5 text-[11px] text-fg-subtle">{band}</p>}
    </div>
  );
}

/**
 * StrikeCalculator — how hot the liquor needs to be so the grain lands on
 * the rest temperature. Server-side so the energy balance and its
 * assumptions live in one place.
 */
function StrikeCalculator({
  defaultTargetC,
  defaultThickness,
  grainKg,
}: {
  defaultTargetC: number;
  defaultThickness: number;
  grainKg: number;
}) {
  const [target, setTarget] = useState(String(Math.round(defaultTargetC)));
  const [grainTemp, setGrainTemp] = useState("18");
  const [thickness, setThickness] = useState(defaultThickness.toFixed(1));

  const plan = useMutation({
    mutationFn: () =>
      mashClient.planStrike({
        targetTempC: Number(target),
        grainTempC: Number(grainTemp),
        thicknessLPerKg: Number(thickness),
        grainKg,
      }),
  });

  return (
    <div className="rounded-md border border-border bg-surface-3/40 p-3">
      <p className="mb-2 text-xs font-medium text-fg-muted">Strike temperature</p>
      <div className="flex flex-wrap items-end gap-3">
        <Num label="Target rest °C" value={target} onChange={setTarget} />
        <Num label="Grain °C" value={grainTemp} onChange={setGrainTemp} />
        <Num label="L/kg" value={thickness} onChange={setThickness} step="0.1" />
        <Button type="button" size="sm" onClick={() => plan.mutate()} disabled={plan.isPending}>
          {plan.isPending ? "…" : "Calculate"}
        </Button>
      </div>
      {plan.data && (
        <div className="mt-3 space-y-2">
          <p className="text-sm">
            <span className="text-fg-muted">Heat liquor to </span>
            <span className="text-lg font-semibold tabular-nums text-fg">
              {plan.data.strikeTempC.toFixed(1)} °C
            </span>
            {plan.data.waterVolumeL > 0 && (
              <span className="text-fg-muted">
                {" "}· {formatQty(plan.data.waterVolumeL)} L of liquor
              </span>
            )}
          </p>
          {plan.data.findings.map((f, i) => (
            <FindingRow key={`${f.code}-${i}`} finding={f} />
          ))}
        </div>
      )}
      {plan.error && (
        <p className="mt-2 text-xs text-danger-fg">{String(plan.error)}</p>
      )}
    </div>
  );
}

function Num({
  label,
  value,
  onChange,
  step = "1",
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  step?: string;
}) {
  return (
    <div>
      <label className="mb-1 block text-[11px] text-fg-muted">{label}</label>
      <input
        type="number"
        step={step}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="w-24 rounded border border-border-strong px-2 py-1 text-sm tabular-nums"
      />
    </div>
  );
}
