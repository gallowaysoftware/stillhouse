import type { MaturationAssessment, MaturationFinding } from "@/gen/stillhouse/v1/barrel_pb";
import { MaturationFindingSeverity } from "@/gen/stillhouse/v1/barrel_pb";

/**
 * MaturationPanel — is this cask losing what it should be losing?
 *
 * A regauge already recorded the loss. What it never said was whether the
 * loss was normal, and a weeping barrel, a slack bung and a mis-read dip
 * all produce the same number in a ledger. This puts the measured rate
 * against the band the curriculum expects for the warehouse, and says
 * which way the strength ought to be drifting for where the cask is
 * sitting.
 *
 * The rate bar is a meter, not a chart: one measured value against a
 * band. It's the honest form for a single ratio against a limit.
 */
export function MaturationPanel({ m }: { m: MaturationAssessment }) {
  if (!m.measurable) {
    return (
      <div className="rounded-lg border border-border bg-surface-2 p-4 shadow-sm">
        <h2 className="text-sm font-semibold text-fg-muted">Angel's share</h2>
        <p className="mt-1 text-sm text-fg-muted">{m.whyNot || "Not enough history yet."}</p>
      </div>
    );
  }

  return (
    <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
      <header className="flex items-center justify-between border-b border-border bg-surface-3 px-4 py-3">
        <h2 className="text-sm font-semibold text-fg-muted">Angel's share</h2>
        <span className="text-xs text-fg-subtle">
          {m.hotDry ? "hot / dry position" : "cool / humid warehouse"}
          {m.climateFromLevel && " · from shelf height"}
        </span>
      </header>

      <div className="space-y-4 p-4">
        <LossMeter
          value={m.annualVolumeLossPct}
          min={m.expectedMinPct}
          max={m.expectedMaxPct}
        />

        <div className="grid grid-cols-2 gap-4">
          <Figure
            label="Alcohol lost"
            value={`${m.annualLaaLossPct.toFixed(1)} %/yr`}
            hint="LAA — the figure duty is charged on"
          />
          <Figure
            label="Strength drift"
            value={`${m.strengthDriftPerYear > 0 ? "+" : ""}${m.strengthDriftPerYear.toFixed(1)} pts/yr`}
            hint={m.expectedDriftSign < 0 ? "should be falling here" : "should be rising here"}
          />
        </div>

        {m.findings.length > 0 && (
          <ul className="space-y-2">
            {m.findings.map((f, i) => (
              <FindingRow key={`${f.code}-${i}`} finding={f} />
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}

/**
 * LossMeter — the measured rate against the expected band on one track.
 * The band is drawn as a lighter region so "inside" or "outside" is read
 * as position rather than by comparing two numbers.
 */
function LossMeter({ value, min, max }: { value: number; min: number; max: number }) {
  // Scale to whichever is larger: twice the top of the band, or enough to
  // keep the marker on screen for a badly leaking cask.
  const scaleMax = Math.max(max * 2, value * 1.15, 1);
  const pct = (v: number) => Math.min(100, (v / scaleMax) * 100);
  const inBand = value >= min && value <= max;
  const over = value > max;

  return (
    <div>
      <div className="mb-1 flex items-baseline justify-between">
        <span className="text-xs text-fg-muted">Volume lost</span>
        <span
          className={`text-2xl font-bold ${
            over ? "text-warning-fg" : inBand ? "text-success-fg" : "text-fg"
          }`}
        >
          {value.toFixed(1)} <span className="text-sm font-normal text-fg-muted">%/yr</span>
        </span>
      </div>
      <div className="relative h-2 w-full rounded-full bg-surface-3">
        {/* Expected band */}
        <div
          className="absolute inset-y-0 rounded-full bg-success/30"
          style={{ left: `${pct(min)}%`, width: `${pct(max) - pct(min)}%` }}
        />
        {/* Measured rate */}
        <div
          className={`absolute inset-y-[-3px] w-[3px] rounded-full ${
            over ? "bg-warning" : "bg-fg"
          }`}
          style={{ left: `calc(${pct(value)}% - 1.5px)` }}
        />
      </div>
      <p className="mt-1 text-[11px] text-fg-subtle">
        expected {min.toFixed(0)}–{max.toFixed(0)} %/yr for this position
      </p>
    </div>
  );
}

function Figure({ label, value, hint }: { label: string; value: string; hint: string }) {
  return (
    <div>
      <p className="text-xs text-fg-muted">{label}</p>
      <p className="mt-0.5 text-lg font-semibold tabular-nums text-fg">{value}</p>
      <p className="text-[11px] text-fg-subtle">{hint}</p>
    </div>
  );
}

function FindingRow({ finding }: { finding: MaturationFinding }) {
  const tone = severityTone(finding.severity);
  return (
    <li className={`rounded-md border border-l-4 px-3 py-2 ${tone.box}`}>
      <p className={`text-sm font-medium ${tone.title}`}>{finding.title}</p>
      {finding.detail && <p className="mt-0.5 text-xs text-fg-muted">{finding.detail}</p>}
    </li>
  );
}

function severityTone(s: MaturationFindingSeverity): { box: string; title: string } {
  switch (s) {
    case MaturationFindingSeverity.PROBLEM:
      return { box: "border-danger/40 border-l-danger bg-danger/10", title: "text-danger-fg" };
    case MaturationFindingSeverity.WARNING:
      return { box: "border-warning/40 border-l-warning bg-warning/10", title: "text-warning-fg" };
    default:
      return { box: "border-border border-l-border-strong bg-surface-3/50", title: "text-fg" };
  }
}
