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
  const [receivedAt, setReceivedAt] = useState("");
  const [notes, setNotes] = useState("");

  const recordReceipt = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof RecordMaterialReceiptRequestSchema>>) =>
      materialClient.recordMaterialReceipt(msg),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["listMaterialLots", id] });
      setSupplierLot("");
      setQuantity("");
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
        // receivedAt left unset = server uses time.Now() (Stage 5 fix).
      }),
    );
  }

  if (!id) return <Shell><p>Missing material id.</p></Shell>;
  if (material.isLoading) return <Shell><p className="text-stone-500">Loading…</p></Shell>;
  if (!material.data?.material) return <Shell><p>Material not found.</p></Shell>;

  const m = material.data.material;
  const totalOnHand = (lots.data?.lots ?? []).reduce((s, l) => s + l.quantityOnHand, 0);
  const totalReceived = (lots.data?.lots ?? []).reduce((s, l) => s + l.quantityReceived, 0);

  return (
    <Shell>
      <header className="mb-6">
        <h1 className="text-2xl font-semibold">{m.name}</h1>
        <p className="text-sm text-stone-500">
          {materialKindLabel(m.kind)} · {m.uom}
          {m.supplier && <> · {m.supplier}</>}
          {m.extractPctSet && <> · extract {(m.extractPct * 100).toFixed(2)}%</>}
        </p>
        {m.notes && <p className="mt-2 text-sm text-stone-700">{m.notes}</p>}
      </header>

      <section className="mb-6 grid grid-cols-2 gap-4 sm:grid-cols-3">
        <Stat label={`On hand (${m.uom})`} value={formatQty(totalOnHand)} highlight />
        <Stat label={`Total received (${m.uom})`} value={formatQty(totalReceived)} />
        <Stat label="Lot count" value={String((lots.data?.lots ?? []).length)} />
      </section>

      <section className="mb-8 rounded-lg border border-stone-200 bg-white p-5 shadow-sm">
        <h2 className="mb-3 text-sm font-semibold uppercase text-stone-500">Record receipt</h2>
        <form onSubmit={submit} className="grid grid-cols-2 gap-4">
          <div>
            <label className="mb-1 block text-xs font-medium text-stone-600">Supplier lot</label>
            <input
              value={supplierLot}
              onChange={(e) => setSupplierLot(e.target.value)}
              placeholder="optional"
              className="w-full rounded border border-stone-300 px-3 py-2 text-sm"
            />
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-stone-600">Quantity received ({m.uom})</label>
            <input
              type="number"
              step="0.01"
              min="0"
              value={quantity}
              onChange={(e) => setQuantity(e.target.value)}
              required
              className="w-full rounded border border-stone-300 px-3 py-2 text-sm"
            />
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-stone-600">Received at (optional)</label>
            <input
              type="date"
              value={receivedAt}
              onChange={(e) => setReceivedAt(e.target.value)}
              className="w-full rounded border border-stone-300 px-3 py-2 text-sm"
            />
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-stone-600">Notes</label>
            <input
              value={notes}
              onChange={(e) => setNotes(e.target.value)}
              className="w-full rounded border border-stone-300 px-3 py-2 text-sm"
            />
          </div>
          <div className="col-span-2 flex items-center gap-3">
            <button
              type="submit"
              disabled={recordReceipt.isPending}
              className="rounded bg-stone-900 px-3 py-2 text-sm font-medium text-white hover:bg-stone-800 disabled:bg-stone-400"
            >
              {recordReceipt.isPending ? "Saving…" : "Record receipt"}
            </button>
            {recordReceipt.error && (
              <span className="text-sm text-red-600">
                {recordReceipt.error instanceof ConnectError ? recordReceipt.error.rawMessage : String(recordReceipt.error)}
              </span>
            )}
          </div>
        </form>
      </section>

      <h2 className="mb-3 text-sm font-semibold uppercase text-stone-500">Lot history</h2>
      <div className="overflow-hidden rounded-lg border border-stone-200 bg-white shadow-sm">
        <table className="min-w-full divide-y divide-stone-200 text-sm">
          <thead className="bg-stone-50 text-left text-xs uppercase text-stone-500">
            <tr>
              <th className="px-4 py-3">Received</th>
              <th className="px-4 py-3">Supplier lot</th>
              <th className="px-4 py-3 text-right">Received qty</th>
              <th className="px-4 py-3 text-right">On hand</th>
              <th className="px-4 py-3">Notes</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-stone-100">
            {lots.isLoading && (
              <tr><td colSpan={5} className="px-4 py-3 text-stone-500">Loading…</td></tr>
            )}
            {!lots.isLoading && (lots.data?.lots ?? []).length === 0 && (
              <tr><td colSpan={5} className="px-4 py-3 text-stone-500">No lots recorded yet.</td></tr>
            )}
            {lots.data?.lots.map((l) => (
              <tr key={l.id}>
                <td className="px-4 py-3 text-stone-600">
                  {l.receivedAt ? new Date(Number(l.receivedAt.seconds) * 1000).toLocaleDateString() : ""}
                </td>
                <td className="px-4 py-3 text-stone-600">{l.supplierLot || "—"}</td>
                <td className="px-4 py-3 text-right text-stone-600">{formatQty(l.quantityReceived)}</td>
                <td className="px-4 py-3 text-right font-medium text-stone-900">{formatQty(l.quantityOnHand)}</td>
                <td className="px-4 py-3 text-stone-600">{l.notes}</td>
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
    <div className={`rounded-lg border bg-white p-4 shadow-sm ${highlight ? "border-emerald-200" : "border-stone-200"}`}>
      <p className="text-xs uppercase text-stone-500">{label}</p>
      <p className={`mt-1 text-xl font-semibold ${highlight ? "text-emerald-700" : "text-stone-900"}`}>{value}</p>
    </div>
  );
}
