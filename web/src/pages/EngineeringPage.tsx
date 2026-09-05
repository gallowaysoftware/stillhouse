import { FormEvent, useEffect, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { Button } from "@/components/Button";
import { PlanCurve } from "@/components/PlanCurve";
import { Shell } from "@/components/Shell";
import { engineeringClient } from "@/lib/clients";
import {
  PlanRunRequestSchema,
  StillGeometry,
  StillHeatingKind,
  StillSpecSchema,
  type RunPlan,
  type StillSpec,
} from "@/gen/stillhouse/v1/engineering_pb";

/**
 * EngineeringPage — the flow chart of the distillery, node by node. This is
 * the first node: the still, and the run planner behind it. A plan is a
 * calculation from a still's specification, or from its calibration once it
 * has one, and it says which on its face.
 */
export function EngineeringPage() {
  const presets = useQuery({
    queryKey: ["listStillPresets"],
    queryFn: () => engineeringClient.listStillPresets({}),
  });

  const [still, setStill] = useState<StillSpec | null>(null);
  const [volume, setVolume] = useState("150");
  const [abv, setAbv] = useState("25");
  const [chargeTemp, setChargeTemp] = useState("18");
  const [firing, setFiring] = useState("60");
  const [reflux, setReflux] = useState("3");
  const [cuts, setCuts] = useState("75, 65, 48");
  const [stop, setStop] = useState("10");

  useEffect(() => {
    if (!still && presets.data?.presets.length) setStill(presets.data.presets[0]);
  }, [presets.data, still]);

  const plan = useMutation({
    mutationFn: (req: ReturnType<typeof create<typeof PlanRunRequestSchema>>) => engineeringClient.planRun(req),
  });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!still) return;
    plan.mutate(
      create(PlanRunRequestSchema, {
        still,
        charge: { volumeL: Number(volume), abvPct: Number(abv), temperatureC: Number(chargeTemp) },
        settings: {
          firingPct: Number(firing),
          refluxRatio: still.dephlegmator ? Number(reflux) : 0,
          cutStrengthsPct: cuts.split(",").map((c) => Number(c.trim())).filter((c) => !Number.isNaN(c) && c > 0),
          stopDistillateAbvPct: Number(stop),
        },
      }),
    );
  };

  const p = plan.data?.plan;

  return (
    <Shell>
      <div className="mb-6">
        <h1 className="text-3xl font-bold tracking-tight">Engineering</h1>
        <p className="text-sm text-fg-muted">
          The still, and what it will do with a charge before you charge it. Strength and time; the cut by nose stays yours.
        </p>
      </div>

      <form onSubmit={submit} className="mb-6 rounded-lg border border-border bg-surface-2 p-5 shadow-sm">
        <div className="mb-4 flex flex-wrap items-end gap-3">
          <div>
            <label className="mb-1 block text-xs text-fg-muted">Still</label>
            <select
              value={still?.name ?? ""}
              onChange={(e) => setStill(presets.data?.presets.find((s) => s.name === e.target.value) ?? null)}
              className="rounded border border-border-strong px-2 py-1.5 text-sm"
            >
              {presets.data?.presets.map((s) => (
                <option key={s.name} value={s.name}>{s.name}</option>
              ))}
            </select>
          </div>
          {still && <StillFields still={still} onChange={setStill} />}
        </div>
        <div className="flex flex-wrap items-end gap-3">
          <Field label="Charge L" value={volume} onChange={setVolume} step="1" />
          <Field label="Charge %" value={abv} onChange={setAbv} step="0.1" />
          <Field label="Charge °C" value={chargeTemp} onChange={setChargeTemp} step="1" />
          <Field label="Run heat %" value={firing} onChange={setFiring} step="1" hint="heat-up is at full" />
          {still?.dephlegmator && <Field label="Reflux ratio" value={reflux} onChange={setReflux} step="0.5" />}
          <div className="w-40">
            <label className="mb-1 block text-xs text-fg-muted">Cuts at %</label>
            <input value={cuts} onChange={(e) => setCuts(e.target.value)} className="w-full rounded border border-border-strong px-2 py-1.5 text-sm" />
          </div>
          <Field label="Stop at %" value={stop} onChange={setStop} step="1" />
          <Button type="submit" disabled={plan.isPending || !still}>{plan.isPending ? "Planning…" : "Plan the run"}</Button>
          {plan.error ? (
            <span className="text-sm text-danger-fg">
              {plan.error instanceof ConnectError ? plan.error.rawMessage : String(plan.error)}
            </span>
          ) : null}
        </div>
      </form>

      {p && <PlanSummary plan={p} />}
      {p && (
        <PlanCurve
          planned={p.points.map((pt) => ({ h: Number(pt.atS) / 3600, v: pt.distillateAbvPct }))}
          collected={p.points.map((pt) => ({ h: Number(pt.atS) / 3600, v: pt.collectedLaa }))}
          cuts={p.cuts.map((c) => ({ abv: c.abvPct, h: Number(c.atS) / 3600, share: c.shareOfCharge }))}
          totalHours={Number(p.runTimeS) / 3600}
          basis={p.basis}
        />
      )}
    </Shell>
  );
}

