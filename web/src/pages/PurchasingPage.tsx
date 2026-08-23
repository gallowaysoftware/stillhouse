import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { EmptyRow } from "@/components/EmptyState";
import { Shell } from "@/components/Shell";
import { materialClient, purchasingClient } from "@/lib/clients";
import { PurchaseOrderStatus } from "@/gen/stillhouse/v1/purchasing_pb";
import { formatCAD } from "@/lib/format";
import { OwnerOnly, WriteOnly } from "@/lib/role";

const statusLabel: Record<number, string> = {
  [PurchaseOrderStatus.DRAFT]: "Draft",
  [PurchaseOrderStatus.PLACED]: "Placed",
  [PurchaseOrderStatus.PARTIALLY_RECEIVED]: "Part received",
  [PurchaseOrderStatus.RECEIVED]: "Received",
  [PurchaseOrderStatus.CLOSED]: "Closed",
  [PurchaseOrderStatus.CANCELLED]: "Cancelled",
};

export function PurchasingPage() {
  const [tab, setTab] = useState<"orders" | "suppliers" | "grni">("orders");
  return (
    <Shell>
      <div className="mb-6">
        <h1 className="text-3xl font-bold tracking-tight">Purchasing</h1>
        <p className="text-sm text-fg-muted">
          The order behind the delivery. What's on order, what arrived against it, and
          what it actually cost once freight and duty are in — because those belong in
          the cost of the grain, not in an expense account.
        </p>
      </div>

      <div className="mb-4 flex gap-1 border-b border-border text-sm">
        {([["orders", "Purchase orders"], ["suppliers", "Suppliers"], ["grni", "Received, not invoiced"]] as const).map(
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
      {tab === "suppliers" && <SuppliersTab />}
      {tab === "grni" && <GRNITab />}
    </Shell>
  );
}

function OrdersTab() {
  const qc = useQueryClient();
  const [openOnly, setOpenOnly] = useState(true);
  const [selected, setSelected] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);

  const orders = useQuery({
    queryKey: ["listPurchaseOrders", openOnly],
    queryFn: () => purchasingClient.listPurchaseOrders({ openOnly }),
  });
  const suppliers = useQuery({
    queryKey: ["listSuppliers"],
    queryFn: () => purchasingClient.listSuppliers({}),
  });
  const create = useMutation({
    mutationFn: (m: Parameters<typeof purchasingClient.createPurchaseOrder>[0]) =>
      purchasingClient.createPurchaseOrder(m),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["listPurchaseOrders"] });
      setSelected(r.purchaseOrder?.id ?? null);
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
              supplierId: fd.get("supplier_id")?.toString() ?? "",
              orderedOn: fd.get("ordered_on")?.toString() ?? "",
              expectedOn: fd.get("expected_on")?.toString() ?? "",
              reference: fd.get("reference")?.toString() ?? "",
              currency: fd.get("currency")?.toString() ?? "",
              notes: fd.get("notes")?.toString() ?? "",
            });
          }}
          className="grid gap-3 rounded-lg border border-border bg-surface-2 p-5 sm:grid-cols-3"
        >
          <div>
            <label className="mb-1 block text-xs text-fg-muted">Supplier</label>
            <select name="supplier_id" required className="w-full rounded border border-border-strong px-2 py-1.5 text-sm">
              <option value="">— choose —</option>
              {suppliers.data?.suppliers.map((s) => (
                <option key={s.id} value={s.id}>{s.name}</option>
              ))}
            </select>
          </div>
          <PField label="Ordered on" name="ordered_on" type="date" />
          <PField label="Expected" name="expected_on" type="date" />
          <PField label="Their reference" name="reference" />
          <PField label="Currency" name="currency" defaultValue="CAD" />
          <PField label="Notes" name="notes" />
          <div className="sm:col-span-3">
            <button type="submit" disabled={create.isPending}
                    className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50">
              {create.isPending ? "Creating…" : "Create draft"}
            </button>
            {create.error && (
              <span className="ml-3 text-sm text-danger-fg">
                {create.error instanceof ConnectError ? create.error.rawMessage : String(create.error)}
              </span>
            )}
          </div>
        </form>
      )}

      <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-3">#</th>
              <th className="px-4 py-3">Supplier</th>
              <th className="px-4 py-3">Status</th>
              <th className="px-4 py-3">Expected</th>
              <th className="px-4 py-3 text-right">Value</th>
              <th className="px-4 py-3 text-right"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {orders.data?.purchaseOrders.length === 0 && (
              <EmptyRow
                colSpan={6}
                title="Nothing on order"
                message="A purchase order is what makes a short delivery visible and what puts freight into the cost of the grain rather than an expense account."
              />
            )}
            {orders.data?.purchaseOrders.map((po) => (
              <tr key={po.id}>
                <td className="px-4 py-3 font-medium text-fg">{po.poNo}</td>
                <td className="px-4 py-3 text-fg-muted">{po.supplierName}</td>
                <td className="px-4 py-3 text-fg-muted">{statusLabel[po.status] ?? "—"}</td>
                <td className="px-4 py-3 text-fg-muted">{po.expectedOn || "—"}</td>
                <td className="px-4 py-3 text-right text-fg-muted">
                  {po.totalValue ? `${po.currency} ${po.totalValue}` : "—"}
                </td>
                <td className="px-4 py-3 text-right">
                  <button
                    onClick={() => setSelected(selected === po.id ? null : po.id)}
                    className="text-xs text-fg-muted hover:text-fg"
                  >
                    {selected === po.id ? "Close" : "Open"}
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
  const [receiving, setReceiving] = useState<string | null>(null);
  const po = useQuery({
    queryKey: ["getPurchaseOrder", id],
    queryFn: () => purchasingClient.getPurchaseOrder({ id }),
  });
  const materials = useQuery({
    queryKey: ["listMaterials"],
    queryFn: () => materialClient.listMaterials({}),
  });
  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["getPurchaseOrder", id] });
    qc.invalidateQueries({ queryKey: ["listPurchaseOrders"] });
    qc.invalidateQueries({ queryKey: ["listMaterialLots"] });
  };
  const addLine = useMutation({
    mutationFn: (m: Parameters<typeof purchasingClient.addPurchaseOrderLine>[0]) =>
      purchasingClient.addPurchaseOrderLine(m),
    onSuccess: invalidate,
  });
  const setStatus = useMutation({
    mutationFn: (m: Parameters<typeof purchasingClient.setPurchaseOrderStatus>[0]) =>
      purchasingClient.setPurchaseOrderStatus(m),
    onSuccess: invalidate,
  });
  const receive = useMutation({
    mutationFn: (m: Parameters<typeof purchasingClient.receiveAgainstPO>[0]) =>
      purchasingClient.receiveAgainstPO(m),
    onSuccess: () => { setReceiving(null); invalidate(); },
  });

  const order = po.data?.purchaseOrder;
  if (!order) return null;
  const isDraft = order.status === PurchaseOrderStatus.DRAFT;

  return (
    <div className="rounded-lg border border-border bg-surface-2 p-5">
      <div className="mb-3 flex flex-wrap items-baseline justify-between gap-3">
        <h2 className="text-sm font-semibold text-fg">
          PO {order.poNo} — {order.supplierName}
          <span className="ml-2 text-xs text-fg-muted">{statusLabel[order.status]}</span>
        </h2>
        <OwnerOnly>
          {isDraft && (
            <button
              onClick={() => setStatus.mutate({ id, status: PurchaseOrderStatus.PLACED })}
              disabled={setStatus.isPending || order.lines.length === 0}
              className="rounded bg-accent px-3 py-1.5 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
            >
              Place order
            </button>
          )}
        </OwnerOnly>
      </div>

      <table className="min-w-full divide-y divide-border text-sm">
        <thead className="text-left text-xs text-fg-muted">
          <tr>
            <th className="px-2 py-1.5">Material</th>
            <th className="px-2 py-1.5 text-right">Ordered</th>
            <th className="px-2 py-1.5 text-right">Received</th>
            <th className="px-2 py-1.5 text-right">Outstanding</th>
            <th className="px-2 py-1.5 text-right">Unit price</th>
            <th className="px-2 py-1.5 text-right"></th>
          </tr>
        </thead>
        <tbody className="divide-y divide-border">
          {order.lines.map((l) => {
            const outstanding = l.quantityOrdered - l.quantityReceived;
            return (
              <tr key={l.id}>
                <td className="px-2 py-1.5 text-fg">{l.materialName}</td>
                <td className="px-2 py-1.5 text-right text-fg-muted">{l.quantityOrdered} {l.uom}</td>
                <td className="px-2 py-1.5 text-right text-fg-muted">{l.quantityReceived}</td>
                <td className={`px-2 py-1.5 text-right ${outstanding > 0 ? "text-fg" : "text-fg-subtle"}`}>
                  {outstanding > 0 ? outstanding : "—"}
                </td>
                <td className="px-2 py-1.5 text-right font-mono text-xs text-fg-muted">{l.unitPrice}</td>
                <td className="px-2 py-1.5 text-right">
                  {!isDraft && outstanding !== 0 && (
                    <WriteOnly>
                      <button
                        onClick={() => setReceiving(receiving === l.id ? null : l.id)}
                        className="text-xs text-accent hover:underline"
                      >
                        {receiving === l.id ? "Cancel" : "Receive"}
                      </button>
                    </WriteOnly>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>

      {receiving && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            const fd = new FormData(e.currentTarget);
            const num = (k: string) => Number(fd.get(k) ?? 0) || 0;
            receive.mutate({
              purchaseOrderLineId: receiving,
              quantityReceived: num("quantity"),
              supplierLot: fd.get("supplier_lot")?.toString() ?? "",
              receivedOn: fd.get("received_on")?.toString() ?? "",
              unitPrice: fd.get("unit_price")?.toString() ?? "",
              freightCad: num("freight"),
              importDutyCad: num("duty"),
              handlingCad: num("handling"),
              invoiceReference: fd.get("invoice_reference")?.toString() ?? "",
              notes: fd.get("notes")?.toString() ?? "",
            });
          }}
          className="mt-4 grid gap-3 border-t border-border pt-4 sm:grid-cols-4"
        >
          <PField label="Quantity arrived" name="quantity" type="number" step="0.001" required />
          <PField label="Supplier lot" name="supplier_lot" />
          <PField label="Received on" name="received_on" type="date" />
          <PField label="Unit price (blank = the line's)" name="unit_price" />
          <PField label="Freight (CAD)" name="freight" type="number" step="0.01" />
          <PField label="Import duty (CAD)" name="duty" type="number" step="0.01" />
          <PField label="Handling (CAD)" name="handling" type="number" step="0.01" />
          <PField label="Invoice reference" name="invoice_reference" />
          <p className="text-xs text-fg-muted sm:col-span-4">
            Freight, duty and handling get spread across what arrived and folded into the
            lot's cost. Leave them blank if the bill hasn't come yet — you can add them
            later and the landed cost recalculates.
          </p>
          <div className="sm:col-span-4">
            <button type="submit" disabled={receive.isPending}
                    className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50">
              {receive.isPending ? "Receiving…" : "Receive"}
            </button>
            {receive.error && (
              <span className="ml-3 text-sm text-danger-fg">
                {receive.error instanceof ConnectError ? receive.error.rawMessage : String(receive.error)}
              </span>
            )}
          </div>
        </form>
      )}

      {receive.isSuccess && receive.data.landedUnitCostKnown && (
        <p className="mt-3 text-sm text-success-fg">
          Received. Landed cost {formatCAD(receive.data.landedUnitCostCad)} per unit —
          the supplier's price plus its share of freight, duty and handling.
        </p>
      )}

      <OwnerOnly>
        {isDraft && (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              const fd = new FormData(e.currentTarget);
              addLine.mutate({
                purchaseOrderId: id,
                materialId: fd.get("material_id")?.toString() ?? "",
                quantityOrdered: Number(fd.get("quantity") ?? 0),
                unitPrice: fd.get("unit_price")?.toString() ?? "0",
                uom: fd.get("uom")?.toString() ?? "",
              });
              e.currentTarget.reset();
            }}
            className="mt-4 grid gap-3 border-t border-border pt-4 sm:grid-cols-5"
          >
            <div className="sm:col-span-2">
              <label className="mb-1 block text-xs text-fg-muted">Material</label>
              <select name="material_id" required className="w-full rounded border border-border-strong px-2 py-1.5 text-sm">
                <option value="">— choose —</option>
                {materials.data?.materials.map((m) => (
                  <option key={m.id} value={m.id}>{m.name}</option>
                ))}
              </select>
            </div>
            <PField label="Quantity" name="quantity" type="number" step="0.001" required />
            <PField label="Unit price" name="unit_price" required />
            <div className="flex items-end">
              <button type="submit" disabled={addLine.isPending}
                      className="rounded border border-border-strong px-3 py-1.5 text-sm text-fg hover:border-accent">
                Add line
              </button>
            </div>
          </form>
        )}
      </OwnerOnly>
    </div>
  );
}

