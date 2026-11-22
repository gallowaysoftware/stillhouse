import type { DistillationReading } from "@/gen/stillhouse/v1/distillation_pb";

/**
 * RunCurve — what the instruments said as the run went on.
 *
 * Strength at the parrot on one plot, temperatures on another, sharing the
 * hours axis, for the reason FermentationCurve gives: two scales on one
 * plot make their crossing an artefact of where the axes were put. The
 * heat setting is drawn as steps under the strength, because it is the
 * one thing the operator changed and the curve is the answer to it.
 *
 * Strength is plotted as read — at the parrot's own temperature, not
 * corrected to 20 °C — because that is the figure the cut was made on.
 */
export function RunCurve({ readings, title = "Run log" }: { readings: DistillationReading[]; title?: string }) {
  const points = [...readings]
    .filter((r) => r.observedAt)
    .sort((a, b) => Number(a.observedAt!.seconds) - Number(b.observedAt!.seconds));
  if (points.length < 2) return null;

  const t0 = Number(points[0].observedAt!.seconds);
  const t1 = Number(points[points.length - 1].observedAt!.seconds);
  const totalHours = Math.max(1, t1 - t0) / 3600;
  const hoursAt = (s: number) => (s - t0) / 3600;

  const strength = points
    .filter((r) => r.distillateAbvPctSet)
    .map((r) => ({ h: hoursAt(Number(r.observedAt!.seconds)), v: r.distillateAbvPct }));
  const heat = points
    .filter((r) => r.heatSettingPctSet)
    .map((r) => ({ h: hoursAt(Number(r.observedAt!.seconds)), v: r.heatSettingPct }));
  const pot = points
    .filter((r) => r.potTemperatureCSet)
    .map((r) => ({ h: hoursAt(Number(r.observedAt!.seconds)), v: r.potTemperatureC }));
  const vapour = points
    .filter((r) => r.vapourTemperatureCSet)
    .map((r) => ({ h: hoursAt(Number(r.observedAt!.seconds)), v: r.vapourTemperatureC }));
  const spirit = points
    .filter((r) => r.distillateTemperatureCSet)
    .map((r) => ({ h: hoursAt(Number(r.observedAt!.seconds)), v: r.distillateTemperatureC }));

  return (
    <section className="mb-8 overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
      <header className="flex flex-wrap items-center justify-between gap-2 border-b border-border bg-surface-3 px-4 py-3">
        <h2 className="text-sm font-semibold text-fg-muted">{title}</h2>
        <span className="text-xs text-fg-subtle">
          {points.length} readings over {totalHours.toFixed(1)} h
        </span>
      </header>
      <div className="space-y-4 p-4">
        {strength.length >= 2 && (
          <Plot
            title="Strength at the parrot, as read"
            series={[{ label: "distillate", points: strength, color: "rgb(var(--sh-series-1))" }]}
            steps={heat.length >= 2 ? { label: "heat", points: heat, color: "rgb(var(--sh-fg-subtle) / 0.5)" } : undefined}
            totalHours={totalHours}
            format={(v) => `${v.toFixed(1)} %`}
            min={0}
            max={100}
          />
        )}
        {(pot.length >= 2 || vapour.length >= 2 || spirit.length >= 2) && (
          <Plot
            title="Temperatures"
            series={[
              { label: "pot", points: pot, color: "rgb(var(--sh-series-1))" },
              { label: "vapour", points: vapour, color: "rgb(var(--sh-fg-muted))" },
              { label: "spirit", points: spirit, color: "rgb(var(--sh-fg-subtle))" },
            ].filter((s) => s.points.length >= 2)}
            totalHours={totalHours}
            format={(v) => `${v.toFixed(0)} °C`}
          />
        )}
      </div>
    </section>
  );
}

type Series = { label: string; points: { h: number; v: number }[]; color: string };

function Plot({
  title,
  series,
  steps,
  totalHours,
  format,
  min,
  max,
}: {
  title: string;
  series: Series[];
  steps?: Series;
  totalHours: number;
  format: (v: number) => string;
  min?: number;
  max?: number;
}) {
  const W = 640;
  const H = 180;
  const padL = 44;
  const padR = 12;
  const padT = 10;
  const padB = 22;

  const all = series.flatMap((s) => s.points.map((p) => p.v));
  const lo = min ?? Math.min(...all);
  const hi = max ?? Math.max(...all);
  const span = Math.max(1e-6, hi - lo);
  const pad = min === undefined ? span * 0.1 : 0;
  const y0 = lo - pad;
  const y1 = hi + pad;

  const x = (h: number) => padL + (h / totalHours) * (W - padL - padR);
  const y = (v: number) => padT + (1 - (v - y0) / (y1 - y0)) * (H - padT - padB);
  const path = (pts: { h: number; v: number }[]) =>
    pts.map((p, i) => `${i === 0 ? "M" : "L"}${x(p.h).toFixed(1)},${y(p.v).toFixed(1)}`).join(" ");
  // The heat setting is a percentage on its own scale, drawn as steps along the bottom.
  const stepPath = (pts: { h: number; v: number }[]) => {
    const yh = (v: number) => padT + (1 - v / 100) * (H - padT - padB);
    let d = "";
    pts.forEach((p, i) => {
      const px = x(p.h).toFixed(1);
      const py = yh(p.v).toFixed(1);
      if (i === 0) d += `M${px},${py}`;
      else d += ` H${px} V${py}`;
    });
    d += ` H${(W - padR).toFixed(1)}`;
    return d;
  };

  const ticks = 4;
  const hourTicks = Array.from({ length: 5 }, (_, i) => (totalHours * i) / 4);

  return (
    <figure>
      <figcaption className="mb-1 flex items-center justify-between text-xs text-fg-muted">
        <span>{title}</span>
        {series.length > 1 && (
          <span className="flex gap-3">
            {series.map((s) => (
              <span key={s.label} className="flex items-center gap-1">
                <span className="inline-block h-2 w-3 rounded-sm" style={{ background: s.color }} />
                {s.label}
              </span>
            ))}
          </span>
        )}
      </figcaption>
      <svg viewBox={`0 0 ${W} ${H}`} className="w-full" role="img" aria-label={`${title} over time`}>
        {Array.from({ length: ticks + 1 }, (_, i) => y0 + ((y1 - y0) * i) / ticks).map((v) => (
          <g key={v}>
            <line x1={padL} x2={W - padR} y1={y(v)} y2={y(v)} stroke="rgb(var(--sh-border))" strokeWidth={1} />
            <text x={padL - 6} y={y(v) + 3} textAnchor="end" fontSize={10} fill="rgb(var(--sh-fg-subtle))">
              {format(v)}
            </text>
          </g>
        ))}
        {hourTicks.map((h) => (
          <text key={h} x={x(h)} y={H - 6} textAnchor="middle" fontSize={10} fill="rgb(var(--sh-fg-subtle))">
            {h.toFixed(1)} h
          </text>
        ))}
        {steps && steps.points.length >= 2 && (
          <path d={stepPath(steps.points)} fill="none" stroke={steps.color} strokeWidth={1.5} strokeDasharray="3 3" />
        )}
        {series.map((s) => (
          <path key={s.label} d={path(s.points)} fill="none" stroke={s.color} strokeWidth={2} strokeLinejoin="round" />
        ))}
      </svg>
    </figure>
  );
}
