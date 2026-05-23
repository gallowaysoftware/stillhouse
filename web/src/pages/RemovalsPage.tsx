import { FormEvent, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { Shell } from "@/components/Shell";
import { bottlingClient, removalClient } from "@/lib/clients";
import {
  CreateRemovalRequestSchema,
  RemovalDestinationKind,
  VoidRemovalRequestSchema,
} from "@/gen/stillhouse/v1/removal_pb";
import { formatLAA, formatQty } from "@/lib/format";
import { WriteOnly, canWrite, useCurrentRole } from "@/lib/role";
import { useConfirm } from "@/components/ConfirmDialog";
import { Pager } from "@/pages/BottlingPage";

const PAGE_SIZE = 50;

const destLabel: Record<RemovalDestinationKind, string> = {
  [RemovalDestinationKind.UNSPECIFIED]: "—",
  [RemovalDestinationKind.DUTY_PAID_CUSTOMER]: "Duty-paid customer",
  [RemovalDestinationKind.EXPORT]: "Export",
  [RemovalDestinationKind.SAMPLE]: "Sample",
  [RemovalDestinationKind.DESTROYED]: "Destroyed",
  [RemovalDestinationKind.TRANSFER_OUT_IN_BOND]: "Transfer out (in bond)",
  [RemovalDestinationKind.OTHER]: "Other",
};

const destOptions: RemovalDestinationKind[] = [
  RemovalDestinationKind.DUTY_PAID_CUSTOMER,
  RemovalDestinationKind.EXPORT,
  RemovalDestinationKind.SAMPLE,
  RemovalDestinationKind.DESTROYED,
  RemovalDestinationKind.TRANSFER_OUT_IN_BOND,
  RemovalDestinationKind.OTHER,
];

export function RemovalsPage() {
  const qc = useQueryClient();
  const role = useCurrentRole();
  const writeable = canWrite(role);
  const [page, setPage] = useState(0);
  const list = useQuery({
    queryKey: ["listRemovals", page],
    queryFn: () =>
      removalClient.listRemovals({ limit: PAGE_SIZE, offset: page * PAGE_SIZE }),
  });
  const packaged = useQuery({
    queryKey: ["listPackagedInventory"],
    queryFn: () => bottlingClient.listPackagedInventory({}),
  });
  const [showForm, setShowForm] = useState(false);
  const [piID, setPiID] = useState("");
  const [bottles, setBottles] = useState("");
  const [dest, setDest] = useState<RemovalDestinationKind>(RemovalDestinationKind.DUTY_PAID_CUSTOMER);
  const [destName, setDestName] = useState("");
  const [reference, setReference] = useState("");
  const [removalDate, setRemovalDate] = useState("");

  const create_ = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof CreateRemovalRequestSchema>>) =>
      removalClient.createRemoval(msg),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["listRemovals"] });
      qc.invalidateQueries({ queryKey: ["listPackagedInventory"] });
      setShowForm(false);
      setBottles("");
      setDestName("");
      setReference("");
    },
  });
  const voidRemoval = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof VoidRemovalRequestSchema>>) =>
      removalClient.voidRemoval(msg),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["listRemovals"] });
      qc.invalidateQueries({ queryKey: ["listPackagedInventory"] });
    },
  });

  const confirm = useConfirm();
  async function onVoid(id: string, no: number, bottles: number) {
    const ok = await confirm({
      title: `Void removal #${no}?`,
      body: <>You're about to void this removal. The {bottles.toLocaleString()} bottles will be refunded to packaged inventory and the duty entry rolled back.</>,
      consequences: [
        `${bottles.toLocaleString()} bottles return to on-hand inventory`,
        "Duty contribution drops out of the current B266 period",
        "Original row stays for audit — voided with timestamp + user",
      ],
      requireReason: { label: "Reason", placeholder: "recorded in error" },
      confirmLabel: "Void removal",
      tone: "danger",
    });
    if (!ok) return;
    voidRemoval.mutate(create(VoidRemovalRequestSchema, { id, reason: ok.reason }));
  }

  function submit(e: FormEvent) {
    e.preventDefault();
    create_.mutate(
      create(CreateRemovalRequestSchema, {
        packagedInventoryId: piID,
        bottlesRemoved: Number(bottles),
        destinationKind: dest,
        destinationName: destName,
        reference,
        removalDate,
      }),
    );
  }

  const selectedRow = packaged.data?.rows.find((r) => r.id === piID);

  return (
    <Shell>
      <div className="mb-6 flex items-center justify-between">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">Removals</h1>
          <p className="text-sm text-fg-muted">
            Removals from the excise warehouse crystallize CRA duty
            ($14.117/LAA for spirits &gt;7%; rate effective April 1, 2026).
          </p>
        </div>
        <WriteOnly>
          <button
            onClick={() => setShowForm((s) => !s)}
            className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover"
          >
            {showForm ? "Cancel" : "Record removal"}
          </button>
        </WriteOnly>
      </div>

      {showForm && (
        <form onSubmit={submit} className="mb-6 grid grid-cols-2 gap-4 rounded-lg border border-border bg-surface-2 p-5 shadow-sm">
          <div className="col-span-2">
            <label className="mb-2 block text-sm font-medium text-fg-muted">Packaged inventory row</label>
            <select value={piID} onChange={(e) => setPiID(e.target.value)} required className="w-full rounded border border-border-strong px-3 py-2 text-sm">
              <option value="">Select…</option>
              {packaged.data?.rows.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.productName} · lot {r.lotCode} · {r.jurisdiction} · {r.bottlesOnHand.toLocaleString()} on hand
                </option>
              ))}
            </select>
            {selectedRow && (
              <p className="mt-1 text-xs text-fg-muted">
                {selectedRow.bottleSizeMl} mL × {selectedRow.targetAbvPct}% = {((selectedRow.bottleSizeMl * selectedRow.targetAbvPct) / 100000).toFixed(4)} L LAA / bottle
              </p>
            )}
          </div>
          <div>
            <label className="mb-2 block text-sm font-medium text-fg-muted">Bottles</label>
            <input type="number" min="1" value={bottles} onChange={(e) => setBottles(e.target.value)} required className="w-full rounded border border-border-strong px-3 py-2 text-sm" />
          </div>
          <div>
            <label className="mb-2 block text-sm font-medium text-fg-muted">Removal date</label>
            <input type="date" value={removalDate} onChange={(e) => setRemovalDate(e.target.value)} className="w-full rounded border border-border-strong px-3 py-2 text-sm" />
          </div>
          <div>
            <label className="mb-2 block text-sm font-medium text-fg-muted">Destination kind</label>
            <select value={dest} onChange={(e) => setDest(Number(e.target.value) as RemovalDestinationKind)} className="w-full rounded border border-border-strong px-3 py-2 text-sm">
              {destOptions.map((d) => <option key={d} value={d}>{destLabel[d]}</option>)}
            </select>
          </div>
          <div>
            <label className="mb-2 block text-sm font-medium text-fg-muted">Destination name</label>
            <input value={destName} onChange={(e) => setDestName(e.target.value)} className="w-full rounded border border-border-strong px-3 py-2 text-sm" />
          </div>
          <div className="col-span-2">
            <label className="mb-2 block text-sm font-medium text-fg-muted">Reference (BOL / invoice)</label>
            <input value={reference} onChange={(e) => setReference(e.target.value)} className="w-full rounded border border-border-strong px-3 py-2 text-sm" />
          </div>
          <div className="col-span-2 flex items-center gap-3">
            <button
              type="submit"
              disabled={create_.isPending}
              className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
            >
              {create_.isPending ? "Saving…" : "Record removal"}
            </button>
            {create_.error && (
              <span className="text-sm text-danger-fg">
                {create_.error instanceof ConnectError ? create_.error.rawMessage : String(create_.error)}
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
              <th className="px-4 py-3">Date</th>
              <th className="px-4 py-3">Product / Lot</th>
              <th className="px-4 py-3">Jurisdiction</th>
              <th className="px-4 py-3">Destination</th>
              <th className="px-4 py-3 text-right">Bottles</th>
              <th className="px-4 py-3 text-right">LAA</th>
              <th className="px-4 py-3 text-right">Duty (CAD)</th>
              {writeable && <th className="px-4 py-3"></th>}
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {list.data?.removals.length === 0 && (
              <tr><td colSpan={writeable ? 9 : 8} className="px-4 py-3 text-fg-muted">No removals yet.</td></tr>
            )}
            {list.data?.removals.map((r) => {
              const voided = !!r.voidedAt;
              return (
                <tr key={r.id} className={voided ? "bg-surface-3 text-fg-subtle" : ""}>
                  <td className="px-4 py-3 font-medium">
                    #{r.removalNo}
                    {voided && (
                      <span className="ml-2 rounded bg-danger/15 px-1.5 py-0.5 text-xs font-normal text-danger-fg">VOIDED</span>
                    )}
                  </td>
                  <td className="px-4 py-3">{r.removalDate}</td>
                  <td className="px-4 py-3">
                    {r.productName} <span className="text-xs">· {r.lotCode}</span>
                    {voided && r.voidedReason && (
                      <div className="text-xs italic">{r.voidedReason}</div>
                    )}
                  </td>
                  <td className="px-4 py-3">{r.jurisdiction}</td>
                  <td className="px-4 py-3">{destLabel[r.destinationKind]}</td>
                  <td className={`px-4 py-3 text-right ${voided ? "line-through" : ""}`}>{r.bottlesRemoved.toLocaleString()}</td>
                  <td className={`px-4 py-3 text-right ${voided ? "line-through" : ""}`}>{formatLAA(r.totalLaa)}</td>
                  <td className={`px-4 py-3 text-right font-medium ${voided ? "line-through" : "text-fg"}`}>${formatQty(r.dutyAmountCad)}</td>
                  {writeable && (
                    <td className="px-4 py-3 text-right">
                      {!voided && (
                        <button
                          onClick={() => onVoid(r.id, r.removalNo, r.bottlesRemoved)}
                          disabled={voidRemoval.isPending}
                          className="text-xs text-fg-muted hover:text-danger-fg disabled:opacity-50"
                        >
                          Void
                        </button>
                      )}
                    </td>
                  )}
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      <Pager
        page={page}
        pageSize={PAGE_SIZE}
        total={list.data?.totalCount ?? 0}
        onPage={setPage}
      />
    </Shell>
  );
}
