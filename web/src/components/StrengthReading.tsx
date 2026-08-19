import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";

import { Callout } from "@/components/Callout";
import { alcoholometryClient } from "@/lib/clients";
import { StrengthSource } from "@/gen/stillhouse/v1/alcoholometry_pb";
import { formatLAA, formatQty } from "@/lib/format";

/**
 * StrengthReading — the measurement trio every gauging form needs:
 * volume, temperature, and a strength (either a raw hydrometer indication
 * or a strength the instrument already expressed at 20 °C).
 *
 * Alcoholic strength only means something at a reference temperature, and
 * for Canadian excise that reference is 20 °C. This control makes the
 * correction visible while the operator is still standing at the tank:
 * type the reading, see what actually lands in the ledger, and see which
 * of the three determination paths it took.
 *
 * The tables themselves stay on the server — the browser asks
 * AlcoholometryService.ResolveStrength rather than shipping 700 KB of
 * lookup table, which also means the preview can never disagree with what
 * gets written.
 */

export const REFERENCE_TEMPERATURE_C = 20;

/** How far off 20 °C a reading can sit before an uncorrected figure is worth warning about. */
const UNCORRECTED_WARN_DELTA_C = 2;

export type StrengthMode = "density" | "abv";

export type StrengthReadingValue = {
  volumeL: string;
  tempC: string;
  mode: StrengthMode;
  /** Hydrometer indication, kg/m³. Used when mode === "density". */
  density: string;
  /** Strength at 20 °C. Used when mode === "abv". */
  abv: string;
};

export function emptyReading(overrides: Partial<StrengthReadingValue> = {}): StrengthReadingValue {
  return { volumeL: "", tempC: "", mode: "abv", density: "", abv: "", ...overrides };
}

/**
 * readingToRequest maps the control's state onto the wire fields shared by
 * RecordProductionGauge / FillBarrel / DumpBarrel / RegaugeBarrel.
 *
 * Note the asymmetry: an abv typed with no temperature goes up as a plain
 * uncorrected strength, which the server records as such. That is
 * deliberate — a distiller without a thermometer reading should not have a
 * correction invented for them.
 */
export function readingToRequest(v: StrengthReadingValue): {
  abvPct: number;
  densityKgM3: number;
  densityKgM3Set: boolean;
  temperatureC: number;
  temperatureCSet: boolean;
} {
  const tempSet = v.tempC.trim() !== "" && !Number.isNaN(Number(v.tempC));
  const densitySet = v.mode === "density" && v.density.trim() !== "" && !Number.isNaN(Number(v.density));
  return {
    abvPct: v.mode === "abv" ? Number(v.abv) || 0 : 0,
    densityKgM3: densitySet ? Number(v.density) : 0,
    densityKgM3Set: densitySet,
    temperatureC: tempSet ? Number(v.tempC) : 0,
    temperatureCSet: tempSet,
  };
}

