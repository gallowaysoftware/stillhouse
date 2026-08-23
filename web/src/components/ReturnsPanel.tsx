import { FormEvent, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { Callout } from "@/components/Callout";
import { bottlingClient, customerClient, removalClient } from "@/lib/clients";
import { PackagedReturnCondition } from "@/gen/stillhouse/v1/removal_pb";
import { formatCAD } from "@/lib/format";
import { WriteOnly } from "@/lib/role";

/**
 * ReturnsPanel — product coming back from the duty-paid market.
 *
 * The load-bearing sentence on this screen is the one about duty, and it
 * is shown before anything is recorded rather than after. Duty
 * crystallised when the goods were packaged or removed; it does not
 * un-crystallise because they came back. Getting it back is a refund
 * claim with a B256 behind it, which Stillhouse does not yet prepare.
 *
 * An operator who assumes the duty came back with the bottles will
 * under-report on a filed return, and they will not find out from the
 * return — which is why the note is on the form and not in a help page.
 */
function errText(e: unknown): string {
  return e instanceof ConnectError ? e.rawMessage : String(e);
}

export function ReturnsPanel() {
  const qc = useQueryClient();
  const returns = useQuery({
    queryKey: ["packagedReturns"],
    queryFn: () => removalClient.listPackagedReturns({ limit: 50 }),
  });
  const lots = useQuery({
    queryKey: ["packagedInventory"],
    queryFn: () => bottlingClient.listPackagedInventory({}),
  });
  const customers = useQuery({
    queryKey: ["customers"],
    queryFn: () => customerClient.listCustomers({}),
  });

  const [open, setOpen] = useState(false);
  const [lot, setLot] = useState("");
  const [customer, setCustomer] = useState("");
  const [bottles, setBottles] = useState("");
  const [condition, setCondition] = useState<PackagedReturnCondition>(
    PackagedReturnCondition.SALEABLE,
  );
  const [returnedOn, setReturnedOn] = useState(new Date().toISOString().slice(0, 10));
  const [reason, setReason] = useState("");
  const [credit, setCredit] = useState("");
  const [creditNo, setCreditNo] = useState("");
  const [err, setErr] = useState<string | null>(null);

  const record = useMutation({
    mutationFn: () =>
      removalClient.recordPackagedReturn({
        packagedInventoryId: lot,
        customerId: customer,
        bottles: Number(bottles),
        condition,
        returnedOn,
        reason,
        creditAmountCad: credit ? Number(credit) : 0,
        creditAmountSet: credit !== "",
        creditNoteNo: creditNo,
      }),
    onSuccess: () => {
      setErr(null);
      setOpen(false);
      setBottles("");
      setReason("");
      setCredit("");
      setCreditNo("");
      void qc.invalidateQueries({ queryKey: ["packagedReturns"] });
      void qc.invalidateQueries({ queryKey: ["packagedInventory"] });
    },
    onError: (e) => setErr(errText(e)),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    setErr(null);
    record.mutate();
  }

  return (
    <section className="mt-8">
      <div className="mb-3 flex items-center justify-between">
        <h2 className="text-sm font-semibold text-fg-muted">Returns from the market</h2>
        <WriteOnly>
          <button
            onClick={() => setOpen((v) => !v)}
            className="rounded border border-border-strong px-3 py-1 text-sm hover:bg-surface-3"
          >
            {open ? "Cancel" : "Record a return"}
          </button>
        </WriteOnly>
      </div>

      {open && (
        <form onSubmit={submit} className="mb-4 rounded-lg border border-border bg-surface-2 p-4">
          {/* Before the fields, not after. An operator who assumes the duty
              came back with the bottles will under-report, and the return
              will not tell them. */}
          <Callout tone="warning" title="Duty does not come back with the bottles">
            It crystallised when these goods were packaged or removed and stays
            crystallised. Recovering it is a refund claim under s.181/s.182 with
            a B256 behind it, which Stillhouse does not yet prepare. This records
            the stock and the credit; it changes no duty figure.
          </Callout>

          <div className="mt-3 grid gap-3 sm:grid-cols-2">
            <label className="text-sm">
              <span className="mb-1 block text-fg-muted">Lot</span>
              <select
                value={lot}
                onChange={(e) => setLot(e.target.value)}
                className="w-full rounded border border-border-strong px-2 py-2 text-sm"
              >
                <option value="">Choose a lot…</option>
                {lots.data?.rows.map((l) => (
                  <option key={l.id} value={l.id}>
                    {l.lotCode} — {l.productName} ({l.bottlesRemoved} removed)
                  </option>
                ))}
              </select>
            </label>
            <label className="text-sm">
              <span className="mb-1 block text-fg-muted">Customer (optional)</span>
              <select
                value={customer}
                onChange={(e) => setCustomer(e.target.value)}
                className="w-full rounded border border-border-strong px-2 py-2 text-sm"
              >
                <option value="">—</option>
                {customers.data?.customers.map((c) => (
                  <option key={c.id} value={c.id}>{c.name}</option>
                ))}
              </select>
            </label>
            <label className="text-sm">
              <span className="mb-1 block text-fg-muted">Bottles</span>
              <input
                type="number" min="1" value={bottles}
                onChange={(e) => setBottles(e.target.value)}
                className="w-full rounded border border-border-strong px-2 py-2 text-sm"
              />
            </label>
            <label className="text-sm">
              <span className="mb-1 block text-fg-muted">Condition</span>
              <select
                value={condition}
                onChange={(e) => setCondition(Number(e.target.value) as PackagedReturnCondition)}
                className="w-full rounded border border-border-strong px-2 py-2 text-sm"
              >
                <option value={PackagedReturnCondition.SALEABLE}>Saleable — back into stock</option>
                <option value={PackagedReturnCondition.UNSALEABLE}>Unsaleable — does not restock</option>
              </select>
            </label>
            <label className="text-sm">
              <span className="mb-1 block text-fg-muted">Returned on</span>
              <input
                type="date" value={returnedOn}
                onChange={(e) => setReturnedOn(e.target.value)}
                className="w-full rounded border border-border-strong px-2 py-2 text-sm"
              />
            </label>
            <label className="text-sm">
              <span className="mb-1 block text-fg-muted">Reason</span>
              <input
                value={reason} onChange={(e) => setReason(e.target.value)}
                className="w-full rounded border border-border-strong px-2 py-2 text-sm"
              />
            </label>
            <label className="text-sm">
              <span className="mb-1 block text-fg-muted">Credit (CAD, optional)</span>
              <input
                type="number" step="0.01" min="0" value={credit}
                onChange={(e) => setCredit(e.target.value)}
                className="w-full rounded border border-border-strong px-2 py-2 text-sm"
              />
            </label>
            <label className="text-sm">
              <span className="mb-1 block text-fg-muted">Credit note no.</span>
              <input
                value={creditNo} onChange={(e) => setCreditNo(e.target.value)}
                className="w-full rounded border border-border-strong px-2 py-2 text-sm"
              />
            </label>
          </div>
          <button
            type="submit"
            disabled={record.isPending || !lot || !bottles}
            className="mt-3 rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
          >
            {record.isPending ? "Recording…" : "Record return"}
          </button>
          {err && <p className="mt-2 text-sm text-danger-fg">{err}</p>}
        </form>
      )}

      <div className="overflow-hidden rounded-lg border border-border bg-surface-2">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-2">#</th>
              <th className="px-4 py-2">Date</th>
              <th className="px-4 py-2">Lot</th>
              <th className="px-4 py-2">From</th>
              <th className="px-4 py-2 text-right">Bottles</th>
              <th className="px-4 py-2">Condition</th>
              <th className="px-4 py-2 text-right">Credit</th>
              <th className="px-4 py-2">Reason</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {(returns.data?.returns ?? []).length === 0 && (
              <tr><td colSpan={8} className="px-4 py-3 text-fg-muted">Nothing has come back.</td></tr>
            )}
            {returns.data?.returns.map((r) => (
              <tr key={r.id} className={r.voided ? "opacity-50 line-through" : ""}>
                <td className="px-4 py-2 tabular-nums">{r.returnNo}</td>
                <td className="px-4 py-2 tabular-nums">{r.returnedOn}</td>
                <td className="px-4 py-2">{r.lotCode}</td>
                <td className="px-4 py-2">{r.customerName || "—"}</td>
                <td className="px-4 py-2 text-right tabular-nums">{r.bottles}</td>
                <td className="px-4 py-2">
                  {r.condition === PackagedReturnCondition.SALEABLE ? "saleable" : "unsaleable"}
                </td>
                <td className="px-4 py-2 text-right tabular-nums">
                  {r.creditAmountSet ? formatCAD(r.creditAmountCad) : "—"}
                </td>
                <td className="px-4 py-2 text-fg-muted">{r.reason}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}
