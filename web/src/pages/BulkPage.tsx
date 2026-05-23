import { FormEvent, useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { Shell } from "@/components/Shell";
import { bulkClient } from "@/lib/clients";
import { WriteOnly } from "@/lib/role";
import {
  BlendSourceInputSchema,
  BulkContainerKind,
  CreateBlendRequestSchema,
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
  const [showBlend, setShowBlend] = useState(false);
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
          <h1 className="text-3xl font-bold tracking-tight">Bulk inventory</h1>
          <p className="text-sm text-fg-muted">
            Tanks, IBCs, and other containers holding bulk spirits.
            {list.data?.summary && (
              <>
                {" "}Total: <span className="font-medium text-fg">
                  {formatLAA(list.data.summary.totalLaa)} L LAA
                </span>{" "}across {list.data.summary.containerCount} container(s).
              </>
            )}
          </p>
        </div>
        <WriteOnly>
          <button
            onClick={() => setShowForm((s) => !s)}
            className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover"
          >
            {showForm ? "Cancel" : "Add container"}
          </button>
          <button
            onClick={() => setShowBlend((s) => !s)}
            className="ml-2 rounded border border-border-strong px-3 py-2 text-sm font-medium text-fg hover:bg-surface-3"
          >
            {showBlend ? "Cancel blend" : "New blend"}
          </button>
        </WriteOnly>
      </div>

      {showBlend && <BlendForm
        containers={list.data?.containers ?? []}
        onDone={() => { setShowBlend(false); qc.invalidateQueries({ queryKey: ["listBulkContainers"] }); }}
      />}

      {showForm && (
        <form
          onSubmit={submit}
          className="mb-6 grid grid-cols-2 gap-4 rounded-lg border border-border bg-surface-2 p-5 shadow-sm"
        >
          <div>
            <label className="mb-2 block text-sm font-medium text-fg-muted">Name</label>
            <input
              name="name"
              required
              className="w-full rounded border border-border-strong px-3 py-2 text-sm"
            />
          </div>
          <div>
            <label className="mb-2 block text-sm font-medium text-fg-muted">Kind</label>
            <select
              name="kind"
              defaultValue={BulkContainerKind.TANK}
              className="w-full rounded border border-border-strong px-3 py-2 text-sm"
            >
              {kindOptions.map((k) => (
                <option key={k.v} value={k.v}>{k.label}</option>
              ))}
            </select>
          </div>
          <div>
            <label className="mb-2 block text-sm font-medium text-fg-muted">Capacity (L)</label>
            <input name="capacity_l" type="number" step="0.1" className="w-full rounded border border-border-strong px-3 py-2 text-sm" />
          </div>
          <div>
            <label className="mb-2 block text-sm font-medium text-fg-muted">Location</label>
            <input name="location" className="w-full rounded border border-border-strong px-3 py-2 text-sm" />
          </div>
          <div className="col-span-2">
            <label className="mb-2 block text-sm font-medium text-fg-muted">Notes</label>
            <input name="notes" className="w-full rounded border border-border-strong px-3 py-2 text-sm" />
          </div>
          <div className="col-span-2 flex items-center gap-3">
            <button
              type="submit"
              disabled={createContainer.isPending}
              className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
            >
              {createContainer.isPending ? "Saving…" : "Save"}
            </button>
            {createContainer.error && (
              <span className="text-sm text-danger-fg">
                {createContainer.error instanceof ConnectError
                  ? createContainer.error.rawMessage
                  : String(createContainer.error)}
              </span>
            )}
          </div>
        </form>
      )}

      <div className="mb-8 overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-3">Name</th>
              <th className="px-4 py-3">Kind</th>
              <th className="px-4 py-3 text-right">Volume (L)</th>
              <th className="px-4 py-3 text-right">ABV</th>
              <th className="px-4 py-3 text-right">LAA</th>
              <th className="px-4 py-3 text-right">Last activity</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {list.isLoading && (
              <tr><td colSpan={6} className="px-4 py-3 text-fg-muted">Loading…</td></tr>
            )}
            {!list.isLoading && list.data?.containers.length === 0 && (
              <tr><td colSpan={6} className="px-4 py-3 text-fg-muted">No containers yet.</td></tr>
            )}
            {list.data?.containers.map((c) => (
              <tr key={c.id}>
                <td className="px-4 py-3">
                  <Link to={`/bulk/${c.id}`} className="text-fg hover:underline">{c.name}</Link>
                </td>
                <td className="px-4 py-3 text-fg-muted">{bulkContainerKindLabel(c.kind)}</td>
                <td className="px-4 py-3 text-right text-fg-muted">{formatQty(c.currentVolumeL)}</td>
                <td className="px-4 py-3 text-right text-fg-muted">
                  {c.currentAbvPctSet ? c.currentAbvPct.toFixed(2) + "%" : "—"}
                </td>
                <td className="px-4 py-3 text-right font-medium text-fg">{formatLAA(c.currentLaa)}</td>
                <td className="px-4 py-3 text-right text-fg-muted">
                  <ActivityCell ts={c.lastMovementAt} fallback={c.createdAt} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <h2 className="mb-3 text-sm font-semibold text-fg-muted">Recent movements</h2>
      <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-3">When</th>
              <th className="px-4 py-3">Reason</th>
              <th className="px-4 py-3">From → To</th>
              <th className="px-4 py-3 text-right">Volume (L)</th>
              <th className="px-4 py-3 text-right">ABV</th>
              <th className="px-4 py-3 text-right">LAA</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {recent.data?.movements.length === 0 && (
              <tr><td colSpan={6} className="px-4 py-3 text-fg-muted">No movements yet.</td></tr>
            )}
            {recent.data?.movements.map((m) => (
              <tr key={m.id}>
                <td className="px-4 py-3 text-fg-muted">
                  {m.occurredAt ? new Date(Number(m.occurredAt.seconds) * 1000).toLocaleString() : ""}
                </td>
                <td className="px-4 py-3 text-fg-muted">{bulkMovementReasonLabel(m.reason)}</td>
                <td className="px-4 py-3 text-fg-muted">
                  {m.sourceContainerName || "—"} → {m.destinationContainerName || "—"}
                </td>
                <td className="px-4 py-3 text-right text-fg-muted">{formatQty(m.volumeL)}</td>
                <td className="px-4 py-3 text-right text-fg-muted">{m.abvPct.toFixed(2)}%</td>
                <td className="px-4 py-3 text-right font-medium text-fg">{formatLAA(m.laa)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Shell>
  );
}

// BlendForm — N-source blend into one destination. Surfaces stage 67's
// CreateBlend RPC. Defaults to 2 sources, can add more rows.
function BlendForm({
  containers,
  onDone,
}: {
  containers: { id: string; name: string; currentVolumeL: number; currentAbvPctSet: boolean; currentAbvPct: number; archived: boolean }[];
}
  & { onDone: () => void }) {
  const active = containers.filter((c) => !c.archived);
  const [destId, setDestId] = useState("");
  const [rows, setRows] = useState<{ srcId: string; vol: string }[]>([
    { srcId: "", vol: "" },
    { srcId: "", vol: "" },
  ]);
  const [notes, setNotes] = useState("");
  const mut = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof CreateBlendRequestSchema>>) =>
      bulkClient.createBlend(msg),
    onSuccess: () => onDone(),
  });
  function submit(e: FormEvent) {
    e.preventDefault();
    const sources = rows
      .filter((r) => r.srcId && r.vol)
      .map((r) =>
        create(BlendSourceInputSchema, { sourceContainerId: r.srcId, volumeL: Number(r.vol) }),
      );
    mut.mutate(create(CreateBlendRequestSchema, { destinationContainerId: destId, sources, notes }));
  }
  return (
    <form
      onSubmit={submit}
      className="mb-6 rounded-lg border border-border bg-surface-2 p-5 shadow-sm"
    >
      <h3 className="mb-3 text-sm font-semibold text-fg-muted">New blend</h3>
      <div className="mb-3">
        <label className="mb-2 block text-sm font-medium text-fg-muted">Destination</label>
        <select required value={destId} onChange={(e) => setDestId(e.target.value)}
          className="w-full rounded border border-border-strong px-3 py-2 text-sm">
          <option value="">Select destination tank…</option>
          {active.map((c) => (
            <option key={c.id} value={c.id}>{c.name} ({formatQty(c.currentVolumeL)} L on hand)</option>
          ))}
        </select>
      </div>
      <div className="space-y-2">
        {rows.map((r, i) => (
          <div key={i} className="flex gap-2">
            <select
              required value={r.srcId}
              onChange={(e) => setRows(rows.map((x, j) => j === i ? { ...x, srcId: e.target.value } : x))}
              className="flex-1 rounded border border-border-strong px-3 py-2 text-sm"
            >
              <option value="">Source #{i + 1}…</option>
              {active.filter((c) => c.id !== destId && c.currentAbvPctSet && c.currentVolumeL > 0).map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name} ({formatQty(c.currentVolumeL)} L @ {c.currentAbvPct.toFixed(1)}%)
                </option>
              ))}
            </select>
            <input
              required type="number" step="0.1" min="0.1" placeholder="Vol (L)"
              value={r.vol}
              onChange={(e) => setRows(rows.map((x, j) => j === i ? { ...x, vol: e.target.value } : x))}
              className="w-32 rounded border border-border-strong px-3 py-2 text-sm"
            />
            {rows.length > 2 && (
              <button type="button" onClick={() => setRows(rows.filter((_, j) => j !== i))}
                className="text-xs text-fg-muted hover:text-danger-fg">×</button>
            )}
          </div>
        ))}
      </div>
      <button type="button" onClick={() => setRows([...rows, { srcId: "", vol: "" }])}
        className="mt-2 text-xs text-fg-muted hover:text-fg">+ Add source</button>
      <div className="mt-3">
        <label className="mb-2 block text-sm font-medium text-fg-muted">Notes</label>
        <input value={notes} onChange={(e) => setNotes(e.target.value)}
          className="w-full rounded border border-border-strong px-3 py-2 text-sm" />
      </div>
      <div className="mt-3 flex items-center gap-3">
        <button
          type="submit" disabled={mut.isPending}
          className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
        >
          {mut.isPending ? "Blending…" : "Create blend"}
        </button>
        {mut.error && (
          <span className="text-sm text-danger-fg">
            {mut.error instanceof ConnectError ? mut.error.rawMessage : String(mut.error)}
          </span>
        )}
      </div>
    </form>
  );
}

// ActivityCell renders "5 days ago" (or whatever) for the most recent
// bulk_movement touching the container. Falls back to createdAt for
// containers that have never moved alcohol.
function ActivityCell({
  ts,
  fallback,
}: {
  ts: { seconds: bigint } | undefined;
  fallback: { seconds: bigint } | undefined;
}) {
  const effective = ts ?? fallback;
  if (!effective) return <>—</>;
  const ms = Number(effective.seconds) * 1000;
  const days = Math.floor((Date.now() - ms) / 86_400_000);
  const stale = days >= 90;
  const label = days === 0 ? "today" : `${days} day${days === 1 ? "" : "s"} ago`;
  return <span className={stale ? "text-warning-fg" : ""}>{label}</span>;
}
