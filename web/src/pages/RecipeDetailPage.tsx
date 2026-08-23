import { FormEvent, useMemo, useState } from "react";
import { useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { SensoryRadar } from "@/components/SensoryRadar";
import { Shell } from "@/components/Shell";
import { materialClient, recipeClient } from "@/lib/clients";
import { MaterialKind } from "@/gen/stillhouse/v1/material_pb";
import {
  BotanicalRole,
  DistillationMethod,
  GinSensoryScoresSchema,
  RecipeIngredient,
  RecipeIngredientInputSchema,
  RecipeVersion,
  SaveRecipeVersionRequestSchema,
  SaveRecipeVersionSensoryRequestSchema,
  SaveRecipeVersionWhiskySensoryRequestSchema,
  SpiritKind,
  WhiskySensoryScoresSchema,
  YieldFindingSeverity,
} from "@/gen/stillhouse/v1/recipe_pb";
import {
  BOTANICAL_ROLE_OPTIONS,
  DISTILLATION_METHOD_OPTIONS,
  botanicalRoleLabel,
  distillationMethodLabel,
  formatLAA,
  formatPct,
  formatQty,
  spiritKindLabel,
} from "@/lib/format";

type IngredientRow = {
  materialId: string;
  quantity: string;
  uom: string;
  notes: string;
  botanicalRole: BotanicalRole;
};

const SENSORY_AXES = [
  { key: "juniper", label: "Juniper" },
  { key: "citrus", label: "Citrus" },
  { key: "herbal", label: "Herbal" },
  { key: "spice", label: "Spice" },
  { key: "floral", label: "Floral" },
  { key: "earth", label: "Earth / root" },
  { key: "body", label: "Body" },
  { key: "heat", label: "Heat" },
  { key: "balance", label: "Balance" },
  { key: "overall", label: "Overall" },
] as const;
type SensoryAxis = (typeof SENSORY_AXES)[number]["key"];

// Whisky tasting axes — Scotch Whisky Research Institute Flavour Wheel
// (8 primary classes from the SWRI 1979 wheel) plus body / finish /
// overall from the standard panel scorecard. "Sulphury" is primarily
// an off-note class (low = clean spirit, high = problem).
const WHISKY_SENSORY_AXES = [
  { key: "cereal",   label: "Cereal",   hint: "porridge / husky / malt / biscuit" },
  { key: "estery",   label: "Estery",   hint: "fruity esters: banana / pear / apple / citrus" },
  { key: "floral",   label: "Floral",   hint: "geranium / rose / honey" },
  { key: "peaty",    label: "Peaty",    hint: "phenolic / smoky / medicinal / iodine" },
  { key: "feinty",   label: "Feinty",   hint: "leather / tobacco / honey-tobacco" },
  { key: "sulphury", label: "Sulphury", hint: "OFF-note: rubbery / vegetative / DMS — low = clean" },
  { key: "woody",    label: "Woody",    hint: "vanilla / toasted oak / resinous" },
  { key: "winey",    label: "Winey",    hint: "sherry / port / brandy (cask-finish notes)" },
  { key: "body",     label: "Body",     hint: "mouthfeel / weight" },
  { key: "finish",   label: "Finish",   hint: "length / persistence" },
  { key: "overall",  label: "Overall",  hint: "gut-call quality" },
] as const;
type WhiskySensoryAxis = (typeof WHISKY_SENSORY_AXES)[number]["key"];

const WHISKY_KINDS = new Set<SpiritKind>([
  SpiritKind.WHISKY,
  SpiritKind.CANADIAN_WHISKY,
  SpiritKind.RYE_WHISKY,
]);

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

  const isGin = recipe.data?.recipe?.spiritKind === SpiritKind.GIN;
  const isWhisky = recipe.data?.recipe?.spiritKind !== undefined && WHISKY_KINDS.has(recipe.data.recipe.spiritKind);

  const [showEditor, setShowEditor] = useState(false);
  // Shared
  const [versionNotes, setVersionNotes] = useState("");
  const [tastingNotes, setTastingNotes] = useState("");
  const [distillEff, setDistillEff] = useState("0.90");
  // Whisky-only
  const [mashEff, setMashEff] = useState("0.85");
  const [fermentEff, setFermentEff] = useState("0.92");
  const [waterL, setWaterL] = useState("");
  // Gin-only
  const [ngsInputL, setNgsInputL] = useState("");
  const [ngsInputAbv, setNgsInputAbv] = useState("");
  const [macerationHours, setMacerationHours] = useState("");
  const [distillationMethod, setDistillationMethod] = useState<DistillationMethod>(
    DistillationMethod.UNSPECIFIED,
  );

  const [rows, setRows] = useState<IngredientRow[]>([
    { materialId: "", quantity: "", uom: "kg", notes: "", botanicalRole: BotanicalRole.UNSPECIFIED },
  ]);

  const fermentableMaterialIds = useMemo(() => {
    const set = new Set<string>();
    for (const m of materials.data?.materials ?? []) {
      if (m.kind === MaterialKind.GRAIN || m.kind === MaterialKind.MALT) set.add(m.id);
    }
    return set;
  }, [materials.data]);

  const botanicalMaterialIds = useMemo(() => {
    const set = new Set<string>();
    for (const m of materials.data?.materials ?? []) {
      if (m.kind === MaterialKind.BOTANICAL) set.add(m.id);
    }
    return set;
  }, [materials.data]);

  const saveVersion = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof SaveRecipeVersionRequestSchema>>) =>
      recipeClient.saveRecipeVersion(msg),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["getRecipe", id] });
      qc.invalidateQueries({ queryKey: ["listRecipes"] });
      qc.invalidateQueries({ queryKey: ["listRecipeVersions", id] });
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
          botanicalRole: r.botanicalRole,
        }),
      );
    const water = waterL.trim();
    const ngsL = ngsInputL.trim();
    const ngsAbv = ngsInputAbv.trim();
    const mac = macerationHours.trim();
    saveVersion.mutate(
      create(SaveRecipeVersionRequestSchema, {
        recipeId: id!,
        notes: versionNotes,
        tastingNotes: tastingNotes,
        mashEfficiencyFraction: Number(mashEff || "1"),
        fermentEfficiencyFraction: Number(fermentEff || "1"),
        distillationRecoveryFraction: Number(distillEff || "0.9"),
        targetWaterL: water ? Number(water) : 0,
        targetWaterLSet: !!water,
        distillationMethod,
        macerationHours: mac ? Number(mac) : 0,
        macerationHoursSet: !!mac,
        ginNgsInputL: ngsL ? Number(ngsL) : 0,
        ginNgsInputLSet: !!ngsL,
        ginNgsInputAbvPct: ngsAbv ? Number(ngsAbv) : 0,
        ginNgsInputAbvPctSet: !!ngsAbv,
        ingredients,
      }),
    );
  }

  function openEditor() {
    if (!showEditor && recipe.data?.currentVersion) {
      const cv = recipe.data.currentVersion;
      setMashEff(String(cv.mashEfficiencyFraction));
      setFermentEff(String(cv.fermentEfficiencyFraction));
      setDistillEff(String(cv.distillationRecoveryFraction));
      setWaterL(cv.targetWaterLSet ? String(cv.targetWaterL) : "");
      setVersionNotes("");
      setTastingNotes(cv.tastingNotes ?? "");
      setDistillationMethod(cv.distillationMethod);
      setMacerationHours(cv.macerationHoursSet ? String(cv.macerationHours) : "");
      setNgsInputL(cv.ginNgsInputLSet ? String(cv.ginNgsInputL) : "");
      setNgsInputAbv(cv.ginNgsInputAbvPctSet ? String(cv.ginNgsInputAbvPct) : "");
      setRows(
        cv.ingredients.length > 0
          ? cv.ingredients.map((ing) => ({
              materialId: ing.materialId,
              quantity: String(ing.quantity),
              uom: ing.uom,
              notes: ing.notes,
              botanicalRole: ing.botanicalRole,
            }))
          : [{ materialId: "", quantity: "", uom: "kg", notes: "", botanicalRole: BotanicalRole.UNSPECIFIED }],
      );
    }
    setShowEditor((s) => !s);
  }

  if (!id) return <Shell><p>Missing recipe id.</p></Shell>;

  return (
    <Shell>
      {recipe.isLoading && <p className="text-fg-muted">Loading recipe…</p>}
      {recipe.data?.recipe && (
        <>
          <div className="mb-6 flex items-start justify-between">
            <div>
              <h1 className="text-3xl font-bold tracking-tight">{recipe.data.recipe.name}</h1>
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
              onClick={openEditor}
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

              {isGin ? (
                <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
                  <Field label="NGS input (L)" value={ngsInputL} onChange={setNgsInputL} placeholder="e.g. 100" />
                  <Field label="NGS input ABV (%)" value={ngsInputAbv} onChange={setNgsInputAbv} placeholder="e.g. 96" />
                  <Field label="Maceration (h)" value={macerationHours} onChange={setMacerationHours} placeholder="e.g. 12" />
                  <Field label="Distillation recovery (0..1)" value={distillEff} onChange={setDistillEff} />
                  <div className="col-span-1 sm:col-span-2 lg:col-span-2">
                    <label className="mb-2 block text-sm font-medium text-fg-muted">Distillation method</label>
                    <select
                      value={distillationMethod}
                      onChange={(e) => setDistillationMethod(Number(e.target.value) as DistillationMethod)}
                      className="w-full rounded border border-border-strong px-3 py-2 text-sm"
                    >
                      {DISTILLATION_METHOD_OPTIONS.map((o) => (
                        <option key={o.value} value={o.value}>{o.label}</option>
                      ))}
                    </select>
                  </div>
                </div>
              ) : (
                <div className="grid grid-cols-4 gap-4">
                  <Field label="Mash efficiency (0..1)" value={mashEff} onChange={setMashEff} />
                  <Field label="Ferment efficiency (0..1)" value={fermentEff} onChange={setFermentEff} />
                  <Field label="Distillation recovery (0..1)" value={distillEff} onChange={setDistillEff} />
                  <Field label="Water (L)" value={waterL} onChange={setWaterL} placeholder="optional" />
                </div>
              )}

              <Field label="Version notes" value={versionNotes} onChange={setVersionNotes} />
              <div>
                <label className="mb-2 block text-sm font-medium text-fg-muted">Tasting notes</label>
                <textarea
                  value={tastingNotes}
                  onChange={(e) => setTastingNotes(e.target.value)}
                  rows={3}
                  placeholder={isGin
                    ? "Aroma: juniper-forward, citrus on the nose. Palate: …"
                    : "Optional — flavor / smell notes on this version."}
                  className="w-full rounded border border-border-strong px-3 py-2 text-sm focus:border-accent focus:outline-none"
                />
              </div>

              <div>
                <p className="mb-2 text-sm font-medium text-fg">
                  {isGin ? "Botanicals" : "Ingredients"}
                </p>
                <div className="space-y-2">
                  {rows.map((r, idx) => {
                    const isBotanical = botanicalMaterialIds.has(r.materialId);
                    return (
                      <div key={idx} className="flex flex-wrap items-center gap-2">
                        <select
                          value={r.materialId}
                          onChange={(e) => updateRow(idx, { materialId: e.target.value })}
                          className="flex-1 min-w-[12rem] rounded border border-border-strong px-3 py-2 text-sm"
                        >
                          <option value="">Select material…</option>
                          {materials.data?.materials.map((m) => (
                            <option key={m.id} value={m.id}>
                              {m.name}
                              {isGin
                                ? botanicalMaterialIds.has(m.id) ? "" : "  (non-botanical)"
                                : fermentableMaterialIds.has(m.id) ? "" : "  (no LAA contribution)"}
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
                          className="w-24 rounded border border-border-strong px-3 py-2 text-sm"
                        />
                        <input
                          type="text"
                          placeholder="uom"
                          value={r.uom}
                          onChange={(e) => updateRow(idx, { uom: e.target.value })}
                          className="w-20 rounded border border-border-strong px-3 py-2 text-sm"
                        />
                        {isGin && isBotanical && (
                          <select
                            value={r.botanicalRole}
                            onChange={(e) => updateRow(idx, { botanicalRole: Number(e.target.value) as BotanicalRole })}
                            className="w-32 rounded border border-border-strong px-3 py-2 text-sm"
                          >
                            {BOTANICAL_ROLE_OPTIONS.map((o) => (
                              <option key={o.value} value={o.value}>{o.label}</option>
                            ))}
                          </select>
                        )}
                        <button
                          type="button"
                          onClick={() => removeRow(idx)}
                          className="text-sm text-fg-muted hover:text-danger-fg"
                        >
                          ✕
                        </button>
                      </div>
                    );
                  })}
                  <button
                    type="button"
                    onClick={() =>
                      setRows((rs) => [
                        ...rs,
                        { materialId: "", quantity: "", uom: isGin ? "g" : "kg", notes: "", botanicalRole: BotanicalRole.UNSPECIFIED },
                      ])
                    }
                    className="text-sm text-fg-muted underline-offset-2 hover:underline"
                  >
                    + Add {isGin ? "botanical" : "ingredient"}
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
                  <span className="text-sm text-danger-fg">
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
                  <h2 className="text-sm font-semibold text-fg-muted">Current version</h2>
                </header>
                <dl className="divide-y divide-border text-sm">
                  <DLRow label="Version">v{recipe.data.currentVersion.versionNo}</DLRow>
                  {isGin ? (
                    <>
                      {recipe.data.currentVersion.ginNgsInputLSet && (
                        <DLRow label="NGS input">
                          {formatQty(recipe.data.currentVersion.ginNgsInputL)} L
                          {recipe.data.currentVersion.ginNgsInputAbvPctSet &&
                            ` @ ${recipe.data.currentVersion.ginNgsInputAbvPct.toFixed(1)}%`}
                        </DLRow>
                      )}
                      <DLRow label="Distillation recovery">
                        {formatPct(recipe.data.currentVersion.distillationRecoveryFraction)}
                      </DLRow>
                      <DLRow label="Distillation method">
                        {distillationMethodLabel(recipe.data.currentVersion.distillationMethod)}
                      </DLRow>
                      {recipe.data.currentVersion.macerationHoursSet && (
                        <DLRow label="Maceration">
                          {recipe.data.currentVersion.macerationHours.toFixed(1)} h
                        </DLRow>
                      )}
                    </>
                  ) : (
                    <>
                      <DLRow label="Mash efficiency">{formatPct(recipe.data.currentVersion.mashEfficiencyFraction)}</DLRow>
                      <DLRow label="Ferment efficiency">{formatPct(recipe.data.currentVersion.fermentEfficiencyFraction)}</DLRow>
                      <DLRow label="Distillation recovery">{formatPct(recipe.data.currentVersion.distillationRecoveryFraction)}</DLRow>
                      {recipe.data.currentVersion.targetWaterLSet && (
                        <DLRow label="Target water">
                          {formatQty(recipe.data.currentVersion.targetWaterL)} L
                        </DLRow>
                      )}
                    </>
                  )}
                </dl>
              </div>

              <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
                <header className="border-b border-border bg-surface-3 px-4 py-3">
                  <h2 className="text-sm font-semibold text-fg-muted">Projection</h2>
                </header>
                <dl className="divide-y divide-border text-sm">
                  <DLRow label="Projected LAA">
                    <span className="font-semibold text-fg">
                      {formatLAA(recipe.data.projection?.totalProjectedLaa)} L
                    </span>
                  </DLRow>
                  {!isGin && (recipe.data.projection?.projectedWashVolumeL ?? 0) > 0 && (
                    <>
                      <DLRow label="Projected wash volume">
                        {formatQty(recipe.data.projection?.projectedWashVolumeL)} L
                      </DLRow>
                      <DLRow label="Projected wash ABV">
                        {recipe.data.projection?.projectedWashAbvPct.toFixed(2)}%
                      </DLRow>
                    </>
                  )}
                  {isGin && (
                    <DLRow label="Math">
                      <span className="text-xs text-fg-muted">NGS LAA × recovery</span>
                    </DLRow>
                  )}
                  {/* Yield per tonne is how the industry quotes this, so
                      it can be checked against a published figure rather
                      than taken on faith. */}
                  {recipe.data.projection?.yieldCheck?.measurable && (
                    <DLRow label="Yield">
                      <span className="tabular-nums text-fg">
                        {recipe.data.projection.yieldCheck.lPerTonne.toFixed(0)} L/tonne
                      </span>
                      <span className="ml-2 text-xs text-fg-subtle">
                        this bill should give ~
                        {recipe.data.projection.yieldCheck.achievableLPerTonne.toFixed(0)}
                      </span>
                    </DLRow>
                  )}
                </dl>
                {(recipe.data.projection?.yieldCheck?.findings.length ?? 0) > 0 && (
                  <div className="space-y-2 border-t border-border p-3">
                    {recipe.data.projection!.yieldCheck!.findings.map((f, i) => (
                      <div
                        key={`${f.code}-${i}`}
                        className={`rounded-md border border-l-4 px-3 py-2 ${
                          f.severity === YieldFindingSeverity.PROBLEM
                            ? "border-danger/40 border-l-danger bg-danger/10"
                            : f.severity === YieldFindingSeverity.WARNING
                              ? "border-warning/40 border-l-warning bg-warning/10"
                              : "border-border border-l-border-strong bg-surface-3/50"
                        }`}
                      >
                        <p
                          className={`text-sm font-medium ${
                            f.severity === YieldFindingSeverity.PROBLEM
                              ? "text-danger-fg"
                              : f.severity === YieldFindingSeverity.WARNING
                                ? "text-warning-fg"
                                : "text-fg"
                          }`}
                        >
                          {f.title}
                        </p>
                        {f.detail && <p className="mt-0.5 text-xs text-fg-muted">{f.detail}</p>}
                      </div>
                    ))}
                  </div>
                )}
              </div>

              {recipe.data.currentVersion.tastingNotes && (
                <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm lg:col-span-2">
                  <header className="border-b border-border bg-surface-3 px-4 py-3">
                    <h2 className="text-sm font-semibold text-fg-muted">Tasting notes</h2>
                  </header>
                  <p className="whitespace-pre-wrap px-4 py-3 text-sm text-fg">
                    {recipe.data.currentVersion.tastingNotes}
                  </p>
                </div>
              )}

              <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm lg:col-span-2">
                <header className="border-b border-border bg-surface-3 px-4 py-3">
                  <h2 className="text-sm font-semibold text-fg-muted">
                    {isGin ? "Botanical bill" : "Ingredients"}
                  </h2>
                </header>
                <table className="min-w-full divide-y divide-border text-sm">
                  <thead className="bg-surface-2 text-left text-xs text-fg-muted">
                    <tr>
                      <th className="px-4 py-3">Material</th>
                      {isGin && <th className="px-4 py-3">Role</th>}
                      <th className="px-4 py-3 text-right">Qty</th>
                      <th className="px-4 py-3">UoM</th>
                      {!isGin && (
                        <>
                          <th className="px-4 py-3 text-right">Fermentable kg</th>
                          <th className="px-4 py-3 text-right">Ethanol kg</th>
                          <th className="px-4 py-3 text-right">Projected LAA</th>
                        </>
                      )}
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-border">
                    {recipe.data.projection?.lines.map((l) => {
                      const ing = recipe.data?.currentVersion?.ingredients.find((i) => i.materialId === l.materialId);
                      return (
                        <tr key={l.materialId}>
                          <td className="px-4 py-3 font-medium text-fg">{l.materialName}</td>
                          {isGin && (
                            <td className="px-4 py-3 text-fg-muted">
                              {ing && ing.botanicalRole !== BotanicalRole.UNSPECIFIED
                                ? botanicalRoleLabel(ing.botanicalRole)
                                : <span className="text-fg-subtle">—</span>}
                            </td>
                          )}
                          <td className="px-4 py-3 text-right text-fg-muted">{formatQty(l.quantity)}</td>
                          <td className="px-4 py-3 text-fg-muted">{l.uom}</td>
                          {!isGin && (
                            <>
                              <td className="px-4 py-3 text-right text-fg-muted">{l.fermentableKg > 0 ? formatQty(l.fermentableKg) : "—"}</td>
                              <td className="px-4 py-3 text-right text-fg-muted">{l.ethanolMassKg > 0 ? formatQty(l.ethanolMassKg) : "—"}</td>
                              <td className="px-4 py-3 text-right font-medium text-fg">{l.projectedLaa > 0 ? formatLAA(l.projectedLaa) : "—"}</td>
                            </>
                          )}
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>

              {isGin && (
                <div className="lg:col-span-2">
                  <SensoryPanel
                    recipeId={id!}
                    versionId={recipe.data.currentVersion.id}
                    current={recipe.data.currentVersion.sensory}
                  />
                </div>
              )}

              {isWhisky && (
                <div className="lg:col-span-2">
                  <WhiskySensoryPanel
                    recipeId={id!}
                    versionId={recipe.data.currentVersion.id}
                    current={recipe.data.currentVersion.whiskySensory}
                  />
                </div>
              )}
            </section>
          ) : (
            <p className="text-fg-muted">No version yet. Click <b>Add first version</b>.</p>
          )}

          <VersionHistory
            recipeId={id}
            currentVersionId={recipe.data.recipe.currentVersionId}
            isGin={isGin}
            isWhisky={isWhisky}
          />
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

// SensoryPanel — only rendered for gin recipes. Edit-in-place: every
// axis is a 0–10 number input; blank means "not tasted on this axis."
// Save = upsert. Reload pulls fresh scores into the form.
function SensoryPanel({
  recipeId,
  versionId,
  current,
}: {
  recipeId: string;
  versionId: string;
  current?: ReturnType<typeof recipeSensory>;
}) {
  const qc = useQueryClient();

  function initFrom(s?: ReturnType<typeof recipeSensory>): Record<SensoryAxis, string> {
    const out = {} as Record<SensoryAxis, string>;
    for (const a of SENSORY_AXES) {
      const set = s?.[`${a.key}Set` as `${SensoryAxis}Set`] as boolean | undefined;
      const v = s?.[a.key] as number | undefined;
      out[a.key] = set ? String(v) : "";
    }
    return out;
  }

  const [scores, setScores] = useState<Record<SensoryAxis, string>>(initFrom(current));
  const [panel, setPanel] = useState<string>(current?.tastingPanel ?? "");
  const [savedAt, setSavedAt] = useState<number | null>(null);

  const save = useMutation({
    mutationFn: () => {
      const built = create(GinSensoryScoresSchema, { tastingPanel: panel });
      for (const a of SENSORY_AXES) {
        const raw = scores[a.key].trim();
        if (raw === "") continue;
        const v = Math.max(0, Math.min(10, Math.round(Number(raw))));
        (built as Record<string, unknown>)[a.key] = v;
        (built as Record<string, unknown>)[`${a.key}Set`] = true;
      }
      return recipeClient.saveRecipeVersionSensory(
        create(SaveRecipeVersionSensoryRequestSchema, {
          recipeVersionId: versionId,
          scores: built,
        }),
      );
    },
    onSuccess: () => {
      setSavedAt(Date.now());
      qc.invalidateQueries({ queryKey: ["getRecipe", recipeId] });
      qc.invalidateQueries({ queryKey: ["listRecipeVersions", recipeId] });
      setTimeout(() => setSavedAt(null), 2500);
    },
  });

  return (
    <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
      <header className="border-b border-border bg-surface-3 px-4 py-3">
        <h2 className="text-sm font-semibold text-fg-muted">Sensory scoring (0–10)</h2>
      </header>
      <div className="space-y-3 p-4">
        {/* The shape you're building, updating as you score. The inputs
            below stay the source of truth — the chart never gates a
            value. */}
        <div className="flex justify-center">
          <SensoryRadar
            axes={SENSORY_AXES.map((a) => ({ key: a.key, label: a.label }))}
            series={[{ name: "This tasting", slot: 1, values: numericScores(scores) }]}
          />
        </div>
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-5">
          {SENSORY_AXES.map((a) => (
            <div key={a.key}>
              <label className="mb-1 block text-xs font-medium text-fg-muted">{a.label}</label>
              <div className="flex items-center gap-2">
                <input
                  type="number"
                  min={0}
                  max={10}
                  value={scores[a.key]}
                  onChange={(e) => setScores((s) => ({ ...s, [a.key]: e.target.value }))}
                  className="w-16 rounded border border-border-strong px-2 py-1 text-sm"
                />
                <ScoreBar value={Number(scores[a.key]) || 0} hasValue={scores[a.key].trim() !== ""} />
              </div>
            </div>
          ))}
        </div>
        <div className="flex flex-wrap items-end gap-3">
          <div className="flex-1 min-w-[12rem]">
            <label className="mb-1 block text-xs font-medium text-fg-muted">Tasting panel</label>
            <input
              value={panel}
              onChange={(e) => setPanel(e.target.value)}
              placeholder="self · Kyle + Jane · …"
              className="w-full rounded border border-border-strong px-2 py-1 text-sm"
            />
          </div>
          <button
            type="button"
            onClick={() => save.mutate()}
            disabled={save.isPending}
            className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
          >
            {save.isPending ? "Saving…" : "Save scores"}
          </button>
          {savedAt && <span className="text-sm text-success-fg">Saved.</span>}
          {save.error && (
            <span className="text-sm text-danger-fg">
              {save.error instanceof ConnectError ? save.error.rawMessage : String(save.error)}
            </span>
          )}
        </div>
        {current?.tastedAt && (
          <p className="text-xs text-fg-muted">
            Last tasted: {new Date(Number(current.tastedAt.seconds) * 1000).toLocaleString()}
            {current.tastingPanel && ` · ${current.tastingPanel}`}
          </p>
        )}
      </div>
    </div>
  );
}

// WhiskySensoryPanel — same shape as SensoryPanel but for the SWRI
// Flavour Wheel axes. Each axis label has a hover hint listing the
// sub-aromas in that primary class. Sulphury is flagged in muted
// red because it's primarily an off-note class (a high score = problem).
function WhiskySensoryPanel({
  recipeId,
  versionId,
  current,
}: {
  recipeId: string;
  versionId: string;
  current?: ReturnType<typeof recipeWhiskySensory>;
}) {
  const qc = useQueryClient();

  function initFrom(s?: ReturnType<typeof recipeWhiskySensory>): Record<WhiskySensoryAxis, string> {
    const out = {} as Record<WhiskySensoryAxis, string>;
    for (const a of WHISKY_SENSORY_AXES) {
      const set = s?.[`${a.key}Set` as `${WhiskySensoryAxis}Set`] as boolean | undefined;
      const v = s?.[a.key] as number | undefined;
      out[a.key] = set ? String(v) : "";
    }
    return out;
  }

  const [scores, setScores] = useState<Record<WhiskySensoryAxis, string>>(initFrom(current));
  const [panel, setPanel] = useState<string>(current?.tastingPanel ?? "");
  const [savedAt, setSavedAt] = useState<number | null>(null);

  const save = useMutation({
    mutationFn: () => {
      const built = create(WhiskySensoryScoresSchema, { tastingPanel: panel });
      for (const a of WHISKY_SENSORY_AXES) {
        const raw = scores[a.key].trim();
        if (raw === "") continue;
        const v = Math.max(0, Math.min(10, Math.round(Number(raw))));
        (built as Record<string, unknown>)[a.key] = v;
        (built as Record<string, unknown>)[`${a.key}Set`] = true;
      }
      return recipeClient.saveRecipeVersionWhiskySensory(
        create(SaveRecipeVersionWhiskySensoryRequestSchema, {
          recipeVersionId: versionId,
          scores: built,
        }),
      );
    },
    onSuccess: () => {
      setSavedAt(Date.now());
      qc.invalidateQueries({ queryKey: ["getRecipe", recipeId] });
      qc.invalidateQueries({ queryKey: ["listRecipeVersions", recipeId] });
      setTimeout(() => setSavedAt(null), 2500);
    },
  });

  return (
    <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
      <header className="border-b border-border bg-surface-3 px-4 py-3">
        <h2 className="text-sm font-semibold text-fg-muted">
          Sensory scoring — SWRI Flavour Wheel (0–10)
        </h2>
      </header>
      <div className="space-y-3 p-4">
        <p className="text-xs text-fg-muted">
          8 SWRI primary classes + body / finish / overall. Sulphury is an off-note class:
          low score = clean spirit.
        </p>
        {/* The wheel this bench scores against, drawn as one. */}
        <div className="flex justify-center">
          <SensoryRadar
            axes={WHISKY_SENSORY_AXES.map((a) => ({ key: a.key, label: a.label, hint: a.hint }))}
            series={[{ name: "This tasting", slot: 1, values: numericScores(scores) }]}
          />
        </div>
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          {WHISKY_SENSORY_AXES.map((a) => (
            <div key={a.key} title={a.hint}>
              <label className="mb-1 block text-xs font-medium text-fg-muted">
                {a.label}
                {a.key === "sulphury" && (
                  <span className="ml-1 text-danger-fg/70">(off)</span>
                )}
              </label>
              <div className="flex items-center gap-2">
                <input
                  type="number"
                  min={0}
                  max={10}
                  value={scores[a.key]}
                  onChange={(e) => setScores((s) => ({ ...s, [a.key]: e.target.value }))}
                  className="w-16 rounded border border-border-strong px-2 py-1 text-sm"
                />
                <ScoreBar value={Number(scores[a.key]) || 0} hasValue={scores[a.key].trim() !== ""} />
              </div>
            </div>
          ))}
        </div>
        <div className="flex flex-wrap items-end gap-3">
          <div className="flex-1 min-w-[12rem]">
            <label className="mb-1 block text-xs font-medium text-fg-muted">Tasting panel</label>
            <input
              value={panel}
              onChange={(e) => setPanel(e.target.value)}
              placeholder="self · Kyle · master blender · …"
              className="w-full rounded border border-border-strong px-2 py-1 text-sm"
            />
          </div>
          <button
            type="button"
            onClick={() => save.mutate()}
            disabled={save.isPending}
            className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
          >
            {save.isPending ? "Saving…" : "Save scores"}
          </button>
          {savedAt && <span className="text-sm text-success-fg">Saved.</span>}
          {save.error && (
            <span className="text-sm text-danger-fg">
              {save.error instanceof ConnectError ? save.error.rawMessage : String(save.error)}
            </span>
          )}
        </div>
        {current?.tastedAt && (
          <p className="text-xs text-fg-muted">
            Last tasted: {new Date(Number(current.tastedAt.seconds) * 1000).toLocaleString()}
            {current.tastingPanel && ` · ${current.tastingPanel}`}
          </p>
        )}
      </div>
    </div>
  );
}

function ScoreBar({ value, hasValue }: { value: number; hasValue: boolean }) {
  if (!hasValue) return <span className="text-xs text-fg-subtle">—</span>;
  const pct = Math.max(0, Math.min(100, (value / 10) * 100));
  return (
    <div className="h-2 flex-1 rounded bg-surface-3" title={`${value}/10`}>
      <div className="h-2 rounded bg-accent" style={{ width: `${pct}%` }} />
    </div>
  );
}

// VersionHistory — with optional compare view. Pick 2 versions to see
// side-by-side ingredients + sensory + projection. Stays collapsed
// until the user opts in.
function VersionHistory({
  recipeId,
  currentVersionId,
  isGin,
  isWhisky,
}: {
  recipeId: string;
  currentVersionId: string;
  isGin: boolean;
  isWhisky: boolean;
}) {
  const versions = useQuery({
    queryKey: ["listRecipeVersions", recipeId],
    queryFn: () => recipeClient.listRecipeVersions({ recipeId }),
  });
  const [selected, setSelected] = useState<string[]>([]);

  if (versions.isLoading) return null;
  const list = versions.data?.versions ?? [];
  if (list.length <= 1) return null;

  function toggle(id: string) {
    setSelected((prev) => {
      if (prev.includes(id)) return prev.filter((x) => x !== id);
      if (prev.length >= 2) return [prev[1], id];
      return [...prev, id];
    });
  }

  return (
    <section className="mt-8">
      <div className="mb-3 flex items-center justify-between">
        <h2 className="text-sm font-semibold text-fg-muted">Version history</h2>
        {selected.length > 0 && (
          <button
            type="button"
            onClick={() => setSelected([])}
            className="text-xs text-fg-muted hover:text-fg"
          >
            Clear selection
          </button>
        )}
      </div>
      <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-3 py-2 w-10">Compare</th>
              <th className="px-4 py-2">Version</th>
              <th className="px-4 py-2">Saved</th>
              {!isGin && (
                <>
                  <th className="px-4 py-2 text-right">Mash %</th>
                  <th className="px-4 py-2 text-right">Ferment %</th>
                </>
              )}
              <th className="px-4 py-2 text-right">Distill %</th>
              {isGin && <th className="px-4 py-2 text-right">NGS LAA</th>}
              <th className="px-4 py-2">Notes</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {list.map((v) => {
              const isSelected = selected.includes(v.id);
              const ngsLAA = v.ginNgsInputLSet && v.ginNgsInputAbvPctSet
                ? (v.ginNgsInputL * v.ginNgsInputAbvPct / 100).toFixed(2)
                : null;
              return (
                <tr key={v.id} className={v.id === currentVersionId ? "bg-success/10" : isSelected ? "bg-accent/10" : ""}>
                  <td className="px-3 py-2">
                    <input
                      type="checkbox"
                      checked={isSelected}
                      onChange={() => toggle(v.id)}
                      aria-label={`Select v${v.versionNo} for compare`}
                    />
                  </td>
                  <td className="px-4 py-2 font-medium text-fg">
                    v{v.versionNo}
                    {v.id === currentVersionId && (
                      <span className="ml-2 rounded bg-success/15 px-2 py-0.5 text-xs text-success-fg">current</span>
                    )}
                  </td>
                  <td className="px-4 py-2 text-fg-muted">
                    {v.createdAt ? new Date(Number(v.createdAt.seconds) * 1000).toLocaleString() : ""}
                  </td>
                  {!isGin && (
                    <>
                      <td className="px-4 py-2 text-right text-fg-muted">{(v.mashEfficiencyFraction * 100).toFixed(1)}%</td>
                      <td className="px-4 py-2 text-right text-fg-muted">{(v.fermentEfficiencyFraction * 100).toFixed(1)}%</td>
                    </>
                  )}
                  <td className="px-4 py-2 text-right text-fg-muted">{(v.distillationRecoveryFraction * 100).toFixed(1)}%</td>
                  {isGin && (
                    <td className="px-4 py-2 text-right text-fg-muted">{ngsLAA ?? "—"}</td>
                  )}
                  <td className="px-4 py-2 text-fg-muted">{v.notes}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>

      {selected.length === 2 && (
        <CompareTwo
          recipeId={recipeId}
          versions={[
            list.find((v) => v.id === selected[0])!,
            list.find((v) => v.id === selected[1])!,
          ].filter(Boolean) as RecipeVersion[]}
          isGin={isGin}
          isWhisky={isWhisky}
        />
      )}
    </section>
  );
}

function CompareTwo({
  versions,
  isGin,
  isWhisky,
}: {
  recipeId: string;
  versions: RecipeVersion[];
  isGin: boolean;
  isWhisky: boolean;
}) {
  if (versions.length !== 2) return null;
  const [a, b] = versions;
  // Pull a stable union of axes used in either version's ingredients.
  const allMaterialIds: string[] = [];
  const seen = new Set<string>();
  for (const v of [a, b]) {
    for (const ing of v.ingredients) {
      if (!seen.has(ing.materialId)) {
        seen.add(ing.materialId);
        allMaterialIds.push(ing.materialId);
      }
    }
  }
  const labelOf = (v: RecipeVersion) => `v${v.versionNo}`;

  return (
    <section className="mt-6">
      <h3 className="mb-3 text-sm font-semibold text-fg-muted">Compare {labelOf(a)} ↔ {labelOf(b)}</h3>
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        {/* Process column */}
        <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
          <header className="border-b border-border bg-surface-3 px-4 py-2 text-xs font-semibold text-fg-muted">Process</header>
          <table className="min-w-full divide-y divide-border text-sm">
            <thead className="bg-surface-3 text-left text-xs text-fg-muted">
              <tr><th className="px-3 py-2"></th><th className="px-3 py-2">{labelOf(a)}</th><th className="px-3 py-2">{labelOf(b)}</th></tr>
            </thead>
            <tbody className="divide-y divide-border">
              {isGin ? (
                <>
                  <CmpRow label="NGS input (L)" a={a.ginNgsInputLSet ? formatQty(a.ginNgsInputL) : "—"} b={b.ginNgsInputLSet ? formatQty(b.ginNgsInputL) : "—"} />
                  <CmpRow label="NGS ABV" a={a.ginNgsInputAbvPctSet ? `${a.ginNgsInputAbvPct.toFixed(1)}%` : "—"} b={b.ginNgsInputAbvPctSet ? `${b.ginNgsInputAbvPct.toFixed(1)}%` : "—"} />
                  <CmpRow label="Method" a={distillationMethodLabel(a.distillationMethod)} b={distillationMethodLabel(b.distillationMethod)} />
                  <CmpRow label="Maceration" a={a.macerationHoursSet ? `${a.macerationHours}h` : "—"} b={b.macerationHoursSet ? `${b.macerationHours}h` : "—"} />
                </>
              ) : (
                <>
                  <CmpRow label="Mash" a={`${(a.mashEfficiencyFraction * 100).toFixed(1)}%`} b={`${(b.mashEfficiencyFraction * 100).toFixed(1)}%`} />
                  <CmpRow label="Ferment" a={`${(a.fermentEfficiencyFraction * 100).toFixed(1)}%`} b={`${(b.fermentEfficiencyFraction * 100).toFixed(1)}%`} />
                </>
              )}
              <CmpRow label="Recovery" a={`${(a.distillationRecoveryFraction * 100).toFixed(1)}%`} b={`${(b.distillationRecoveryFraction * 100).toFixed(1)}%`} />
            </tbody>
          </table>
        </div>

        {/* Ingredients column */}
        <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
          <header className="border-b border-border bg-surface-3 px-4 py-2 text-xs font-semibold text-fg-muted">{isGin ? "Botanicals" : "Ingredients"}</header>
          <table className="min-w-full divide-y divide-border text-sm">
            <thead className="bg-surface-3 text-left text-xs text-fg-muted">
              <tr><th className="px-3 py-2">Material</th><th className="px-3 py-2 text-right">{labelOf(a)}</th><th className="px-3 py-2 text-right">{labelOf(b)}</th></tr>
            </thead>
            <tbody className="divide-y divide-border">
              {allMaterialIds.map((mid) => {
                const ia = a.ingredients.find((i: RecipeIngredient) => i.materialId === mid);
                const ib = b.ingredients.find((i: RecipeIngredient) => i.materialId === mid);
                const name = ia?.materialName ?? ib?.materialName ?? mid;
                const fmt = (i?: RecipeIngredient) => (i ? `${formatQty(i.quantity)} ${i.uom}` : "—");
                const diff = ia && ib && ia.quantity !== ib.quantity;
                return (
                  <tr key={mid} className={diff ? "bg-warning/5" : ""}>
                    <td className="px-3 py-2 text-fg">{name}</td>
                    <td className="px-3 py-2 text-right text-fg-muted">{fmt(ia)}</td>
                    <td className="px-3 py-2 text-right text-fg-muted">{fmt(ib)}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>

        {/* Sensory column — gin axes for gin, SWRI for whisky */}
        <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
          <header className="border-b border-border bg-surface-3 px-4 py-2 text-xs font-semibold text-fg-muted">
            Sensory {isWhisky ? "— SWRI Flavour Wheel " : ""}(0–10)
          </header>
          {/* Two profiles on one plot: the difference between versions is
              a change of shape, which is far easier to see than two
              columns of digits. The table below carries the values. */}
          <div className="flex justify-center border-b border-border p-3">
            <SensoryRadar
              axes={(isWhisky ? WHISKY_SENSORY_AXES : SENSORY_AXES).map((ax) => ({
                key: ax.key,
                label: ax.label,
              }))}
              series={[
                { name: labelOf(a), slot: 1, values: sensoryValues(a, isWhisky) },
                { name: labelOf(b), slot: 2, values: sensoryValues(b, isWhisky) },
              ]}
            />
          </div>
          <table className="min-w-full divide-y divide-border text-sm">
            <thead className="bg-surface-3 text-left text-xs text-fg-muted">
              <tr><th className="px-3 py-2"></th><th className="px-3 py-2 text-right">{labelOf(a)}</th><th className="px-3 py-2 text-right">{labelOf(b)}</th></tr>
            </thead>
            <tbody className="divide-y divide-border">
              {isWhisky
                ? WHISKY_SENSORY_AXES.map((axis) => {
                    const va = recipeWhiskySensory(a)?.[`${axis.key}Set` as `${WhiskySensoryAxis}Set`] ? recipeWhiskySensory(a)?.[axis.key] as number : null;
                    const vb = recipeWhiskySensory(b)?.[`${axis.key}Set` as `${WhiskySensoryAxis}Set`] ? recipeWhiskySensory(b)?.[axis.key] as number : null;
                    const diff = va !== null && vb !== null && va !== vb;
                    return (
                      <tr key={axis.key} className={diff ? "bg-warning/5" : ""}>
                        <td className="px-3 py-2 text-fg">{axis.label}</td>
                        <td className="px-3 py-2 text-right text-fg-muted">{va ?? "—"}</td>
                        <td className="px-3 py-2 text-right text-fg-muted">{vb ?? "—"}</td>
                      </tr>
                    );
                  })
                : SENSORY_AXES.map((axis) => {
                    const va = recipeSensory(a)?.[`${axis.key}Set` as `${SensoryAxis}Set`] ? recipeSensory(a)?.[axis.key] as number : null;
                    const vb = recipeSensory(b)?.[`${axis.key}Set` as `${SensoryAxis}Set`] ? recipeSensory(b)?.[axis.key] as number : null;
                    const diff = va !== null && vb !== null && va !== vb;
                    return (
                      <tr key={axis.key} className={diff ? "bg-warning/5" : ""}>
                        <td className="px-3 py-2 text-fg">{axis.label}</td>
                        <td className="px-3 py-2 text-right text-fg-muted">{va ?? "—"}</td>
                        <td className="px-3 py-2 text-right text-fg-muted">{vb ?? "—"}</td>
                      </tr>
                    );
                  })}
            </tbody>
          </table>
        </div>
      </div>
    </section>
  );
}

// numericScores turns the string-keyed form state into the numbers the
// radar plots. A blank input means "not scored on this axis" and stays
// undefined rather than becoming a zero, which would draw a dent the
// taster never recorded.
function numericScores(scores: Record<string, string>): Record<string, number | undefined> {
  const out: Record<string, number | undefined> = {};
  for (const [k, v] of Object.entries(scores)) {
    const trimmed = v.trim();
    out[k] = trimmed === "" ? undefined : Math.max(0, Math.min(10, Number(trimmed)));
  }
  return out;
}

// sensoryValues reads a saved version's scores for whichever bench applies.
function sensoryValues(v: RecipeVersion, isWhisky: boolean): Record<string, number | undefined> {
  const out: Record<string, number | undefined> = {};
  if (isWhisky) {
    const s = recipeWhiskySensory(v);
    for (const ax of WHISKY_SENSORY_AXES) {
      out[ax.key] = s?.[`${ax.key}Set` as `${WhiskySensoryAxis}Set`]
        ? (s?.[ax.key] as number)
        : undefined;
    }
    return out;
  }
  const s = recipeSensory(v);
  for (const ax of SENSORY_AXES) {
    out[ax.key] = s?.[`${ax.key}Set` as `${SensoryAxis}Set`] ? (s?.[ax.key] as number) : undefined;
  }
  return out;
}

function CmpRow({ label, a, b }: { label: string; a: string; b: string }) {
  const diff = a !== b;
  return (
    <tr className={diff ? "bg-warning/5" : ""}>
      <td className="px-3 py-2 text-fg-muted">{label}</td>
      <td className="px-3 py-2 text-fg">{a}</td>
      <td className="px-3 py-2 text-fg">{b}</td>
    </tr>
  );
}

// recipeSensory pulls the optional gin sensory message off a
// RecipeVersion in a typed way. Stage 113 made list_recipe_versions
// load this too, so the compare view sees populated data for every
// version (not just the current one).
function recipeSensory(v: RecipeVersion) {
  return v.sensory;
}

// recipeWhiskySensory — same shape, whisky bench.
function recipeWhiskySensory(v: RecipeVersion) {
  return v.whiskySensory;
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
      <label className="mb-2 block text-sm font-medium text-fg-muted">{label}</label>
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
