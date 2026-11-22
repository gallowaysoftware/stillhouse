/**
 * PlanCurve — what the planner expects of a run, and, when there are
 * readings, what the still actually did over the top of it.
 *
 * Strength against hours, with collected alcohol on a second plot sharing
 * the axis — never a second y-axis, for the reason FermentationCurve gives.
 * The cut envelope is drawn as verticals where the planned strength passes
 * each named cut; the planned line is grey and the actual is the series
 * colour, because the actual is the one that happened.
 */
export type CurvePoint = { h: number; v: number };

export function PlanCurve({
  planned,
  collected,
  actual,
  cuts,
  totalHours,
  basis,
}: {
  planned: CurvePoint[];
  collected: CurvePoint[];
  actual?: CurvePoint[];
  cuts: { abv: number; h: number; share: number }[];
  totalHours: number;
  basis?: string;
}) {
  if (planned.length < 2) return null;
  return (
    <section className="mb-8 overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
      <header className="flex flex-wrap items-center justify-between gap-2 border-b border-border bg-surface-3 px-4 py-3">
        <h2 className="text-sm font-semibold text-fg-muted">Planned run</h2>
        {basis && <span className="text-xs text-fg-subtle">{basis}</span>}
      </header>
      <div className="space-y-4 p-4">
        <Plot
          title="Strength at the parrot"
          totalHours={totalHours}
          format={(v) => `${v.toFixed(0)} %`}
          min={0}
          max={100}
          series={[
            { label: "planned", points: planned, color: "rgb(var(--sh-fg-subtle) / 0.7)", dashed: true },
            ...(actual && actual.length >= 2 ? [{ label: "as read", points: actual, color: "rgb(var(--sh-series-1))" }] : []),
          ]}
          verticals={cuts.map((c) => ({ h: c.h, label: `${c.abv.toFixed(0)} % · ${(c.share * 100).toFixed(0)} %` }))}
        />
        <Plot
          title="Alcohol collected"
          totalHours={totalHours}
          format={(v) => `${v.toFixed(1)} LAA`}
          min={0}
          series={[{ label: "planned", points: collected, color: "rgb(var(--sh-fg-subtle) / 0.7)", dashed: true }]}
          verticals={cuts.map((c) => ({ h: c.h, label: "" }))}
        />
      </div>
    </section>
  );
}

type Series = { label: string; points: CurvePoint[]; color: string; dashed?: boolean };

function Plot({
  title,
  series,
  verticals,
  totalHours,
  format,
  min,
  max,
}: {
  title: string;
  series: Series[];
  verticals: { h: number; label: string }[];
  totalHours: number;
  format: (v: number) => string;
  min?: number;
  max?: number;
}) {
  const W = 640;
  const H = 180;
  const padL = 52;
  const padR = 12;
  const padT = 14;
  const padB = 22;

  const all = series.flatMap((s) => s.points.map((p) => p.v));
  const lo = min ?? Math.min(...all);
  const hi = max ?? Math.max(...all) * 1.05;
  const y0 = lo;
  const y1 = Math.max(hi, lo + 1e-6);

  const x = (h: number) => padL + (h / Math.max(1e-6, totalHours)) * (W - padL - padR);
  const y = (v: number) => padT + (1 - (v - y0) / (y1 - y0)) * (H - padT - padB);
  const path = (pts: CurvePoint[]) =>
    pts.map((p, i) => `${i === 0 ? "M" : "L"}${x(p.h).toFixed(1)},${y(p.v).toFixed(1)}`).join(" ");
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
                <span className="inline-block h-0.5 w-4" style={{ background: s.color, opacity: s.dashed ? 0.8 : 1 }} />
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
        {verticals.map((c, i) => (
          <g key={i}>
            <line x1={x(c.h)} x2={x(c.h)} y1={padT} y2={H - padB} stroke="rgb(var(--sh-fg-subtle) / 0.5)" strokeWidth={1} strokeDasharray="2 3" />
            {c.label && (
              <text x={x(c.h) + 3} y={padT + 9} fontSize={9} fill="rgb(var(--sh-fg-subtle))">
                {c.label}
              </text>
            )}
          </g>
        ))}
        {series.map((s) => (
          <path
            key={s.label}
            d={path(s.points)}
            fill="none"
            stroke={s.color}
            strokeWidth={s.dashed ? 1.5 : 2}
            strokeDasharray={s.dashed ? "4 3" : undefined}
            strokeLinejoin="round"
          />
        ))}
      </svg>
    </figure>
  );
}
