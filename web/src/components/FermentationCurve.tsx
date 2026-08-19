import type { FermentationAnalysis, FermentationLog } from "@/gen/stillhouse/v1/fermentation_pb";
import { FermentationPhase, FermentFindingSeverity } from "@/gen/stillhouse/v1/fermentation_pb";

/**
 * FermentationCurve — the shape of a ferment over time.
 *
 * Two measures, two charts. Gravity and temperature are on different
 * scales, and putting them on one plot with two y-axes is the single most
 * misleading thing a chart can do — the crossing point is an artefact of
 * where you happened to put the axes. They share an x-axis instead, so
 * "the temperature spiked here and the gravity stalled there" is read by
 * looking straight down.
 *
 * Both series are single-series plots, so neither needs a legend; the
 * panel heading names them.
 */
export function FermentationCurve({
  logs,
  analysis,
  targetFG,
}: {
  logs: FermentationLog[];
  analysis?: FermentationAnalysis;
  targetFG?: number;
}) {
  const points = [...logs]
    .filter((l) => l.observedAt)
    .sort((a, b) => Number(a.observedAt!.seconds) - Number(b.observedAt!.seconds));
  if (points.length < 2) return null;

  const t0 = Number(points[0].observedAt!.seconds);
  const t1 = Number(points[points.length - 1].observedAt!.seconds);
  const span = Math.max(1, t1 - t0);
  const hoursAt = (s: number) => (s - t0) / 3600;
  const totalHours = span / 3600;

  const gravity = points
    .filter((l) => l.specificGravitySet)
    .map((l) => ({ h: hoursAt(Number(l.observedAt!.seconds)), v: l.specificGravity }));
  const temps = points
    .filter((l) => l.temperatureCSet)
    .map((l) => ({ h: hoursAt(Number(l.observedAt!.seconds)), v: l.temperatureC }));

  return (
    <section className="mb-8 overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
      <header className="flex flex-wrap items-center justify-between gap-2 border-b border-border bg-surface-3 px-4 py-3">
        <h2 className="text-sm font-semibold text-fg-muted">Fermentation curve</h2>
        {analysis?.measurable && (
          <span className="text-xs text-fg-subtle">
            {phaseLabel(analysis.phase)} · {analysis.attenuationPct.toFixed(0)} % attenuated ·{" "}
            {analysis.estimatedAbv.toFixed(1)} % ABV
          </span>
        )}
      </header>

      <div className="space-y-4 p-4">
        {gravity.length >= 2 && (
          <Plot
            title="Specific gravity"
            points={gravity}
            totalHours={totalHours}
            color="rgb(var(--color-series-1))"
            format={(v) => v.toFixed(3)}
            reference={targetFG}
            referenceLabel="target"
            // Gravity falls, so don't force zero — the interesting range
            // is the top 60-odd points.
            padFraction={0.15}
          />
        )}
        {temps.length >= 2 && (
          <Plot
            title="Temperature"
            points={temps}
            totalHours={totalHours}
            color="rgb(var(--color-series-2))"
            format={(v) => `${v.toFixed(1)} °C`}
            padFraction={0.25}
          />
        )}

        {analysis?.measurable && (
          <div className="grid grid-cols-3 gap-4">
            <Figure label="Original gravity" value={analysis.originalGravity.toFixed(3)} />
            <Figure label="Now" value={analysis.currentGravity.toFixed(3)} />
            <Figure
              label="Elapsed"
              value={`${Math.round(analysis.hoursElapsed)} h`}
              hint={analysis.peakTempCSet ? `peak ${analysis.peakTempC.toFixed(1)} °C` : undefined}
            />
          </div>
        )}

        {(analysis?.findings.length ?? 0) > 0 && (
          <ul className="space-y-2">
            {analysis!.findings.map((f, i) => {
              const tone =
                f.severity === FermentFindingSeverity.PROBLEM
                  ? { box: "border-danger/40 border-l-danger bg-danger/10", title: "text-danger-fg" }
                  : f.severity === FermentFindingSeverity.WARNING
                    ? { box: "border-warning/40 border-l-warning bg-warning/10", title: "text-warning-fg" }
                    : { box: "border-border border-l-border-strong bg-surface-3/50", title: "text-fg" };
              return (
                <li key={`${f.code}-${i}`} className={`rounded-md border border-l-4 px-3 py-2 ${tone.box}`}>
                  <p className={`text-sm font-medium ${tone.title}`}>{f.title}</p>
                  {f.detail && <p className="mt-0.5 text-xs text-fg-muted">{f.detail}</p>}
                </li>
              );
            })}
          </ul>
        )}
      </div>
    </section>
  );
}

