import { FormEvent, useState } from "react";
import { useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { Shell } from "@/components/Shell";
import { materialClient } from "@/lib/clients";
import { RecordMaterialReceiptRequestSchema } from "@/gen/stillhouse/v1/material_pb";
import { formatQty, materialKindLabel } from "@/lib/format";

export function MaterialDetailPage() {
  const { id } = useParams();
  const qc = useQueryClient();

  const material = useQuery({
    queryKey: ["getMaterial", id],
    queryFn: () => materialClient.getMaterial({ id: id! }),
    enabled: !!id,
  });
  const lots = useQuery({
    queryKey: ["listMaterialLots", id],
    queryFn: () => materialClient.listMaterialLots({ materialId: id! }),
    enabled: !!id,
  });
  const [supplierLot, setSupplierLot] = useState("");
  const [quantity, setQuantity] = useState("");
  const [unitCost, setUnitCost] = useState("");
  const [receivedAt, setReceivedAt] = useState("");
  const [notes, setNotes] = useState("");

  const recordReceipt = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof RecordMaterialReceiptRequestSchema>>) =>
      materialClient.recordMaterialReceipt(msg),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["listMaterialLots", id] });
      setSupplierLot("");
      setQuantity("");
      setUnitCost("");
      setNotes("");
    },
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    if (!id || !quantity) return;
    recordReceipt.mutate(
      create(RecordMaterialReceiptRequestSchema, {
        materialId: id,
        supplierLot,
        quantityReceived: Number(quantity),
        notes,
        unitCostCad: unitCost ? Number(unitCost) : 0,
        unitCostCadSet: !!unitCost,
        // receivedAt left unset = server uses time.Now() (Stage 5 fix).
      }),
    );
  }

  if (!id) return <Shell><p>Missing material id.</p></Shell>;
  if (material.isLoading) return <Shell><p className="text-fg-muted">Loading…</p></Shell>;
  if (!material.data?.material) return <Shell><p>Material not found.</p></Shell>;

  const m = material.data.material;
  const totalOnHand = (lots.data?.lots ?? []).reduce((s, l) => s + l.quantityOnHand, 0);
  const totalReceived = (lots.data?.lots ?? []).reduce((s, l) => s + l.quantityReceived, 0);

  return (
    <Shell>
      <header className="mb-6">
        <h1 className="text-3xl font-bold tracking-tight">{m.name}</h1>
        <p className="text-sm text-fg-muted">
          {materialKindLabel(m.kind)} · {m.uom}
          {m.supplier && <> · {m.supplier}</>}
          {m.extractPctSet && <> · extract {(m.extractPct * 100).toFixed(2)}%</>}
        </p>
        {m.notes && <p className="mt-2 text-sm text-fg">{m.notes}</p>}
      </header>

      <section className="mb-6 grid grid-cols-2 gap-4 sm:grid-cols-3">
        <Stat label={`On hand (${m.uom})`} value={formatQty(totalOnHand)} highlight />
        <Stat label={`Total received (${m.uom})`} value={formatQty(totalReceived)} />
        <Stat label="Lot count" value={String((lots.data?.lots ?? []).length)} />
      </section>

      <section className="mb-8 rounded-lg border border-border bg-surface-2 p-5 shadow-sm">
        <h2 className="mb-3 text-sm font-semibold text-fg-muted">Record receipt</h2>
        <form onSubmit={submit} className="grid grid-cols-2 gap-4">
          <div>
            <label className="mb-2 block text-sm font-medium text-fg-muted">Supplier lot</label>
            <input
              value={supplierLot}
              onChange={(e) => setSupplierLot(e.target.value)}
              placeholder="optional"
              className="w-full rounded border border-border-strong px-3 py-2 text-sm"
            />
          </div>
          <div>
            <label className="mb-2 block text-sm font-medium text-fg-muted">Quantity received ({m.uom})</label>
            <input
              type="number"
              step="0.01"
              min="0"
              value={quantity}
              onChange={(e) => setQuantity(e.target.value)}
              required
              className="w-full rounded border border-border-strong px-3 py-2 text-sm"
            />
          </div>
          <div>
            <label className="mb-2 block text-sm font-medium text-fg-muted">Received at (optional)</label>
            <input
              type="date"
              value={receivedAt}
              onChange={(e) => setReceivedAt(e.target.value)}
              className="w-full rounded border border-border-strong px-3 py-2 text-sm"
            />
          </div>
          <div>
            <label className="mb-2 block text-sm font-medium text-fg-muted">Unit cost (CAD/{m.uom}, optional)</label>
            <input
              type="number"
              step="0.001"
              min="0"
              value={unitCost}
              onChange={(e) => setUnitCost(e.target.value)}
              className="w-full rounded border border-border-strong px-3 py-2 text-sm"
            />
          </div>
          <div>
            <label className="mb-2 block text-sm font-medium text-fg-muted">Notes</label>
            <input
              value={notes}
              onChange={(e) => setNotes(e.target.value)}
              className="w-full rounded border border-border-strong px-3 py-2 text-sm"
            />
          </div>
          <div className="col-span-2 flex items-center gap-3">
            <button
              type="submit"
              disabled={recordReceipt.isPending}
              className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
            >
              {recordReceipt.isPending ? "Saving…" : "Record receipt"}
            </button>
            {recordReceipt.error && (
              <span className="text-sm text-red-400">
                {recordReceipt.error instanceof ConnectError ? recordReceipt.error.rawMessage : String(recordReceipt.error)}
              </span>
            )}
          </div>
        </form>
      </section>

      <h2 className="mb-3 text-sm font-semibold text-fg-muted">Lot history</h2>
      <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-3">Received</th>
              <th className="px-4 py-3">Supplier lot</th>
              <th className="px-4 py-3 text-right">Received qty</th>
              <th className="px-4 py-3 text-right">On hand</th>
              <th className="px-4 py-3 text-right">Unit cost (CAD)</th>
              <th className="px-4 py-3">Notes</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {lots.isLoading && (
              <tr><td colSpan={6} className="px-4 py-3 text-fg-muted">Loading…</td></tr>
            )}
            {!lots.isLoading && (lots.data?.lots ?? []).length === 0 && (
              <tr><td colSpan={6} className="px-4 py-3 text-fg-muted">No lots recorded yet.</td></tr>
            )}
            {lots.data?.lots.map((l) => (
              <tr key={l.id}>
                <td className="px-4 py-3 text-fg-muted">
                  {l.receivedAt ? new Date(Number(l.receivedAt.seconds) * 1000).toLocaleDateString() : ""}
                </td>
                <td className="px-4 py-3 text-fg-muted">{l.supplierLot || "—"}</td>
                <td className="px-4 py-3 text-right text-fg-muted">{formatQty(l.quantityReceived)}</td>
                <td className="px-4 py-3 text-right font-medium text-fg">{formatQty(l.quantityOnHand)}</td>
                <td className="px-4 py-3 text-right text-fg-muted">{l.unitCostCadSet ? `$${l.unitCostCad.toFixed(3)}` : "—"}</td>
                <td className="px-4 py-3 text-fg-muted">{l.notes}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Shell>
  );
}

function Stat({ label, value, highlight }: { label: string; value: string; highlight?: boolean }) {
  return (
    <div className={`rounded-lg border bg-surface-2 p-4 shadow-sm ${highlight ? "border-emerald-500/30" : "border-border"}`}>
      <p className="text-xs text-fg-muted">{label}</p>
      <p className={`mt-1 text-xl font-semibold ${highlight ? "text-emerald-400" : "text-fg"}`}>{value}</p>
    </div>
  );
}
