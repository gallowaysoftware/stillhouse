import { FormEvent, useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { EmptyRow } from "@/components/EmptyState";
import { Shell } from "@/components/Shell";
import { materialClient } from "@/lib/clients";
import {
  CreateMaterialRequestSchema,
  MaterialKind,
} from "@/gen/stillhouse/v1/material_pb";
import { create } from "@bufbuild/protobuf";
import { materialKindLabel } from "@/lib/format";
import { WriteOnly } from "@/lib/role";

const kindOptions: { value: MaterialKind; label: string }[] = [
  { value: MaterialKind.GRAIN, label: "Grain" },
  { value: MaterialKind.MALT, label: "Malt" },
  { value: MaterialKind.YEAST, label: "Yeast" },
  { value: MaterialKind.WATER, label: "Water" },
  { value: MaterialKind.NGS, label: "Neutral grain spirit" },
  { value: MaterialKind.BOTANICAL, label: "Botanical" },
  { value: MaterialKind.PACKAGING, label: "Packaging" },
  { value: MaterialKind.OTHER, label: "Other" },
];

export function MaterialsPage() {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: ["listMaterials"],
    queryFn: () => materialClient.listMaterials({}),
  });

  const [showForm, setShowForm] = useState(false);

  const createMaterial = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof CreateMaterialRequestSchema>>) =>
      materialClient.createMaterial(msg),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["listMaterials"] });
      setShowForm(false);
    },
  });

  function onSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = e.currentTarget;
    const fd = new FormData(form);
    const kindVal = Number(fd.get("kind")) as MaterialKind;
    const isFermentable =
      kindVal === MaterialKind.GRAIN || kindVal === MaterialKind.MALT;
    const extractPctRaw = fd.get("extract_pct")?.toString().trim() ?? "";
    const moisturePctRaw = fd.get("moisture_pct")?.toString().trim() ?? "";
    const req = create(CreateMaterialRequestSchema, {
      name: fd.get("name")?.toString() ?? "",
      kind: kindVal,
      uom: fd.get("uom")?.toString() ?? "kg",
      supplier: fd.get("supplier")?.toString() ?? "",
      notes: fd.get("notes")?.toString() ?? "",
      extractPct: isFermentable && extractPctRaw ? Number(extractPctRaw) : 0,
      extractPctSet: !!(isFermentable && extractPctRaw),
      moisturePct: isFermentable && moisturePctRaw ? Number(moisturePctRaw) : 0,
      moisturePctSet: !!(isFermentable && moisturePctRaw),
    });
    createMaterial.mutate(req);
  }

  return (
    <Shell>
      <div className="mb-6 flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">Materials</h1>
          <p className="text-sm text-fg-muted">
            Raw materials master. Fermentable sources (grain, malt) need an extract %
            so recipes can project alcohol yield.
          </p>
        </div>
        <WriteOnly>
          <button
            onClick={() => setShowForm((s) => !s)}
            className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover"
          >
            {showForm ? "Cancel" : "Add material"}
          </button>
        </WriteOnly>
      </div>

      {showForm && (
        <form
          onSubmit={onSubmit}
          className="mb-6 grid grid-cols-2 gap-4 rounded-lg border border-border bg-surface-2 p-5 shadow-sm"
        >
          <Field label="Name" name="name" required />
          <Field label="Kind" name="kind" as="select" required>
            {kindOptions.map((k) => (
              <option key={k.value} value={k.value}>
                {k.label}
              </option>
            ))}
          </Field>
          <Field label="UoM" name="uom" placeholder="kg / L / each" defaultValue="kg" required />
          <Field label="Supplier" name="supplier" />
          <Field
            label="Extract % (0..1)"
            name="extract_pct"
            type="number"
            step="0.01"
            min="0"
            max="1"
            placeholder="0.78"
          />
          <Field
            label="Moisture % (0..1)"
            name="moisture_pct"
            type="number"
            step="0.01"
            min="0"
            max="1"
            placeholder="0.04"
          />
          <Field label="Notes" name="notes" className="col-span-2" />
          <div className="col-span-2 flex items-center gap-3">
            <button
              type="submit"
              disabled={createMaterial.isPending}
              className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
            >
              {createMaterial.isPending ? "Saving…" : "Save material"}
            </button>
            {createMaterial.error && (
              <span className="text-sm text-red-400">
                {createMaterial.error instanceof ConnectError
                  ? createMaterial.error.rawMessage
                  : String(createMaterial.error)}
              </span>
            )}
          </div>
        </form>
      )}

      <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs uppercase text-fg-muted">
            <tr>
              <th className="px-4 py-3">Name</th>
              <th className="px-4 py-3">Kind</th>
              <th className="px-4 py-3">UoM</th>
              <th className="px-4 py-3 text-right">Extract %</th>
              <th className="px-4 py-3">Supplier</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {isLoading && (
              <tr>
                <td className="px-4 py-3 text-fg-muted" colSpan={5}>Loading…</td>
              </tr>
            )}
            {!isLoading && data?.materials.length === 0 && (
              <EmptyRow
                colSpan={5}
                title="No materials yet"
                message="Grain, malt, yeast — everything that feeds a mash starts here. Add your first to begin tracking inventory and recipes."
                action={
                  <WriteOnly>
                    <button
                      onClick={() => setShowForm(true)}
                      className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover"
                    >
                      Add material
                    </button>
                  </WriteOnly>
                }
              />
            )}
            {data?.materials.map((m) => (
              <tr key={m.id}>
                <td className="px-4 py-3 font-medium text-fg">
                  <Link to={`/materials/${m.id}`} className="hover:underline">{m.name}</Link>
                </td>
                <td className="px-4 py-3 text-fg-muted">{materialKindLabel(m.kind)}</td>
                <td className="px-4 py-3 text-fg-muted">{m.uom}</td>
                <td className="px-4 py-3 text-right text-fg-muted">
                  {m.extractPctSet ? (m.extractPct * 100).toFixed(2) + "%" : "—"}
                </td>
                <td className="px-4 py-3 text-fg-muted">{m.supplier || "—"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Shell>
  );
}

type FieldProps = React.InputHTMLAttributes<HTMLInputElement> & {
  label: string;
  as?: "input" | "select";
  children?: React.ReactNode;
};

function Field({ label, as = "input", className, children, ...rest }: FieldProps) {
  const labelEl = (
    <label className="mb-1 block text-xs font-medium text-fg-muted">{label}</label>
  );
  const inputClass =
    "w-full rounded border border-border-strong px-3 py-2 text-sm focus:border-accent focus:outline-none";
  return (
    <div className={className}>
      {labelEl}
      {as === "select" ? (
        <select
          name={rest.name}
          required={rest.required}
          defaultValue={rest.defaultValue as string}
          className={inputClass}
        >
          {children}
        </select>
      ) : (
        <input {...rest} className={inputClass} />
      )}
    </div>
  );
}
