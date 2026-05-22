import { FormEvent, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { Shell } from "@/components/Shell";
import { productClient } from "@/lib/clients";
import { CreateProductRequestSchema } from "@/gen/stillhouse/v1/product_pb";
import { SpiritKind } from "@/gen/stillhouse/v1/recipe_pb";
import { spiritKindLabel } from "@/lib/format";

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
          <h1 className="text-2xl font-semibold">Products</h1>
          <p className="text-sm text-stone-500">Finished-product SKUs: name, bottle size, bottle proof.</p>
        </div>
        <button
          onClick={() => setShowForm((s) => !s)}
          className="rounded bg-stone-900 px-3 py-2 text-sm font-medium text-white hover:bg-stone-800"
        >
          {showForm ? "Cancel" : "New product"}
        </button>
      </div>

      {showForm && (
        <form
          onSubmit={submit}
          className="mb-6 grid grid-cols-2 gap-4 rounded-lg border border-stone-200 bg-white p-5 shadow-sm"
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
              className="rounded bg-stone-900 px-3 py-2 text-sm font-medium text-white hover:bg-stone-800 disabled:bg-stone-400"
            >
              {createProduct.isPending ? "Saving…" : "Save"}
            </button>
            {createProduct.error && (
              <span className="text-sm text-red-600">
                {createProduct.error instanceof ConnectError
                  ? createProduct.error.rawMessage
                  : String(createProduct.error)}
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
              <th className="px-4 py-3">Spirit</th>
              <th className="px-4 py-3 text-right">Bottle (mL)</th>
              <th className="px-4 py-3 text-right">Target ABV</th>
              <th className="px-4 py-3">Notes</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-stone-100">
            {list.isLoading && (
              <tr><td colSpan={5} className="px-4 py-3 text-stone-500">Loading…</td></tr>
            )}
            {!list.isLoading && list.data?.products.length === 0 && (
              <tr><td colSpan={5} className="px-4 py-3 text-stone-500">No products yet.</td></tr>
            )}
            {list.data?.products.map((p) => (
              <tr key={p.id}>
                <td className="px-4 py-3 font-medium text-stone-900">{p.name}</td>
                <td className="px-4 py-3 text-stone-600">{spiritKindLabel(p.spiritKind)}</td>
                <td className="px-4 py-3 text-right text-stone-600">{p.bottleSizeMl}</td>
                <td className="px-4 py-3 text-right text-stone-600">{p.targetAbvPct.toFixed(1)}%</td>
                <td className="px-4 py-3 text-stone-600">{p.labelNotes}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Shell>
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
      <label className="mb-1 block text-xs font-medium text-stone-600">{label}</label>
      {as === "select" ? (
        <select
          name={name}
          required={required}
          defaultValue={defaultValue}
          className="w-full rounded border border-stone-300 px-3 py-2 text-sm"
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
          className="w-full rounded border border-stone-300 px-3 py-2 text-sm"
        />
      )}
    </div>
  );
}
