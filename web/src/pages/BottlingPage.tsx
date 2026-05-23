import { FormEvent, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { Shell } from "@/components/Shell";
import {
  bottlingClient,
  bulkClient,
  exciseStampClient,
  productClient,
} from "@/lib/clients";
import { CreateBottlingRunRequestSchema, VoidBottlingRunRequestSchema } from "@/gen/stillhouse/v1/bottling_pb";
import { formatLAA, formatQty } from "@/lib/format";
import { WriteOnly, canWrite, useCurrentRole } from "@/lib/role";

const PAGE_SIZE = 50;

export function BottlingPage() {
  const qc = useQueryClient();
  const role = useCurrentRole();
  const writeable = canWrite(role);
  const [page, setPage] = useState(0);
  const runs = useQuery({
    queryKey: ["listBottlingRuns", page],
    queryFn: () =>
      bottlingClient.listBottlingRuns({ limit: PAGE_SIZE, offset: page * PAGE_SIZE }),
  });
  const packaged = useQuery({
    queryKey: ["listPackagedInventory"],
    queryFn: () => bottlingClient.listPackagedInventory({}),
  });
  const products = useQuery({
    queryKey: ["listProducts"],
    queryFn: () => productClient.listProducts({}),
  });
  const containers = useQuery({
    queryKey: ["listBulkContainers"],
    queryFn: () => bulkClient.listBulkContainers({}),
  });
  const stamps = useQuery({
    queryKey: ["listStampOrders"],
    queryFn: () => exciseStampClient.listStampOrders({}),
  });

  const [showForm, setShowForm] = useState(false);
  const [productId, setProductId] = useState("");
  const [sourceId, setSourceId] = useState("");
  const [jurisdiction, setJurisdiction] = useState("");
  const [bottleCount, setBottleCount] = useState("");
  const [lotCode, setLotCode] = useState("");
  const [bottlingLoss, setBottlingLoss] = useState("0");
  const [notes, setNotes] = useState("");

  const product = products.data?.products.find((p) => p.id === productId);
  const source = containers.data?.containers.find((c) => c.id === sourceId);
  const jurisdictionSummary = stamps.data?.summaries.find((s) => s.jurisdiction === jurisdiction);

  const requiredVol = useMemo(() => {
    const n = Number(bottleCount);
    if (!product || !Number.isFinite(n) || n <= 0) return null;
    return n * product.bottleSizeMl / 1000 + Number(bottlingLoss || 0);
  }, [product, bottleCount, bottlingLoss]);

  const createRun = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof CreateBottlingRunRequestSchema>>) =>
      bottlingClient.createBottlingRun(msg),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["listBottlingRuns"] });
      qc.invalidateQueries({ queryKey: ["listPackagedInventory"] });
      qc.invalidateQueries({ queryKey: ["listBulkContainers"] });
      qc.invalidateQueries({ queryKey: ["listStampOrders"] });
      setShowForm(false);
      setBottleCount("");
      setLotCode("");
    },
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    createRun.mutate(
      create(CreateBottlingRunRequestSchema, {
        productId,
        sourceContainerId: sourceId,
        destinationJurisdiction: jurisdiction,
        bottleCount: Number(bottleCount),
        bottlingLossL: Number(bottlingLoss || 0),
        lotCode,
        notes,
      }),
    );
  }

  const voidRun = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof VoidBottlingRunRequestSchema>>) =>
      bottlingClient.voidBottlingRun(msg),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["listBottlingRuns"] });
      qc.invalidateQueries({ queryKey: ["listPackagedInventory"] });
      qc.invalidateQueries({ queryKey: ["listBulkContainers"] });
      qc.invalidateQueries({ queryKey: ["listStampOrders"] });
    },
  });

  function onVoidRun(id: string, no: number, bottles: number) {
    const reason = window.prompt(
      `Void bottling run #${no}? This will refund ${bottles.toLocaleString()} bottles to inventory, ` +
        `release the applied stamps, and add the LAA back to the source tank. ` +
        `Will fail if any of those bottles have already been removed. Reason:`,
      "recorded in error",
    );
    if (!reason || !reason.trim()) return;
    voidRun.mutate(create(VoidBottlingRunRequestSchema, { id, reason: reason.trim() }));
  }

  return (
    <Shell>
      <div className="mb-6 flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">Bottling</h1>
          <p className="text-sm text-stone-500">
            A bottling run debits the source container, applies province-coded stamps,
            and produces packaged inventory.
          </p>
        </div>
        <WriteOnly>
          <button
            onClick={() => setShowForm((s) => !s)}
            className="rounded bg-stone-900 px-3 py-2 text-sm font-medium text-white hover:bg-stone-800"
          >
            {showForm ? "Cancel" : "New bottling run"}
          </button>
        </WriteOnly>
      </div>

      {showForm && (
        <form
          onSubmit={submit}
          className="mb-6 grid grid-cols-2 gap-4 rounded-lg border border-stone-200 bg-white p-5 shadow-sm"
        >
          <div>
            <label className="mb-1 block text-xs font-medium text-stone-600">Product</label>
            <select value={productId} onChange={(e) => setProductId(e.target.value)} required className="w-full rounded border border-stone-300 px-3 py-2 text-sm">
              <option value="">Select product…</option>
              {products.data?.products.map((p) => (
                <option key={p.id} value={p.id}>{p.name} ({p.bottleSizeMl} mL @ {p.targetAbvPct}%)</option>
              ))}
            </select>
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-stone-600">Source container</label>
            <select value={sourceId} onChange={(e) => setSourceId(e.target.value)} required className="w-full rounded border border-stone-300 px-3 py-2 text-sm">
              <option value="">Select source…</option>
              {containers.data?.containers
                .filter((c) => !c.archived && c.currentVolumeL > 0)
                .map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name} ({formatQty(c.currentVolumeL)} L @ {c.currentAbvPct.toFixed(1)}%)
                  </option>
                ))}
            </select>
            {source && (
              <p className="mt-1 text-xs text-stone-500">
                {formatLAA(source.currentLaa)} L LAA on hand
              </p>
            )}
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-stone-600">Destination jurisdiction</label>
            <input value={jurisdiction} onChange={(e) => setJurisdiction(e.target.value)} required placeholder="CA-ON" className="w-full rounded border border-stone-300 px-3 py-2 text-sm" />
            {jurisdictionSummary && (
              <p className="mt-1 text-xs text-stone-500">
                {jurisdictionSummary.totalOnHand.toLocaleString()} stamps on hand for {jurisdiction}
              </p>
            )}
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-stone-600">Bottle count</label>
            <input value={bottleCount} onChange={(e) => setBottleCount(e.target.value)} type="number" min="1" required className="w-full rounded border border-stone-300 px-3 py-2 text-sm" />
            {requiredVol !== null && (
              <p className="mt-1 text-xs text-stone-500">
                Will draw {formatQty(requiredVol)} L from {source?.name ?? "source"}
              </p>
            )}
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-stone-600">Lot code</label>
            <input value={lotCode} onChange={(e) => setLotCode(e.target.value)} required placeholder="L2026-0001" className="w-full rounded border border-stone-300 px-3 py-2 text-sm" />
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-stone-600">Bottling loss (L)</label>
            <input value={bottlingLoss} onChange={(e) => setBottlingLoss(e.target.value)} type="number" step="0.01" min="0" className="w-full rounded border border-stone-300 px-3 py-2 text-sm" />
          </div>
          <div className="col-span-2">
            <label className="mb-1 block text-xs font-medium text-stone-600">Notes</label>
            <input value={notes} onChange={(e) => setNotes(e.target.value)} className="w-full rounded border border-stone-300 px-3 py-2 text-sm" />
          </div>
          <div className="col-span-2 flex items-center gap-3">
            <button
              type="submit"
              disabled={createRun.isPending}
              className="rounded bg-stone-900 px-3 py-2 text-sm font-medium text-white hover:bg-stone-800 disabled:bg-stone-400"
            >
              {createRun.isPending ? "Bottling…" : "Run bottling"}
            </button>
            {createRun.error && (
              <span className="text-sm text-red-600">
                {createRun.error instanceof ConnectError
                  ? createRun.error.rawMessage
                  : String(createRun.error)}
              </span>
            )}
          </div>
        </form>
      )}

      <h2 className="mb-3 text-sm font-semibold uppercase text-stone-500">Recent bottling runs</h2>
      <div className="mb-8 overflow-hidden rounded-lg border border-stone-200 bg-white shadow-sm">
        <table className="min-w-full divide-y divide-stone-200 text-sm">
          <thead className="bg-stone-50 text-left text-xs uppercase text-stone-500">
            <tr>
              <th className="px-4 py-3">#</th>
              <th className="px-4 py-3">Date</th>
              <th className="px-4 py-3">Product</th>
              <th className="px-4 py-3">Lot</th>
              <th className="px-4 py-3">Jurisdiction</th>
              <th className="px-4 py-3 text-right">Bottles</th>
              <th className="px-4 py-3 text-right">LAA</th>
              {writeable && <th className="px-4 py-3"></th>}
            </tr>
          </thead>
          <tbody className="divide-y divide-stone-100">
            {runs.data?.runs.length === 0 && (
              <tr><td colSpan={writeable ? 8 : 7} className="px-4 py-3 text-stone-500">No runs yet.</td></tr>
            )}
            {runs.data?.runs.map((r) => {
              const voided = !!r.voidedAt;
              return (
                <tr key={r.id} className={voided ? "bg-stone-50 text-stone-400" : ""}>
                  <td className="px-4 py-3 font-medium">
                    <Link to={`/bottling/${r.id}`} className="hover:underline">#{r.runNo}</Link>
                    {voided && (
                      <span className="ml-2 rounded bg-red-100 px-1.5 py-0.5 text-xs font-normal text-red-700">VOIDED</span>
                    )}
                  </td>
                  <td className="px-4 py-3">{r.bottlingDate}</td>
                  <td className="px-4 py-3">
                    {r.productName}
                    {voided && r.voidedReason && (
                      <div className="text-xs italic">{r.voidedReason}</div>
                    )}
                  </td>
                  <td className="px-4 py-3">{r.lotCode}</td>
                  <td className="px-4 py-3">{r.destinationJurisdiction}</td>
                  <td className={`px-4 py-3 text-right ${voided ? "line-through" : ""}`}>{r.bottleCount.toLocaleString()}</td>
                  <td className={`px-4 py-3 text-right font-medium ${voided ? "line-through" : "text-stone-900"}`}>{formatLAA(r.tankGaugeLaa)}</td>
                  {writeable && (
                    <td className="px-4 py-3 text-right">
                      {!voided && (
                        <button
                          onClick={() => onVoidRun(r.id, r.runNo, r.bottleCount)}
                          disabled={voidRun.isPending}
                          className="text-xs text-stone-600 hover:text-red-700 disabled:opacity-50"
                        >
                          Void
                        </button>
                      )}
                    </td>
                  )}
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      <Pager
        page={page}
        pageSize={PAGE_SIZE}
        total={runs.data?.totalCount ?? 0}
        onPage={setPage}
      />

      <h2 className="mb-3 text-sm font-semibold uppercase text-stone-500">Packaged inventory</h2>
      <div className="overflow-hidden rounded-lg border border-stone-200 bg-white shadow-sm">
        <table className="min-w-full divide-y divide-stone-200 text-sm">
          <thead className="bg-stone-50 text-left text-xs uppercase text-stone-500">
            <tr>
              <th className="px-4 py-3">Product</th>
              <th className="px-4 py-3">Lot</th>
              <th className="px-4 py-3">Jurisdiction</th>
              <th className="px-4 py-3 text-right">On hand</th>
              <th className="px-4 py-3 text-right">Packaged</th>
              <th className="px-4 py-3 text-right">Removed</th>
              <th className="px-4 py-3 text-right">Age</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-stone-100">
            {packaged.data?.rows.length === 0 && (
              <tr><td colSpan={7} className="px-4 py-3 text-stone-500">Nothing packaged yet.</td></tr>
            )}
            {packaged.data?.rows.map((r) => (
              <tr key={r.id}>
                <td className="px-4 py-3 font-medium text-stone-900">{r.productName}</td>
                <td className="px-4 py-3 text-stone-600">{r.lotCode}</td>
                <td className="px-4 py-3 text-stone-600">{r.jurisdiction}</td>
                <td className="px-4 py-3 text-right font-medium text-stone-900">{r.bottlesOnHand.toLocaleString()}</td>
                <td className="px-4 py-3 text-right text-stone-600">{r.bottlesPackaged.toLocaleString()}</td>
                <td className="px-4 py-3 text-right text-stone-600">{r.bottlesRemoved.toLocaleString()}</td>
                <td className="px-4 py-3 text-right text-stone-500">
                  <PackagedAge bottledOn={r.firstBottledDate} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Shell>
  );
}

// PackagedAge renders "12d" / "8mo" / "2 yr" / "—" for a packaged inventory
// row's first-bottled date. Amber when older than 365 days — most distilled
// spirits sit happily in the warehouse for years, but the visual nudge helps
// operators spot lots they might want to push.
function PackagedAge({ bottledOn }: { bottledOn: string }) {
  if (!bottledOn) return <>—</>;
  const days = Math.floor((Date.now() - Date.parse(bottledOn + "T00:00:00Z")) / 86_400_000);
  if (!Number.isFinite(days) || days < 0) return <>—</>;
  const stale = days >= 365;
  let label: string;
  if (days < 60) label = `${days}d`;
  else if (days < 730) label = `${Math.round(days / 30)}mo`;
  else label = `${(days / 365).toFixed(1)} yr`;
  return <span className={stale ? "text-amber-700" : ""}>{label}</span>;
}

// Pager renders prev / next + "showing N–M of T" for a server-paginated list.
// Hidden entirely when there's only one page.
export function Pager({
  page, pageSize, total, onPage,
}: { page: number; pageSize: number; total: number; onPage: (n: number) => void }) {
  const pageCount = Math.max(1, Math.ceil(total / pageSize));
  if (pageCount <= 1) return null;
  const from = page * pageSize + 1;
  const to = Math.min((page + 1) * pageSize, total);
  return (
    <div className="mt-3 flex items-center justify-between text-xs text-stone-500">
      <span>Showing {from.toLocaleString()}–{to.toLocaleString()} of {total.toLocaleString()}</span>
      <div className="flex gap-2">
        <button
          disabled={page === 0}
          onClick={() => onPage(Math.max(0, page - 1))}
          className="rounded border border-stone-300 px-2 py-1 hover:bg-stone-100 disabled:opacity-40"
        >
          ← Prev
        </button>
        <button
          disabled={page >= pageCount - 1}
          onClick={() => onPage(page + 1)}
          className="rounded border border-stone-300 px-2 py-1 hover:bg-stone-100 disabled:opacity-40"
        >
          Next →
        </button>
      </div>
    </div>
  );
}