function Plot({
  title,
  points,
  totalHours,
  color,
  format,
  reference,
  referenceLabel,
  padFraction,
}: {
  title: string;
  points: { h: number; v: number }[];
  totalHours: number;
  color: string;
  format: (v: number) => string;
  reference?: number;
  referenceLabel?: string;
  padFraction: number;
}) {
  const W = 600;
  const H = 120;
  const padL = 8;
  const padR = 8;
  const padT = 10;
  const padB = 18;

  const values = points.map((p) => p.v);
  if (reference !== undefined && reference > 0) values.push(reference);
  let lo = Math.min(...values);
  let hi = Math.max(...values);
  const pad = Math.max((hi - lo) * padFraction, 1e-6);
  lo -= pad;
  hi += pad;

  const x = (h: number) => padL + (h / Math.max(totalHours, 1e-9)) * (W - padL - padR);
  const y = (v: number) => padT + (1 - (v - lo) / Math.max(hi - lo, 1e-9)) * (H - padT - padB);

  const d = points.map((p, i) => `${i === 0 ? "M" : "L"} ${x(p.h)} ${y(p.v)}`).join(" ");
  const last = points[points.length - 1];

  return (
    <div>
      <div className="mb-1 flex items-baseline justify-between">
        <span className="text-xs font-medium text-fg-muted">{title}</span>
        <span className="text-sm font-semibold tabular-nums text-fg">{format(last.v)}</span>
      </div>
      <svg viewBox={`0 0 ${W} ${H}`} className="w-full" role="img" aria-label={`${title} over time`}>
        {/* Baseline only — a full grid on a 120px sparkline is noise. */}
        <line
          x1={padL}
          y1={H - padB}
          x2={W - padR}
          y2={H - padB}
          className="stroke-border"
          strokeWidth={1}
        />
        {reference !== undefined && reference > 0 && (
          <>
            <line
              x1={padL}
              y1={y(reference)}
              x2={W - padR}
              y2={y(reference)}
              className="stroke-border-strong"
              strokeWidth={1}
            />
            <text x={padL + 2} y={y(reference) - 3} className="fill-fg-subtle text-[9px]">
              {referenceLabel} {format(reference)}
            </text>
          </>
        )}
        <path d={d} fill="none" stroke={color} strokeWidth={2} strokeLinejoin="round" />
        {points.map((p, i) => (
          <circle key={i} cx={x(p.h)} cy={y(p.v)} r={3} fill={color}>
            <title>{`${Math.round(p.h)} h — ${format(p.v)}`}</title>
          </circle>
        ))}
        {/* Only the endpoint is labelled; a number on every point is chaos. */}
        <text x={padL} y={H - 5} className="fill-fg-subtle text-[9px]">0 h</text>
        <text x={W - padR} y={H - 5} textAnchor="end" className="fill-fg-subtle text-[9px]">
          {Math.round(totalHours)} h
        </text>
      </svg>
    </div>
  );
}

function Figure({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <div>
      <p className="text-xs text-fg-muted">{label}</p>
      <p className="mt-0.5 text-lg font-semibold tabular-nums text-fg">{value}</p>
      {hint && <p className="text-[11px] text-fg-subtle">{hint}</p>}
    </div>
  );
}

function phaseLabel(p: FermentationPhase): string {
  switch (p) {
    case FermentationPhase.LAG:
      return "Lag phase";
    case FermentationPhase.GROWTH:
      return "Growth phase";
    case FermentationPhase.STATIONARY:
      return "Stationary";
    case FermentationPhase.FINISHED:
      return "Finished";
    default:
      return "—";
  }
}
