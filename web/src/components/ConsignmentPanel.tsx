import { FormEvent, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { Callout } from "@/components/Callout";
import { bottlingClient, customerClient, removalClient } from "@/lib/clients";
import { ConsignmentStatus } from "@/gen/stillhouse/v1/removal_pb";
import { WriteOnly } from "@/lib/role";

/**
 * ConsignmentPanel — our stock, at somebody else's premises.
 *
 * The sentence that matters is above the form rather than in a help page:
 * a consignment is not a removal. The stock is still ours and still on
 * hand — it is simply not here, and not available to promise to anybody
 * else — and duty does not move until it sells through.
 *
 * An operator watching bottles go out the door will assume otherwise, and
 * the return will not correct them.
 */
function errText(e: unknown): string {
  return e instanceof ConnectError ? e.rawMessage : String(e);
}

const statusLabel: Record<number, string> = {
  [ConsignmentStatus.UNSPECIFIED]: "—",
  [ConsignmentStatus.OUT]: "out",
  [ConsignmentStatus.SETTLED]: "settled",
  [ConsignmentStatus.RECALLED]: "recalled",
};

export function ConsignmentPanel() {
  const qc = useQueryClient();
  const list = useQuery({
    queryKey: ["consignments"],
    queryFn: () => removalClient.listConsignments({ limit: 100 }),
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
  const [err, setErr] = useState<string | null>(null);

  const send = useMutation({
    mutationFn: () =>
      removalClient.sendOnConsignment({
        packagedInventoryId: lot,
        customerId: customer,
        bottles: Number(bottles),
        sentOn: new Date().toISOString().slice(0, 10),
      }),
    onSuccess: () => {
      setErr(null);
      setOpen(false);
      setBottles("");
      void qc.invalidateQueries({ queryKey: ["consignments"] });
    },
    onError: (e) => setErr(errText(e)),
  });

  const settle = useMutation({
    mutationFn: (v: { id: string; bottlesSold: number; bottlesRecalled: number }) =>
      removalClient.settleConsignment({ ...v, on: new Date().toISOString().slice(0, 10) }),
    onSuccess: () => {
      setErr(null);
      void qc.invalidateQueries({ queryKey: ["consignments"] });
    },
    onError: (e) => setErr(errText(e)),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    setErr(null);
    send.mutate();
  }

  const d = list.data;
  return (
    <section className="mt-8">
      <div className="mb-3 flex items-center justify-between">
        <h2 className="text-sm font-semibold text-fg-muted">
          On consignment
          {d && d.openConsignments > 0 && (
            <span className="ml-2 font-normal text-fg-subtle">
              {d.bottlesOut} bottles across {d.openConsignments}
            </span>
          )}
        </h2>
        <WriteOnly>
          <button
            onClick={() => setOpen((v) => !v)}
            className="rounded border border-border-strong px-3 py-1 text-sm hover:bg-surface-3"
          >
            {open ? "Cancel" : "Send on consignment"}
          </button>
        </WriteOnly>
      </div>

      {err && <Callout tone="danger" title="That failed">{err}</Callout>}

      {open && (
        <form onSubmit={submit} className="mb-4 rounded-lg border border-border bg-surface-2 p-4">
          <Callout tone="info" title="This is not a removal">
            The stock stays yours and stays on hand — it is simply not here, and
            not available to promise to anybody else. A removal is recorded when
            it sells through, which is when duty falls due at an at-removal duty
            point. If your own arrangement treats the shipment itself as the
            removal, record a removal instead and do not use this.
          </Callout>
          <div className="mt-3 grid gap-3 sm:grid-cols-3">
            <label className="text-sm">
              <span className="mb-1 block text-fg-muted">Lot</span>
              <select value={lot} onChange={(e) => setLot(e.target.value)}
                      className="w-full rounded border border-border-strong px-2 py-2 text-sm">
                <option value="">Choose…</option>
                {lots.data?.rows.map((l) => (
                  <option key={l.id} value={l.id}>
                    {l.lotCode} — {l.productName} ({l.bottlesOnHand} on hand)
                  </option>
                ))}
              </select>
            </label>
            <label className="text-sm">
              <span className="mb-1 block text-fg-muted">Customer</span>
              <select value={customer} onChange={(e) => setCustomer(e.target.value)}
                      className="w-full rounded border border-border-strong px-2 py-2 text-sm">
                <option value="">Choose…</option>
                {customers.data?.customers.map((c) => (
                  <option key={c.id} value={c.id}>{c.name}</option>
                ))}
              </select>
            </label>
            <label className="text-sm">
              <span className="mb-1 block text-fg-muted">Bottles</span>
              <input type="number" min="1" value={bottles}
                     onChange={(e) => setBottles(e.target.value)}
                     className="w-full rounded border border-border-strong px-2 py-2 text-sm" />
            </label>
          </div>
          <button type="submit" disabled={send.isPending || !lot || !customer || !bottles}
                  className="mt-3 rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50">
            {send.isPending ? "Sending…" : "Send"}
          </button>
        </form>
      )}

      <div className="overflow-x-auto rounded-lg border border-border bg-surface-2">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-2">#</th>
              <th className="px-4 py-2">Sent</th>
              <th className="px-4 py-2">Lot</th>
              <th className="px-4 py-2">With</th>
              <th className="px-4 py-2 text-right">Sent</th>
              <th className="px-4 py-2 text-right">Sold</th>
              <th className="px-4 py-2 text-right">Back</th>
              <th className="px-4 py-2 text-right">Still out</th>
              <th className="px-4 py-2">Status</th>
              <th className="px-4 py-2"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {(d?.consignments ?? []).length === 0 && (
              <tr><td colSpan={10} className="px-4 py-3 text-fg-muted">Nothing out on consignment.</td></tr>
            )}
            {d?.consignments.map((c) => (
              <tr key={c.id}>
                <td className="px-4 py-2 tabular-nums">{c.consignmentNo}</td>
                <td className="px-4 py-2 tabular-nums">{c.sentOn}</td>
                <td className="px-4 py-2">{c.lotCode}</td>
                <td className="px-4 py-2">{c.customerName}</td>
                <td className="px-4 py-2 text-right tabular-nums">{c.bottles}</td>
                <td className="px-4 py-2 text-right tabular-nums">{c.bottlesSettled}</td>
                <td className="px-4 py-2 text-right tabular-nums">{c.bottlesRecalled}</td>
                <td className="px-4 py-2 text-right font-medium tabular-nums">{c.bottlesOut}</td>
                <td className="px-4 py-2">{statusLabel[c.status]}</td>
                <td className="px-4 py-2 text-right">
                  {c.status === ConsignmentStatus.OUT && (
                    <WriteOnly>
                      <button
                        onClick={() => {
                          const sold = window.prompt(`How many of the ${c.bottlesOut} sold through?`, "0");
                          if (sold === null) return;
                          const back = window.prompt("And how many came back unsold?", "0");
                          if (back === null) return;
                          settle.mutate({
                            id: c.id,
                            bottlesSold: Number(sold),
                            bottlesRecalled: Number(back),
                          });
                        }}
                        className="text-xs underline"
                      >
                        settle
                      </button>
                    </WriteOnly>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}