function SuppliersTab() {
  const qc = useQueryClient();
  const [showForm, setShowForm] = useState(false);
  const list = useQuery({
    queryKey: ["listSuppliers"],
    queryFn: () => purchasingClient.listSuppliers({}),
  });
  const save = useMutation({
    mutationFn: (m: Parameters<typeof purchasingClient.saveSupplier>[0]) =>
      purchasingClient.saveSupplier(m),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["listSuppliers"] }); setShowForm(false); },
  });

  return (
    <div className="space-y-4">
      <OwnerOnly>
        <button
          onClick={() => setShowForm((v) => !v)}
          className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover"
        >
          {showForm ? "Cancel" : "New supplier"}
        </button>
      </OwnerOnly>

      {showForm && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            const fd = new FormData(e.currentTarget);
            const terms = fd.get("terms")?.toString() ?? "";
            save.mutate({
              name: fd.get("name")?.toString() ?? "",
              accountReference: fd.get("account")?.toString() ?? "",
              contactName: fd.get("contact")?.toString() ?? "",
              email: fd.get("email")?.toString() ?? "",
              phone: fd.get("phone")?.toString() ?? "",
              address: fd.get("address")?.toString() ?? "",
              country: fd.get("country")?.toString() ?? "",
              // -1, not 0: "no terms recorded" and "due on receipt" are
              // different statements.
              paymentTermsDays: terms === "" ? -1 : Number(terms),
              notes: fd.get("notes")?.toString() ?? "",
            });
          }}
          className="grid gap-3 rounded-lg border border-border bg-surface-2 p-5 sm:grid-cols-3"
        >
          <PField label="Name" name="name" required />
          <PField label="Account reference" name="account" />
          <PField label="Country" name="country" />
          <PField label="Contact" name="contact" />
          <PField label="Email" name="email" type="email" />
          <PField label="Phone" name="phone" />
          <PField label="Payment terms (days)" name="terms" type="number" />
          <PField label="Address" name="address" className="sm:col-span-2" />
          <PField label="Notes" name="notes" className="sm:col-span-3" />
          <div className="sm:col-span-3">
            <button type="submit" disabled={save.isPending}
                    className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50">
              {save.isPending ? "Saving…" : "Save"}
            </button>
            {save.error && (
              <span className="ml-3 text-sm text-danger-fg">
                {save.error instanceof ConnectError ? save.error.rawMessage : String(save.error)}
              </span>
            )}
          </div>
        </form>
      )}

      <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-3">Name</th>
              <th className="px-4 py-3">Country</th>
              <th className="px-4 py-3">Contact</th>
              <th className="px-4 py-3">Terms</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {list.data?.suppliers.length === 0 && (
              <EmptyRow colSpan={4} title="No suppliers yet"
                        message="Who you buy from. A purchase order needs one." />
            )}
            {list.data?.suppliers.map((s) => (
              <tr key={s.id}>
                <td className="px-4 py-3 font-medium text-fg">{s.name}</td>
                <td className="px-4 py-3 text-fg-muted">{s.country || "—"}</td>
                <td className="px-4 py-3 text-fg-muted">{s.contactName || s.email || "—"}</td>
                <td className="px-4 py-3 text-fg-muted">
                  {s.paymentTermsDays < 0 ? "—" : s.paymentTermsDays === 0 ? "on receipt" : `net ${s.paymentTermsDays}`}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function GRNITab() {
  const qc = useQueryClient();
  const grni = useQuery({
    queryKey: ["listGRNI"],
    queryFn: () => purchasingClient.listGRNI({}),
  });
  const markInvoiced = useMutation({
    mutationFn: (v: { id: string; ref: string }) =>
      purchasingClient.markLotInvoiced({ materialLotId: v.id, invoiceReference: v.ref }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["listGRNI"] }),
  });

  return (
    <div className="space-y-3">
      <p className="text-sm text-fg-muted">
        Arrived and not yet billed. The one thing a monthly close needs out of receiving:
        an accrual you can point at, line by line.
      </p>
      {grni.data && grni.data.lines.length > 0 && (
        <p className="text-sm text-fg">
          {formatCAD(grni.data.totalValueCad)} outstanding across {grni.data.lines.length} lot
          {grni.data.lines.length === 1 ? "" : "s"}.
        </p>
      )}
      <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-3">Received</th>
              <th className="px-4 py-3">Material</th>
              <th className="px-4 py-3">Supplier</th>
              <th className="px-4 py-3 text-right">Quantity</th>
              <th className="px-4 py-3 text-right">Landed / unit</th>
              <th className="px-4 py-3 text-right">Value</th>
              <th className="px-4 py-3 text-right"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {grni.data?.lines.length === 0 && (
              <EmptyRow colSpan={7} title="Nothing outstanding"
                        message="Everything received has been matched to an invoice." />
            )}
            {grni.data?.lines.map((l) => (
              <tr key={l.materialLotId}>
                <td className="px-4 py-3 text-fg-muted">{l.receivedOn}</td>
                <td className="px-4 py-3 font-medium text-fg">{l.materialName}</td>
                <td className="px-4 py-3 text-fg-muted">{l.supplierName || "—"}</td>
                <td className="px-4 py-3 text-right text-fg-muted">{l.quantity}</td>
                <td className="px-4 py-3 text-right text-fg-muted">{formatCAD(l.landedUnitCostCad)}</td>
                <td className="px-4 py-3 text-right font-medium text-fg">{formatCAD(l.valueCad)}</td>
                <td className="px-4 py-3 text-right">
                  <WriteOnly>
                    <button
                      onClick={() => {
                        const ref = window.prompt("Invoice reference?");
                        if (ref) markInvoiced.mutate({ id: l.materialLotId, ref });
                      }}
                      className="text-xs text-fg-muted hover:text-fg"
                    >
                      Mark invoiced
                    </button>
                  </WriteOnly>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function PField({ label, name, type = "text", step, defaultValue, required, className }: {
  label: string; name: string; type?: string; step?: string;
  defaultValue?: string; required?: boolean; className?: string;
}) {
  return (
    <div className={className}>
      <label className="mb-1 block text-xs text-fg-muted">{label}</label>
      <input name={name} type={type} step={step} defaultValue={defaultValue} required={required}
             className="w-full rounded border border-border-strong px-2 py-1.5 text-sm" />
    </div>
  );
}
