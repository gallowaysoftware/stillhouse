import { FormEvent, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { Shell } from "@/components/Shell";
import { exciseStampClient } from "@/lib/clients";
import { WriteOnly, canWrite, useCurrentRole } from "@/lib/role";
import {
  CreateStampOrderRequestSchema,
  ExciseStampOrderStatus,
  ReceiveStampOrderRequestSchema,
  VoidStampsRequestSchema,
} from "@/gen/stillhouse/v1/excise_stamp_pb";

const statusLabel: Record<ExciseStampOrderStatus, string> = {
  [ExciseStampOrderStatus.UNSPECIFIED]: "—",
  [ExciseStampOrderStatus.ORDERED]: "Ordered",
  [ExciseStampOrderStatus.RECEIVED]: "Received",
  [ExciseStampOrderStatus.CLOSED]: "Closed",
};

export function StampsPage() {
  const qc = useQueryClient();
  const role = useCurrentRole();
  const writeable = canWrite(role);
  const list = useQuery({
    queryKey: ["listStampOrders"],
    queryFn: () => exciseStampClient.listStampOrders({}),
  });
  const [showOrderForm, setShowOrderForm] = useState(false);
  const [receivingId, setReceivingId] = useState<string | null>(null);

  const createOrder = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof CreateStampOrderRequestSchema>>) =>
      exciseStampClient.createStampOrder(msg),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["listStampOrders"] });
      setShowOrderForm(false);
    },
  });
  const receive = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof ReceiveStampOrderRequestSchema>>) =>
      exciseStampClient.receiveStampOrder(msg),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["listStampOrders"] });
      setReceivingId(null);
    },
  });
  const voidStamps = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof VoidStampsRequestSchema>>) =>
      exciseStampClient.voidStamps(msg),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["listStampOrders"] }),
  });

  function onVoid(orderId: string, available: number) {
    if (available <= 0) return;
    const qtyRaw = window.prompt(`How many stamps to void? (max ${available})`, "1");
    if (!qtyRaw) return;
    const qty = Number(qtyRaw);
    if (!Number.isFinite(qty) || qty <= 0 || qty > available) return;
    const reason = window.prompt("Reason (damaged, misprint, misapplied, …):", "damaged in application");
    if (!reason || !reason.trim()) return;
    voidStamps.mutate(
      create(VoidStampsRequestSchema, { id: orderId, quantity: qty, reason: reason.trim() }),
    );
  }

  function submitOrder(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const fd = new FormData(e.currentTarget);
    createOrder.mutate(
      create(CreateStampOrderRequestSchema, {
        jurisdiction: fd.get("jurisdiction")?.toString() ?? "",
        quantityOrdered: Number(fd.get("quantity_ordered") ?? 0),
        notes: fd.get("notes")?.toString() ?? "",
      }),
    );
  }

  function submitReceive(orderId: string, e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const fd = new FormData(e.currentTarget);
    receive.mutate(
      create(ReceiveStampOrderRequestSchema, {
        id: orderId,
        quantityReceived: Number(fd.get("quantity_received") ?? 0),
        serialStart: fd.get("serial_start")?.toString() ?? "",
        serialEnd: fd.get("serial_end")?.toString() ?? "",
      }),
    );
  }

  return (
    <Shell>
      <div className="mb-6 flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">Excise stamps</h1>
          <p className="text-sm text-stone-500">
            Province-coded stamps from CRA. Order, receive, then apply at bottling.
            Jurisdiction codes use ISO 3166-2 (CA-ON, CA-QC, CA-BC, …).
          </p>
        </div>
        <WriteOnly>
          <button
            onClick={() => setShowOrderForm((s) => !s)}
            className="rounded bg-stone-900 px-3 py-2 text-sm font-medium text-white hover:bg-stone-800"
          >
            {showOrderForm ? "Cancel" : "Order stamps"}
          </button>
        </WriteOnly>
      </div>

      {list.data?.summaries && list.data.summaries.length > 0 && (
        <section className="mb-6 grid grid-cols-2 gap-4 sm:grid-cols-4">
          {list.data.summaries.map((s) => (
            <div key={s.jurisdiction} className="rounded-lg border border-stone-200 bg-white p-4 shadow-sm">
              <p className="text-xs uppercase text-stone-500">{s.jurisdiction}</p>
              <p className="mt-1 text-2xl font-semibold text-stone-900">{s.totalOnHand.toLocaleString()}</p>
              <p className="text-xs text-stone-500">
                on hand · {s.totalApplied.toLocaleString()} applied · {s.totalReceived.toLocaleString()} received
              </p>
            </div>
          ))}
        </section>
      )}

      {showOrderForm && (
        <form
          onSubmit={submitOrder}
          className="mb-6 grid grid-cols-3 gap-4 rounded-lg border border-stone-200 bg-white p-5 shadow-sm"
        >
          <div>
            <label className="mb-1 block text-xs font-medium text-stone-600">Jurisdiction</label>
            <input name="jurisdiction" placeholder="CA-ON" required className="w-full rounded border border-stone-300 px-3 py-2 text-sm" />
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-stone-600">Quantity</label>
            <input name="quantity_ordered" type="number" min="1" required className="w-full rounded border border-stone-300 px-3 py-2 text-sm" />
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-stone-600">Notes</label>
            <input name="notes" className="w-full rounded border border-stone-300 px-3 py-2 text-sm" />
          </div>
          <div className="col-span-3 flex items-center gap-3">
            <button
              type="submit"
              disabled={createOrder.isPending}
              className="rounded bg-stone-900 px-3 py-2 text-sm font-medium text-white hover:bg-stone-800 disabled:bg-stone-400"
            >
              {createOrder.isPending ? "Saving…" : "Place order"}
            </button>
            {createOrder.error && (
              <span className="text-sm text-red-600">
                {createOrder.error instanceof ConnectError
                  ? createOrder.error.rawMessage
                  : String(createOrder.error)}
              </span>
            )}
          </div>
        </form>
      )}

      <div className="overflow-hidden rounded-lg border border-stone-200 bg-white shadow-sm">
        <table className="min-w-full divide-y divide-stone-200 text-sm">
          <thead className="bg-stone-50 text-left text-xs uppercase text-stone-500">
            <tr>
              <th className="px-4 py-3">Jurisdiction</th>
              <th className="px-4 py-3">Ordered</th>
              <th className="px-4 py-3">Status</th>
              <th className="px-4 py-3 text-right">Ordered</th>
              <th className="px-4 py-3 text-right">Received</th>
              <th className="px-4 py-3 text-right">Applied</th>
              <th className="px-4 py-3 text-right">On hand</th>
              <th className="px-4 py-3">Serials</th>
              <th className="px-4 py-3"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-stone-100">
            {list.data?.orders.length === 0 && (
              <tr><td colSpan={9} className="px-4 py-3 text-stone-500">No stamp orders yet.</td></tr>
            )}
            {list.data?.orders.map((o) => (
              <tr key={o.id}>
                <td className="px-4 py-3 font-medium text-stone-900">{o.jurisdiction}</td>
                <td className="px-4 py-3 text-stone-600">
                  {o.orderedAt ? new Date(Number(o.orderedAt.seconds) * 1000).toLocaleDateString() : ""}
                </td>
                <td className="px-4 py-3 text-stone-600">{statusLabel[o.status]}</td>
                <td className="px-4 py-3 text-right text-stone-600">{o.quantityOrdered}</td>
                <td className="px-4 py-3 text-right text-stone-600">{o.quantityReceived}</td>
                <td className="px-4 py-3 text-right text-stone-600">{o.quantityApplied}</td>
                <td className="px-4 py-3 text-right font-medium text-stone-900">{o.availableCount}</td>
                <td className="px-4 py-3 text-stone-600">
                  {o.serialStart && o.serialEnd ? `${o.serialStart}..${o.serialEnd}` : "—"}
                </td>
                <td className="px-4 py-3 space-x-3">
                  {o.status === ExciseStampOrderStatus.ORDERED && writeable && (
                    <button
                      onClick={() => setReceivingId(receivingId === o.id ? null : o.id)}
                      className="text-stone-600 hover:text-stone-900"
                    >
                      {receivingId === o.id ? "Cancel" : "Receive"}
                    </button>
                  )}
                  {o.availableCount > 0 && writeable && (
                    <button
                      onClick={() => onVoid(o.id, o.availableCount)}
                      disabled={voidStamps.isPending}
                      className="text-xs text-stone-600 hover:text-red-700 disabled:opacity-50"
                    >
                      Void
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {receivingId && (() => {
        const order = list.data?.orders.find((o) => o.id === receivingId);
        if (!order) return null;
        return (
          <form
            onSubmit={(e) => submitReceive(order.id, e)}
            className="mt-4 grid grid-cols-3 gap-4 rounded-lg border border-stone-200 bg-white p-5 shadow-sm"
          >
            <h2 className="col-span-3 text-sm font-semibold uppercase text-stone-500">
              Receive order for {order.jurisdiction}
            </h2>
            <div>
              <label className="mb-1 block text-xs font-medium text-stone-600">Quantity received</label>
              <input
                name="quantity_received"
                type="number"
                min="1"
                defaultValue={order.quantityOrdered}
                required
                className="w-full rounded border border-stone-300 px-3 py-2 text-sm"
              />
            </div>
            <div>
              <label className="mb-1 block text-xs font-medium text-stone-600">Serial start</label>
              <input name="serial_start" placeholder="ABC00001" className="w-full rounded border border-stone-300 px-3 py-2 text-sm" />
            </div>
            <div>
              <label className="mb-1 block text-xs font-medium text-stone-600">Serial end</label>
              <input name="serial_end" placeholder="ABC10000" className="w-full rounded border border-stone-300 px-3 py-2 text-sm" />
            </div>
            <div className="col-span-3 flex items-center gap-3">
              <button
                type="submit"
                disabled={receive.isPending}
                className="rounded bg-stone-900 px-3 py-2 text-sm font-medium text-white hover:bg-stone-800 disabled:bg-stone-400"
              >
                {receive.isPending ? "Saving…" : "Mark received"}
              </button>
              {receive.error && (
                <span className="text-sm text-red-600">
                  {receive.error instanceof ConnectError
                    ? receive.error.rawMessage
                    : String(receive.error)}
                </span>
              )}
            </div>
          </form>
        );
      })()}
    </Shell>
  );
}
