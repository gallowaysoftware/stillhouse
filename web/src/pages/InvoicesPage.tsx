import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { EmptyRow } from "@/components/EmptyState";
import { Shell } from "@/components/Shell";
import { customerClient, invoicingClient, salesClient } from "@/lib/clients";
import { InvoiceKind, InvoiceStatus } from "@/gen/stillhouse/v1/invoicing_pb";
import { RequirementProvenance } from "@/gen/stillhouse/v1/provincial_pb";
import { OwnerOnly } from "@/lib/role";

const statusLabel: Record<number, string> = {
  [InvoiceStatus.UNSPECIFIED]: "—",
  [InvoiceStatus.DRAFT]: "Draft",
  [InvoiceStatus.ISSUED]: "Issued",
  [InvoiceStatus.PART_PAID]: "Part paid",
  [InvoiceStatus.PAID]: "Paid",
  [InvoiceStatus.VOID]: "Void",
};

function errText(e: unknown): string {
  return e instanceof ConnectError ? e.rawMessage : String(e);
}

export function InvoicesPage() {
  const [tab, setTab] = useState<"invoices" | "ageing" | "tax">("invoices");
  return (
    <Shell>
      <div data-print-hide className="mb-6">
        <h1 className="text-3xl font-bold tracking-tight">Invoices</h1>
        <p className="text-sm text-fg-muted">
          An order and a shipment do not ask anybody for money. This is the
          document a customer pays against, and the record of whether they have.
          Tax rates are yours to record — Stillhouse does not know them, and an
          invoice with no rate configured shows no tax and says so rather than
          showing zero.
        </p>
      </div>

      <div data-print-hide className="mb-4 flex gap-1 border-b border-border text-sm">
        {([["invoices", "Invoices"], ["ageing", "What's owed"], ["tax", "Tax rates"]] as const).map(
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

      {tab === "invoices" && <InvoicesTab />}
      {tab === "ageing" && <AgeingTab />}
      {tab === "tax" && <TaxTab />}
    </Shell>
  );
}

function InvoicesTab() {
  const qc = useQueryClient();
  const [openOnly, setOpenOnly] = useState(true);
  const [selected, setSelected] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);

  const invoices = useQuery({
    queryKey: ["listInvoices", openOnly],
    queryFn: () => invoicingClient.listInvoices({ openOnly }),
  });
  const customers = useQuery({
    queryKey: ["listCustomers"],
    queryFn: () => customerClient.listCustomers({}),
  });
  const shipments = useQuery({
    queryKey: ["listShipments", false],
    queryFn: () => salesClient.listShipments({ openOnly: false }),
  });
  const create = useMutation({
    mutationFn: (m: Parameters<typeof invoicingClient.createInvoice>[0]) =>
      invoicingClient.createInvoice(m),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["listInvoices"] });
      setSelected(r.invoice?.id ?? null);
      setCreating(false);
    },
  });

  return (
    <div className="space-y-4">
      <div data-print-hide className="flex flex-wrap items-center gap-3">
        <label className="flex items-center gap-2 text-sm text-fg-muted">
          <input type="checkbox" checked={openOnly} onChange={(e) => setOpenOnly(e.target.checked)} />
          Only what's still owed
        </label>
        <OwnerOnly>
          <button
            onClick={() => setCreating((v) => !v)}
            className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover"
          >
            {creating ? "Cancel" : "New invoice"}
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
              shipmentId: fd.get("shipment_id")?.toString() ?? "",
              customerReference: fd.get("customer_reference")?.toString() ?? "",
              notes: fd.get("notes")?.toString() ?? "",
            });
          }}
          className="grid gap-3 rounded-lg border border-border bg-surface-2 p-5 sm:grid-cols-2"
        >
          <div>
            <label className="mb-1 block text-xs text-fg-muted">Against a shipment</label>
            <select name="shipment_id" className="w-full rounded border border-border-strong px-2 py-1.5 text-sm">
              <option value="">— none; bill something else —</option>
              {shipments.data?.shipments
                .filter((s) => s.shipDate)
                .map((s) => (
                  <option key={s.id} value={s.id}>
                    #{s.shipmentNo} — {s.customerName} ({s.bottles} bottles, {s.shipDate})
                  </option>
                ))}
            </select>
            <p className="mt-1 text-xs text-fg-subtle">
              Fills the lines from what actually left, at the price agreed on the order.
            </p>
          </div>
          <div>
            <label className="mb-1 block text-xs text-fg-muted">Or a customer</label>
            <select name="customer_id" className="w-full rounded border border-border-strong px-2 py-1.5 text-sm">
              <option value="">— choose —</option>
              {customers.data?.customers.map((c) => (
                <option key={c.id} value={c.id}>{c.name}</option>
              ))}
            </select>
          </div>
          <Field label="Their PO number" name="customer_reference" />
          <Field label="Notes" name="notes" />
          <div className="sm:col-span-2">
            <button type="submit" disabled={create.isPending}
                    className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50">
              Create draft
            </button>
            {create.error && <span className="ml-3 text-sm text-danger-fg">{errText(create.error)}</span>}
          </div>
        </form>
      )}

      <div data-print-hide className="overflow-hidden rounded-lg border border-border bg-surface-2">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-2">#</th>
              <th className="px-4 py-2">Customer</th>
              <th className="px-4 py-2">Issued</th>
              <th className="px-4 py-2">Due</th>
              <th className="px-4 py-2">Status</th>
              <th className="px-4 py-2 text-right">Total</th>
              <th className="px-4 py-2 text-right">Outstanding</th>
              <th className="px-4 py-2"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {invoices.data?.invoices.length === 0 && (
              <EmptyRow colSpan={8} title="Nothing outstanding"
                        message="Ship something and invoice it, or bill for a service." />
            )}
            {invoices.data?.invoices.map((i) => (
              <tr key={i.id} className={i.daysOverdue > 0 ? "bg-danger-bg" : undefined}>
                <td className="px-4 py-2 font-medium text-fg">
                  {i.kind === InvoiceKind.CREDIT_NOTE ? "CN" : ""}{i.invoiceNo}
                </td>
                <td className="px-4 py-2 text-fg-muted">{i.customerName}</td>
                <td className="px-4 py-2 text-fg-muted">{i.issueDate || "—"}</td>
                <td className="px-4 py-2 text-fg-muted">
                  {i.dueDate || "—"}
                  {i.daysOverdue > 0 && (
                    <span className="ml-2 text-xs text-danger-fg">{i.daysOverdue} d late</span>
                  )}
                </td>
                <td className="px-4 py-2 text-fg-muted">{statusLabel[i.status]}</td>
                <td className="px-4 py-2 text-right text-fg-muted">${i.total}</td>
                <td className="px-4 py-2 text-right font-medium text-fg">${i.outstanding}</td>
                <td className="px-4 py-2 text-right">
                  <button
                    onClick={() => setSelected(selected === i.id ? null : i.id)}
                    className="text-xs text-fg-muted hover:text-fg"
                  >
                    {selected === i.id ? "Close" : "Open"}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {selected && <InvoiceDetail id={selected} />}
    </div>
  );
}

function InvoiceDetail({ id }: { id: string }) {
  const qc = useQueryClient();
  const detail = useQuery({
    queryKey: ["getInvoice", id],
    queryFn: () => invoicingClient.getInvoice({ id }),
  });
  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["getInvoice", id] });
    qc.invalidateQueries({ queryKey: ["listInvoices"] });
    qc.invalidateQueries({ queryKey: ["arAgeing"] });
    qc.invalidateQueries({ queryKey: ["listAlerts"] });
  };
  const addLine = useMutation({
    mutationFn: (m: Parameters<typeof invoicingClient.addInvoiceLine>[0]) =>
      invoicingClient.addInvoiceLine(m),
    onSuccess: invalidate,
  });
  const removeLine = useMutation({
    mutationFn: (m: Parameters<typeof invoicingClient.removeInvoiceLine>[0]) =>
      invoicingClient.removeInvoiceLine(m),
    onSuccess: invalidate,
  });
  const issue = useMutation({
    mutationFn: (m: Parameters<typeof invoicingClient.issueInvoice>[0]) =>
      invoicingClient.issueInvoice(m),
    onSuccess: invalidate,
  });
  const pay = useMutation({
    mutationFn: (m: Parameters<typeof invoicingClient.recordPayment>[0]) =>
      invoicingClient.recordPayment(m),
    onSuccess: invalidate,
  });
  const voidIt = useMutation({
    mutationFn: (m: Parameters<typeof invoicingClient.voidInvoice>[0]) =>
      invoicingClient.voidInvoice(m),
    onSuccess: invalidate,
  });
  const credit = useMutation({
    mutationFn: (m: Parameters<typeof invoicingClient.createCreditNote>[0]) =>
      invoicingClient.createCreditNote(m),
    onSuccess: invalidate,
  });

  const i = detail.data?.invoice;
  if (!i) return null;
  const draft = i.status === InvoiceStatus.DRAFT;

  return (
    <div className="space-y-4 rounded-lg border border-border bg-surface-2 p-5">
      <div className="flex flex-wrap items-baseline justify-between gap-3">
        <div>
          <h2 className="text-sm font-semibold text-fg">
            {i.kind === InvoiceKind.CREDIT_NOTE ? "Credit note" : "Invoice"} {i.invoiceNo}
            <span className="ml-2 text-xs text-fg-muted">{statusLabel[i.status]}</span>
          </h2>
          <p className="text-xs text-fg-muted">
            {i.billToName || i.customerName}
            {i.billToAddress && <> · {i.billToAddress}</>}
            {i.customerReference && <> · their ref {i.customerReference}</>}
          </p>
        </div>
        <button
          data-print-hide
          onClick={() => window.print()}
          className="rounded border border-border-strong px-2 py-1 text-xs text-fg hover:bg-surface-3"
        >
          Print
        </button>
      </div>

      {i.warnings.map((w, n) => (
        <p key={n} className="rounded border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-fg">
          {w}
        </p>
      ))}

      <table className="min-w-full divide-y divide-border text-sm">
        <thead className="text-left text-xs text-fg-muted">
          <tr>
            <th className="px-2 py-1.5">Description</th>
            <th className="px-2 py-1.5 text-right">Qty</th>
            <th className="px-2 py-1.5 text-right">Unit</th>
            <th className="px-2 py-1.5 text-right">Amount</th>
            <th className="px-2 py-1.5 text-right">Tax</th>
            <th className="px-2 py-1.5"></th>
          </tr>
        </thead>
        <tbody className="divide-y divide-border">
          {i.lines.map((l) => (
            <tr key={l.id}>
              <td className="px-2 py-1.5 text-fg">{l.description}</td>
              <td className="px-2 py-1.5 text-right text-fg-muted">{Number(l.quantity)}</td>
              <td className="px-2 py-1.5 text-right font-mono text-xs text-fg-muted">
                ${Number(l.unitPrice).toFixed(2)}
              </td>
              <td className="px-2 py-1.5 text-right text-fg">${l.lineTotal}</td>
              <td className="px-2 py-1.5 text-right text-fg-muted">
                {l.taxName ? `${l.taxName} $${l.taxAmount}` : "—"}
              </td>
              <td className="px-2 py-1.5 text-right">
                {draft && (
                  <OwnerOnly>
                    <button
                      data-print-hide
                      onClick={() => removeLine.mutate({ lineId: l.id })}
                      className="text-xs text-fg-muted hover:text-danger-fg"
                    >
                      Remove
                    </button>
                  </OwnerOnly>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      <dl className="ml-auto max-w-xs space-y-1 text-sm">
        <Row k="Subtotal" v={`$${i.subtotal}`} />
        <Row k={i.lines[0]?.taxName || "Tax"} v={`$${i.tax}`} />
        <Row k="Total" v={`$${i.total}`} bold />
        {Number(i.paid) !== 0 && <Row k="Paid" v={`$${i.paid}`} />}
        {Number(i.paid) !== 0 && <Row k="Outstanding" v={`$${i.outstanding}`} bold />}
      </dl>

      {draft && (
        <OwnerOnly>
          <form
            data-print-hide
            onSubmit={(e) => {
              e.preventDefault();
              const fd = new FormData(e.currentTarget);
              addLine.mutate({
                invoiceId: id,
                description: fd.get("description")?.toString() ?? "",
                quantity: fd.get("quantity")?.toString() ?? "",
                unitPrice: fd.get("unit_price")?.toString() ?? "",
              });
              e.currentTarget.reset();
            }}
            className="grid gap-3 border-t border-border pt-4 sm:grid-cols-4"
          >
            <Field label="Description" name="description" required className="sm:col-span-2" />
            <Field label="Quantity" name="quantity" required />
            <Field label="Unit price" name="unit_price" required />
            <div className="sm:col-span-4 flex flex-wrap gap-2">
              <button type="submit"
                      className="rounded border border-border-strong px-3 py-1.5 text-sm text-fg hover:bg-surface-3">
                Add line
              </button>
              <button
                type="button"
                onClick={() => issue.mutate({ id, issueDate: "", termsDays: -1 })}
                disabled={issue.isPending || i.lines.length === 0}
                className="rounded bg-accent px-3 py-1.5 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
              >
                Issue it
              </button>
              <button
                type="button"
                onClick={() => {
                  const reason = window.prompt("Why is this being voided?");
                  if (reason) voidIt.mutate({ id, reason });
                }}
                className="rounded border border-border-strong px-3 py-1.5 text-sm text-fg-muted hover:text-fg"
              >
                Void
              </button>
            </div>
            {(addLine.error || issue.error || voidIt.error) && (
              <p className="text-sm text-danger-fg sm:col-span-4">
                {errText(addLine.error ?? issue.error ?? voidIt.error)}
              </p>
            )}
          </form>
        </OwnerOnly>
      )}

      {!draft && i.kind === InvoiceKind.INVOICE && i.status !== InvoiceStatus.VOID && (
        <OwnerOnly>
          <div data-print-hide className="flex flex-wrap gap-2 border-t border-border pt-4">
            <button
              onClick={() => {
                const amount = window.prompt("How much came in?", i.outstanding);
                if (amount) pay.mutate({ invoiceId: id, amount });
              }}
              className="rounded bg-accent px-3 py-1.5 text-sm font-medium text-white hover:bg-accent-hover"
            >
              Record a payment
            </button>
            <button
              onClick={() => {
                const reason = window.prompt("Why is this being credited?");
                if (reason) credit.mutate({ invoiceId: id, reason });
              }}
              className="rounded border border-border-strong px-3 py-1.5 text-sm text-fg hover:bg-surface-3"
            >
              Credit it in full
            </button>
            {(pay.error || credit.error) && (
              <span className="text-sm text-danger-fg">{errText(pay.error ?? credit.error)}</span>
            )}
          </div>
        </OwnerOnly>
      )}

      {detail.data && detail.data.payments.length > 0 && (
        <div className="border-t border-border pt-4">
          <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-fg-muted">
            Payments
          </h3>
          <ul className="space-y-1 text-sm text-fg-muted">
            {detail.data.payments.map((p) => (
              <li key={p.id}>
                {p.receivedOn} · ${p.amount}
                {p.method && <> · {p.method}</>}
                {p.reference && <> · {p.reference}</>}
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}

function AgeingTab() {
  const ageing = useQuery({
    queryKey: ["arAgeing"],
    queryFn: () => invoicingClient.ageingReport({}),
  });
  const d = ageing.data;
  // The server names the bands, so the table follows whatever it sent
  // rather than hard-coding a set of columns.
  const labels = d?.lines[0]?.buckets.map((b) => b.label) ?? [];
  return (
    <div className="space-y-3">
      {d && <p className="text-xs text-fg-subtle">{d.basis}</p>}
      <div className="overflow-hidden rounded-lg border border-border bg-surface-2">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-2">Customer</th>
              {labels.map((l) => (
                <th key={l} className="px-4 py-2 text-right">{l}</th>
              ))}
              <th className="px-4 py-2 text-right">Total</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {d?.lines.length === 0 && (
              <EmptyRow colSpan={labels.length + 2} title="Nobody owes anything"
                        message="Every issued invoice is settled." />
            )}
            {d?.lines.map((l) => (
              <tr key={l.customerId}>
                <td className="px-4 py-2 text-fg">{l.customerName}</td>
                {l.buckets.map((b) => (
                  <td key={b.label}
                      className={`px-4 py-2 text-right ${
                        b.overdue && Number(b.amount) !== 0 ? "text-danger-fg" : "text-fg-muted"
                      }`}>
                    ${b.amount}
                  </td>
                ))}
                <td className="px-4 py-2 text-right font-medium text-fg">${l.total}</td>
              </tr>
            ))}
          </tbody>
          {d && d.lines.length > 0 && (
            <tfoot>
              <tr className="bg-surface-3">
                <td className="px-4 py-2 text-xs font-semibold text-fg-muted"
                    colSpan={labels.length + 1}>
                  Total owed
                </td>
                <td className="px-4 py-2 text-right font-semibold text-fg">${d.total}</td>
              </tr>
            </tfoot>
          )}
        </table>
      </div>
    </div>
  );
}

function TaxTab() {
  const qc = useQueryClient();
  const [adding, setAdding] = useState(false);
  const rates = useQuery({
    queryKey: ["listTaxRates"],
    queryFn: () => invoicingClient.listTaxRates({}),
  });
  const save = useMutation({
    mutationFn: (m: Parameters<typeof invoicingClient.saveTaxRate>[0]) =>
      invoicingClient.saveTaxRate(m),
    onSuccess: () => { setAdding(false); qc.invalidateQueries({ queryKey: ["listTaxRates"] }); },
  });
  const remove = useMutation({
    mutationFn: (m: Parameters<typeof invoicingClient.deleteTaxRate>[0]) =>
      invoicingClient.deleteTaxRate(m),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["listTaxRates"] }),
  });

  return (
    <div className="space-y-4">
      <p className="text-sm text-fg-muted">
        Effective-dated, so an invoice already issued keeps the rate it was
        issued at. Superseding a rate means adding a new row from the date it
        changed, not editing the old one. The rate is a fraction — 0.13 for
        thirteen percent.
      </p>
      <OwnerOnly>
        <button
          onClick={() => setAdding((v) => !v)}
          className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover"
        >
          {adding ? "Cancel" : "Add a rate"}
        </button>
      </OwnerOnly>

      {adding && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            const fd = new FormData(e.currentTarget);
            save.mutate({
              jurisdiction: fd.get("jurisdiction")?.toString() ?? "",
              name: fd.get("name")?.toString() ?? "",
              rate: fd.get("rate")?.toString() ?? "",
              effectiveFrom: fd.get("effective_from")?.toString() ?? "",
              registrationNo: fd.get("registration_no")?.toString() ?? "",
              provenance: Number(fd.get("provenance") ?? 0) as RequirementProvenance,
              authority: fd.get("authority")?.toString() ?? "",
            });
          }}
          className="grid gap-3 rounded-lg border border-border bg-surface-2 p-5 sm:grid-cols-3"
        >
          <Field label="Jurisdiction (blank = everywhere)" name="jurisdiction" placeholder="CA-ON" />
          <Field label="Name" name="name" placeholder="HST" required />
          <Field label="Rate as a fraction" name="rate" placeholder="0.13" required />
          <Field label="Effective from" name="effective_from" type="date" />
          <Field label="Your registration number" name="registration_no" />
          <div>
            <label className="mb-1 block text-xs text-fg-muted">How well do you know this?</label>
            <select name="provenance" className="w-full rounded border border-border-strong px-2 py-1.5 text-sm">
              <option value={RequirementProvenance.UNKNOWN}>Unknown</option>
              <option value={RequirementProvenance.INDICATIVE}>Indicative</option>
              <option value={RequirementProvenance.SOURCED}>Sourced</option>
            </select>
          </div>
          <Field label="Source (required if sourced)" name="authority" className="sm:col-span-3" />
          <div className="sm:col-span-3">
            <button type="submit" disabled={save.isPending}
                    className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50">
              Save
            </button>
            {save.error && <span className="ml-3 text-sm text-danger-fg">{errText(save.error)}</span>}
          </div>
        </form>
      )}

      <div className="overflow-hidden rounded-lg border border-border bg-surface-2">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-2">Where</th>
              <th className="px-4 py-2">Name</th>
              <th className="px-4 py-2 text-right">Rate</th>
              <th className="px-4 py-2">From</th>
              <th className="px-4 py-2">Source</th>
              <th className="px-4 py-2"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {rates.data?.rates.length === 0 && (
              <EmptyRow colSpan={6} title="No tax rates"
                        message="Invoices will show no tax at all, and say so on each one." />
            )}
            {rates.data?.rates.map((r) => (
              <tr key={r.id}>
                <td className="px-4 py-2 text-fg-muted">{r.jurisdiction || "everywhere"}</td>
                <td className="px-4 py-2 text-fg">{r.name}</td>
                <td className="px-4 py-2 text-right text-fg-muted">
                  {(Number(r.rate) * 100).toFixed(3).replace(/0+$/, "").replace(/\.$/, "")} %
                </td>
                <td className="px-4 py-2 text-fg-muted">{r.effectiveFrom}</td>
                <td className="px-4 py-2 text-xs text-fg-muted">
                  {r.provenance === RequirementProvenance.SOURCED ? (
                    <span className="text-success-fg">{r.authority}</span>
                  ) : (
                    <span className="text-warning-fg">not confirmed</span>
                  )}
                </td>
                <td className="px-4 py-2 text-right">
                  <OwnerOnly>
                    <button onClick={() => remove.mutate({ id: r.id })}
                            className="text-xs text-fg-muted hover:text-danger-fg">
                      Remove
                    </button>
                  </OwnerOnly>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function Row({ k, v, bold }: { k: string; v: string; bold?: boolean }) {
  return (
    <div className="flex justify-between">
      <dt className="text-fg-muted">{k}</dt>
      <dd className={`font-mono ${bold ? "font-semibold text-fg" : "text-fg-muted"}`}>{v}</dd>
    </div>
  );
}

function Field({ label, name, type = "text", placeholder, required, className }: {
  label: string; name: string; type?: string;
  placeholder?: string; required?: boolean; className?: string;
}) {
  return (
    <div className={className}>
      <label className="mb-1 block text-xs text-fg-muted">{label}</label>
      <input name={name} type={type} placeholder={placeholder} required={required}
             className="w-full rounded border border-border-strong px-2 py-1.5 text-sm" />
    </div>
  );
}
