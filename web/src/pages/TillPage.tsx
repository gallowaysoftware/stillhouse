import { FormEvent, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { Callout } from "@/components/Callout";
import { Shell } from "@/components/Shell";
import { bottlingClient, posClient } from "@/lib/clients";
import { formatCAD } from "@/lib/format";
import { OwnerOnly, WriteOnly } from "@/lib/role";

/**
 * TillPage — tasting-room and web sales, and what became of each.
 *
 * The screen is a queue rather than a log because that is what the
 * failure mode demands. A sale whose SKU nobody mapped is rejected and
 * KEPT, so the gap between what the till took and what reached the return
 * is always visible and always explained by something — either a removal,
 * or a rejection with a reason, or somebody's decision to ignore it.
 *
 * A sale that quietly vanished would be the under-reporting this whole
 * feature exists to prevent, arriving through the door it opened.
 */
function errText(e: unknown): string {
  return e instanceof ConnectError ? e.rawMessage : String(e);
}

export function TillPage() {
  const qc = useQueryClient();
  const [tab, setTab] = useState<"sales" | "skus">("sales");
  return (
    <Shell>
      <div className="mb-6">
        <h1 className="text-3xl font-bold tracking-tight">Till</h1>
        <p className="text-sm text-fg-muted">
          Sales arriving from a point of sale, and the duty-paid removals they
          become. Ingest is idempotent on the till's own line id, so a system
          that delivers a sale twice — and they all do — cannot report the duty
          twice.
        </p>
      </div>

      <div className="mb-4 flex gap-1 border-b border-border text-sm">
        {([["sales", "Sales"], ["skus", "SKUs"]] as const).map(([k, label]) => (
          <button
            key={k}
            onClick={() => setTab(k)}
            className={`-mb-px border-b-2 px-3 py-2 ${
              tab === k ? "border-accent text-fg" : "border-transparent text-fg-muted hover:text-fg"
            }`}
          >
            {label}
          </button>
        ))}
      </div>

      {tab === "sales" && <SalesTab qc={qc} />}
      {tab === "skus" && <SKUTab />}
    </Shell>
  );
}

function SalesTab({ qc }: { qc: ReturnType<typeof useQueryClient> }) {
  const [status, setStatus] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [result, setResult] = useState<string | null>(null);

  const sales = useQuery({
    queryKey: ["posSales", status],
    queryFn: () => posClient.listPOSSales({ status, limit: 200 }),
  });

  const post = useMutation({
    mutationFn: () => posClient.postPOSSales({}),
    onSuccess: (r) => {
      setErr(null);
      setResult(
        `${r.posted} posted, ${r.rejected} rejected` +
          (r.rejections.length ? ` — ${r.rejections.join("; ")}` : ""),
      );
      void qc.invalidateQueries({ queryKey: ["posSales"] });
    },
    onError: (e) => setErr(errText(e)),
  });

  const ignore = useMutation({
    mutationFn: (v: { id: string; reason: string }) => posClient.ignorePOSSale(v),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["posSales"] }),
    onError: (e) => setErr(errText(e)),
  });

  const d = sales.data;
  return (
    <>
      {d && (
        <div className="mb-4 grid gap-3 sm:grid-cols-4">
          <Stat label="Pending" value={String(d.pending)} />
          <Stat label="Posted" value={String(d.posted)} />
          <Stat label="Rejected" value={String(d.rejected)} />
          <Stat label="Ignored" value={String(d.ignored)} />
        </div>
      )}

      {d && d.rejected > 0 && (
        <Callout tone="warning" title={`${d.rejected} sale(s) did not reach the return`}>
          Each is kept with its reason. Map the SKU or fix the stock, then post
          again — a sale that never posts is a sale missing from a filed return.
        </Callout>
      )}

      <div className="my-4 flex flex-wrap items-center gap-3">
        <select
          value={status}
          onChange={(e) => setStatus(e.target.value)}
          className="rounded border border-border-strong px-2 py-1 text-sm"
        >
          <option value="">All</option>
          <option value="pending">Pending</option>
          <option value="rejected">Rejected</option>
          <option value="posted">Posted</option>
          <option value="ignored">Ignored</option>
        </select>
        <WriteOnly>
          <button
            onClick={() => post.mutate()}
            disabled={post.isPending}
            className="rounded bg-accent px-3 py-1 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
          >
            {post.isPending ? "Posting…" : "Post pending sales"}
          </button>
        </WriteOnly>
      </div>

      {result && <Callout tone="info" title="Posted">{result}</Callout>}
      {err && <Callout tone="danger" title="That failed">{err}</Callout>}

      <div className="mt-4 overflow-x-auto rounded-lg border border-border bg-surface-2">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-2">Sold</th>
              <th className="px-4 py-2">Source</th>
              <th className="px-4 py-2">SKU</th>
              <th className="px-4 py-2">Product</th>
              <th className="px-4 py-2 text-right">Qty</th>
              <th className="px-4 py-2 text-right">Price</th>
              <th className="px-4 py-2">Status</th>
              <th className="px-4 py-2"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {(d?.sales ?? []).length === 0 && (
              <tr><td colSpan={8} className="px-4 py-3 text-fg-muted">Nothing from the till yet.</td></tr>
            )}
            {d?.sales.map((s) => (
              <tr key={s.id}>
                <td className="px-4 py-2 tabular-nums">{s.soldAt.slice(0, 10)}</td>
                <td className="px-4 py-2">{s.source}</td>
                <td className="px-4 py-2 font-mono text-xs">{s.externalSku}</td>
                <td className="px-4 py-2">{s.productName || <span className="text-fg-muted">unmapped</span>}</td>
                <td className="px-4 py-2 text-right tabular-nums">{s.quantity}</td>
                <td className="px-4 py-2 text-right tabular-nums">
                  {s.unitPriceSet ? formatCAD(s.unitPriceCad) : "—"}
                </td>
                <td className={`px-4 py-2 ${s.status === "rejected" ? "text-danger-fg" : ""}`}>
                  {s.status}
                  {s.rejectReason && (
                    <span className="block max-w-md text-xs text-fg-muted">{s.rejectReason}</span>
                  )}
                </td>
                <td className="px-4 py-2 text-right">
                  {(s.status === "pending" || s.status === "rejected") && (
                    <WriteOnly>
                      <button
                        onClick={() => {
                          const reason = window.prompt(
                            "Why is this sale not going on a return? (a test sale, a comp, a correction)",
                          );
                          if (!reason) return;
                          ignore.mutate({ id: s.id, reason });
                        }}
                        className="text-xs underline"
                      >
                        ignore
                      </button>
                    </WriteOnly>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  );
}

function SKUTab() {
  const qc = useQueryClient();
  const maps = useQuery({
    queryKey: ["posMappings"],
    queryFn: () => posClient.listPOSProductMappings({}),
  });
  const products = useQuery({
    queryKey: ["products"],
    queryFn: () => bottlingClient.listPackagedInventory({}),
  });
  const [source, setSource] = useState("square");
  const [sku, setSku] = useState("");
  const [productId, setProductId] = useState("");
  const [err, setErr] = useState<string | null>(null);

  const save = useMutation({
    mutationFn: () => posClient.savePOSProductMapping({ source, externalSku: sku, productId }),
    onSuccess: () => {
      setErr(null);
      setSku("");
      void qc.invalidateQueries({ queryKey: ["posMappings"] });
      void qc.invalidateQueries({ queryKey: ["posSales"] });
    },
    onError: (e) => setErr(errText(e)),
  });
  const remove = useMutation({
    mutationFn: (id: string) => posClient.deletePOSProductMapping({ id }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["posMappings"] }),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    setErr(null);
    save.mutate();
  }

  // Distinct products, from the lots on hand.
  const opts = new Map<string, string>();
  for (const l of products.data?.rows ?? []) opts.set(l.productId, l.productName);

  return (
    <>
      <Callout tone="info" title="Stillhouse will not guess a SKU">
        A sale posted against the wrong product is wrong duty and wrong stock on
        a filed return, so an unmapped SKU is rejected and kept rather than
        guessed at. Map it here and post the sale again.
      </Callout>

      <OwnerOnly>
        <form onSubmit={submit} className="my-4 grid gap-3 rounded-lg border border-border bg-surface-2 p-4 sm:grid-cols-4">
          <label className="text-sm">
            <span className="mb-1 block text-fg-muted">Source</span>
            <input value={source} onChange={(e) => setSource(e.target.value)}
                   className="w-full rounded border border-border-strong px-2 py-1" />
          </label>
          <label className="text-sm">
            <span className="mb-1 block text-fg-muted">SKU</span>
            <input value={sku} onChange={(e) => setSku(e.target.value)}
                   className="w-full rounded border border-border-strong px-2 py-1" />
          </label>
          <label className="text-sm">
            <span className="mb-1 block text-fg-muted">Product</span>
            <select value={productId} onChange={(e) => setProductId(e.target.value)}
                    className="w-full rounded border border-border-strong px-2 py-1">
              <option value="">Choose…</option>
              {[...opts].map(([id, name]) => <option key={id} value={id}>{name}</option>)}
            </select>
          </label>
          <div className="flex items-end">
            <button type="submit" disabled={save.isPending || !sku || !productId}
                    className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50">
              Map
            </button>
          </div>
          {err && <p className="col-span-4 text-sm text-danger-fg">{err}</p>}
        </form>
      </OwnerOnly>

      <div className="overflow-hidden rounded-lg border border-border bg-surface-2">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-2">Source</th>
              <th className="px-4 py-2">SKU</th>
              <th className="px-4 py-2">Product</th>
              <th className="px-4 py-2"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {(maps.data?.mappings ?? []).length === 0 && (
              <tr><td colSpan={4} className="px-4 py-3 text-fg-muted">No SKUs mapped.</td></tr>
            )}
            {maps.data?.mappings.map((m) => (
              <tr key={m.id}>
                <td className="px-4 py-2">{m.source}</td>
                <td className="px-4 py-2 font-mono text-xs">{m.externalSku}</td>
                <td className="px-4 py-2">{m.productName}</td>
                <td className="px-4 py-2 text-right">
                  <OwnerOnly>
                    <button onClick={() => remove.mutate(m.id)} className="text-xs text-danger-fg underline">
                      remove
                    </button>
                  </OwnerOnly>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg border border-border bg-surface-2 p-3">
      <p className="text-xs text-fg-muted">{label}</p>
      <p className="mt-1 text-xl font-semibold tabular-nums">{value}</p>
    </div>
  );
}