export function StrengthReading({
  value,
  onChange,
  volumeLabel = "Volume (L, as gauged)",
}: {
  value: StrengthReadingValue;
  onChange: (v: StrengthReadingValue) => void;
  volumeLabel?: string;
}) {
  const set = (patch: Partial<StrengthReadingValue>) => onChange({ ...value, ...patch });
  const resolved = useResolvedStrength(value);

  const req = readingToRequest(value);
  const uncorrected = !req.temperatureCSet && value.mode === "abv" && value.abv.trim() !== "";

  return (
    <div className="space-y-3">
      <NumField label={volumeLabel} value={value.volumeL} onChange={(volumeL) => set({ volumeL })} />

      <div>
        <div className="mb-1 flex items-center justify-between">
          <label className="text-xs text-fg-muted">Strength</label>
          <ModeToggle mode={value.mode} onChange={(mode) => set({ mode })} />
        </div>
        {value.mode === "density" ? (
          <NumField
            label=""
            placeholder="e.g. 922.6"
            step="0.1"
            value={value.density}
            onChange={(density) => set({ density })}
            suffix="kg/m³"
          />
        ) : (
          <NumField
            label=""
            placeholder="e.g. 53.7"
            value={value.abv}
            onChange={(abv) => set({ abv })}
            suffix="% at 20 °C"
          />
        )}
      </div>

      <NumField
        label="Temperature at reading"
        placeholder={value.mode === "density" ? "required" : "optional but recommended"}
        step="0.1"
        value={value.tempC}
        onChange={(tempC) => set({ tempC })}
        suffix="°C"
      />

      {resolved.error && <Callout tone="danger">{resolved.error}</Callout>}

      {resolved.data && !resolved.error && (
        <CorrectionPreview
          observedVolumeL={Number(value.volumeL) || 0}
          strengthPct={resolved.data.reading?.strengthPct20c ?? 0}
          volumeFactorC={resolved.data.reading?.volumeFactorC ?? 1}
          volumeL20C={resolved.data.volumeL20c}
          laa={resolved.data.laa}
          source={resolved.data.source}
        />
      )}

      {uncorrected && (
        <Callout tone="warning">
          No temperature recorded — this strength goes in the ledger exactly as typed. Strength is
          only defined at 20 °C, so an uncorrected reading taken more than {UNCORRECTED_WARN_DELTA_C} °C
          off will carry through to your B266.
        </Callout>
      )}
    </div>
  );
}

/** Debounced ResolveStrength lookup. Idle until the inputs are usable. */
function useResolvedStrength(v: StrengthReadingValue) {
  const [debounced, setDebounced] = useState(v);
  useEffect(() => {
    const t = setTimeout(() => setDebounced(v), 250);
    return () => clearTimeout(t);
  }, [v.volumeL, v.tempC, v.mode, v.density, v.abv]);

  const req = readingToRequest(debounced);
  const volumeL = Number(debounced.volumeL);
  const hasVolume = debounced.volumeL.trim() !== "" && !Number.isNaN(volumeL) && volumeL > 0;
  // Nothing to preview until there is a temperature — with no temperature
  // there is no correction to show.
  const enabled =
    req.temperatureCSet && (req.densityKgM3Set || (debounced.mode === "abv" && debounced.abv.trim() !== ""));

  const q = useQuery({
    queryKey: ["resolveStrength", req.temperatureC, req.densityKgM3, req.densityKgM3Set, req.abvPct, hasVolume ? volumeL : 0],
    queryFn: () =>
      alcoholometryClient.resolveStrength({
        temperatureC: req.temperatureC,
        densityKgM3: req.densityKgM3,
        densityKgM3Set: req.densityKgM3Set,
        strengthPct20c: req.abvPct,
        strengthPct20cSet: !req.densityKgM3Set,
        observedVolumeL: hasVolume ? volumeL : 0,
        observedVolumeLSet: hasVolume,
      }),
    enabled,
    retry: false,
    staleTime: 5 * 60 * 1000,
  });

  return {
    data: q.data,
    error: q.error ? readableError(q.error) : null,
  };
}

function readableError(e: unknown): string {
  const msg = e instanceof Error ? e.message : String(e);
  // Connect prefixes the code; the operator only needs the sentence.
  return msg.replace(/^\[[a-z_]+\]\s*/, "");
}

/**
 * CorrectionPreview reads like an instrument readout: what you measured on
 * the left, what lands in the ledger on the right. When the two agree
 * (a reading taken at 20 °C) it says so rather than showing a redundant
 * arrow.
 */
