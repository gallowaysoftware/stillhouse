import { useState } from "react";

/**
 * SensoryRadar — a flavour profile as a shape.
 *
 * Why a radar here, when a radar is usually the wrong answer: the job is
 * to read the *shape* of a spirit across a fixed set of axes and to see
 * how one version differs from another. The axes are a closed, ordered
 * set that never changes (the SWRI Flavour Wheel classes for whisky, the
 * botanical axes for gin), which is the one case a radar handles well.
 * It is also the form the trade already reads — the whisky bench is
 * literally scoring against a wheel.
 *
 * The numbers stay visible in the scoring inputs beside it, so the chart
 * enhances and never gates: every value is readable without hovering
 * anything.
 *
 * Design notes, per the house data-viz rules:
 *   - Two series maximum. Colors are the validated categorical slots 1
 *     and 2 (see --color-series-* in index.css); do not substitute a
 *     semantic state color, which would read as a status.
 *   - Hairline grid one shade off the surface, no dashes.
 *   - Thin 2px marks, low-opacity fills so overlap stays readable, and a
 *     surface-colored ring on each vertex so overlapping points separate.
 *   - No number on every vertex. The hover layer and the inputs carry
 *     values; only the axis names are drawn.
 *   - A legend whenever there are two series, so identity is never
 *     carried by color alone.
 */

export type RadarAxis = { key: string; label: string; hint?: string };

export type RadarSeries = {
  name: string;
  /** Axis key → score. Missing or undefined means "not scored on this axis". */
  values: Record<string, number | undefined>;
  /** Which categorical slot to paint with. */
  slot: 1 | 2;
};

const MAX = 10;
const RINGS = [2, 4, 6, 8, 10];

export function SensoryRadar({
  axes,
  series,
  size = 300,
}: {
  axes: RadarAxis[];
  series: RadarSeries[];
  size?: number;
}) {
  const [hovered, setHovered] = useState<number | null>(null);

  // Generous margin: the axis labels live outside the plot and must not
  // be clipped by the viewBox.
  const margin = 58;
  const cx = size / 2;
  const cy = size / 2;
  const r = size / 2 - margin;

  const angleFor = (i: number) => (Math.PI * 2 * i) / axes.length - Math.PI / 2;
  const pointAt = (i: number, value: number) => {
    const a = angleFor(i);
    const radius = (Math.max(0, Math.min(MAX, value)) / MAX) * r;
    return [cx + Math.cos(a) * radius, cy + Math.sin(a) * radius] as const;
  };

  const drawn = series.filter((s) => axes.some((a) => s.values[a.key] !== undefined));

  return (
    <div>
      <svg
        viewBox={`0 0 ${size} ${size}`}
        className="w-full max-w-sm"
        role="img"
        aria-label={`Sensory profile across ${axes.length} axes${
          drawn.length > 1 ? `, comparing ${drawn.map((s) => s.name).join(" and ")}` : ""
        }`}
      >
        {/* Grid: solid hairlines, one shade off the surface. */}
        <g className="text-border">
          {RINGS.map((ring) => (
            <polygon
              key={ring}
              points={axes
                .map((_, i) => {
                  const [x, y] = pointAt(i, ring);
                  return `${x},${y}`;
                })
                .join(" ")}
              fill="none"
              stroke="currentColor"
              strokeWidth={1}
            />
          ))}
          {axes.map((_, i) => {
            const [x, y] = pointAt(i, MAX);
            return <line key={i} x1={cx} y1={cy} x2={x} y2={y} stroke="currentColor" strokeWidth={1} />;
          })}
        </g>

        {/* Series polygons, thin marks over a low-opacity fill. */}
        {drawn.map((s) => {
          const pts = axes.map((a, i) => pointAt(i, s.values[a.key] ?? 0));
          const d = pts.map(([x, y]) => `${x},${y}`).join(" ");
          const color = s.slot === 1 ? "rgb(var(--color-series-1))" : "rgb(var(--color-series-2))";
          return (
            <g key={s.name}>
              <polygon points={d} fill={color} fillOpacity={0.16} stroke={color} strokeWidth={2} />
              {pts.map(([x, y], i) => (
                <circle
                  key={i}
                  cx={x}
                  cy={y}
                  r={3.5}
                  fill={color}
                  // Surface ring so two series' vertices stay separable
                  // where they land on top of each other.
                  stroke="rgb(var(--color-surface-2))"
                  strokeWidth={2}
                />
              ))}
            </g>
          );
        })}

        {/* Axis labels, outside the plot. */}
        {axes.map((a, i) => {
          const angle = angleFor(i);
          const lx = cx + Math.cos(angle) * (r + 18);
          const ly = cy + Math.sin(angle) * (r + 18);
          const anchor =
            Math.abs(Math.cos(angle)) < 0.3 ? "middle" : Math.cos(angle) > 0 ? "start" : "end";
          return (
            <text
              key={a.key}
              x={lx}
              y={ly}
              textAnchor={anchor}
              dominantBaseline="middle"
              className={`text-[9px] ${hovered === i ? "fill-fg font-medium" : "fill-fg-muted"}`}
            >
              {a.label}
            </text>
          );
        })}

        {/* Hit layer: one wedge per axis, comfortably larger than the
            vertex it selects. */}
        {axes.map((a, i) => {
          const angle = angleFor(i);
          const half = Math.PI / axes.length;
          const [x1, y1] = [cx + Math.cos(angle - half) * r, cy + Math.sin(angle - half) * r];
          const [x2, y2] = [cx + Math.cos(angle + half) * r, cy + Math.sin(angle + half) * r];
          return (
            <path
              key={a.key}
              d={`M ${cx} ${cy} L ${x1} ${y1} A ${r} ${r} 0 0 1 ${x2} ${y2} Z`}
              fill="transparent"
              onMouseEnter={() => setHovered(i)}
              onMouseLeave={() => setHovered(null)}
            >
              <title>
                {a.label}
                {a.hint ? ` — ${a.hint}` : ""}
                {"\n"}
                {drawn
                  .map((s) => `${s.name}: ${s.values[a.key] ?? "not scored"}`)
                  .join("\n")}
              </title>
            </path>
          );
        })}
      </svg>

      {/* A legend whenever two profiles share the plot. One series needs
          none — the panel heading names it. */}
      {drawn.length > 1 && (
        <div className="mt-1 flex flex-wrap items-center gap-3">
          {drawn.map((s) => (
            <span key={s.name} className="flex items-center gap-1.5 text-xs text-fg-muted">
              <span
                className="inline-block h-2.5 w-2.5 rounded-sm"
                style={{
                  background:
                    s.slot === 1 ? "rgb(var(--color-series-1))" : "rgb(var(--color-series-2))",
                }}
              />
              {s.name}
            </span>
          ))}
        </div>
      )}
    </div>
  );
}
