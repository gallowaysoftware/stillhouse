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
  const list = useQuery({
    queryKey: ["listRemovals"],
    queryFn: () => removalClient.listRemovals({}),
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

  function onVoid(id: string, no: number, bottles: number) {
    const reason = window.prompt(
      `Void removal #${no} (${bottles.toLocaleString()} bottles will be refunded to inventory). Reason:`,
      "recorded in error",
    );
    if (!reason || !reason.trim()) return;
    voidRemoval.mutate(create(VoidRemovalRequestSchema, { id, reason: reason.trim() }));
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
          <h1 className="text-2xl font-semibold">Removals</h1>
          <p className="text-sm text-stone-500">
            Removals from the excise warehouse crystallize CRA duty
            ($14.117/LAA for spirits &gt;7%; rate effective April 1, 2026).
          </p>
        </div>
        <WriteOnly>
          <button
            onClick={() => setShowForm((s) => !s)}
            className="rounded bg-stone-900 px-3 py-2 text-sm font-medium text-white hover:bg-stone-800"
          >
            {showForm ? "Cancel" : "Record removal"}
          </button>
        </WriteOnly>
      </div>

      {showForm && (
        <form onSubmit={submit} className="mb-6 grid grid-cols-2 gap-4 rounded-lg border border-stone-200 bg-white p-5 shadow-sm">
          <div className="col-span-2">
            <label className="mb-1 block text-xs font-medium text-stone-600">Packaged inventory row</label>
            <select value={piID} onChange={(e) => setPiID(e.target.value)} required className="w-full rounded border border-stone-300 px-3 py-2 text-sm">
              <option value="">Select…</option>
              {packaged.data?.rows.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.productName} · lot {r.lotCode} · {r.jurisdiction} · {r.bottlesOnHand.toLocaleString()} on hand
                </option>
              ))}
            </select>
            {selectedRow && (
              <p className="mt-1 text-xs text-stone-500">
                {selectedRow.bottleSizeMl} mL × {selectedRow.targetAbvPct}% = {((selectedRow.bottleSizeMl * selectedRow.targetAbvPct) / 100000).toFixed(4)} L LAA / bottle
              </p>
            )}
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-stone-600">Bottles</label>
            <input type="number" min="1" value={bottles} onChange={(e) => setBottles(e.target.value)} required className="w-full rounded border border-stone-300 px-3 py-2 text-sm" />
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-stone-600">Removal date</label>
            <input type="date" value={removalDate} onChange={(e) => setRemovalDate(e.target.value)} className="w-full rounded border border-stone-300 px-3 py-2 text-sm" />
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-stone-600">Destination kind</label>
            <select value={dest} onChange={(e) => setDest(Number(e.target.value) as RemovalDestinationKind)} className="w-full rounded border border-stone-300 px-3 py-2 text-sm">
              {destOptions.map((d) => <option key={d} value={d}>{destLabel[d]}</option>)}
            </select>
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-stone-600">Destination name</label>
            <input value={destName} onChange={(e) => setDestName(e.target.value)} className="w-full rounded border border-stone-300 px-3 py-2 text-sm" />
          </div>
          <div className="col-span-2">
            <label className="mb-1 block text-xs font-medium text-stone-600">Reference (BOL / invoice)</label>
            <input value={reference} onChange={(e) => setReference(e.target.value)} className="w-full rounded border border-stone-300 px-3 py-2 text-sm" />
          </div>
          <div className="col-span-2 flex items-center gap-3">
            <button
              type="submit"
              disabled={create_.isPending}
              className="rounded bg-stone-900 px-3 py-2 text-sm font-medium text-white hover:bg-stone-800 disabled:bg-stone-400"
            >
              {create_.isPending ? "Saving…" : "Record removal"}
            </button>
            {create_.error && (
              <span className="text-sm text-red-600">
                {create_.error instanceof ConnectError ? create_.error.rawMessage : String(create_.error)}
              </span>
            )}
          </div>
        </form>
      )}

      <div className="overflow-hidden rounded-lg border border-stone-200 bg-white shadow-sm">
        <table className="min-w-full divide-y divide-stone-200 text-sm">
          <thead className="bg-stone-50 text-left text-xs uppercase text-stone-500">
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
          <tbody className="divide-y divide-stone-100">
            {list.data?.removals.length === 0 && (
              <tr><td colSpan={writeable ? 9 : 8} className="px-4 py-3 text-stone-500">No removals yet.</td></tr>
            )}
            {list.data?.removals.map((r) => {
              const voided = !!r.voidedAt;
              return (
                <tr key={r.id} className={voided ? "bg-stone-50 text-stone-400" : ""}>
                  <td className="px-4 py-3 font-medium">
                    #{r.removalNo}
                    {voided && (
                      <span className="ml-2 rounded bg-red-100 px-1.5 py-0.5 text-xs font-normal text-red-700">VOIDED</span>
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
                  <td className={`px-4 py-3 text-right font-medium ${voided ? "line-through" : "text-stone-900"}`}>${formatQty(r.dutyAmountCad)}</td>
                  {writeable && (
                    <td className="px-4 py-3 text-right">
                      {!voided && (
                        <button
                          onClick={() => onVoid(r.id, r.removalNo, r.bottlesRemoved)}
                          disabled={voidRemoval.isPending}
                          className="text-xs text-stone-600 hover:text-red-700 disabled:opacity-50"
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
    </Shell>
  );
}
