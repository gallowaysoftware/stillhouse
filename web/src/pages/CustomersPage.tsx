import { FormEvent, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { EmptyRow } from "@/components/EmptyState";
import { Shell } from "@/components/Shell";
import { customerClient, productClient } from "@/lib/clients";
import { CustomerKind } from "@/gen/stillhouse/v1/customer_pb";
import { RemovalDestinationKind } from "@/gen/stillhouse/v1/removal_pb";
import { SalesChannel } from "@/gen/stillhouse/v1/pricing_pb";
import { OwnerOnly } from "@/lib/role";

// The buyer kinds, paired with what a removal to them *is* for excise.
// Showing the consequence next to the choice is the point of the page:
// the classification that decides whether duty is charged is a property
// of the buyer, and this is where it gets decided — once.
const kindOptions: { v: CustomerKind; label: string; consequence: string }[] = [
  {
    v: CustomerKind.PROVINCIAL_BOARD,
    label: "Provincial board",
    consequence: "duty-paid removal",
  },
  {
    v: CustomerKind.LICENSEE,
    label: "Licensee (bar / restaurant)",
    consequence: "duty-paid removal",
  },
  {
    v: CustomerKind.PRIVATE_RETAIL,
    label: "Private retail",
    consequence: "duty-paid removal",
  },
  {
    v: CustomerKind.SPIRITS_LICENSEE,
    label: "Another spirits licensee",
    consequence: "transfer in bond — no duty",
  },
  { v: CustomerKind.EXPORT, label: "Export", consequence: "export — no duty" },
  {
    v: CustomerKind.ON_SITE_RETAIL,
    label: "On-site retail",
    consequence: "duty-paid removal",
  },
  { v: CustomerKind.OTHER, label: "Other", consequence: "classified per removal" },
];

const kindLabel = (k: CustomerKind) =>
  kindOptions.find((o) => o.v === k)?.label ?? "—";

export function destinationKindLabel(k: RemovalDestinationKind): string {
  switch (k) {
    case RemovalDestinationKind.DUTY_PAID_CUSTOMER: return "Duty-paid customer";
    case RemovalDestinationKind.EXPORT: return "Export";
    case RemovalDestinationKind.SAMPLE: return "Sample";
    case RemovalDestinationKind.DESTROYED: return "Destroyed";
    case RemovalDestinationKind.TRANSFER_OUT_IN_BOND: return "Transfer out, in bond";
    case RemovalDestinationKind.OTHER: return "Other";
    default: return "—";
  }
}

export function CustomersPage() {
  const qc = useQueryClient();
  const [showForm, setShowForm] = useState(false);
  const [includeArchived, setIncludeArchived] = useState(false);
  const [tab, setTab] = useState<"customers" | "prices">("customers");

  const list = useQuery({
    queryKey: ["listCustomers", includeArchived],
    queryFn: () => customerClient.listCustomers({ includeArchived }),
  });
  const priceLists = useQuery({
    queryKey: ["listPriceLists"],
    queryFn: () => customerClient.listPriceLists({}),
  });

  const createCustomer = useMutation({
    mutationFn: (msg: Parameters<typeof customerClient.createCustomer>[0]) =>
      customerClient.createCustomer(msg),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["listCustomers"] });
      setShowForm(false);
    },
  });
  const archive = useMutation({
    mutationFn: (v: { id: string; archived: boolean }) =>
      customerClient.setCustomerArchived(v),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["listCustomers"] }),
  });

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const fd = new FormData(e.currentTarget);
    const terms = fd.get("payment_terms_days")?.toString() ?? "";
    createCustomer.mutate({
      name: fd.get("name")?.toString() ?? "",
      kind: Number(fd.get("kind")) as CustomerKind,
      jurisdiction: fd.get("jurisdiction")?.toString() ?? "",
      licenceNumber: fd.get("licence_number")?.toString() ?? "",
      accountReference: fd.get("account_reference")?.toString() ?? "",
      contactName: fd.get("contact_name")?.toString() ?? "",
      email: fd.get("email")?.toString() ?? "",
      phone: fd.get("phone")?.toString() ?? "",
      address: fd.get("address")?.toString() ?? "",
      // -1, not 0: "no terms recorded" and "due on receipt" are
      // different statements and a zero would collapse them.
      paymentTermsDays: terms === "" ? -1 : Number(terms),
      notes: fd.get("notes")?.toString() ?? "",
      priceListId: fd.get("price_list_id")?.toString() ?? "",
    });
  }

  return (
    <Shell>
      <div className="mb-6 flex items-center justify-between">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">Customers</h1>
          <p className="text-sm text-fg-muted">
            Who the alcohol goes to. A customer's kind decides whether a removal to
            them is duty-paid, in bond or an export — recorded here once, rather than
            re-chosen on every removal.
          </p>
        </div>
        {tab === "customers" && (
          <OwnerOnly>
            <button
              onClick={() => setShowForm((s) => !s)}
              className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover"
            >
              {showForm ? "Cancel" : "New customer"}
            </button>
          </OwnerOnly>
        )}
      </div>

      <div className="mb-4 flex gap-1 border-b border-border text-sm">
        {(["customers", "prices"] as const).map((t) => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className={`-mb-px border-b-2 px-3 py-2 ${
              tab === t
                ? "border-accent text-fg"
                : "border-transparent text-fg-muted hover:text-fg"
            }`}
          >
            {t === "customers" ? "Customers" : "Price lists"}
          </button>
        ))}
      </div>

      {tab === "customers" && showForm && (
        <form
          onSubmit={submit}
          className="mb-6 grid grid-cols-2 gap-4 rounded-lg border border-border bg-surface-2 p-5 shadow-sm"
        >
          <Field label="Name" name="name" required />
          <Field label="Kind" name="kind" as="select" defaultValue={String(CustomerKind.PROVINCIAL_BOARD)}>
            {kindOptions.map((k) => (
              <option key={k.v} value={k.v}>
                {k.label} — {k.consequence}
              </option>
            ))}
          </Field>
          <Field label="Jurisdiction (CA-ON, CA-BC…)" name="jurisdiction" />
          <Field label="Their excise licence number" name="licence_number" />
          <Field label="Account / vendor reference" name="account_reference" />
          <Field label="Payment terms (days)" name="payment_terms_days" type="number" />
          <Field label="Contact name" name="contact_name" />
          <Field label="Email" name="email" type="email" />
          <Field label="Phone" name="phone" />
          <Field label="Price list" name="price_list_id" as="select">
            <option value="">— none —</option>
            {priceLists.data?.priceLists.map((p) => (
              <option key={p.id} value={p.id}>{p.name}</option>
            ))}
          </Field>
          <Field label="Address" name="address" className="col-span-2" />
          <Field label="Notes" name="notes" className="col-span-2" />
          <div className="col-span-2 flex items-center gap-3">
            <button
              type="submit"
              disabled={createCustomer.isPending}
              className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
            >
              {createCustomer.isPending ? "Saving…" : "Save"}
            </button>
            {createCustomer.error && (
              <span className="text-sm text-danger-fg">
                {createCustomer.error instanceof ConnectError
                  ? createCustomer.error.rawMessage
                  : String(createCustomer.error)}
              </span>
            )}
          </div>
        </form>
      )}

      {tab === "customers" && (
        <>
          <label className="mb-3 flex items-center gap-2 text-sm text-fg-muted">
            <input
              type="checkbox"
              checked={includeArchived}
              onChange={(e) => setIncludeArchived(e.target.checked)}
            />
            Show archived
          </label>

          <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
            <table className="min-w-full divide-y divide-border text-sm">
              <thead className="bg-surface-3 text-left text-xs text-fg-muted">
                <tr>
                  <th className="px-4 py-3">Name</th>
                  <th className="px-4 py-3">Kind</th>
                  <th className="px-4 py-3">Removal is</th>
                  <th className="px-4 py-3">Jurisdiction</th>
                  <th className="px-4 py-3">Licence</th>
                  <th className="px-4 py-3">Terms</th>
                  <th className="px-4 py-3 text-right"></th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {list.isLoading && (
                  <tr><td colSpan={7} className="px-4 py-3 text-fg-muted">Loading…</td></tr>
                )}
                {!list.isLoading && list.data?.customers.length === 0 && (
                  <EmptyRow
                    colSpan={7}
                    title="No customers yet"
                    message="Add the provincial board you ship to, and any licensee or export buyer. A removal that names a customer takes its excise classification from them, so it can't be typed as the wrong kind of movement."
                    action={
                      <OwnerOnly>
                        <button
                          onClick={() => setShowForm(true)}
                          className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover"
                        >
                          New customer
                        </button>
                      </OwnerOnly>
                    }
                  />
                )}
                {list.data?.customers.map((c) => {
                  const archived = !!c.archivedAt;
                  return (
                    <tr key={c.id} className={archived ? "opacity-60" : ""}>
                      <td className="px-4 py-3 font-medium text-fg">
                        {c.name}
                        {archived && <span className="ml-2 text-xs text-fg-subtle">archived</span>}
                      </td>
                      <td className="px-4 py-3 text-fg-muted">{kindLabel(c.kind)}</td>
                      <td className="px-4 py-3 text-fg-muted">
                        {destinationKindLabel(c.defaultDestinationKind)}
                      </td>
                      <td className="px-4 py-3 text-fg-muted">{c.jurisdiction || "—"}</td>
                      <td className="px-4 py-3 font-mono text-xs text-fg-muted">
                        {c.licenceNumber || "—"}
                      </td>
                      <td className="px-4 py-3 text-fg-muted">
                        {c.paymentTermsDays < 0
                          ? "—"
                          : c.paymentTermsDays === 0
                            ? "on receipt"
                            : `net ${c.paymentTermsDays}`}
                      </td>
                      <td className="px-4 py-3 text-right">
                        <OwnerOnly>
                          <button
                            onClick={() => archive.mutate({ id: c.id, archived: !archived })}
                            disabled={archive.isPending}
                            className="text-xs text-fg-muted hover:text-fg disabled:opacity-50"
                          >
                            {archived ? "Un-archive" : "Archive"}
                          </button>
                        </OwnerOnly>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </>
      )}

      {tab === "prices" && <PriceListsTab />}
    </Shell>
  );
}

function PriceListsTab() {
  const qc = useQueryClient();
  const [selected, setSelected] = useState<string>("");
  const [showForm, setShowForm] = useState(false);

  const lists = useQuery({
    queryKey: ["listPriceLists"],
    queryFn: () => customerClient.listPriceLists({}),
  });
  const products = useQuery({
    queryKey: ["listProducts"],
    queryFn: () => productClient.listProducts({}),
  });
  const detail = useQuery({
    queryKey: ["getPriceList", selected],
    queryFn: () => customerClient.getPriceList({ id: selected }),
    enabled: !!selected,
  });

  const createList = useMutation({
    mutationFn: (msg: Parameters<typeof customerClient.createPriceList>[0]) =>
      customerClient.createPriceList(msg),
    onSuccess: (resp) => {
      qc.invalidateQueries({ queryKey: ["listPriceLists"] });
      setSelected(resp.priceList?.id ?? "");
      setShowForm(false);
    },
  });
  const setEntry = useMutation({
    mutationFn: (msg: Parameters<typeof customerClient.setPriceListEntry>[0]) =>
      customerClient.setPriceListEntry(msg),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["getPriceList", selected] }),
  });

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end gap-3">
        <div className="min-w-[16rem]">
          <label className="mb-2 block text-sm font-medium text-fg-muted">Price list</label>
          <select
            value={selected}
            onChange={(e) => setSelected(e.target.value)}
            className="w-full rounded border border-border-strong bg-surface px-3 py-2 text-sm text-fg"
          >
            <option value="">— choose —</option>
            {lists.data?.priceLists.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name} ({p.effectiveFrom}{p.effectiveTo ? `–${p.effectiveTo}` : " →"})
              </option>
            ))}
          </select>
        </div>
        <OwnerOnly>
          <button
            onClick={() => setShowForm((s) => !s)}
            className="rounded border border-border-strong px-3 py-2 text-sm text-fg hover:border-accent"
          >
            {showForm ? "Cancel" : "New price list"}
          </button>
        </OwnerOnly>
      </div>

      {showForm && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            const fd = new FormData(e.currentTarget);
            createList.mutate({
              name: fd.get("name")?.toString() ?? "",
              channel: Number(fd.get("channel")) as SalesChannel,
              jurisdiction: fd.get("jurisdiction")?.toString() ?? "",
              currency: fd.get("currency")?.toString() ?? "",
              effectiveFrom: fd.get("effective_from")?.toString() ?? "",
              effectiveTo: fd.get("effective_to")?.toString() ?? "",
              notes: fd.get("notes")?.toString() ?? "",
            });
          }}
          className="grid grid-cols-2 gap-4 rounded-lg border border-border bg-surface-2 p-5 shadow-sm"
        >
          <Field label="Name" name="name" required />
          <Field label="Channel" name="channel" as="select" defaultValue={String(SalesChannel.WHOLESALE)}>
            <option value={SalesChannel.WHOLESALE}>Wholesale (to the board)</option>
            <option value={SalesChannel.ON_SITE_RETAIL}>On-site retail</option>
            <option value={SalesChannel.EXPORT}>Export</option>
          </Field>
          <Field label="Jurisdiction" name="jurisdiction" />
          <Field label="Currency" name="currency" defaultValue="CAD" />
          <Field label="Effective from" name="effective_from" type="date" required />
          <Field label="Effective to (blank = still in force)" name="effective_to" type="date" />
          <Field label="Notes" name="notes" className="col-span-2" />
          <div className="col-span-2 flex items-center gap-3">
            <button
              type="submit"
              disabled={createList.isPending}
              className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
            >
              {createList.isPending ? "Saving…" : "Save"}
            </button>
            {createList.error && (
              <span className="text-sm text-danger-fg">
                {createList.error instanceof ConnectError
                  ? createList.error.rawMessage
                  : String(createList.error)}
              </span>
            )}
          </div>
        </form>
      )}

      {selected && detail.data?.priceList && (
        <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
          <table className="min-w-full divide-y divide-border text-sm">
            <thead className="bg-surface-3 text-left text-xs text-fg-muted">
              <tr>
                <th className="px-4 py-3">Product</th>
                <th className="px-4 py-3 text-right">Bottle (mL)</th>
                <th className="px-4 py-3 text-right">Unit price</th>
                <th className="px-4 py-3 text-right">Case size</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {products.data?.products.length === 0 && (
                <tr><td colSpan={4} className="px-4 py-3 text-fg-muted">Define a product first.</td></tr>
              )}
              {products.data?.products.map((p) => {
                const entry = detail.data.priceList?.entries.find((e) => e.productId === p.id);
                return (
                  <tr key={p.id}>
                    <td className="px-4 py-3 font-medium text-fg">{p.name}</td>
                    <td className="px-4 py-3 text-right text-fg-muted">{p.bottleSizeMl}</td>
                    <td className="px-4 py-3 text-right">
                      <PriceCell
                        value={entry?.unitPrice ?? ""}
                        onSave={(v) =>
                          setEntry.mutate({
                            priceListId: selected,
                            productId: p.id,
                            unitPrice: v,
                            caseSize: entry?.caseSize ?? 0,
                          })
                        }
                      />
                    </td>
                    <td className="px-4 py-3 text-right text-fg-muted">
                      {entry?.caseSize ? entry.caseSize : "—"}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
          {setEntry.error && (
            <p className="border-t border-border px-4 py-2 text-sm text-danger-fg">
              {setEntry.error instanceof ConnectError
                ? setEntry.error.rawMessage
                : String(setEntry.error)}
            </p>
          )}
        </div>
      )}
    </div>
  );
}

// Prices are edited as text and sent as text. They are stored as NUMERIC
// and cross the wire as a decimal string: rendering 34.95 through a
// double and back is how a cent goes missing, and this is money somebody
// invoices.
function PriceCell({ value, onSave }: { value: string; onSave: (v: string) => void }) {
  const [draft, setDraft] = useState(value);
  const [editing, setEditing] = useState(false);
  if (!editing) {
    return (
      <button
        onClick={() => { setDraft(value); setEditing(true); }}
        className="font-mono text-sm text-fg hover:text-accent"
      >
        {value === "" ? <span className="text-fg-subtle">— set —</span> : value}
      </button>
    );
  }
  return (
    <span className="inline-flex items-center gap-1">
      <input
        autoFocus
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") { onSave(draft); setEditing(false); }
          if (e.key === "Escape") setEditing(false);
        }}
        placeholder="34.95"
        className="w-24 rounded border border-border-strong bg-surface px-2 py-1 text-right font-mono text-sm"
      />
      <button
        onClick={() => { onSave(draft); setEditing(false); }}
        className="text-xs text-accent hover:underline"
      >
        save
      </button>
    </span>
  );
}

function Field({
  label, name, type = "text", as = "input", required, defaultValue, children, className,
}: {
  label: string; name: string; type?: string; as?: "input" | "select";
  required?: boolean; defaultValue?: string;
  children?: React.ReactNode; className?: string;
}) {
  return (
    <div className={className}>
      <label className="mb-2 block text-sm font-medium text-fg-muted">{label}</label>
      {as === "select" ? (
        <select
          name={name}
          required={required}
          defaultValue={defaultValue}
          className="w-full rounded border border-border-strong px-3 py-2 text-sm"
        >
          {children}
        </select>
      ) : (
        <input
          name={name}
          type={type}
          required={required}
          defaultValue={defaultValue}
          className="w-full rounded border border-border-strong px-3 py-2 text-sm"
        />
      )}
    </div>
  );
}
