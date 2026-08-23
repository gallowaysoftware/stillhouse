import { FormEvent, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { Callout } from "@/components/Callout";
import { Shell } from "@/components/Shell";
import { customerClient, kegClient } from "@/lib/clients";
import { KegEventKind, KegStatus } from "@/gen/stillhouse/v1/keg_pb";
import { formatCAD, formatLAA } from "@/lib/format";
import { OwnerOnly, WriteOnly } from "@/lib/role";

/**
 * KegsPage — the returnable-asset register.
 *
 * The register tracks the VESSEL. A keg's spirits are recorded elsewhere
 * — as a marked special container at 100 L and above, as packaged spirits
 * below it — and their LAA and duty reach the B266 from there. The
 * contents shown here are read from whichever row owns them, never stored
 * on the keg, because a copy would put the same alcohol on a filed return
 * twice.
 *
 * What this screen knows that nothing else does: where each physical keg
 * is, what deposit is outstanding on it, and how long its contents have
 * been sitting.
 */
const statusLabel: Record<number, string> = {
  [KegStatus.UNSPECIFIED]: "—",
  [KegStatus.AVAILABLE]: "available",
  [KegStatus.FILLED]: "filled",
  [KegStatus.AT_CUSTOMER]: "at customer",
  [KegStatus.RETURNED_DIRTY]: "returned, dirty",
  [KegStatus.OUT_OF_SERVICE]: "out of service",
  [KegStatus.LOST]: "lost",
};

// Which moves are offered from each status. The server enforces this; the
// UI only avoids offering a button that will be refused.
const movesFrom: Record<number, [KegEventKind, string][]> = {
  [KegStatus.AVAILABLE]: [
    [KegEventKind.FILLED, "Fill"],
    [KegEventKind.CONDEMNED, "Condemn"],
    [KegEventKind.LOST, "Mark lost"],
  ],
  [KegStatus.FILLED]: [
    [KegEventKind.SHIPPED, "Ship"],
    [KegEventKind.CONDEMNED, "Condemn"],
    [KegEventKind.LOST, "Mark lost"],
  ],
  [KegStatus.AT_CUSTOMER]: [
    [KegEventKind.RETURNED, "Returned"],
    [KegEventKind.LOST, "Mark lost"],
  ],
  [KegStatus.RETURNED_DIRTY]: [
    [KegEventKind.CLEANED, "Cleaned"],
    [KegEventKind.CONDEMNED, "Condemn"],
  ],
  [KegStatus.OUT_OF_SERVICE]: [[KegEventKind.CLEANED, "Back in service"]],
  [KegStatus.LOST]: [],
};

function errText(e: unknown): string {
  return e instanceof ConnectError ? e.rawMessage : String(e);
}

export function KegsPage() {
  const qc = useQueryClient();
  const kegs = useQuery({ queryKey: ["kegs"], queryFn: () => kegClient.listKegs({}) });
  const customers = useQuery({
    queryKey: ["customers"],
    queryFn: () => customerClient.listCustomers({}),
  });
  const [err, setErr] = useState<string | null>(null);

  const moveKeg = useMutation({
    mutationFn: (v: {
      kegId: string;
      kind: KegEventKind;
      customerId?: string;
      markedContainerId?: string;
      packagedInventoryId?: string;
    }) =>
      kegClient.moveKeg({
        ...v,
        occurredOn: new Date().toISOString().slice(0, 10),
      }),
    onSuccess: () => {
      setErr(null);
      void qc.invalidateQueries({ queryKey: ["kegs"] });
    },
    onError: (e) => setErr(errText(e)),
  });

  const d = kegs.data;

  return (
    <Shell>
      <div className="mb-6">
        <h1 className="text-3xl font-bold tracking-tight">Kegs</h1>
        <p className="text-sm text-fg-muted">
          Where each keg is, what deposit is outstanding on it, and how long its
          contents have been sitting. The spirits themselves are counted on the
          marked special container or the packaged lot the keg points at — never
          here, so nothing is counted twice.
        </p>
      </div>

      {err && <Callout tone="danger" title="That move was refused">{err}</Callout>}

      {d && (
        <div className="mb-6 grid gap-3 sm:grid-cols-4">
          <Stat label="Total" value={String(d.total)} />
          <Stat label="Available" value={String(d.available)} />
          <Stat label="At customers" value={String(d.atCustomer)} />
          <Stat
            label="Deposits outstanding"
            value={formatCAD(d.totalOutstandingDepositsCad)}
          />
        </div>
      )}

      <NewKegForm />

      <div className="mb-6 overflow-x-auto rounded-lg border border-border bg-surface-2">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-2">Serial</th>
              <th className="px-4 py-2 text-right">Capacity</th>
              <th className="px-4 py-2">Status</th>
              <th className="px-4 py-2">Where</th>
              <th className="px-4 py-2 text-right">Contents</th>
              <th className="px-4 py-2 text-right">Days filled</th>
              <th className="px-4 py-2 text-right">Deposit</th>
              <th className="px-4 py-2"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {(d?.kegs ?? []).length === 0 && (
              <tr><td colSpan={8} className="px-4 py-3 text-fg-muted">No kegs in the register.</td></tr>
            )}
            {d?.kegs.map((k) => (
              <tr key={k.id}>
                <td className="px-4 py-2 font-medium">{k.serial}</td>
                <td className="px-4 py-2 text-right tabular-nums">{k.capacityL} L</td>
                <td className="px-4 py-2">{statusLabel[k.status]}</td>
                <td className="px-4 py-2">{k.customerName || k.locationName || "here"}</td>
                <td className="px-4 py-2 text-right tabular-nums">
                  {k.contentsLaa > 0 ? formatLAA(k.contentsLaa) : "—"}
                </td>
                <td className="px-4 py-2 text-right tabular-nums">
                  {k.daysSinceFillSet ? k.daysSinceFill : "—"}
                </td>
                <td className="px-4 py-2 text-right tabular-nums">
                  {k.depositSet ? formatCAD(k.depositCad) : "—"}
                </td>
                <td className="px-4 py-2 text-right">
                  <WriteOnly>
                    {(movesFrom[k.status] ?? []).map(([kind, label]) => (
                      <button
                        key={kind}
                        disabled={moveKeg.isPending}
                        onClick={() => {
                          if (kind === KegEventKind.FILLED) {
                            // Which id is wanted follows from the keg's
                            // size against the Act's 100 L threshold; the
                            // server refuses the wrong one.
                            const id = window.prompt(
                              k.capacityL >= 100
                                ? "Marked special container id (this keg is ≥100 L)"
                                : "Packaged lot id (this keg is <100 L)",
                            );
                            if (!id) return;
                            moveKeg.mutate(
                              k.capacityL >= 100
                                ? { kegId: k.id, kind, markedContainerId: id }
                                : { kegId: k.id, kind, packagedInventoryId: id },
                            );
                            return;
                          }
                          if (kind === KegEventKind.SHIPPED) {
                            const c = customers.data?.customers[0];
                            const id = window.prompt(
                              "Customer id (the deposit is owed by somebody)",
                              c?.id ?? "",
                            );
                            if (!id) return;
                            moveKeg.mutate({ kegId: k.id, kind, customerId: id });
                            return;
                          }
                          moveKeg.mutate({ kegId: k.id, kind });
                        }}
                        className="ml-2 text-xs underline"
                      >
                        {label}
                      </button>
                    ))}
                  </WriteOnly>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <h2 className="mb-2 text-sm font-semibold text-fg-muted">Deposits outstanding</h2>
      <div className="overflow-hidden rounded-lg border border-border bg-surface-2">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-2">Customer</th>
              <th className="px-4 py-2 text-right">Shipped</th>
              <th className="px-4 py-2 text-right">Returned</th>
              <th className="px-4 py-2 text-right">Outstanding</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {(d?.deposits ?? []).length === 0 && (
              <tr><td colSpan={4} className="px-4 py-3 text-fg-muted">Nothing outstanding.</td></tr>
            )}
            {d?.deposits.map((l) => (
              <tr key={l.customerId}>
                <td className="px-4 py-2">{l.customerName}</td>
                <td className="px-4 py-2 text-right tabular-nums">{l.kegsShipped}</td>
                <td className="px-4 py-2 text-right tabular-nums">{l.kegsReturned}</td>
                <td className="px-4 py-2 text-right tabular-nums">{formatCAD(l.outstandingCad)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Shell>
  );
}

function NewKegForm() {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [serial, setSerial] = useState("");
  const [capacity, setCapacity] = useState("50");
  const [deposit, setDeposit] = useState("");
  const [err, setErr] = useState<string | null>(null);

  const create = useMutation({
    mutationFn: () =>
      kegClient.createKeg({
        serial,
        capacityL: Number(capacity),
        depositCad: deposit ? Number(deposit) : 0,
        depositSet: deposit !== "",
      }),
    onSuccess: () => {
      setErr(null);
      setSerial("");
      setOpen(false);
      void qc.invalidateQueries({ queryKey: ["kegs"] });
    },
    onError: (e) => setErr(errText(e)),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    setErr(null);
    create.mutate();
  }

  return (
    <OwnerOnly>
      <div className="mb-4">
        <button
          onClick={() => setOpen((v) => !v)}
          className="rounded border border-border-strong px-3 py-1 text-sm hover:bg-surface-3"
        >
          {open ? "Cancel" : "Add a keg"}
        </button>
        {open && (
          <form onSubmit={submit} className="mt-3 grid gap-3 rounded-lg border border-border bg-surface-2 p-4 sm:grid-cols-4">
            <label className="text-sm">
              <span className="mb-1 block text-fg-muted">Serial</span>
              <input value={serial} onChange={(e) => setSerial(e.target.value)}
                     className="w-full rounded border border-border-strong px-2 py-1" />
            </label>
            <label className="text-sm">
              <span className="mb-1 block text-fg-muted">Capacity (L)</span>
              <input type="number" min="1" value={capacity} onChange={(e) => setCapacity(e.target.value)}
                     className="w-full rounded border border-border-strong px-2 py-1" />
            </label>
            <label className="text-sm">
              <span className="mb-1 block text-fg-muted">Deposit (CAD)</span>
              <input type="number" step="0.01" min="0" value={deposit} onChange={(e) => setDeposit(e.target.value)}
                     className="w-full rounded border border-border-strong px-2 py-1" />
            </label>
            <div className="flex items-end">
              <button type="submit" disabled={create.isPending || !serial}
                      className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50">
                {create.isPending ? "Adding…" : "Add"}
              </button>
            </div>
            {err && <p className="col-span-4 text-sm text-danger-fg">{err}</p>}
          </form>
        )}
      </div>
    </OwnerOnly>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg border border-border bg-surface-2 p-3">
      <p className="text-xs text-fg-muted">{label}</p>
      <p className="mt-1 text-xl font-semibold tabular-nums">{value}</p>
    </div>
  );
}
