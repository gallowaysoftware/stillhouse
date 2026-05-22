import { FormEvent, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { Shell } from "@/components/Shell";
import { fermentationClient, mashClient, materialClient } from "@/lib/clients";
import {
  AddMashIngredientRequestSchema,
  AddMashMetricRequestSchema,
  MashMetricKind,
  MashStatus,
  UpdateMashStatusRequestSchema,
} from "@/gen/stillhouse/v1/mash_pb";
import { CreateFermentationRunRequestSchema } from "@/gen/stillhouse/v1/fermentation_pb";
import {
  fermentationStatusLabel,
  formatQty,
  mashMetricKindLabel,
  mashStatusLabel,
} from "@/lib/format";

const metricKindOptions = [
  { value: MashMetricKind.ORIGINAL_GRAVITY, label: "Original gravity", unit: "" },
  { value: MashMetricKind.MASH_PH, label: "Mash pH", unit: "" },
  { value: MashMetricKind.MASH_TEMP_C, label: "Mash temp (°C)", unit: "°C" },
  { value: MashMetricKind.WATER_VOLUME_L, label: "Water volume (L)", unit: "L" },
  { value: MashMetricKind.STRIKE_TEMP_C, label: "Strike temp (°C)", unit: "°C" },
  { value: MashMetricKind.OTHER, label: "Other", unit: "" },
];

const statusOptions = [
  MashStatus.PLANNED,
  MashStatus.IN_PROGRESS,
  MashStatus.FERMENTING,
  MashStatus.DISTILLED,
  MashStatus.CANCELLED,
];

export function MashDetailPage() {
  const { id } = useParams();
  const qc = useQueryClient();

  const mash = useQuery({
    queryKey: ["getMashRun", id],
    queryFn: () => mashClient.getMashRun({ id: id! }),
    enabled: !!id,
  });
  const ferments = useQuery({
    queryKey: ["listFermentationRuns", "mash", id],
    queryFn: () => fermentationClient.listFermentationRuns({ mashRunId: id! }),
    enabled: !!id,
  });
  const materials = useQuery({
    queryKey: ["listMaterials"],
    queryFn: () => materialClient.listMaterials({}),
  });

  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["getMashRun", id] });
    qc.invalidateQueries({ queryKey: ["listFermentationRuns", "mash", id] });
  };

  const addIngredient = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof AddMashIngredientRequestSchema>>) =>
      mashClient.addMashIngredient(msg),
    onSuccess: refresh,
  });
  const addMetric = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof AddMashMetricRequestSchema>>) =>
      mashClient.addMashMetric(msg),
    onSuccess: refresh,
  });
  const updateStatus = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof UpdateMashStatusRequestSchema>>) =>
      mashClient.updateMashStatus(msg),
    onSuccess: refresh,
  });
  const createFerment = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof CreateFermentationRunRequestSchema>>) =>
      fermentationClient.createFermentationRun(msg),
    onSuccess: refresh,
  });

  if (!id) return <Shell><p>Missing mash id.</p></Shell>;
  if (mash.isLoading) return <Shell><p className="text-stone-500">Loading…</p></Shell>;
  if (!mash.data?.mashRun) return <Shell><p>Mash not found.</p></Shell>;

  const m = mash.data.mashRun;

  return (
    <Shell>
      <header className="mb-6 flex items-start justify-between">
        <div>
          <h1 className="text-2xl font-semibold">Mash #{m.mashNo}</h1>
          <p className="text-sm text-stone-500">
            {m.mashDate} · {m.recipeName} v{m.recipeVersionNo}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <label className="text-xs text-stone-500">Status</label>
          <select
            value={m.status}
            onChange={(e) =>
              updateStatus.mutate(
                create(UpdateMashStatusRequestSchema, {
                  id: m.id,
                  status: Number(e.target.value) as MashStatus,
                }),
              )
            }
            className="rounded border border-stone-300 px-2 py-1 text-sm"
          >
            {statusOptions.map((s) => (
              <option key={s} value={s}>
                {mashStatusLabel(s)}
              </option>
            ))}
          </select>
        </div>
      </header>

      {m.notes && <p className="mb-6 rounded bg-white p-4 text-sm text-stone-700 shadow-sm">{m.notes}</p>}

      <section className="mb-8 grid grid-cols-1 gap-6 lg:grid-cols-2">
        <Panel
          title="Ingredients used"
          right={
            <InlineForm
              fields={[
                {
                  name: "material_id",
                  label: "Material",
                  type: "select",
                  options: (materials.data?.materials ?? []).map((mat) => ({
                    value: mat.id,
                    label: mat.name,
                  })),
                  required: true,
                },
                { name: "quantity_used", label: "Qty", type: "number", required: true, step: "0.01" },
                { name: "uom", label: "UoM", type: "text", defaultValue: "kg", required: true },
              ]}
              submitting={addIngredient.isPending}
              error={addIngredient.error}
              onSubmit={(values) =>
                addIngredient.mutate(
                  create(AddMashIngredientRequestSchema, {
                    mashRunId: m.id,
                    materialId: values.material_id,
                    quantityUsed: Number(values.quantity_used),
                    uom: values.uom,
                  }),
                )
              }
            />
          }
        >
          <table className="min-w-full divide-y divide-stone-200 text-sm">
            <thead className="text-left text-xs uppercase text-stone-500">
              <tr>
                <th className="px-3 py-2">Material</th>
                <th className="px-3 py-2 text-right">Qty</th>
                <th className="px-3 py-2">UoM</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-stone-100">
              {m.ingredients.length === 0 && (
                <tr>
                  <td colSpan={3} className="px-3 py-2 text-stone-500">
                    None recorded.
                  </td>
                </tr>
              )}
              {m.ingredients.map((ing) => (
                <tr key={ing.id}>
                  <td className="px-3 py-2 text-stone-900">{ing.materialName}</td>
                  <td className="px-3 py-2 text-right text-stone-600">{formatQty(ing.quantityUsed)}</td>
                  <td className="px-3 py-2 text-stone-600">{ing.uom}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </Panel>

        <Panel
          title="Metrics"
          right={
            <InlineForm
              fields={[
                {
                  name: "kind",
                  label: "Kind",
                  type: "select",
                  options: metricKindOptions.map((k) => ({
                    value: String(k.value),
                    label: k.label,
                  })),
                  required: true,
                },
                { name: "value", label: "Value", type: "number", step: "0.001", required: true },
                { name: "unit", label: "Unit", type: "text" },
              ]}
              submitting={addMetric.isPending}
              error={addMetric.error}
              onSubmit={(values) =>
                addMetric.mutate(
                  create(AddMashMetricRequestSchema, {
                    mashRunId: m.id,
                    kind: Number(values.kind) as MashMetricKind,
                    value: Number(values.value),
                    unit: values.unit ?? "",
                  }),
                )
              }
            />
          }
        >
          <table className="min-w-full divide-y divide-stone-200 text-sm">
            <thead className="text-left text-xs uppercase text-stone-500">
              <tr>
                <th className="px-3 py-2">When</th>
                <th className="px-3 py-2">Kind</th>
                <th className="px-3 py-2 text-right">Value</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-stone-100">
              {m.metrics.length === 0 && (
                <tr>
                  <td colSpan={3} className="px-3 py-2 text-stone-500">
                    None recorded.
                  </td>
                </tr>
              )}
              {m.metrics.map((mc) => (
                <tr key={mc.id}>
                  <td className="px-3 py-2 text-stone-600">
                    {mc.observedAt
                      ? new Date(Number(mc.observedAt.seconds) * 1000).toLocaleString()
                      : ""}
                  </td>
                  <td className="px-3 py-2 text-stone-900">{mashMetricKindLabel(mc.kind)}</td>
                  <td className="px-3 py-2 text-right text-stone-600">
                    {mc.value} {mc.unit}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </Panel>
      </section>

      <section>
        <Panel
          title="Fermentations"
          right={
            <InlineForm
              fields={[
                { name: "fermenter_label", label: "Fermenter", type: "text", required: true, placeholder: "Fermenter A" },
                { name: "initial_volume_l", label: "Volume (L)", type: "number", step: "0.1" },
              ]}
              submitting={createFerment.isPending}
              error={createFerment.error}
              onSubmit={(values) => {
                const volRaw = values.initial_volume_l?.trim() ?? "";
                createFerment.mutate(
                  create(CreateFermentationRunRequestSchema, {
                    mashRunId: m.id,
                    fermenterLabel: values.fermenter_label,
                    initialVolumeL: volRaw ? Number(volRaw) : 0,
                    initialVolumeLSet: !!volRaw,
                  }),
                );
              }}
            />
          }
        >
          <table className="min-w-full divide-y divide-stone-200 text-sm">
            <thead className="text-left text-xs uppercase text-stone-500">
              <tr>
                <th className="px-3 py-2">Fermenter</th>
                <th className="px-3 py-2">Pitched</th>
                <th className="px-3 py-2 text-right">Volume (L)</th>
                <th className="px-3 py-2">Status</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-stone-100">
              {ferments.data?.runs.length === 0 && (
                <tr>
                  <td colSpan={4} className="px-3 py-2 text-stone-500">
                    No fermentation runs yet.
                  </td>
                </tr>
              )}
              {ferments.data?.runs.map((f) => (
                <tr key={f.id}>
                  <td className="px-3 py-2">
                    <Link to={`/fermentations/${f.id}`} className="text-stone-900 hover:underline">
                      {f.fermenterLabel}
                    </Link>
                  </td>
                  <td className="px-3 py-2 text-stone-600">
                    {f.pitchAt
                      ? new Date(Number(f.pitchAt.seconds) * 1000).toLocaleString()
                      : ""}
                  </td>
                  <td className="px-3 py-2 text-right text-stone-600">
                    {f.initialVolumeLSet ? formatQty(f.initialVolumeL) : "—"}
                  </td>
                  <td className="px-3 py-2 text-stone-600">{fermentationStatusLabel(f.status)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </Panel>
      </section>
    </Shell>
  );
}

type Field = {
  name: string;
  label: string;
  type: "text" | "number" | "select";
  required?: boolean;
  defaultValue?: string;
  step?: string;
  placeholder?: string;
  options?: { value: string; label: string }[];
};

function InlineForm({
  fields,
  submitting,
  error,
  onSubmit,
}: {
  fields: Field[];
  submitting: boolean;
  error: Error | null;
  onSubmit: (values: Record<string, string>) => void;
}) {
  const [values, setValues] = useState<Record<string, string>>(() => {
    const init: Record<string, string> = {};
    for (const f of fields) init[f.name] = f.defaultValue ?? "";
    return init;
  });
  function submit(e: FormEvent) {
    e.preventDefault();
    onSubmit(values);
    // Reset numeric/text fields, keep selects as-is.
    setValues((vs) => {
      const next = { ...vs };
      for (const f of fields) if (f.type !== "select") next[f.name] = f.defaultValue ?? "";
      return next;
    });
  }
  return (
    <form onSubmit={submit} className="flex items-end gap-2">
      {fields.map((f) => (
        <div key={f.name}>
          <label className="mb-1 block text-xs text-stone-500">{f.label}</label>
          {f.type === "select" ? (
            <select
              required={f.required}
              value={values[f.name]}
              onChange={(e) => setValues({ ...values, [f.name]: e.target.value })}
              className="rounded border border-stone-300 px-2 py-1 text-sm"
            >
              <option value="">—</option>
              {f.options?.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </select>
          ) : (
            <input
              required={f.required}
              type={f.type}
              step={f.step}
              placeholder={f.placeholder}
              value={values[f.name]}
              onChange={(e) => setValues({ ...values, [f.name]: e.target.value })}
              className="w-28 rounded border border-stone-300 px-2 py-1 text-sm"
            />
          )}
        </div>
      ))}
      <button
        type="submit"
        disabled={submitting}
        className="rounded bg-stone-900 px-3 py-1 text-sm font-medium text-white hover:bg-stone-800 disabled:bg-stone-400"
      >
        {submitting ? "…" : "Add"}
      </button>
      {error && (
        <span className="text-sm text-red-600">
          {error instanceof ConnectError ? error.rawMessage : String(error)}
        </span>
      )}
    </form>
  );
}

function Panel({
  title,
  right,
  children,
}: {
  title: string;
  right?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <div className="overflow-hidden rounded-lg border border-stone-200 bg-white shadow-sm">
      <header className="flex items-center justify-between border-b border-stone-200 bg-stone-50 px-4 py-3">
        <h2 className="text-sm font-semibold uppercase text-stone-500">{title}</h2>
        {right}
      </header>
      <div className="overflow-x-auto">{children}</div>
    </div>
  );
}
