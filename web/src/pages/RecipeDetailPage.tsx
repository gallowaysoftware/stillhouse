import { FormEvent, useMemo, useState } from "react";
import { useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { Shell } from "@/components/Shell";
import { materialClient, recipeClient } from "@/lib/clients";
import {
  MaterialKind,
} from "@/gen/stillhouse/v1/material_pb";
import {
  RecipeIngredientInputSchema,
  SaveRecipeVersionRequestSchema,
} from "@/gen/stillhouse/v1/recipe_pb";
import { formatLAA, formatPct, formatQty, spiritKindLabel } from "@/lib/format";

type IngredientRow = {
  materialId: string;
  quantity: string; // user input as text
  uom: string;
  notes: string;
};

export function RecipeDetailPage() {
  const { id } = useParams();
  const qc = useQueryClient();

  const recipe = useQuery({
    queryKey: ["getRecipe", id],
    queryFn: () => recipeClient.getRecipe({ id: id! }),
    enabled: !!id,
  });
  const materials = useQuery({
    queryKey: ["listMaterials"],
    queryFn: () => materialClient.listMaterials({}),
  });

  const [showEditor, setShowEditor] = useState(false);
  const [mashEff, setMashEff] = useState("0.85");
  const [fermentEff, setFermentEff] = useState("0.92");
  const [distillEff, setDistillEff] = useState("0.90");
  const [waterL, setWaterL] = useState("");
  const [versionNotes, setVersionNotes] = useState("");
  const [rows, setRows] = useState<IngredientRow[]>([
    { materialId: "", quantity: "", uom: "kg", notes: "" },
  ]);

  const fermentableMaterialIds = useMemo(() => {
    const set = new Set<string>();
    for (const m of materials.data?.materials ?? []) {
      if (m.kind === MaterialKind.GRAIN || m.kind === MaterialKind.MALT) set.add(m.id);
    }
    return set;
  }, [materials.data]);

  const saveVersion = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof SaveRecipeVersionRequestSchema>>) =>
      recipeClient.saveRecipeVersion(msg),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["getRecipe", id] });
      qc.invalidateQueries({ queryKey: ["listRecipes"] });
      setShowEditor(false);
    },
  });

  function onSave(e: FormEvent) {
    e.preventDefault();
    const ingredients = rows
      .filter((r) => r.materialId && r.quantity)
      .map((r, idx) =>
        create(RecipeIngredientInputSchema, {
          materialId: r.materialId,
          quantity: Number(r.quantity),
          uom: r.uom,
          notes: r.notes,
          sortOrder: idx + 1,
        }),
      );
    const water = waterL.trim();
    saveVersion.mutate(
      create(SaveRecipeVersionRequestSchema, {
        recipeId: id!,
        notes: versionNotes,
        mashEfficiencyPct: Number(mashEff),
        fermentEfficiencyPct: Number(fermentEff),
        distillationRecoveryPct: Number(distillEff),
        targetWaterL: water ? Number(water) : 0,
        targetWaterLSet: !!water,
        ingredients,
      }),
    );
  }

  if (!id) return <Shell><p>Missing recipe id.</p></Shell>;

  return (
    <Shell>
      {recipe.isLoading && <p className="text-fg-muted">Loading recipe…</p>}
      {recipe.data?.recipe && (
        <>
          <div className="mb-6 flex items-start justify-between">
            <div>
              <h1 className="text-2xl font-semibold">{recipe.data.recipe.name}</h1>
              <p className="text-sm text-fg-muted">
                {spiritKindLabel(recipe.data.recipe.spiritKind)}
                {recipe.data.currentVersion && (
                  <> · v{recipe.data.currentVersion.versionNo}</>
                )}
              </p>
              {recipe.data.recipe.notes && (
                <p className="mt-2 text-sm text-fg">{recipe.data.recipe.notes}</p>
              )}
            </div>
            <button
              onClick={() => {
                if (!showEditor && recipe.data.currentVersion) {
                  const cv = recipe.data.currentVersion;
                  setMashEff(String(cv.mashEfficiencyPct));
                  setFermentEff(String(cv.fermentEfficiencyPct));
                  setDistillEff(String(cv.distillationRecoveryPct));
                  setWaterL(cv.targetWaterLSet ? String(cv.targetWaterL) : "");
                  setVersionNotes("");
                  setRows(
                    cv.ingredients.length > 0
                      ? cv.ingredients.map((ing) => ({
                          materialId: ing.materialId,
                          quantity: String(ing.quantity),
                          uom: ing.uom,
                          notes: ing.notes,
                        }))
                      : [{ materialId: "", quantity: "", uom: "kg", notes: "" }],
                  );
                }
                setShowEditor((s) => !s);
              }}
              className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover"
            >
              {showEditor
                ? "Cancel"
                : recipe.data.currentVersion
                ? "New version"
                : "Add first version"}
            </button>
          </div>

          {showEditor && (
            <form
              onSubmit={onSave}
              className="mb-8 space-y-4 rounded-lg border border-border bg-surface-2 p-5 shadow-sm"
            >
              <h2 className="text-lg font-medium">New version</h2>

              <div className="grid grid-cols-4 gap-4">
                <Field label="Mash efficiency (0..1)" value={mashEff} onChange={setMashEff} />
                <Field label="Ferment efficiency (0..1)" value={fermentEff} onChange={setFermentEff} />
                <Field label="Distillation recovery (0..1)" value={distillEff} onChange={setDistillEff} />
                <Field label="Water (L)" value={waterL} onChange={setWaterL} placeholder="optional" />
              </div>
              <Field label="Version notes" value={versionNotes} onChange={setVersionNotes} />

              <div>
                <p className="mb-2 text-sm font-medium text-fg">Ingredients</p>
                <div className="space-y-2">
                  {rows.map((r, idx) => (
                    <div key={idx} className="flex items-center gap-2">
                      <select
                        value={r.materialId}
                        onChange={(e) => updateRow(idx, { materialId: e.target.value })}
                        className="flex-1 rounded border border-border-strong px-3 py-2 text-sm"
                      >
                        <option value="">Select material…</option>
                        {materials.data?.materials.map((m) => (
                          <option key={m.id} value={m.id}>
                            {m.name}
                            {fermentableMaterialIds.has(m.id) ? "" : "  (no LAA contribution)"}
                          </option>
                        ))}
                      </select>
                      <input
                        type="number"
                        step="0.01"
                        min="0"
                        placeholder="qty"
                        value={r.quantity}
                        onChange={(e) => updateRow(idx, { quantity: e.target.value })}
                        className="w-28 rounded border border-border-strong px-3 py-2 text-sm"
                      />
                      <input
                        type="text"
                        placeholder="uom"
                        value={r.uom}
                        onChange={(e) => updateRow(idx, { uom: e.target.value })}
                        className="w-20 rounded border border-border-strong px-3 py-2 text-sm"
                      />
                      <button
                        type="button"
                        onClick={() => removeRow(idx)}
                        className="text-sm text-fg-muted hover:text-red-400"
                      >
                        ✕
                      </button>
                    </div>
                  ))}
                  <button
                    type="button"
                    onClick={() =>
                      setRows((rs) => [...rs, { materialId: "", quantity: "", uom: "kg", notes: "" }])
                    }
                    className="text-sm text-fg-muted underline-offset-2 hover:underline"
                  >
                    + Add ingredient
                  </button>
                </div>
              </div>

              <div className="flex items-center gap-3">
                <button
                  type="submit"
                  disabled={saveVersion.isPending}
                  className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
                >
                  {saveVersion.isPending ? "Saving…" : "Save version"}
                </button>
                {saveVersion.error && (
                  <span className="text-sm text-red-400">
                    {saveVersion.error instanceof ConnectError
                      ? saveVersion.error.rawMessage
                      : String(saveVersion.error)}
                  </span>
                )}
              </div>
            </form>
          )}

          {recipe.data.currentVersion ? (
            <section className="grid grid-cols-1 gap-4 lg:grid-cols-2">
              <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
                <header className="border-b border-border bg-surface-3 px-4 py-3">
                  <h2 className="text-sm font-semibold uppercase text-fg-muted">
                    Current version
                  </h2>
                </header>
                <dl className="divide-y divide-border text-sm">
                  <DLRow label="Version">v{recipe.data.currentVersion.versionNo}</DLRow>
                  <DLRow label="Mash efficiency">
                    {formatPct(recipe.data.currentVersion.mashEfficiencyPct)}
                  </DLRow>
                  <DLRow label="Ferment efficiency">
                    {formatPct(recipe.data.currentVersion.fermentEfficiencyPct)}
                  </DLRow>
                  <DLRow label="Distillation recovery">
                    {formatPct(recipe.data.currentVersion.distillationRecoveryPct)}
                  </DLRow>
                  {recipe.data.currentVersion.targetWaterLSet && (
                    <DLRow label="Target water">
                      {formatQty(recipe.data.currentVersion.targetWaterL)} L
                    </DLRow>
                  )}
                </dl>
              </div>

              <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
                <header className="border-b border-border bg-surface-3 px-4 py-3">
                  <h2 className="text-sm font-semibold uppercase text-fg-muted">Projection</h2>
                </header>
                <dl className="divide-y divide-border text-sm">
                  <DLRow label="Projected LAA">
                    <span className="font-semibold text-fg">
                      {formatLAA(recipe.data.projection?.totalProjectedLaa)} L
                    </span>
                  </DLRow>
                  {(recipe.data.projection?.projectedWashVolumeL ?? 0) > 0 && (
                    <>
                      <DLRow label="Projected wash volume">
                        {formatQty(recipe.data.projection?.projectedWashVolumeL)} L
                      </DLRow>
                      <DLRow label="Projected wash ABV">
                        {recipe.data.projection?.projectedWashAbvPct.toFixed(2)}%
                      </DLRow>
                    </>
                  )}
                </dl>
              </div>

              <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm lg:col-span-2">
                <header className="border-b border-border bg-surface-3 px-4 py-3">
                  <h2 className="text-sm font-semibold uppercase text-fg-muted">
                    Ingredients
                  </h2>
                </header>
                <table className="min-w-full divide-y divide-border text-sm">
                  <thead className="bg-surface-2 text-left text-xs uppercase text-fg-muted">
                    <tr>
                      <th className="px-4 py-3">Material</th>
                      <th className="px-4 py-3 text-right">Qty</th>
                      <th className="px-4 py-3">UoM</th>
                      <th className="px-4 py-3 text-right">Fermentable kg</th>
                      <th className="px-4 py-3 text-right">Ethanol kg</th>
                      <th className="px-4 py-3 text-right">Projected LAA</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-border">
                    {recipe.data.projection?.lines.map((l) => (
                      <tr key={l.materialId}>
                        <td className="px-4 py-3 font-medium text-fg">{l.materialName}</td>
                        <td className="px-4 py-3 text-right text-fg-muted">{formatQty(l.quantity)}</td>
                        <td className="px-4 py-3 text-fg-muted">{l.uom}</td>
                        <td className="px-4 py-3 text-right text-fg-muted">
                          {l.fermentableKg > 0 ? formatQty(l.fermentableKg) : "—"}
                        </td>
                        <td className="px-4 py-3 text-right text-fg-muted">
                          {l.ethanolMassKg > 0 ? formatQty(l.ethanolMassKg) : "—"}
                        </td>
                        <td className="px-4 py-3 text-right font-medium text-fg">
                          {l.projectedLaa > 0 ? formatLAA(l.projectedLaa) : "—"}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </section>
          ) : (
            <p className="text-fg-muted">No version yet. Click <b>Add first version</b>.</p>
          )}

          <VersionHistory recipeId={id} currentVersionId={recipe.data.recipe.currentVersionId} />
        </>
      )}
    </Shell>
  );

  function updateRow(idx: number, patch: Partial<IngredientRow>) {
    setRows((rs) => rs.map((r, i) => (i === idx ? { ...r, ...patch } : r)));
  }
  function removeRow(idx: number) {
    setRows((rs) => (rs.length === 1 ? rs : rs.filter((_, i) => i !== idx)));
  }
}

function VersionHistory({ recipeId, currentVersionId }: { recipeId: string; currentVersionId: string }) {
  const versions = useQuery({
    queryKey: ["listRecipeVersions", recipeId],
    queryFn: () => recipeClient.listRecipeVersions({ recipeId }),
  });
  if (versions.isLoading) return null;
  const list = versions.data?.versions ?? [];
  if (list.length <= 1) return null;
  return (
    <section className="mt-8">
      <h2 className="mb-3 text-sm font-semibold uppercase text-fg-muted">Version history</h2>
      <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs uppercase text-fg-muted">
            <tr>
              <th className="px-4 py-2">Version</th>
              <th className="px-4 py-2">Saved</th>
              <th className="px-4 py-2 text-right">Mash %</th>
              <th className="px-4 py-2 text-right">Ferment %</th>
              <th className="px-4 py-2 text-right">Distill %</th>
              <th className="px-4 py-2 text-right">Target water (L)</th>
              <th className="px-4 py-2">Notes</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {list.map((v) => (
              <tr key={v.id} className={v.id === currentVersionId ? "bg-emerald-500/10" : ""}>
                <td className="px-4 py-2 font-medium text-fg">
                  v{v.versionNo}
                  {v.id === currentVersionId && (
                    <span className="ml-2 rounded bg-emerald-200 px-2 py-0.5 text-xs text-emerald-800">current</span>
                  )}
                </td>
                <td className="px-4 py-2 text-fg-muted">
                  {v.createdAt ? new Date(Number(v.createdAt.seconds) * 1000).toLocaleString() : ""}
                </td>
                <td className="px-4 py-2 text-right text-fg-muted">{(v.mashEfficiencyPct * 100).toFixed(1)}%</td>
                <td className="px-4 py-2 text-right text-fg-muted">{(v.fermentEfficiencyPct * 100).toFixed(1)}%</td>
                <td className="px-4 py-2 text-right text-fg-muted">{(v.distillationRecoveryPct * 100).toFixed(1)}%</td>
                <td className="px-4 py-2 text-right text-fg-muted">{v.targetWaterLSet ? v.targetWaterL.toFixed(0) : "—"}</td>
                <td className="px-4 py-2 text-fg-muted">{v.notes}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function Field({
  label,
  value,
  onChange,
  placeholder,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
}) {
  return (
    <div>
      <label className="mb-1 block text-xs font-medium text-fg-muted">{label}</label>
      <input
        value={value}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
        className="w-full rounded border border-border-strong px-3 py-2 text-sm focus:border-accent focus:outline-none"
      />
    </div>
  );
}

function DLRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-4 px-4 py-3">
      <dt className="text-fg-muted">{label}</dt>
      <dd className="text-fg">{children}</dd>
    </div>
  );
}
