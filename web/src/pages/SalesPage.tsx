import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { EmptyRow } from "@/components/EmptyState";
import { Shell } from "@/components/Shell";
import { bottlingClient, customerClient, productClient, salesClient, tenantClient } from "@/lib/clients";
import { SalesOrderStatus, ShipmentStatus } from "@/gen/stillhouse/v1/sales_pb";
import { formatLAA, formatQty } from "@/lib/format";
import { OwnerOnly, WriteOnly } from "@/lib/role";
import { ScanToPick } from "@/components/ScanToPick";

const orderStatusLabel: Record<number, string> = {
  [SalesOrderStatus.DRAFT]: "Draft",
  [SalesOrderStatus.CONFIRMED]: "Confirmed",
  [SalesOrderStatus.PARTIALLY_SHIPPED]: "Part shipped",
  [SalesOrderStatus.SHIPPED]: "Shipped",
  [SalesOrderStatus.CLOSED]: "Closed",
  [SalesOrderStatus.CANCELLED]: "Cancelled",
};

const shipmentStatusLabel: Record<number, string> = {
  [ShipmentStatus.PICKING]: "Picking",
  [ShipmentStatus.SHIPPED]: "Shipped",
  [ShipmentStatus.CANCELLED]: "Cancelled",
};

function errText(e: unknown): string {
  return e instanceof ConnectError ? e.rawMessage : String(e);
}

export function SalesPage() {
  const [tab, setTab] = useState<"orders" | "shipments" | "stock">("orders");
  return (
    <Shell>
      <div className="mb-6">
        <h1 className="text-3xl font-bold tracking-tight">Sales &amp; shipping</h1>
        <p className="text-sm text-fg-muted">
          Orders, picking, and the removals a shipment writes. Marking a shipment
          shipped records the removals against the lots that were actually picked —
          so what left the building and what the return says left are the same rows.
        </p>
      </div>

      <div className="mb-4 flex gap-1 border-b border-border text-sm">
        {([["orders", "Sales orders"], ["shipments", "Shipments"], ["stock", "What's spoken for"]] as const).map(
          ([k, label]) => (
            <button
              key={k}
              onClick={() => setTab(k)}
              className={`-mb-px border-b-2 px-3 py-2 ${
                tab === k ? "border-accent text-fg" : "border-transparent text-fg-muted hover:text-fg"
              }`}
            >
              {label}
            </button>
          ),
        )}
      </div>

      {tab === "orders" && <OrdersTab />}
      {tab === "shipments" && <ShipmentsTab />}
      {tab === "stock" && <StockTab />}
    </Shell>
  );
}