function StillFields({ still, onChange }: { still: StillSpec; onChange: (s: StillSpec) => void }) {
  const set = (patch: Partial<StillSpec>) => onChange(create(StillSpecSchema, { ...still, ...patch }));
  return (
    <>
      <Field label="Pot L" value={String(still.potVolumeL)} onChange={(v) => set({ potVolumeL: Number(v) })} step="1" />
      <Field label="Heat W" value={String(still.ratedW)} onChange={(v) => set({ ratedW: Number(v) })} step="100" />
      <div>
        <label className="mb-1 block text-xs text-fg-muted">Heating</label>
        <select
          value={still.heatingKind}
          onChange={(e) => set({ heatingKind: Number(e.target.value) as StillHeatingKind })}
          className="rounded border border-border-strong px-2 py-1.5 text-sm"
        >
          <option value={StillHeatingKind.ELECTRIC}>Electric elements</option>
          <option value={StillHeatingKind.STEAM}>Steam jacket</option>
        </select>
      </div>
      <div>
        <label className="mb-1 block text-xs text-fg-muted">Shape</label>
        <select
          value={still.geometry}
          onChange={(e) => set({ geometry: Number(e.target.value) as StillGeometry })}
          className="rounded border border-border-strong px-2 py-1.5 text-sm"
        >
          <option value={StillGeometry.SQUAT}>Squat</option>
          <option value={StillGeometry.STANDARD}>Standard</option>
          <option value={StillGeometry.TALL}>Tall</option>
        </select>
      </div>
      <Field label="Plates" value={String(still.plates)} onChange={(v) => set({ plates: Number(v) })} step="1" />
      <label className="flex items-center gap-1 pb-2 text-sm">
        <input type="checkbox" checked={still.dephlegmator} onChange={(e) => set({ dephlegmator: e.target.checked })} />
        Dephlegmator
      </label>
      <label className="flex items-center gap-1 pb-2 text-sm">
        <input type="checkbox" checked={still.boilBall} onChange={(e) => set({ boilBall: e.target.checked })} />
        Boil ball
      </label>
    </>
  );
}

function PlanSummary({ plan }: { plan: RunPlan }) {
  const hm = (s: bigint | number) => {
    const n = Number(s);
    return `${Math.floor(n / 3600)} h ${Math.round((n % 3600) / 60)} min`;
  };
  return (
    <section className="mb-6 rounded-lg border border-border bg-surface-2 p-5 shadow-sm">
      <div className="mb-3 grid grid-cols-2 gap-4 sm:grid-cols-5">
        <Stat label="Charge" value={`${plan.chargeLaa.toFixed(1)} LAA`} />
        <Stat label="First drop" value={hm(plan.timeToFirstDropS)} />
        <Stat label="Run" value={hm(plan.runTimeS)} />
        <Stat label="Collected" value={`${plan.collectedLaa.toFixed(1)} LAA · ${(plan.shareOfCharge * 100).toFixed(0)} %`} />
        <Stat label="Electricity" value={`${plan.energyKwh.toFixed(1)} kWh`} />
      </div>
      <table className="mb-3 min-w-full text-sm">
        <thead className="text-left text-xs text-fg-muted">
          <tr>
            <th className="py-1 pr-4">Distillate passes</th>
            <th className="py-1 pr-4">At</th>
            <th className="py-1 pr-4 text-right">Collected</th>
            <th className="py-1 text-right">Of the charge</th>
          </tr>
        </thead>
        <tbody>
          {plan.cuts.map((c) => (
            <tr key={c.abvPct} className="border-t border-border">
              <td className="py-1 pr-4">{c.abvPct.toFixed(0)} %</td>
              <td className="py-1 pr-4">{hm(c.atS)}</td>
              <td className="py-1 pr-4 text-right">{c.collectedLaa.toFixed(1)} LAA</td>
              <td className="py-1 text-right">{(c.shareOfCharge * 100).toFixed(0)} %</td>
            </tr>
          ))}
        </tbody>
      </table>
      <p className="text-xs text-fg-subtle">
        {plan.stages.toFixed(2)} effective stages · {plan.basis}
      </p>
      {plan.directions.map((d) => (
        <p key={d} className="mt-1 text-xs text-fg-subtle">{d}</p>
      ))}
    </section>
  );
}

function Field({ label, value, onChange, step, hint }: { label: string; value: string; onChange: (v: string) => void; step: string; hint?: string }) {
  return (
    <div className="w-28">
      <label className="mb-1 block text-xs text-fg-muted">{label}</label>
      <input type="number" step={step} value={value} onChange={(e) => onChange(e.target.value)}
             className="w-full rounded border border-border-strong px-2 py-1.5 text-sm" />
      {hint && <span className="text-[10px] text-fg-subtle">{hint}</span>}
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-xs text-fg-muted">{label}</div>
      <div className="text-lg font-semibold">{value}</div>
    </div>
  );
}