function CorrectionPreview({
  observedVolumeL,
  strengthPct,
  volumeFactorC,
  volumeL20C,
  laa,
  source,
}: {
  observedVolumeL: number;
  strengthPct: number;
  volumeFactorC: number;
  volumeL20C: number;
  laa: number;
  source: StrengthSource;
}) {
  const moved = Math.abs(volumeFactorC - 1) > 1e-9;
  return (
    <div className="rounded-md border border-border bg-surface-3/60 p-3">
      <div className="mb-2 flex items-center justify-between gap-2">
        <span className="text-xs font-medium text-fg-muted">At 20 °C</span>
        <SourceBadge source={source} />
      </div>
      <dl className="space-y-1 text-sm tabular-nums">
        <Line
          k="Strength"
          v={`${strengthPct.toFixed(1)} %`}
          emphasis
        />
        {observedVolumeL > 0 && (
          <Line
            k="Volume"
            v={
              moved ? (
                <>
                  <span className="text-fg-subtle line-through">{formatQty(observedVolumeL)}</span>
                  {" → "}
                  <span className="text-fg">{formatQty(volumeL20C)} L</span>
                  <span className="ml-1 text-xs text-fg-subtle">(×{volumeFactorC.toFixed(4)})</span>
                </>
              ) : (
                `${formatQty(volumeL20C)} L`
              )
            }
          />
        )}
        {observedVolumeL > 0 && <Line k="LAA" v={`${formatLAA(laa)} L`} emphasis />}
      </dl>
    </div>
  );
}

function Line({ k, v, emphasis }: { k: string; v: React.ReactNode; emphasis?: boolean }) {
  return (
    <div className="flex items-baseline justify-between gap-3">
      <dt className="text-xs text-fg-muted">{k}</dt>
      <dd className={emphasis ? "font-semibold text-fg" : "text-fg-muted"}>{v}</dd>
    </div>
  );
}

/**
 * SourceBadge names the determination path. The distinction is not
 * cosmetic — only the density path is a determination against the
 * published tables using CRA's approved instrument.
 */
export function SourceBadge({ source }: { source: StrengthSource }) {
  const { label, tone, title } = sourceMeta(source);
  return (
    <span
      title={title}
      className={`rounded px-1.5 py-0.5 text-[11px] font-medium ${tone}`}
    >
      {label}
    </span>
  );
}

function sourceMeta(source: StrengthSource): { label: string; tone: string; title: string } {
  switch (source) {
    case StrengthSource.TABLE_DENSITY:
      return {
        label: "Tables · density",
        tone: "bg-success/15 text-success-fg",
        title:
          "Hydrometer indication + temperature resolved through the Canadian Alcoholometric Tables 1980. CRA's approved determination.",
      };
    case StrengthSource.TABLE_STRENGTH:
      return {
        label: "Tables · volume only",
        tone: "bg-info/15 text-info-fg",
        title:
          "Strength taken as already expressed at 20 °C by the instrument; the tables supplied the volume factor for the measurement temperature.",
      };
    default:
      return {
        label: "Uncorrected",
        tone: "bg-warning/15 text-warning-fg",
        title: "No temperature recorded — the figure is stored exactly as entered.",
      };
  }
}

function ModeToggle({ mode, onChange }: { mode: StrengthMode; onChange: (m: StrengthMode) => void }) {
  return (
    <div className="flex overflow-hidden rounded border border-border-strong text-[11px]">
      {(
        [
          ["density", "Hydrometer"],
          ["abv", "% ABV"],
        ] as const
      ).map(([m, label]) => (
        <button
          key={m}
          type="button"
          onClick={() => onChange(m)}
          className={`px-2 py-0.5 transition-colors ${
            mode === m ? "bg-accent text-accent-fg font-medium" : "text-fg-muted hover:bg-surface-3 hover:text-fg"
          }`}
        >
          {label}
        </button>
      ))}
    </div>
  );
}

function NumField({
  label,
  value,
  onChange,
  step = "0.01",
  placeholder,
  suffix,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  step?: string;
  placeholder?: string;
  suffix?: string;
}) {
  return (
    <div>
      {label && <label className="mb-1 block text-xs text-fg-muted">{label}</label>}
      <div className="relative">
        <input
          type="number"
          step={step}
          placeholder={placeholder}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          className={`w-full rounded border border-border-strong px-3 py-2 text-sm tabular-nums ${
            suffix ? "pr-20" : ""
          }`}
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
