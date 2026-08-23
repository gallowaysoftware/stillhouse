import { FormEvent, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { useConfirm } from "@/components/ConfirmDialog";
import { EmptyRow } from "@/components/EmptyState";
import { Shell } from "@/components/Shell";
import { StampReconciliation } from "@/components/StampReconciliation";
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
  const confirm = useConfirm();
  const qc = useQueryClient();
  const role = useCurrentRole();
  const writeable = canWrite(role);
  const list = useQuery({
    queryKey: ["listStampOrders"],
    queryFn: () => exciseStampClient.listStampOrders({}),
  });
  const [showOrderForm, setShowOrderForm] = useState(false);
  // Which order's serial-by-serial account is open.
  const [reconcilingId, setReconcilingId] = useState<string | null>(null);
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

  async function onVoid(orderId: string, available: number) {
    if (available <= 0) return;
    const ok = await confirm({
      title: "Void stamps from this order?",
      body: <>Voiding records that physical stamps were destroyed or misapplied so the on-hand count stays accurate.</>,
      consequences: [
        "Quantity voided increments on the order; available count drops accordingly",
        "Audit-logged with the reason for CRA records",
      ],
      requireQuantity: { label: "Quantity to void", max: available, defaultValue: 1 },
      requireReason: { label: "Reason", placeholder: "damaged in application" },
      confirmLabel: "Void stamps",
      tone: "warning",
    });
    if (!ok || ok.quantity === undefined) return;
    voidStamps.mutate(
      create(VoidStampsRequestSchema, { id: orderId, quantity: ok.quantity, reason: ok.reason }),
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
          <h1 className="text-3xl font-bold tracking-tight">Excise stamps</h1>
          <p className="text-sm text-fg-muted">
            Province-coded stamps from CRA. Order, receive, then apply at bottling.
            Jurisdiction codes use ISO 3166-2 (CA-ON, CA-QC, CA-BC, …).
          </p>
        </div>
        <WriteOnly>
          <button
            onClick={() => setShowOrderForm((s) => !s)}
            className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover"
          >
            {showOrderForm ? "Cancel" : "Order stamps"}
          </button>
        </WriteOnly>
      </div>

      {list.data?.summaries && list.data.summaries.length > 0 && (
        <section className="mb-6 grid grid-cols-2 gap-4 sm:grid-cols-4">
          {list.data.summaries.map((s) => (
            <div key={s.jurisdiction} className="rounded-lg border border-border bg-surface-2 p-4 shadow-sm">
              <p className="text-xs text-fg-muted">{s.jurisdiction}</p>
              <p className="mt-1 text-3xl font-bold tracking-tight text-fg">{s.totalOnHand.toLocaleString()}</p>
              <p className="text-xs text-fg-muted">
                on hand · {s.totalApplied.toLocaleString()} applied · {s.totalReceived.toLocaleString()} received
              </p>
            </div>
          ))}
        </section>
      )}

      {showOrderForm && (
        <form
          onSubmit={submitOrder}
          className="mb-6 grid grid-cols-3 gap-4 rounded-lg border border-border bg-surface-2 p-5 shadow-sm"
        >
          <div>
            <label className="mb-2 block text-sm font-medium text-fg-muted">Jurisdiction</label>
            <input name="jurisdiction" placeholder="CA-ON" required className="w-full rounded border border-border-strong px-3 py-2 text-sm" />
          </div>
          <div>
            <label className="mb-2 block text-sm font-medium text-fg-muted">Quantity</label>
            <input name="quantity_ordered" type="number" min="1" required className="w-full rounded border border-border-strong px-3 py-2 text-sm" />
          </div>
          <div>
            <label className="mb-2 block text-sm font-medium text-fg-muted">Notes</label>
            <input name="notes" className="w-full rounded border border-border-strong px-3 py-2 text-sm" />
          </div>
          <div className="col-span-3 flex items-center gap-3">
            <button
              type="submit"
              disabled={createOrder.isPending}
              className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
            >
              {createOrder.isPending ? "Saving…" : "Place order"}
            </button>
            {createOrder.error && (
              <span className="text-sm text-danger-fg">
                {createOrder.error instanceof ConnectError
                  ? createOrder.error.rawMessage
                  : String(createOrder.error)}
              </span>
            )}
          </div>
        </form>
      )}

      <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
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
          <tbody className="divide-y divide-border">
            {list.data?.orders.length === 0 && (
              <EmptyRow
                colSpan={9}
                title="No stamp orders yet"
                message="CRA-issued excise stamps are province-coded — every bottle sold duty-paid needs one. Place an order ahead of your first bottling run; lead time is weeks."
                action={
                  <WriteOnly>
                    <button
                      onClick={() => setShowOrderForm(true)}
                      className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover"
                    >
                      Order stamps
                    </button>
                  </WriteOnly>
                }
              />
            )}
            {list.data?.orders.map((o) => (
              <tr key={o.id}>
                <td className="px-4 py-3 font-medium text-fg">{o.jurisdiction}</td>
                <td className="px-4 py-3 text-fg-muted">
                  {o.orderedAt ? new Date(Number(o.orderedAt.seconds) * 1000).toLocaleDateString() : ""}
                </td>
                <td className="px-4 py-3 text-fg-muted">{statusLabel[o.status]}</td>
                <td className="px-4 py-3 text-right text-fg-muted">{o.quantityOrdered}</td>
                <td className="px-4 py-3 text-right text-fg-muted">{o.quantityReceived}</td>
                <td className="px-4 py-3 text-right text-fg-muted">{o.quantityApplied}</td>
                <td className="px-4 py-3 text-right font-medium text-fg">{o.availableCount}</td>
                <td className="px-4 py-3 text-fg-muted">
                  {o.serialStart && o.serialEnd ? `${o.serialStart}..${o.serialEnd}` : "—"}
                </td>
                <td className="px-4 py-3 space-x-3">
                  {o.status === ExciseStampOrderStatus.ORDERED && writeable && (
                    <button
                      onClick={() => setReceivingId(receivingId === o.id ? null : o.id)}
                      className="text-fg-muted hover:text-fg"
                    >
                      {receivingId === o.id ? "Cancel" : "Receive"}
                    </button>
                  )}
                  {o.availableCount > 0 && writeable && (
                    <button
                      onClick={() => onVoid(o.id, o.availableCount)}
                      disabled={voidStamps.isPending}
                      className="text-xs text-fg-muted hover:text-danger-fg disabled:opacity-50"
                    >
                      Void
                    </button>
                  )}
                  {o.quantityReceived > 0 && (
                    <button
                      onClick={() => setReconcilingId(reconcilingId === o.id ? null : o.id)}
                      className="text-xs text-fg-muted hover:text-fg"
                    >
                      {reconcilingId === o.id ? "Hide account" : "Account"}
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {reconcilingId && <StampReconciliation orderId={reconcilingId} />}

      {receivingId && (() => {
        const order = list.data?.orders.find((o) => o.id === receivingId);
        if (!order) return null;
        return (
          <form
            onSubmit={(e) => submitReceive(order.id, e)}
            className="mt-4 grid grid-cols-3 gap-4 rounded-lg border border-border bg-surface-2 p-5 shadow-sm"
          >
            <h2 className="col-span-3 text-sm font-semibold text-fg-muted">
              Receive order for {order.jurisdiction}
            </h2>
            <div>
              <label className="mb-2 block text-sm font-medium text-fg-muted">Quantity received</label>
              <input
                name="quantity_received"
                type="number"
                min="1"
                defaultValue={order.quantityOrdered}
                required
                className="w-full rounded border border-border-strong px-3 py-2 text-sm"
              />
            </div>
            <div>
              <label className="mb-2 block text-sm font-medium text-fg-muted">Serial start</label>
              <input name="serial_start" placeholder="ABC00001" className="w-full rounded border border-border-strong px-3 py-2 text-sm" />
            </div>
            <div>
              <label className="mb-2 block text-sm font-medium text-fg-muted">Serial end</label>
              <input name="serial_end" placeholder="ABC10000" className="w-full rounded border border-border-strong px-3 py-2 text-sm" />
            </div>
            <div className="col-span-3 flex items-center gap-3">
              <button
                type="submit"
                disabled={receive.isPending}
                className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
              >
                {receive.isPending ? "Saving…" : "Mark received"}
              </button>
              {receive.error && (
                <span className="text-sm text-danger-fg">
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
