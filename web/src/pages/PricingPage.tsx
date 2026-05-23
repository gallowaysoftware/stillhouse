import { FormEvent, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { Shell } from "@/components/Shell";
import { pricingClient, productClient } from "@/lib/clients";
import { ComputeProvincialPricingRequestSchema } from "@/gen/stillhouse/v1/pricing_pb";
import { formatQty } from "@/lib/format";

export function PricingPage() {
  const products = useQuery({
    queryKey: ["listProducts"],
    queryFn: () => productClient.listProducts({}),
  });
  const [productId, setProductId] = useState("");
  const [fob, setFob] = useState("");

  const compute = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof ComputeProvincialPricingRequestSchema>>) =>
      pricingClient.computeProvincialPricing(msg),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    compute.mutate(
      create(ComputeProvincialPricingRequestSchema, {
        productId,
        fobCad: Number(fob),
      }),
    );
  }

  const data = compute.data;

  return (
    <Shell>
      <div className="mb-6">
        <h1 className="text-3xl font-bold tracking-tight">Provincial pricing</h1>
        <p className="text-sm text-fg-muted">
          Run an FOB price for one of your products through each province's
          monopoly markup to see the rough consumer shelf price. Markups are
          published values as of late 2025 / early 2026; treat as estimates.
        </p>
      </div>

      <form
        onSubmit={submit}
        className="mb-8 flex flex-wrap items-end gap-3 rounded-lg border border-border bg-surface-2 p-5 shadow-sm"
      >
        <div>
          <label className="mb-2 block text-sm font-medium text-fg-muted">Product</label>
          <select
            value={productId}
            onChange={(e) => setProductId(e.target.value)}
            required
            className="rounded border border-border-strong px-3 py-2 text-sm"
          >
            <option value="">Select product…</option>
            {products.data?.products.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name} ({p.bottleSizeMl} mL @ {p.targetAbvPct}%)
              </option>
            ))}
          </select>
        </div>
        <div>
          <label className="mb-2 block text-sm font-medium text-fg-muted">FOB price (CAD)</label>
          <input
            value={fob}
            onChange={(e) => setFob(e.target.value)}
            type="number"
            step="0.01"
            required
            placeholder="14.00"
            className="w-32 rounded border border-border-strong px-3 py-2 text-sm"
          />
        </div>
        <button
          type="submit"
          disabled={compute.isPending}
          className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
        >
          {compute.isPending ? "Computing…" : "Compute"}
        </button>
        {compute.error && (
          <span className="text-sm text-danger-fg">
            {compute.error instanceof ConnectError ? compute.error.rawMessage : String(compute.error)}
          </span>
        )}
      </form>

      {data && (
        <section>
          <p className="mb-3 text-sm text-fg-muted">
            For <span className="font-medium">{data.productName}</span> ({data.bottleSizeMl} mL @ {data.bottleAbvPct}%):
          </p>
          <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
            <table className="min-w-full divide-y divide-border text-sm">
              <thead className="bg-surface-3 text-left text-xs text-fg-muted">
                <tr>
                  <th className="px-4 py-3">Jurisdiction</th>
                  <th className="px-4 py-3 text-right">FOB</th>
                  <th className="px-4 py-3 text-right">Markup</th>
                  <th className="px-4 py-3 text-right">Per-L</th>
                  <th className="px-4 py-3 text-right">Basic tax</th>
                  <th className="px-4 py-3 text-right">Excise</th>
                  <th className="px-4 py-3 text-right">Deposit</th>
                  <th className="px-4 py-3 text-right">Shelf (pre-HST)</th>
                  <th className="px-4 py-3 text-right">On-site net</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {data.breakdowns.map((b) => (
                  <tr key={b.jurisdiction}>
                    <td className="px-4 py-3">
                      <div className="font-medium text-fg">{b.name}</div>
                      <div className="text-xs text-fg-subtle">{b.jurisdiction}</div>
                    </td>
                    <td className="px-4 py-3 text-right text-fg-muted">${formatQty(b.fobCad)}</td>
                    <td className="px-4 py-3 text-right text-fg-muted">${formatQty(b.markupCad)}</td>
                    <td className="px-4 py-3 text-right text-fg-muted">${formatQty(b.perLitreCad)}</td>
                    <td className="px-4 py-3 text-right text-fg-muted">${formatQty(b.basicTaxCad)}</td>
                    <td className="px-4 py-3 text-right text-fg-muted">${formatQty(b.federalExciseCad)}</td>
                    <td className="px-4 py-3 text-right text-fg-muted">${formatQty(b.containerDepositCad)}</td>
                    <td className="px-4 py-3 text-right font-medium text-fg">${formatQty(b.shelfBeforeSalesTax)}</td>
                    <td className="px-4 py-3 text-right font-medium text-success-fg">${formatQty(b.onSiteRetailNetCad)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="mt-6 space-y-2 text-xs text-fg-muted">
            {data.breakdowns.map((b) => (
              <p key={b.jurisdiction}>
                <span className="font-medium text-fg">{b.jurisdiction}:</span> {b.notes}
              </p>
            ))}
          </div>
        </section>
      )}
    </Shell>
  );
}
