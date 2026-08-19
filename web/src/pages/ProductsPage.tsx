import { FormEvent, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { EmptyRow } from "@/components/EmptyState";
import { Shell } from "@/components/Shell";
import { materialClient, productClient } from "@/lib/clients";
import { CreateProductRequestSchema } from "@/gen/stillhouse/v1/product_pb";
import { SpiritKind } from "@/gen/stillhouse/v1/recipe_pb";
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

      <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-3">Name</th>
              <th className="px-4 py-3">Spirit</th>
              <th className="px-4 py-3 text-right">Bottle (mL)</th>
              <th className="px-4 py-3 text-right">Target ABV</th>
              <th className="px-4 py-3 text-right">Avg cost/bottle</th>
              <th className="px-4 py-3">Notes</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {list.isLoading && (
              <tr><td colSpan={6} className="px-4 py-3 text-fg-muted">Loading…</td></tr>
            )}
            {!list.isLoading && list.data?.products.length === 0 && (
              <EmptyRow
                colSpan={6}
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
              <tr key={p.id}>
                <td className="px-4 py-3 font-medium text-fg">{p.name}</td>
                <td className="px-4 py-3 text-fg-muted">{spiritKindLabel(p.spiritKind)}</td>
                <td className="px-4 py-3 text-right text-fg-muted">{p.bottleSizeMl}</td>
                <td className="px-4 py-3 text-right text-fg-muted">{p.targetAbvPct.toFixed(1)}%</td>
                <td className="px-4 py-3 text-right text-fg-muted"><CostCell productId={p.id} /></td>
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
