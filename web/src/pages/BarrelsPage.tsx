import { FormEvent, useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { Shell } from "@/components/Shell";
import { barrelClient } from "@/lib/clients";
import { CreateBarrelRequestSchema } from "@/gen/stillhouse/v1/barrel_pb";
import { formatLAA, formatQty } from "@/lib/format";

export function BarrelsPage() {
  const qc = useQueryClient();
  const list = useQuery({
    queryKey: ["listBarrels"],
    queryFn: () => barrelClient.listBarrels({}),
  });
  const [showForm, setShowForm] = useState(false);

  const createBarrel = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof CreateBarrelRequestSchema>>) =>
      barrelClient.createBarrel(msg),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["listBarrels"] });
      qc.invalidateQueries({ queryKey: ["listBulkContainers"] });
      setShowForm(false);
    },
  });

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const fd = new FormData(e.currentTarget);
    const capRaw = fd.get("capacity_l")?.toString().trim() ?? "";
    const charRaw = fd.get("char_level")?.toString().trim() ?? "";
    createBarrel.mutate(
      create(CreateBarrelRequestSchema, {
        name: fd.get("name")?.toString() ?? "",
        capacityL: capRaw ? Number(capRaw) : 0,
        capacityLSet: !!capRaw,
        cooperageSupplier: fd.get("cooperage_supplier")?.toString() ?? "",
        charLevel: charRaw ? Number(charRaw) : 0,
        charLevelSet: !!charRaw,
        woodSpecies: fd.get("wood_species")?.toString() ?? "",
        priorUse: fd.get("prior_use")?.toString() ?? "",
        serialBurnin: fd.get("serial_burnin")?.toString() ?? "",
        rickhouse: fd.get("rickhouse")?.toString() ?? "",
        rowPosition: fd.get("row_position")?.toString() ?? "",
        levelPosition: fd.get("level_position")?.toString() ?? "",
        columnPosition: fd.get("column_position")?.toString() ?? "",
        location: fd.get("location")?.toString() ?? "",
        notes: fd.get("notes")?.toString() ?? "",
      }),
    );
  }

  const summary = list.data;

  return (
    <Shell>
      <div className="mb-6 flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">Barrels</h1>
          <p className="text-sm text-stone-500">
            Cooperage inventory + maturation. Canadian Whisky eligibility (FDR B.02.020):
            ≥3 years in small wood (≤700 L).
          </p>
        </div>
        <button
          onClick={() => setShowForm((s) => !s)}
          className="rounded bg-stone-900 px-3 py-2 text-sm font-medium text-white hover:bg-stone-800"
        >
          {showForm ? "Cancel" : "Add barrel"}
        </button>
      </div>

      {summary && (
        <section className="mb-6 grid grid-cols-4 gap-4">
          <Stat label="Total barrels" value={String(summary.totalCount)} />
          <Stat label="Aging" value={String(summary.agingCount)} />
          <Stat label="CW eligible" value={String(summary.eligibleCount)} highlight />
          <Stat label="Total LAA" value={`${formatLAA(summary.totalLaa)} L`} />
        </section>
      )}

      {summary && summary.barrels.length > 0 && (
        <AgingBuckets barrels={summary.barrels} />
      )}

      {showForm && (
        <form
          onSubmit={submit}
          className="mb-6 grid grid-cols-3 gap-4 rounded-lg border border-stone-200 bg-white p-5 shadow-sm"
        >
          <FormField name="name" label="Name / serial" required />
          <FormField name="capacity_l" label="Capacity (L)" type="number" step="0.1" placeholder="200" />
          <FormField name="char_level" label="Char level (1–4)" type="number" min="1" max="4" />
          <FormField name="cooperage_supplier" label="Cooperage supplier" />
          <FormField name="wood_species" label="Wood species" placeholder="American oak" />
          <FormField name="prior_use" label="Prior use" placeholder="new / ex-bourbon / ex-wine" />
          <FormField name="serial_burnin" label="Burnin serial" />
          <FormField name="rickhouse" label="Rickhouse" />
          <FormField name="location" label="Location" />
          <FormField name="row_position" label="Row" />
          <FormField name="level_position" label="Level" />
          <FormField name="column_position" label="Column" />
          <FormField name="notes" label="Notes" className="col-span-3" />
          <div className="col-span-3 flex items-center gap-3">
            <button
              type="submit"
              disabled={createBarrel.isPending}
              className="rounded bg-stone-900 px-3 py-2 text-sm font-medium text-white hover:bg-stone-800 disabled:bg-stone-400"
            >
              {createBarrel.isPending ? "Saving…" : "Save barrel"}
            </button>
            {createBarrel.error && (
              <span className="text-sm text-red-600">
                {createBarrel.error instanceof ConnectError
                  ? createBarrel.error.rawMessage
                  : String(createBarrel.error)}
              </span>
            )}
          </div>
        </form>
      )}

      <div className="overflow-hidden rounded-lg border border-stone-200 bg-white shadow-sm">
        <table className="min-w-full divide-y divide-stone-200 text-sm">
          <thead className="bg-stone-50 text-left text-xs uppercase text-stone-500">
            <tr>
              <th className="px-4 py-3">Name</th>
              <th className="px-4 py-3">Cooperage</th>
              <th className="px-4 py-3">Cap. (L)</th>
              <th className="px-4 py-3">Filled</th>
              <th className="px-4 py-3 text-right">Days aged</th>
              <th className="px-4 py-3 text-right">Vol (L)</th>
              <th className="px-4 py-3 text-right">ABV</th>
              <th className="px-4 py-3 text-right">LAA</th>
              <th className="px-4 py-3">Status</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-stone-100">
            {list.isLoading && (
              <tr><td colSpan={9} className="px-4 py-3 text-stone-500">Loading…</td></tr>
            )}
            {!list.isLoading && summary && summary.barrels.length === 0 && (
              <tr><td colSpan={9} className="px-4 py-3 text-stone-500">No barrels yet.</td></tr>
            )}
            {summary?.barrels.map((b) => (
              <tr key={b.id}>
                <td className="px-4 py-3">
                  <Link to={`/barrels/${b.id}`} className="font-medium text-stone-900 hover:underline">{b.name}</Link>
                  {b.serialBurnin && <span className="ml-2 text-xs text-stone-500">#{b.serialBurnin}</span>}
                </td>
                <td className="px-4 py-3 text-stone-600">
                  {b.cooperageSupplier || "—"}
                  {b.priorUse && <span className="ml-1 text-xs text-stone-500">({b.priorUse})</span>}
                </td>
                <td className="px-4 py-3 text-stone-600">{b.capacityLSet ? formatQty(b.capacityL) : "—"}</td>
                <td className="px-4 py-3 text-stone-600">{b.fillDate || "—"}</td>
                <td className="px-4 py-3 text-right text-stone-600">{b.daysAged || "—"}</td>
                <td className="px-4 py-3 text-right text-stone-600">{formatQty(b.currentVolumeL)}</td>
                <td className="px-4 py-3 text-right text-stone-600">
                  {b.currentAbvPctSet ? b.currentAbvPct.toFixed(2) + "%" : "—"}
                </td>
                <td className="px-4 py-3 text-right font-medium text-stone-900">{formatLAA(b.currentLaa)}</td>
                <td className="px-4 py-3">
                  <MaturationBadge barrel={b} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Shell>
  );
}

const buckets = [
  { label: "0–90 days", min: 1, max: 90 },
  { label: "90 days–1 yr", min: 91, max: 365 },
  { label: "1–2 yrs", min: 366, max: 730 },
  { label: "2–3 yrs", min: 731, max: 1094 },
  { label: "CW eligible (≥3 yrs, small wood)", min: 1095, max: Infinity },
];

function AgingBuckets({ barrels }: { barrels: { daysAged: number; currentLaa: number; canadianWhiskyEligible: boolean; smallWood: boolean }[] }) {
  const stats = buckets.map((b) => {
    const matching = barrels.filter((br) => {
      if (br.daysAged < b.min || br.daysAged > b.max) return false;
      // The "CW eligible" bucket only counts barrels that ARE eligible
      // (small wood AND aged enough); a non-small-wood barrel ≥3 yrs
      // counts in 2-3 yrs even when daysAged > 1094.
      if (b.min >= 1095) return br.canadianWhiskyEligible;
      return true;
    });
    return { ...b, count: matching.length, laa: matching.reduce((s, m) => s + m.currentLaa, 0) };
  });
  return (
    <section className="mb-6 rounded-lg border border-stone-200 bg-white p-5 shadow-sm">
      <h2 className="mb-3 text-sm font-semibold uppercase text-stone-500">Aging buckets</h2>
      <div className="grid grid-cols-5 gap-3">
        {stats.map((s) => (
          <div key={s.label} className={`rounded border p-3 ${s.min >= 1095 ? "border-emerald-200 bg-emerald-50" : "border-stone-200"}`}>
            <p className="text-xs uppercase text-stone-500">{s.label}</p>
            <p className={`mt-1 text-lg font-semibold ${s.min >= 1095 ? "text-emerald-700" : "text-stone-900"}`}>
              {s.count}
              {s.laa > 0 && (
                <span className="ml-2 text-xs font-normal text-stone-500">{formatLAA(s.laa)} L LAA</span>
              )}
            </p>
          </div>
        ))}
      </div>
    </section>
  );
}

function MaturationBadge({ barrel }: { barrel: { daysAged: number; canadianWhiskyEligible: boolean; smallWood: boolean; daysToCanadianWhiskyEligible: number; currentVolumeL: number } }) {
  if (barrel.currentVolumeL === 0 && barrel.daysAged === 0) {
    return <span className="rounded bg-stone-100 px-2 py-0.5 text-xs text-stone-600">Empty</span>;
  }
  if (barrel.canadianWhiskyEligible) {
    return <span className="rounded bg-emerald-100 px-2 py-0.5 text-xs text-emerald-700">CW eligible</span>;
  }
  if (!barrel.smallWood) {
    return <span className="rounded bg-amber-100 px-2 py-0.5 text-xs text-amber-700">Aging (not small wood)</span>;
  }
  return (
    <span className="rounded bg-blue-100 px-2 py-0.5 text-xs text-blue-700">
      Aging ({barrel.daysToCanadianWhiskyEligible} d to CW)
    </span>
  );
}

function Stat({ label, value, highlight }: { label: string; value: string; highlight?: boolean }) {
  return (
    <div className={`rounded-lg border bg-white p-4 shadow-sm ${highlight ? "border-emerald-200" : "border-stone-200"}`}>
      <p className="text-xs uppercase text-stone-500">{label}</p>
      <p className={`mt-1 text-xl font-semibold ${highlight ? "text-emerald-700" : "text-stone-900"}`}>{value}</p>
    </div>
  );
}

function FormField({
  name,
  label,
  type = "text",
  step,
  min,
  max,
  placeholder,
  required,
  className,
}: {
  name: string;
  label: string;
  type?: string;
  step?: string;
  min?: string;
  max?: string;
  placeholder?: string;
  required?: boolean;
  className?: string;
}) {
  return (
    <div className={className}>
      <label className="mb-1 block text-xs font-medium text-stone-600">{label}</label>
      <input
        name={name}
        type={type}
        step={step}
        min={min}
        max={max}
        placeholder={placeholder}
        required={required}
        className="w-full rounded border border-stone-300 px-3 py-2 text-sm focus:border-stone-500 focus:outline-none"
      />
    </div>
  );
}
