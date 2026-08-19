import type { CutAnalysis, CutFinding, DistillationCut } from "@/gen/stillhouse/v1/distillation_pb";
import { CutFindingSeverity, DistillationCutKind } from "@/gen/stillhouse/v1/distillation_pb";
import { cutKindLabel, formatLAA } from "@/lib/format";

/**
 * CutProfile — where the alcohol went, and whether it adds up.
 *
 * The form is deliberately an emphasis chart rather than five categorical
 * colours: on a spirit run exactly one fraction is the product, and the
 * question the distiller is asking is "how much of the charge became
 * hearts?". Painting all five fractions in competing hues would bury that.
 * Hearts takes the series colour; everything else recedes into neutrals.
 *
 * No cut-point pass/fail here. Cut points are not universal — a wash run's
 * heads and tails cuts sit near 54 % and 48 % ABV, a botanical spirit's
 * tails divert between 30 % and 20 % — so the hearts window is shown as a
 * figure to compare against your own previous runs, which is what
 * consistency actually means.
 */

// Fractions in the order they come off the still. Anything not hearts is a
// neutral; the ramp only exists so adjacent segments stay separable.
const NEUTRALS: Partial<Record<DistillationCutKind, string>> = {
  [DistillationCutKind.FORESHOTS]: "rgb(var(--color-fg-subtle) / 0.75)",
  [DistillationCutKind.HEADS]: "rgb(var(--color-fg-subtle) / 0.5)",
  [DistillationCutKind.TAILS]: "rgb(var(--color-fg-subtle) / 0.35)",
  [DistillationCutKind.FEINTS_SAVED]: "rgb(var(--color-fg-subtle) / 0.22)",
};
const HEARTS_COLOR = "rgb(var(--color-series-1))";

function colorFor(kind: DistillationCutKind): string {
  return kind === DistillationCutKind.HEARTS
    ? HEARTS_COLOR
    : NEUTRALS[kind] ?? "rgb(var(--color-fg-subtle) / 0.3)";
}

export function CutProfile({
  analysis,
  cuts,
}: {
  analysis: CutAnalysis;
  cuts: DistillationCut[];
}) {
  const ordered = [...cuts].sort((a, b) => a.cutOrder - b.cutOrder);
  const total = ordered.reduce((s, c) => s + c.laa, 0);
  if (total <= 0) return null;

  return (
    <section className="mb-8 overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
      <header className="flex items-center justify-between border-b border-border bg-surface-3 px-4 py-3">
        <h2 className="text-sm font-semibold text-fg-muted">Cut profile</h2>
        {analysis.heartsSet && (
          <span className="text-xs text-fg-subtle">
            hearts window {analysis.heartsStartAbv.toFixed(1)} % → {analysis.heartsEndAbv.toFixed(1)} %
          </span>
        )}
      </header>

      <div className="space-y-4 p-4">
        <div>
          <div className="mb-1 flex items-baseline justify-between">
            <span className="text-xs text-fg-muted">Alcohol by fraction</span>
            <span className="text-2xl font-bold text-fg">
              {analysis.heartsSharePct.toFixed(0)}
              <span className="ml-1 text-sm font-normal text-fg-muted">% hearts</span>
            </span>
          </div>
          {/* Stacked bar with a 2px surface gap between segments, so the
              boundaries read without drawing borders on the marks. */}
          <div className="flex h-6 w-full gap-[2px] overflow-hidden rounded">
            {ordered.map((c) => {
              const pct = (c.laa / total) * 100;
              if (pct <= 0) return null;
              return (
                <div
                  key={c.id}
                  className="flex items-center justify-center"
                  style={{ width: `${pct}%`, background: colorFor(c.kind) }}
                  title={`${cutKindLabel(c.kind)} — ${formatLAA(c.laa)} L LAA at ${c.abvPct.toFixed(1)} %`}
                >
                  {/* Only label a segment that can actually hold the text. */}
                  {pct > 14 && (
                    <span
                      className={`truncate px-1 text-[10px] font-medium ${
                        c.kind === DistillationCutKind.HEARTS ? "text-white" : "text-fg"
                      }`}
                    >
                      {cutKindLabel(c.kind)}
                    </span>
                  )}
                </div>
              );
            })}
          </div>
          <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1">
            {ordered.map((c) => (
              <span key={c.id} className="flex items-center gap-1.5 text-[11px] text-fg-muted">
                <span
                  className="inline-block h-2.5 w-2.5 rounded-sm"
                  style={{ background: colorFor(c.kind) }}
                />
                {cutKindLabel(c.kind)}
                <span className="tabular-nums text-fg-subtle">
                  {formatLAA(c.laa)} L · {c.abvPct.toFixed(1)} %
                </span>
              </span>
            ))}
          </div>
        </div>

        <div className="grid grid-cols-3 gap-4">
          <Figure label="Charged in" value={`${formatLAA(analysis.chargeLaa)} L`} />
          <Figure label="Collected in cuts" value={`${formatLAA(analysis.cutLaa)} L`} />
          <Figure
            label="Accounted for"
            value={analysis.chargeLaa > 0 ? `${analysis.accountedPct.toFixed(1)} %` : "—"}
            hint={analysis.chargeLaa > 0 ? "the rest stayed in the pot and the line" : "no charge recorded"}
          />
        </div>

        {analysis.findings.length > 0 && (
          <ul className="space-y-2">
            {analysis.findings.map((f, i) => (
              <FindingRow key={`${f.code}-${i}`} finding={f} />
            ))}
          </ul>
        )}
      </div>
    </section>
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

function FindingRow({ finding }: { finding: CutFinding }) {
  const tone = severityTone(finding.severity);
  return (
    <li className={`rounded-md border border-l-4 px-3 py-2 ${tone.box}`}>
      <p className={`text-sm font-medium ${tone.title}`}>{finding.title}</p>
      {finding.detail && <p className="mt-0.5 text-xs text-fg-muted">{finding.detail}</p>}
    </li>
  );
}

function severityTone(s: CutFindingSeverity): { box: string; title: string } {
  switch (s) {
    case CutFindingSeverity.PROBLEM:
      return { box: "border-danger/40 border-l-danger bg-danger/10", title: "text-danger-fg" };
    case CutFindingSeverity.WARNING:
      return { box: "border-warning/40 border-l-warning bg-warning/10", title: "text-warning-fg" };
    default:
      return { box: "border-border border-l-border-strong bg-surface-3/50", title: "text-fg" };
  }
}
