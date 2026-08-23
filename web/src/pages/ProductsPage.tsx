import { FormEvent, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { EmptyRow } from "@/components/EmptyState";
import { Shell } from "@/components/Shell";
import { materialClient, productClient } from "@/lib/clients";
import { CreateProductRequestSchema } from "@/gen/stillhouse/v1/product_pb";
import { SpiritKind } from "@/gen/stillhouse/v1/recipe_pb";
import { Product } from "@/gen/stillhouse/v1/product_pb";
import { formatCAD, spiritKindLabel } from "@/lib/format";
import { WriteOnly } from "@/lib/role";

const spiritOptions = [
  { v: SpiritKind.CANADIAN_WHISKY, label: "Canadian Whisky" },
  { v: SpiritKind.RYE_WHISKY, label: "Rye Whisky" },
  { v: SpiritKind.WHISKY, label: "Whisky" },
  { v: SpiritKind.GIN, label: "Gin" },
  { v: SpiritKind.VODKA, label: "Vodka" },
  { v: SpiritKind.RUM, label: "Rum" },
  { v: SpiritKind.BRANDY, label: "Brandy" },
  { v: SpiritKind.LIQUEUR, label: "Liqueur" },
  { v: SpiritKind.OTHER, label: "Other" },
];

export function ProductsPage() {
  const qc = useQueryClient();
  const list = useQuery({
    queryKey: ["listProducts"],
    queryFn: () => productClient.listProducts({}),
  });
  const [showForm, setShowForm] = useState(false);
  // Trade and label details are edited apart from the production ones:
  // bottle size and strength change what is in the bottle, a GTIN or a
  // case configuration changes how it is sold, and they are set by
  // different people on different days.
  const [editingSKU, setEditingSKU] = useState<string | null>(null);

  const createProduct = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof CreateProductRequestSchema>>) =>
      productClient.createProduct(msg),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["listProducts"] });
      setShowForm(false);
    },
  });

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const fd = new FormData(e.currentTarget);
    createProduct.mutate(
      create(CreateProductRequestSchema, {
        name: fd.get("name")?.toString() ?? "",
        spiritKind: Number(fd.get("spirit_kind")) as SpiritKind,
        bottleSizeMl: Number(fd.get("bottle_size_ml") ?? 0),
        targetAbvPct: Number(fd.get("target_abv_pct") ?? 0),
        labelNotes: fd.get("label_notes")?.toString() ?? "",
      }),
    );
  }

  return (
    <Shell>
      <div className="mb-6 flex items-center justify-between">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">Products</h1>
          <p className="text-sm text-fg-muted">Finished-product SKUs: name, bottle size, bottle proof.</p>
        </div>
        <WriteOnly>
          <button
            onClick={() => setShowForm((s) => !s)}
            className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover"
          >
            {showForm ? "Cancel" : "New product"}
          </button>
        </WriteOnly>
      </div>

      {showForm && (
        <form
          onSubmit={submit}
          className="mb-6 grid grid-cols-2 gap-4 rounded-lg border border-border bg-surface-2 p-5 shadow-sm"
        >
          <Field label="Name" name="name" required />
          <Field label="Spirit kind" name="spirit_kind" as="select">
            {spiritOptions.map((s) => <option key={s.v} value={s.v}>{s.label}</option>)}
          </Field>
          <Field label="Bottle size (mL)" name="bottle_size_ml" type="number" defaultValue="750" required />
          <Field label="Target ABV %" name="target_abv_pct" type="number" step="0.1" defaultValue="40" required />
          <Field label="Label notes" name="label_notes" className="col-span-2" />
          <div className="col-span-2 flex items-center gap-3">
            <button
              type="submit"
              disabled={createProduct.isPending}
              className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
            >
              {createProduct.isPending ? "Saving…" : "Save"}
            </button>
            {createProduct.error && (
              <span className="text-sm text-danger-fg">
                {createProduct.error instanceof ConnectError
                  ? createProduct.error.rawMessage
                  : String(createProduct.error)}
              </span>
            )}
          </div>
        </form>
      )}

      {editingSKU && (
        <SKUPanel
          product={list.data?.products.find((p) => p.id === editingSKU)}
          onClose={() => setEditingSKU(null)}
          onSaved={() => {
            qc.invalidateQueries({ queryKey: ["listProducts"] });
            setEditingSKU(null);
          }}
        />
      )}

      <div className="overflow-x-auto rounded-lg border border-border bg-surface-2 shadow-sm">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-3">Name</th>
              <th className="px-4 py-3">Spirit</th>
              <th className="px-4 py-3 text-right">Bottle (mL)</th>
              <th className="px-4 py-3 text-right">Target ABV</th>
              <th className="px-4 py-3 text-right">Avg cost/bottle</th>
              <th className="px-4 py-3">GTIN</th>
              <th className="px-4 py-3 text-right">Per case</th>
              <th className="px-4 py-3">Notes</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {list.isLoading && (
              <tr><td colSpan={8} className="px-4 py-3 text-fg-muted">Loading…</td></tr>
            )}
            {!list.isLoading && list.data?.products.length === 0 && (
              <EmptyRow
                colSpan={8}
                title="No products yet"
                message="A product is a finished SKU — bottle size, target proof, label notes. Define one before recording a bottling run."
                action={
                  <WriteOnly>
                    <button
                      onClick={() => setShowForm(true)}
                      className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover"
                    >
                      New product
                    </button>
                  </WriteOnly>
                }
              />
            )}
            {list.data?.products.map((p) => (
              <tr key={p.id} onClick={() => setEditingSKU(p.id)} className="cursor-pointer hover:bg-surface-3">
                <td className="px-4 py-3 font-medium text-fg">{p.name}</td>
                <td className="px-4 py-3 text-fg-muted">{spiritKindLabel(p.spiritKind)}</td>
                <td className="px-4 py-3 text-right text-fg-muted">{p.bottleSizeMl}</td>
                <td className="px-4 py-3 text-right text-fg-muted">{p.targetAbvPct.toFixed(1)}%</td>
                <td className="px-4 py-3 text-right text-fg-muted"><CostCell productId={p.id} /></td>
                <td className="px-4 py-3 font-mono text-xs text-fg-muted">
                  {p.gtin || <span className="text-fg-subtle">—</span>}
                </td>
                <td className="px-4 py-3 text-right text-fg-muted">
                  {p.bottlesPerCase || <span className="text-fg-subtle">—</span>}
                </td>
                <td className="px-4 py-3 text-fg-muted">{p.labelNotes}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Shell>
  );
}

function CostCell({ productId }: { productId: string }) {
  // Lazy-loaded so the products list doesn't fan out into N+1 cost calls
  // on every render — only fires when the cell mounts. Stays cheap because
  // useQuery dedupes across re-renders.
  const { data, isLoading, error } = useQuery({
    queryKey: ["productCostSummary", productId],
    queryFn: () => materialClient.productCostSummary({ productId }),
    enabled: !!productId,
    staleTime: 60_000,
  });
  if (isLoading) return <span className="text-fg-subtle">…</span>;
  if (error) return <span className="text-fg-subtle">—</span>;
  if (!data || data.totalBottles === 0) return <span className="text-fg-subtle">—</span>;
  return (
    <span title={`${data.runCount} run${data.runCount === 1 ? "" : "s"}, ${data.totalBottles.toLocaleString()} bottles`}>
      {formatCAD(data.averageMaterialCostPerBottleCad)}
      {data.runsWithMissingPrices > 0 && (
        <span className="ml-1 text-warning-fg" title={`${data.runsWithMissingPrices} runs missing price data`}>*</span>
      )}
    </span>
  );
}

function Field({
  label, name, type = "text", as = "input", required, defaultValue, step, children, className,
}: {
  label: string; name: string; type?: string; as?: "input" | "select";
  required?: boolean; defaultValue?: string; step?: string;
  children?: React.ReactNode; className?: string;
}) {
  return (
    <div className={className}>
      <label className="mb-2 block text-sm font-medium text-fg-muted">{label}</label>
      {as === "select" ? (
        <select
          name={name}
          required={required}
          defaultValue={defaultValue}
          className="w-full rounded border border-border-strong px-3 py-2 text-sm"
        >
          {children}
        </select>
      ) : (
        <input
          name={name}
          type={type}
          step={step}
          required={required}
          defaultValue={defaultValue}
          className="w-full rounded border border-border-strong px-3 py-2 text-sm"
        />
      )}
    </div>
  );
}

/**
 * Trade and label details for one SKU.
 *
 * Everything here is the licensee's declaration, not Stillhouse's
 * derivation. In particular the common name — the standard-of-identity
 * name under Division 2 of the Food and Drug Regulations — is not
 * inferred from the spirit kind, and the age statement is not taken from
 * the maturation clock. Whether a spirit qualifies, and what a blend may
 * claim, rest on how it was made and how long it sat. Filling either in
 * automatically would be putting words on somebody's label.
 */
function SKUPanel({
  product, onClose, onSaved,
}: {
  product?: Product;
  onClose: () => void;
  onSaved: () => void;
}) {
  const save = useMutation({
    mutationFn: (m: Parameters<typeof productClient.updateProductSKU>[0]) =>
      productClient.updateProductSKU(m),
    onSuccess: onSaved,
  });
  if (!product) return null;

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        const fd = new FormData(e.currentTarget);
        const num = (k: string) => Number(fd.get(k) ?? 0) || 0;
        save.mutate({
          id: product.id,
          gtin: fd.get("gtin")?.toString() ?? "",
          cspcCode: fd.get("cspc_code")?.toString() ?? "",
          bottlesPerCase: num("bottles_per_case"),
          casesPerLayer: num("cases_per_layer"),
          layersPerPallet: num("layers_per_pallet"),
          caseGrossWeightKg: num("case_weight"),
          commonName: fd.get("common_name")?.toString() ?? "",
          ageStatement: fd.get("age_statement")?.toString() ?? "",
          containerMarking: fd.get("container_marking")?.toString() ?? "",
          allergenStatement: fd.get("allergen_statement")?.toString() ?? "",
          countryOfOrigin: fd.get("country_of_origin")?.toString() ?? "",
          marketingDescription: fd.get("marketing_description")?.toString() ?? "",
        });
      }}
      className="mb-6 rounded-lg border border-border bg-surface-2 p-5 shadow-sm"
    >
      <div className="mb-4 flex items-baseline justify-between">
        <h2 className="text-sm font-semibold text-fg">{product.name} — trade &amp; label</h2>
        <button type="button" onClick={onClose} className="text-xs text-fg-muted hover:text-fg">
          Close
        </button>
      </div>

      <p className="mb-3 text-xs font-semibold uppercase tracking-wider text-fg-subtle">Identifiers</p>
      <div className="mb-4 grid gap-3 sm:grid-cols-2">
        <SKUField label="GTIN" name="gtin" defaultValue={product.gtin}
                  help="8, 12, 13 or 14 digits. The check digit is verified." />
        <SKUField label="Board product number (CSPC)" name="cspc_code" defaultValue={product.cspcCode} />
      </div>

      <p className="mb-3 text-xs font-semibold uppercase tracking-wider text-fg-subtle">Case &amp; pallet</p>
      <div className="mb-4 grid gap-3 sm:grid-cols-4">
        <SKUField label="Bottles per case" name="bottles_per_case" type="number"
                  defaultValue={product.bottlesPerCase ? String(product.bottlesPerCase) : ""} />
        <SKUField label="Cases per layer" name="cases_per_layer" type="number"
                  defaultValue={product.casesPerLayer ? String(product.casesPerLayer) : ""} />
        <SKUField label="Layers per pallet" name="layers_per_pallet" type="number"
                  defaultValue={product.layersPerPallet ? String(product.layersPerPallet) : ""} />
        <SKUField label="Case weight (kg)" name="case_weight" type="number" step="0.01"
                  defaultValue={product.caseGrossWeightKg ? String(product.caseGrossWeightKg) : ""} />
      </div>

      <p className="mb-1 text-xs font-semibold uppercase tracking-wider text-fg-subtle">Label</p>
      <p className="mb-3 text-xs text-fg-muted">
        These are your declarations. Stillhouse doesn't infer the common name from the
        spirit kind or the age from the maturation clock — whether a spirit qualifies,
        and what a blend may claim, are yours to say.
      </p>
      <div className="mb-4 grid gap-3 sm:grid-cols-2">
        <SKUField label="Common name (standard of identity)" name="common_name"
                  defaultValue={product.commonName} help="&quot;Canadian Whisky&quot;, &quot;Gin&quot;, &quot;Vodka&quot;…" />
        <SKUField label="Age statement" name="age_statement" defaultValue={product.ageStatement} />
        <SKUField label="Country of origin" name="country_of_origin" defaultValue={product.countryOfOrigin} />
        <SKUField label="Allergen statement" name="allergen_statement" defaultValue={product.allergenStatement} />
        <SKUField label="Container marking (Excise Act s.87)" name="container_marking"
                  className="sm:col-span-2" defaultValue={product.containerMarking} />
        <SKUField label="Marketing description" name="marketing_description"
                  className="sm:col-span-2" defaultValue={product.marketingDescription} />
      </div>

      <WriteOnly>
        <div className="flex items-center gap-3">
          <button type="submit" disabled={save.isPending}
                  className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50">
            {save.isPending ? "Saving…" : "Save"}
          </button>
          {save.error && (
            <span className="text-sm text-danger-fg">
              {save.error instanceof ConnectError ? save.error.rawMessage : String(save.error)}
            </span>
          )}
        </div>
      </WriteOnly>
    </form>
  );
}

function SKUField({ label, name, type = "text", step, defaultValue, help, className }: {
  label: string; name: string; type?: string; step?: string;
  defaultValue?: string; help?: string; className?: string;
}) {
  return (
    <div className={className}>
      <label className="mb-1 block text-xs text-fg-muted">{label}</label>
      <input
        key={defaultValue}
        name={name}
        type={type}
        step={step}
        defaultValue={defaultValue ?? ""}
        className="w-full rounded border border-border-strong px-2 py-1.5 text-sm"
      />
      {help && <p className="mt-1 text-xs text-fg-subtle">{help}</p>}
    </div>
  );
}
