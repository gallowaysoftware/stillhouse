import { FormEvent, useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { Shell } from "@/components/Shell";
import { bulkClient } from "@/lib/clients";
import {
  BulkContainerKind,
  CreateBulkContainerRequestSchema,
} from "@/gen/stillhouse/v1/bulk_pb";
import {
  bulkContainerKindLabel,
  bulkMovementReasonLabel,
  formatLAA,
  formatQty,
} from "@/lib/format";

const kindOptions = [
  { v: BulkContainerKind.SPIRIT_RECEIVER, label: "Spirit receiver" },
  { v: BulkContainerKind.TANK, label: "Tank" },
  { v: BulkContainerKind.IBC, label: "IBC" },
  { v: BulkContainerKind.TOTE, label: "Tote" },
  { v: BulkContainerKind.BLEND_TANK, label: "Blend tank" },
  { v: BulkContainerKind.BOTTLING_TANK, label: "Bottling tank" },
  { v: BulkContainerKind.OTHER, label: "Other" },
];

export function BulkPage() {
  const qc = useQueryClient();
  const list = useQuery({
    queryKey: ["listBulkContainers"],
    queryFn: () => bulkClient.listBulkContainers({}),
  });
  const recent = useQuery({
    queryKey: ["listRecentBulkMovements"],
    queryFn: () => bulkClient.listRecentBulkMovements({}),
  });

  const [showForm, setShowForm] = useState(false);
  const createContainer = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof CreateBulkContainerRequestSchema>>) =>
      bulkClient.createBulkContainer(msg),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["listBulkContainers"] });
      setShowForm(false);
    },
  });

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const fd = new FormData(e.currentTarget);
    const capRaw = fd.get("capacity_l")?.toString().trim() ?? "";
    createContainer.mutate(
      create(CreateBulkContainerRequestSchema, {
        name: fd.get("name")?.toString() ?? "",
        kind: Number(fd.get("kind")) as BulkContainerKind,
        capacityL: capRaw ? Number(capRaw) : 0,
        capacityLSet: !!capRaw,
        location: fd.get("location")?.toString() ?? "",
        notes: fd.get("notes")?.toString() ?? "",
      }),
    );
  }

  return (
    <Shell>
      <div className="mb-6 flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">Bulk inventory</h1>
          <p className="text-sm text-stone-500">
            Tanks, IBCs, and other containers holding bulk spirits.
            {list.data?.summary && (
              <>
                {" "}Total: <span className="font-medium text-stone-900">
                  {formatLAA(list.data.summary.totalLaa)} L LAA
                </span>{" "}across {list.data.summary.containerCount} container(s).
              </>
            )}
          </p>
        </div>
        <button
          onClick={() => setShowForm((s) => !s)}
          className="rounded bg-stone-900 px-3 py-2 text-sm font-medium text-white hover:bg-stone-800"
        >
          {showForm ? "Cancel" : "Add container"}
        </button>
      </div>

      {showForm && (
        <form
          onSubmit={submit}
          className="mb-6 grid grid-cols-2 gap-4 rounded-lg border border-stone-200 bg-white p-5 shadow-sm"
        >
          <div>
            <label className="mb-1 block text-xs font-medium text-stone-600">Name</label>
            <input
              name="name"
              required
              className="w-full rounded border border-stone-300 px-3 py-2 text-sm"
            />
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-stone-600">Kind</label>
            <select
              name="kind"
              defaultValue={BulkContainerKind.TANK}
              className="w-full rounded border border-stone-300 px-3 py-2 text-sm"
            >
              {kindOptions.map((k) => (
                <option key={k.v} value={k.v}>{k.label}</option>
              ))}
            </select>
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-stone-600">Capacity (L)</label>
            <input name="capacity_l" type="number" step="0.1" className="w-full rounded border border-stone-300 px-3 py-2 text-sm" />
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-stone-600">Location</label>
            <input name="location" className="w-full rounded border border-stone-300 px-3 py-2 text-sm" />
          </div>
          <div className="col-span-2">
            <label className="mb-1 block text-xs font-medium text-stone-600">Notes</label>
            <input name="notes" className="w-full rounded border border-stone-300 px-3 py-2 text-sm" />
          </div>
          <div className="col-span-2 flex items-center gap-3">
            <button
              type="submit"
              disabled={createContainer.isPending}
              className="rounded bg-stone-900 px-3 py-2 text-sm font-medium text-white hover:bg-stone-800 disabled:bg-stone-400"
            >
              {createContainer.isPending ? "Saving…" : "Save"}
            </button>
            {createContainer.error && (
              <span className="text-sm text-red-600">
                {createContainer.error instanceof ConnectError
                  ? createContainer.error.rawMessage
                  : String(createContainer.error)}
              </span>
            )}
          </div>
        </form>
      )}

      <div className="mb-8 overflow-hidden rounded-lg border border-stone-200 bg-white shadow-sm">
        <table className="min-w-full divide-y divide-stone-200 text-sm">
          <thead className="bg-stone-50 text-left text-xs uppercase text-stone-500">
            <tr>
              <th className="px-4 py-3">Name</th>
              <th className="px-4 py-3">Kind</th>
              <th className="px-4 py-3 text-right">Volume (L)</th>
              <th className="px-4 py-3 text-right">ABV</th>
              <th className="px-4 py-3 text-right">LAA</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-stone-100">
            {list.isLoading && (
              <tr><td colSpan={5} className="px-4 py-3 text-stone-500">Loading…</td></tr>
            )}
            {!list.isLoading && list.data?.containers.length === 0 && (
              <tr><td colSpan={5} className="px-4 py-3 text-stone-500">No containers yet.</td></tr>
            )}
            {list.data?.containers.map((c) => (
              <tr key={c.id}>
                <td className="px-4 py-3">
                  <Link to={`/bulk/${c.id}`} className="text-stone-900 hover:underline">{c.name}</Link>
                </td>
                <td className="px-4 py-3 text-stone-600">{bulkContainerKindLabel(c.kind)}</td>
                <td className="px-4 py-3 text-right text-stone-600">{formatQty(c.currentVolumeL)}</td>
                <td className="px-4 py-3 text-right text-stone-600">
                  {c.currentAbvPctSet ? c.currentAbvPct.toFixed(2) + "%" : "—"}
                </td>
                <td className="px-4 py-3 text-right font-medium text-stone-900">{formatLAA(c.currentLaa)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <h2 className="mb-3 text-sm font-semibold uppercase text-stone-500">Recent movements</h2>
      <div className="overflow-hidden rounded-lg border border-stone-200 bg-white shadow-sm">
        <table className="min-w-full divide-y divide-stone-200 text-sm">
          <thead className="bg-stone-50 text-left text-xs uppercase text-stone-500">
            <tr>
              <th className="px-4 py-3">When</th>
              <th className="px-4 py-3">Reason</th>
              <th className="px-4 py-3">From → To</th>
              <th className="px-4 py-3 text-right">Volume (L)</th>
              <th className="px-4 py-3 text-right">ABV</th>
              <th className="px-4 py-3 text-right">LAA</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-stone-100">
            {recent.data?.movements.length === 0 && (
              <tr><td colSpan={6} className="px-4 py-3 text-stone-500">No movements yet.</td></tr>
            )}
            {recent.data?.movements.map((m) => (
              <tr key={m.id}>
                <td className="px-4 py-3 text-stone-600">
                  {m.occurredAt ? new Date(Number(m.occurredAt.seconds) * 1000).toLocaleString() : ""}
                </td>
                <td className="px-4 py-3 text-stone-600">{bulkMovementReasonLabel(m.reason)}</td>
                <td className="px-4 py-3 text-stone-600">
                  {m.sourceContainerName || "—"} → {m.destinationContainerName || "—"}
                </td>
                <td className="px-4 py-3 text-right text-stone-600">{formatQty(m.volumeL)}</td>
                <td className="px-4 py-3 text-right text-stone-600">{m.abvPct.toFixed(2)}%</td>
                <td className="px-4 py-3 text-right font-medium text-stone-900">{formatLAA(m.laa)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Shell>
  );
}
