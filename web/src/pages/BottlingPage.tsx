import { FormEvent, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { Button } from "@/components/Button";
import { ReductionCalculator } from "@/components/ReductionCalculator";
import { Shell } from "@/components/Shell";
import {
  bottlingClient,
  bulkClient,
  exciseStampClient,
  productClient,
} from "@/lib/clients";
import { CreateBottlingRunRequestSchema, VoidBottlingRunRequestSchema } from "@/gen/stillhouse/v1/bottling_pb";
import { formatLAA, formatQty } from "@/lib/format";
import { useConfirm } from "@/components/ConfirmDialog";
import { WriteOnly, canWrite, useCurrentRole } from "@/lib/role";

const PAGE_SIZE = 50;

export function BottlingPage() {
  const confirm = useConfirm();
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
  const [showReduce, setShowReduce] = useState(false);
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

  async function onVoidRun(id: string, no: number, bottles: number) {
    const ok = await confirm({
      title: `Void bottling run #${no}?`,
      body: <>This reverses every side-effect of the run: stamps, packaged inventory, and the source tank balance.</>,
      consequences: [
        `${bottles.toLocaleString()} bottles drop out of packaged inventory`,
        "Applied stamps go back to available",
        "Source tank LAA is refunded via an offsetting bulk movement",
        "Fails if any of these bottles have been removed downstream — void those removals first",
      ],
      requireReason: { label: "Reason", placeholder: "recorded in error" },
      confirmLabel: "Void run",
      tone: "danger",
    });
    if (!ok) return;
    voidRun.mutate(create(VoidBottlingRunRequestSchema, { id, reason: ok.reason }));
  }

  return (
    <Shell>
      <div className="mb-6 flex items-center justify-between">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">Bottling</h1>
          <p className="text-sm text-fg-muted">
            A bottling run debits the source container, applies province-coded stamps,
            and produces packaged inventory.
          </p>
        </div>
        <WriteOnly>
          <div className="flex items-center gap-2">
            <Button variant="secondary" onClick={() => setShowReduce((s) => !s)}>
              {showReduce ? "Hide calculator" : "Reduce to strength"}
            </Button>
            <Button onClick={() => setShowForm((s) => !s)}>
              {showForm ? "Cancel" : "New bottling run"}
            </Button>
          </div>
        </WriteOnly>
      </div>

      {/* Proofing down is the step before a bottling run, so it lives here
          rather than making the operator go find a tank page. */}
      {showReduce && (
        <section className="mb-6 max-w-xl">
          <ReductionCalculator title="Reduce to bottling strength" />
        </section>
      )}

      {showForm && (
        <form
          onSubmit={submit}
          className="mb-6 grid grid-cols-1 sm:grid-cols-2 gap-3 sm:gap-4 rounded-lg border border-border bg-surface-2 p-4 sm:p-5 shadow-sm"
        >
          <div>
            <label className="mb-2 block text-sm font-medium text-fg-muted">Product</label>
            <select value={productId} onChange={(e) => setProductId(e.target.value)} required className="w-full rounded border border-border-strong px-3 py-2 text-sm">
              <option value="">Select product…</option>
              {products.data?.products.map((p) => (
                <option key={p.id} value={p.id}>{p.name} ({p.bottleSizeMl} mL @ {p.targetAbvPct}%)</option>
              ))}
            </select>
          </div>
          <div>
            <label className="mb-2 block text-sm font-medium text-fg-muted">Source container</label>
            <select value={sourceId} onChange={(e) => setSourceId(e.target.value)} required className="w-full rounded border border-border-strong px-3 py-2 text-sm">
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
              <p className="mt-1 text-xs text-fg-muted">
                {formatLAA(source.currentLaa)} L LAA on hand
              </p>
            )}
          </div>
          <div>
            <label className="mb-2 block text-sm font-medium text-fg-muted">Destination jurisdiction</label>
            <input value={jurisdiction} onChange={(e) => setJurisdiction(e.target.value)} required placeholder="CA-ON" className="w-full rounded border border-border-strong px-3 py-2 text-sm" />
            {jurisdictionSummary && (
              <p className="mt-1 text-xs text-fg-muted">
                {jurisdictionSummary.totalOnHand.toLocaleString()} stamps on hand for {jurisdiction}
              </p>
            )}
          </div>
          <div>
            <label className="mb-2 block text-sm font-medium text-fg-muted">Bottle count</label>
            <input value={bottleCount} onChange={(e) => setBottleCount(e.target.value)} type="number" min="1" required className="w-full rounded border border-border-strong px-3 py-2 text-sm" />
            {requiredVol !== null && (
              <p className="mt-1 text-xs text-fg-muted">
                Will draw {formatQty(requiredVol)} L from {source?.name ?? "source"}
              </p>
            )}
          </div>
          <div>
            <label className="mb-2 block text-sm font-medium text-fg-muted">Lot code</label>
            <input value={lotCode} onChange={(e) => setLotCode(e.target.value)} required placeholder="L2026-0001" className="w-full rounded border border-border-strong px-3 py-2 text-sm" />
          </div>
          <div>
            <label className="mb-2 block text-sm font-medium text-fg-muted">Bottling loss (L)</label>
            <input value={bottlingLoss} onChange={(e) => setBottlingLoss(e.target.value)} type="number" step="0.01" min="0" className="w-full rounded border border-border-strong px-3 py-2 text-sm" />
          </div>
          <div className="col-span-2">
            <label className="mb-2 block text-sm font-medium text-fg-muted">Notes</label>
            <input value={notes} onChange={(e) => setNotes(e.target.value)} className="w-full rounded border border-border-strong px-3 py-2 text-sm" />
          </div>
          <div className="col-span-2 flex items-center gap-3">
            <button
              type="submit"
              disabled={createRun.isPending}
              className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
            >
              {createRun.isPending ? "Bottling…" : "Run bottling"}
            </button>
            {createRun.error && (
              <span className="text-sm text-danger-fg">
                {createRun.error instanceof ConnectError
                  ? createRun.error.rawMessage
                  : String(createRun.error)}
              </span>
            )}
          </div>
        </form>
      )}

      <h2 className="mb-3 text-sm font-semibold text-fg-muted">Recent bottling runs</h2>
      <div className="mb-8 overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
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
          <tbody className="divide-y divide-border">
            {runs.data?.runs.length === 0 && (
              <tr><td colSpan={writeable ? 8 : 7} className="px-4 py-3 text-fg-muted">No runs yet.</td></tr>
            )}
            {runs.data?.runs.map((r) => {
              const voided = !!r.voidedAt;
              return (
                <tr key={r.id} className={voided ? "bg-surface-3 text-fg-subtle" : ""}>
                  <td className="px-4 py-3 font-medium">
                    <Link to={`/bottling/${r.id}`} className="hover:underline">#{r.runNo}</Link>
                    {voided && (
                      <span className="ml-2 rounded bg-danger/15 px-1.5 py-0.5 text-xs font-normal text-danger-fg">VOIDED</span>
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
                  <td className={`px-4 py-3 text-right font-medium ${voided ? "line-through" : "text-fg"}`}>{formatLAA(r.tankGaugeLaa)}</td>
                  {writeable && (
                    <td className="px-4 py-3 text-right">
                      {!voided && (
                        <button
                          onClick={() => onVoidRun(r.id, r.runNo, r.bottleCount)}
                          disabled={voidRun.isPending}
                          className="text-xs text-fg-muted hover:text-danger-fg disabled:opacity-50"
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

      <h2 className="mb-3 text-sm font-semibold text-fg-muted">Packaged inventory</h2>
      <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
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
          <tbody className="divide-y divide-border">
            {packaged.data?.rows.length === 0 && (
              <tr><td colSpan={7} className="px-4 py-3 text-fg-muted">Nothing packaged yet.</td></tr>
            )}
            {packaged.data?.rows.map((r) => (
              <tr key={r.id}>
                <td className="px-4 py-3 font-medium text-fg">{r.productName}</td>
                <td className="px-4 py-3 text-fg-muted">{r.lotCode}</td>
                <td className="px-4 py-3 text-fg-muted">{r.jurisdiction}</td>
                <td className="px-4 py-3 text-right font-medium text-fg">{r.bottlesOnHand.toLocaleString()}</td>
                <td className="px-4 py-3 text-right text-fg-muted">{r.bottlesPackaged.toLocaleString()}</td>
                <td className="px-4 py-3 text-right text-fg-muted">{r.bottlesRemoved.toLocaleString()}</td>
                <td className="px-4 py-3 text-right text-fg-muted">
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
  return <span className={stale ? "text-warning-fg" : ""}>{label}</span>;
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
    <div className="mt-3 flex items-center justify-between text-xs text-fg-muted">
      <span>Showing {from.toLocaleString()}–{to.toLocaleString()} of {total.toLocaleString()}</span>
      <div className="flex gap-2">
        <button
          disabled={page === 0}
          onClick={() => onPage(Math.max(0, page - 1))}
          className="rounded border border-border-strong px-2 py-1 hover:bg-surface-3 disabled:opacity-40"
        >
          ← Prev
        </button>
        <button
          disabled={page >= pageCount - 1}
          onClick={() => onPage(page + 1)}
          className="rounded border border-border-strong px-2 py-1 hover:bg-surface-3 disabled:opacity-40"
        >
          Next →
        </button>
      </div>
    </div>
  );
}