function OrdersTab() {
  const qc = useQueryClient();
  const [openOnly, setOpenOnly] = useState(true);
  const [selected, setSelected] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);

  const orders = useQuery({
    queryKey: ["listSalesOrders", openOnly],
    queryFn: () => salesClient.listSalesOrders({ openOnly }),
  });
  const customers = useQuery({
    queryKey: ["listCustomers"],
    queryFn: () => customerClient.listCustomers({}),
  });
  const create = useMutation({
    mutationFn: (m: Parameters<typeof salesClient.createSalesOrder>[0]) =>
      salesClient.createSalesOrder(m),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["listSalesOrders"] });
      setSelected(r.salesOrder?.id ?? null);
      setCreating(false);
    },
  });

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <label className="flex items-center gap-2 text-sm text-fg-muted">
          <input type="checkbox" checked={openOnly} onChange={(e) => setOpenOnly(e.target.checked)} />
          Only what's still owed
        </label>
        <OwnerOnly>
          <button
            onClick={() => setCreating((v) => !v)}
            className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover"
          >
            {creating ? "Cancel" : "New order"}
          </button>
        </OwnerOnly>
      </div>

      {creating && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            const fd = new FormData(e.currentTarget);
            create.mutate({
              customerId: fd.get("customer_id")?.toString() ?? "",
              orderedOn: fd.get("ordered_on")?.toString() ?? "",
              requiredBy: fd.get("required_by")?.toString() ?? "",
              customerReference: fd.get("customer_reference")?.toString() ?? "",
              notes: fd.get("notes")?.toString() ?? "",
            });
          }}
          className="grid gap-3 rounded-lg border border-border bg-surface-2 p-5 sm:grid-cols-3"
        >
          <div>
            <label className="mb-1 block text-xs text-fg-muted">Customer</label>
            <select name="customer_id" required className="w-full rounded border border-border-strong px-2 py-1.5 text-sm">
              <option value="">— choose —</option>
              {customers.data?.customers.map((c) => (
                <option key={c.id} value={c.id}>{c.name}</option>
              ))}
            </select>
            <p className="mt-1 text-xs text-fg-subtle">
              Their classification decides whether the removals carry duty.
            </p>
          </div>
          <SField label="Ordered on" name="ordered_on" type="date" />
          <SField label="Required by" name="required_by" type="date" />
          <SField label="Their PO number" name="customer_reference" />
          <SField label="Notes" name="notes" className="sm:col-span-2" />
          <div className="sm:col-span-3">
            <button type="submit" disabled={create.isPending}
                    className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50">
              {create.isPending ? "Creating…" : "Create draft"}
            </button>
            {create.error && <span className="ml-3 text-sm text-danger-fg">{errText(create.error)}</span>}
          </div>
        </form>
      )}

      <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-3">#</th>
              <th className="px-4 py-3">Customer</th>
              <th className="px-4 py-3">Status</th>
              <th className="px-4 py-3">Required by</th>
              <th className="px-4 py-3 text-right">Bottles</th>
              <th className="px-4 py-3 text-right">Value</th>
              <th className="px-4 py-3 text-right"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {orders.data?.salesOrders.length === 0 && (
              <EmptyRow
                colSpan={7}
                title="Nothing on order"
                message="An order is what a shipment gets picked against, and what tells the stock screen the cases in front of you are already promised."
              />
            )}
            {orders.data?.salesOrders.map((so) => (
              <tr key={so.id}>
                <td className="px-4 py-3 font-medium text-fg">{so.orderNo}</td>
                <td className="px-4 py-3 text-fg-muted">{so.customerName}</td>
                <td className="px-4 py-3 text-fg-muted">{orderStatusLabel[so.status] ?? "—"}</td>
                <td className="px-4 py-3 text-fg-muted">{so.requiredBy || "—"}</td>
                <td className="px-4 py-3 text-right text-fg-muted">
                  {so.bottlesShipped}/{so.bottlesOrdered}
                </td>
                <td className="px-4 py-3 text-right text-fg-muted">
                  {so.totalValue ? `CAD ${so.totalValue}` : "—"}
                </td>
                <td className="px-4 py-3 text-right">
                  <button
                    onClick={() => setSelected(selected === so.id ? null : so.id)}
                    className="text-xs text-fg-muted hover:text-fg"
                  >
                    {selected === so.id ? "Close" : "Open"}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {selected && <OrderDetail id={selected} />}
    </div>
  );
}

function OrderDetail({ id }: { id: string }) {
  const qc = useQueryClient();
  const order = useQuery({
    queryKey: ["getSalesOrder", id],
    queryFn: () => salesClient.getSalesOrder({ id }),
  });
  const products = useQuery({
    queryKey: ["listProducts"],
    queryFn: () => productClient.listProducts({}),
  });
  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["getSalesOrder", id] });
    qc.invalidateQueries({ queryKey: ["listSalesOrders"] });
    qc.invalidateQueries({ queryKey: ["listStockCommitments"] });
  };
  const addLine = useMutation({
    mutationFn: (m: Parameters<typeof salesClient.addSalesOrderLine>[0]) =>
      salesClient.addSalesOrderLine(m),
    onSuccess: invalidate,
  });
  const removeLine = useMutation({
    mutationFn: (m: Parameters<typeof salesClient.removeSalesOrderLine>[0]) =>
      salesClient.removeSalesOrderLine(m),
    onSuccess: invalidate,
  });
  const setStatus = useMutation({
    mutationFn: (m: Parameters<typeof salesClient.setSalesOrderStatus>[0]) =>
      salesClient.setSalesOrderStatus(m),
    onSuccess: invalidate,
  });
  const createShipment = useMutation({
    mutationFn: (m: Parameters<typeof salesClient.createShipment>[0]) =>
      salesClient.createShipment(m),
    onSuccess: () => {
      invalidate();
      qc.invalidateQueries({ queryKey: ["listShipments"] });
    },
  });

  const so = order.data?.salesOrder;
  if (!so) return null;
  const editable =
    so.status === SalesOrderStatus.DRAFT || so.status === SalesOrderStatus.CONFIRMED;

  return (
    <div className="space-y-4 rounded-lg border border-border bg-surface-2 p-5">
      <div className="flex flex-wrap items-baseline justify-between gap-3">
        <h2 className="text-sm font-semibold text-fg">
          Order {so.orderNo} — {so.customerName}
          <span className="ml-2 text-xs text-fg-muted">{orderStatusLabel[so.status]}</span>
          {so.customerReference && (
            <span className="ml-2 text-xs text-fg-subtle">their ref {so.customerReference}</span>
          )}
        </h2>
        <div className="flex gap-2">
          <OwnerOnly>
            {so.status === SalesOrderStatus.DRAFT && (
              <button
                onClick={() => setStatus.mutate({ id, status: SalesOrderStatus.CONFIRMED })}
                disabled={setStatus.isPending || so.lines.length === 0}
                className="rounded bg-accent px-3 py-1.5 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
              >
                Confirm
              </button>
            )}
          </OwnerOnly>
          <WriteOnly>
            {(so.status === SalesOrderStatus.CONFIRMED ||
              so.status === SalesOrderStatus.PARTIALLY_SHIPPED) && (
              <button
                onClick={() => createShipment.mutate({ salesOrderId: id })}
                disabled={createShipment.isPending}
                className="rounded border border-border-strong px-3 py-1.5 text-sm text-fg hover:bg-surface-3"
              >
                Start a shipment
              </button>
            )}
          </WriteOnly>
        </div>
      </div>
      {setStatus.error && <p className="text-sm text-danger-fg">{errText(setStatus.error)}</p>}
      {createShipment.error && <p className="text-sm text-danger-fg">{errText(createShipment.error)}</p>}

      <table className="min-w-full divide-y divide-border text-sm">
        <thead className="text-left text-xs text-fg-muted">
          <tr>
            <th className="px-2 py-1.5">Product</th>
            <th className="px-2 py-1.5 text-right">Ordered</th>
            <th className="px-2 py-1.5 text-right">Shipped</th>
            <th className="px-2 py-1.5 text-right">Outstanding</th>
            <th className="px-2 py-1.5 text-right">Unit price</th>
            <th className="px-2 py-1.5 text-right"></th>
          </tr>
        </thead>
        <tbody className="divide-y divide-border">
          {so.lines.map((l) => {
            const outstanding = l.bottlesOrdered - l.bottlesShipped;
            return (
              <tr key={l.id}>
                <td className="px-2 py-1.5 text-fg">
                  {l.productName}
                  <span className="ml-2 text-xs text-fg-subtle">
                    {l.bottleSizeMl} mL · {l.bottleAbvPct} %
                  </span>
                </td>
                <td className="px-2 py-1.5 text-right text-fg-muted">{l.bottlesOrdered}</td>
                <td className="px-2 py-1.5 text-right text-fg-muted">{l.bottlesShipped}</td>
                <td className={`px-2 py-1.5 text-right ${outstanding > 0 ? "text-fg" : "text-fg-subtle"}`}>
                  {outstanding > 0 ? outstanding : "—"}
                </td>
                <td className="px-2 py-1.5 text-right font-mono text-xs text-fg-muted">{l.unitPrice}</td>
                <td className="px-2 py-1.5 text-right">
                  {editable && l.bottlesShipped === 0 && (
                    <OwnerOnly>
                      <button
                        onClick={() => removeLine.mutate({ lineId: l.id })}
                        className="text-xs text-fg-muted hover:text-danger-fg"
                      >
                        Remove
                      </button>
                    </OwnerOnly>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
      {removeLine.error && <p className="text-sm text-danger-fg">{errText(removeLine.error)}</p>}

      {editable && (
        <OwnerOnly>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              const fd = new FormData(e.currentTarget);
              addLine.mutate({
                salesOrderId: id,
                productId: fd.get("product_id")?.toString() ?? "",
                bottlesOrdered: Number(fd.get("bottles") ?? 0) || 0,
                unitPrice: fd.get("unit_price")?.toString() ?? "",
              });
              e.currentTarget.reset();
            }}
            className="grid gap-3 border-t border-border pt-4 sm:grid-cols-4"
          >
            <div>
              <label className="mb-1 block text-xs text-fg-muted">Product</label>
              <select name="product_id" required className="w-full rounded border border-border-strong px-2 py-1.5 text-sm">
                <option value="">— choose —</option>
                {products.data?.products.map((p) => (
                  <option key={p.id} value={p.id}>{p.name}</option>
                ))}
              </select>
            </div>
            <SField label="Bottles" name="bottles" type="number" required />
            <SField label="Unit price (blank = price list)" name="unit_price" />
            <div className="flex items-end">
              <button type="submit" disabled={addLine.isPending}
                      className="rounded border border-border-strong px-3 py-1.5 text-sm text-fg hover:bg-surface-3">
                Add line
              </button>
            </div>
            {addLine.error && (
              <p className="text-sm text-danger-fg sm:col-span-4">{errText(addLine.error)}</p>
            )}
          </form>
        </OwnerOnly>
      )}

      {order.data && order.data.shipments.length > 0 && (
        <div className="border-t border-border pt-4">
          <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-fg-muted">
            Shipments
          </h3>
          <ul className="space-y-1 text-sm">
            {order.data.shipments.map((s) => (
              <li key={s.id} className="text-fg-muted">
                #{s.shipmentNo} · {shipmentStatusLabel[s.status]} · {s.bottles} bottles ·{" "}
                {formatLAA(s.totalLaa)} LAA {s.shipDate && `· ${s.shipDate}`}
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}

function ShipmentsTab() {
  const [openOnly, setOpenOnly] = useState(true);
  const [selected, setSelected] = useState<string | null>(null);
  const shipments = useQuery({
    queryKey: ["listShipments", openOnly],
    queryFn: () => salesClient.listShipments({ openOnly }),
  });

  return (
    <div className="space-y-4">
      <label className="flex items-center gap-2 text-sm text-fg-muted">
        <input type="checkbox" checked={openOnly} onChange={(e) => setOpenOnly(e.target.checked)} />
        Only what's still being picked
      </label>

      <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-3">#</th>
              <th className="px-4 py-3">Customer</th>
              <th className="px-4 py-3">Order</th>
              <th className="px-4 py-3">Status</th>
              <th className="px-4 py-3">Ship date</th>
              <th className="px-4 py-3 text-right">Bottles</th>
              <th className="px-4 py-3 text-right"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {shipments.data?.shipments.length === 0 && (
              <EmptyRow
                colSpan={7}
                title="Nothing being picked"
                message="Start a shipment from an order, or raise a standalone one for a sample or a licensee collecting in person."
              />
            )}
            {shipments.data?.shipments.map((s) => (
              <tr key={s.id}>
                <td className="px-4 py-3 font-medium text-fg">{s.shipmentNo}</td>
                <td className="px-4 py-3 text-fg-muted">{s.customerName}</td>
                <td className="px-4 py-3 text-fg-muted">{s.orderNo ? `#${s.orderNo}` : "—"}</td>
                <td className="px-4 py-3 text-fg-muted">{shipmentStatusLabel[s.status] ?? "—"}</td>
                <td className="px-4 py-3 text-fg-muted">{s.shipDate || "—"}</td>
                <td className="px-4 py-3 text-right text-fg-muted">{s.bottles}</td>
                <td className="px-4 py-3 text-right">
                  <button
                    onClick={() => setSelected(selected === s.id ? null : s.id)}
                    className="text-xs text-fg-muted hover:text-fg"
                  >
                    {selected === s.id ? "Close" : "Open"}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {selected && <ShipmentDetail id={selected} />}
    </div>
  );
}

function ShipmentDetail({ id }: { id: string }) {
  const qc = useQueryClient();
  const [confirming, setConfirming] = useState(false);
  const shipment = useQuery({
    queryKey: ["getShipment", id],
    queryFn: () => salesClient.getShipment({ id }),
  });
  const lots = useQuery({
    queryKey: ["listPackagedInventory"],
    queryFn: () => bottlingClient.listPackagedInventory({}),
  });
  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["getShipment", id] });
    qc.invalidateQueries({ queryKey: ["listShipments"] });
    qc.invalidateQueries({ queryKey: ["listSalesOrders"] });
    qc.invalidateQueries({ queryKey: ["getSalesOrder"] });
    qc.invalidateQueries({ queryKey: ["listPackagedInventory"] });
    qc.invalidateQueries({ queryKey: ["listStockCommitments"] });
    qc.invalidateQueries({ queryKey: ["listRemovals"] });
  };
  const addLine = useMutation({
    mutationFn: (m: Parameters<typeof salesClient.addShipmentLine>[0]) =>
      salesClient.addShipmentLine(m),
    onSuccess: invalidate,
  });
  const removeLine = useMutation({
    mutationFn: (m: Parameters<typeof salesClient.removeShipmentLine>[0]) =>
      salesClient.removeShipmentLine(m),
    onSuccess: invalidate,
  });
  const ship = useMutation({
    mutationFn: (m: Parameters<typeof salesClient.shipShipment>[0]) => salesClient.shipShipment(m),
    onSuccess: () => { setConfirming(false); invalidate(); },
  });
  const cancel = useMutation({
    mutationFn: (m: Parameters<typeof salesClient.cancelShipment>[0]) =>
      salesClient.cancelShipment(m),
    onSuccess: invalidate,
  });

  const s = shipment.data?.shipment;
  if (!s) return null;
  const picking = s.status === ShipmentStatus.PICKING;
  const orderLines = shipment.data?.shipment?.lines ?? [];

  return (
    <div className="space-y-4 rounded-lg border border-border bg-surface-2 p-5">
      <PackingSlipHeader s={s} />

      <div data-print-hide className="flex flex-wrap items-baseline justify-between gap-3">
        <h2 className="text-sm font-semibold text-fg">
          Shipment {s.shipmentNo} — {s.customerName}
          <span className="ml-2 text-xs text-fg-muted">{shipmentStatusLabel[s.status]}</span>
        </h2>
        <div className="flex items-center gap-3 text-xs text-fg-muted">
          <span>
            {s.bottles} bottles · {formatQty(s.totalLitres)} L · {formatLAA(s.totalLaa)} LAA
          </span>
          <button
            onClick={() => window.print()}
            className="rounded border border-border-strong px-2 py-1 text-xs text-fg hover:bg-surface-3"
          >
            Packing slip
          </button>
        </div>
      </div>

      <table className="min-w-full divide-y divide-border text-sm">
        <thead className="text-left text-xs text-fg-muted">
          <tr>
            <th className="px-2 py-1.5">Lot</th>
            <th className="px-2 py-1.5">Product</th>
            <th className="px-2 py-1.5">Jurisdiction</th>
            <th className="px-2 py-1.5 text-right">Bottles</th>
            <th className="px-2 py-1.5">Removal</th>
            <th className="px-2 py-1.5 text-right"></th>
          </tr>
        </thead>
        <tbody className="divide-y divide-border">
          {orderLines.map((l) => (
            <tr key={l.id}>
              <td className="px-2 py-1.5 font-mono text-xs text-fg">{l.lotCode}</td>
              <td className="px-2 py-1.5 text-fg-muted">
                {l.productName}
                {l.onHold && <span className="ml-2 text-xs text-danger-fg">on hold</span>}
              </td>
              <td className="px-2 py-1.5 text-fg-muted">{l.jurisdiction}</td>
              <td className="px-2 py-1.5 text-right text-fg-muted">{l.bottles}</td>
              <td className="px-2 py-1.5 text-fg-muted">
                {l.removalNo ? `#${l.removalNo}` : picking ? "on shipping" : "—"}
              </td>
              <td className="px-2 py-1.5 text-right">
                {picking && (
                  <WriteOnly>
                    <button
                      onClick={() => removeLine.mutate({ lineId: l.id })}
                      className="text-xs text-fg-muted hover:text-danger-fg"
                    >
                      Remove
                    </button>
                  </WriteOnly>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {removeLine.error && <p className="text-sm text-danger-fg">{errText(removeLine.error)}</p>}

      {picking && (
        <WriteOnly>
          <form
            data-print-hide
            onSubmit={(e) => {
              e.preventDefault();
              const fd = new FormData(e.currentTarget);
              addLine.mutate({
                shipmentId: id,
                packagedInventoryId: fd.get("lot_id")?.toString() ?? "",
                bottles: Number(fd.get("bottles") ?? 0) || 0,
              });
              e.currentTarget.reset();
            }}
            className="grid gap-3 border-t border-border pt-4 sm:grid-cols-4"
          >
            <div className="sm:col-span-2">
              <label className="mb-1 block text-xs text-fg-muted">Lot</label>
              <ScanToPick
                onPicked={(lotId) => {
                  const sel = document.querySelector<HTMLSelectElement>('select[name="lot_id"]');
                  if (sel) sel.value = lotId;
                }}
              />
              <select name="lot_id" required className="w-full rounded border border-border-strong px-2 py-1.5 text-sm">
                <option value="">— choose —</option>
                {lots.data?.rows
                  .filter((l) => l.bottlesOnHand > 0 && !l.heldAt)
                  .map((l) => (
                    <option key={l.id} value={l.id}>
                      {l.lotCode} — {l.productName} ({l.bottlesOnHand} on hand, {l.jurisdiction})
                    </option>
                  ))}
              </select>
              <p className="mt-1 text-xs text-fg-subtle">
                The lot, not the product — it carries the jurisdiction, the stamps and
                the duty basis the removal will be written against.
              </p>
            </div>
            <SField label="Bottles" name="bottles" type="number" required />
            <div className="flex items-end">
              <button type="submit" disabled={addLine.isPending}
                      className="rounded border border-border-strong px-3 py-1.5 text-sm text-fg hover:bg-surface-3">
                Pick
              </button>
            </div>
            {addLine.error && (
              <p className="text-sm text-danger-fg sm:col-span-4">{errText(addLine.error)}</p>
            )}
          </form>
        </WriteOnly>
      )}

      {picking && orderLines.length > 0 && (
        <WriteOnly>
          <div data-print-hide className="border-t border-border pt-4">
            {!confirming ? (
              <div className="flex flex-wrap gap-2">
                <button
                  onClick={() => setConfirming(true)}
                  className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover"
                >
                  Ship it
                </button>
                <button
                  onClick={() => {
                    const reason = window.prompt("Why is this shipment being cancelled?");
                    if (reason) cancel.mutate({ id, reason });
                  }}
                  className="rounded border border-border-strong px-3 py-2 text-sm text-fg-muted hover:text-fg"
                >
                  Cancel shipment
                </button>
              </div>
            ) : (
              <form
                onSubmit={(e) => {
                  e.preventDefault();
                  const fd = new FormData(e.currentTarget);
                  ship.mutate({
                    id,
                    shipDate: fd.get("ship_date")?.toString() ?? "",
                    reference: fd.get("reference")?.toString() ?? "",
                  });
                }}
                className="grid gap-3 sm:grid-cols-3"
              >
                <p className="text-sm text-fg-muted sm:col-span-3">
                  Shipping writes {orderLines.length}{" "}
                  {orderLines.length === 1 ? "removal" : "removals"} for {formatLAA(s.totalLaa)} LAA
                  and takes the stock off hand. The ship date is the date the duty rate is
                  read at, and it cannot fall in a period that has been filed.
                </p>
                <SField label="Ship date (blank = today)" name="ship_date" type="date" />
                <SField label="Reference on the removals" name="reference" />
                <div className="flex items-end gap-2">
                  <button type="submit" disabled={ship.isPending}
                          className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50">
                    {ship.isPending ? "Shipping…" : "Confirm"}
                  </button>
                  <button type="button" onClick={() => setConfirming(false)}
                          className="text-sm text-fg-muted hover:text-fg">
                    Back
                  </button>
                </div>
                {ship.error && (
                  <p className="text-sm text-danger-fg sm:col-span-3">{errText(ship.error)}</p>
                )}
              </form>
            )}
            {cancel.error && <p className="mt-2 text-sm text-danger-fg">{errText(cancel.error)}</p>}
          </div>
        </WriteOnly>
      )}

      {!picking && s.status === ShipmentStatus.SHIPPED && (
        <p data-print-hide className="border-t border-border pt-4 text-xs text-fg-subtle">
          The removals above are on the B266 for the period containing {s.shipDate}. To
          undo this, void them from the Removals page — the shipment itself stays as the
          record of what went out.
        </p>
      )}
    </div>
  );
}

// PackingSlipHeader is the document that travels with the pallet. It only
// appears on paper (data-print-only), so the screen stays a working view
// and the print stays a document: consignor, consignee, what is in the
// load, and the reference the carrier signs against.
function PackingSlipHeader({ s }: { s: { shipmentNo: number; shipDate: string; customerName: string;
  customerAddress: string; customerLicenceNumber: string; customerJurisdiction: string;
  carrier: string; trackingRef: string; bolReference: string; orderNo: number } }) {
  const tenant = useQuery({ queryKey: ["getTenant"], queryFn: () => tenantClient.getTenant({}) });
  const t = tenant.data?.tenant;
  return (
    <div data-print-only className="mb-4">
      <div className="flex justify-between gap-6">
        <div>
          <div className="text-lg font-bold">{t?.name ?? ""}</div>
          {t?.craSpiritsLicenceNumber && (
            <div className="text-xs">Spirits licence {t.craSpiritsLicenceNumber}</div>
          )}
          {t?.exciseWarehouseLicenceNumber && (
            <div className="text-xs">Excise warehouse licence {t.exciseWarehouseLicenceNumber}</div>
          )}
        </div>
        <div className="text-right">
          <div className="text-lg font-bold">Packing slip</div>
          <div className="text-xs">Shipment {s.shipmentNo}</div>
          {s.orderNo > 0 && <div className="text-xs">Order {s.orderNo}</div>}
          <div className="text-xs">{s.shipDate || "date on despatch"}</div>
        </div>
      </div>
      <div className="mt-3 grid grid-cols-2 gap-6 text-xs">
        <div>
          <div className="font-semibold">Consignee</div>
          <div>{s.customerName}</div>
          {s.customerAddress && <div className="whitespace-pre-line">{s.customerAddress}</div>}
          {s.customerLicenceNumber && <div>Licence {s.customerLicenceNumber}</div>}
          {s.customerJurisdiction && <div>{s.customerJurisdiction}</div>}
        </div>
        <div>
          <div className="font-semibold">Carriage</div>
          <div>{s.carrier || "—"}</div>
          {s.trackingRef && <div>Tracking {s.trackingRef}</div>}
          {s.bolReference && <div>BOL {s.bolReference}</div>}
        </div>
      </div>
    </div>
  );
}

function StockTab() {
  const commitments = useQuery({
    queryKey: ["listStockCommitments"],
    queryFn: () => salesClient.listStockCommitments({}),
  });

  return (
    <div className="space-y-3">
      <p className="text-sm text-fg-muted">
        Reservation is deliberately soft: an order does not decrement stock, because
        the alcohol has not gone anywhere and a return built on promises rather than
        movements would be wrong. This screen is where the promise shows up instead.
      </p>
      <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-3">Product</th>
              <th className="px-4 py-3 text-right">On hand</th>
              <th className="px-4 py-3 text-right">Spoken for</th>
              <th className="px-4 py-3 text-right">Picked</th>
              <th className="px-4 py-3 text-right">Free</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {commitments.data?.commitments.length === 0 && (
              <EmptyRow colSpan={5} title="Nothing packaged" message="Bottle something, or take an order for it." />
            )}
            {commitments.data?.commitments.map((c) => (
              <tr key={c.productId} className={c.oversold ? "bg-danger-bg" : undefined}>
                <td className="px-4 py-3 text-fg">
                  {c.productName}
                  <span className="ml-2 text-xs text-fg-subtle">
                    {c.bottleSizeMl} mL · {c.bottleAbvPct} %
                  </span>
                  {c.oversold && (
                    <span className="ml-2 text-xs text-danger-fg">
                      promised more than exists
                    </span>
                  )}
                </td>
                <td className="px-4 py-3 text-right text-fg-muted">{c.bottlesOnHand}</td>
                <td className="px-4 py-3 text-right text-fg-muted">{c.bottlesReserved || "—"}</td>
                <td className="px-4 py-3 text-right text-fg-muted">{c.bottlesPicked || "—"}</td>
                <td className="px-4 py-3 text-right font-medium text-fg">{c.bottlesAvailable}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function SField({ label, name, type = "text", defaultValue, required, className }: {
  label: string; name: string; type?: string;
  defaultValue?: string; required?: boolean; className?: string;
}) {
  return (
    <div className={className}>
      <label className="mb-1 block text-xs text-fg-muted">{label}</label>
      <input name={name} type={type} defaultValue={defaultValue} required={required}
             className="w-full rounded border border-border-strong px-2 py-1.5 text-sm" />
    </div>
  );
}
