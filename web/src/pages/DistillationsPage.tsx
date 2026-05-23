import { FormEvent, useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { useConfirm } from "@/components/ConfirmDialog";
import { EmptyRow } from "@/components/EmptyState";
import { Shell } from "@/components/Shell";
import { distillationClient } from "@/lib/clients";
import { CreateDistillationRunRequestSchema, VoidDistillationRunRequestSchema } from "@/gen/stillhouse/v1/distillation_pb";
import { distillationStatusLabel } from "@/lib/format";
import { WriteOnly, canWrite, useCurrentRole } from "@/lib/role";

export function DistillationsPage() {
  const confirm = useConfirm();
  const qc = useQueryClient();
  const role = useCurrentRole();
  const writeable = canWrite(role);
  const list = useQuery({
    queryKey: ["listDistillationRuns"],
    queryFn: () => distillationClient.listDistillationRuns({}),
  });
  const [showForm, setShowForm] = useState(false);
  const createRun = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof CreateDistillationRunRequestSchema>>) =>
      distillationClient.createDistillationRun(msg),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["listDistillationRuns"] });
      setShowForm(false);
    },
  });
  const voidRun = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof VoidDistillationRunRequestSchema>>) =>
      distillationClient.voidDistillationRun(msg),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["listDistillationRuns"] });
      qc.invalidateQueries({ queryKey: ["listBulkContainers"] });
    },
  });
  async function onVoidRun(id: string, runNo: number) {
    const ok = await confirm({
      title: `Void distillation run #${runNo}?`,
      body: <>The production-gauge LAA will be refunded from the destination tank and an offsetting ledger row written.</>,
      consequences: [
        "Destination tank's running balance drops by the gauged volume + LAA",
        "B266 production line for this period drops by the same amount",
        "Fails if downstream movements have drained the tank below the gauged volume — void those first",
      ],
      requireReason: { label: "Reason", placeholder: "recorded in error" },
      confirmLabel: "Void run",
      tone: "danger",
    });
    if (!ok) return;
    voidRun.mutate(create(VoidDistillationRunRequestSchema, { id, reason: ok.reason }));
  }

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const fd = new FormData(e.currentTarget);
    createRun.mutate(
      create(CreateDistillationRunRequestSchema, {
        stillLabel: fd.get("still_label")?.toString() ?? "",
        runDate: fd.get("run_date")?.toString() ?? "",
        notes: fd.get("notes")?.toString() ?? "",
      }),
    );
  }

  return (
    <Shell>
      <div className="mb-6 flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">Distillations</h1>
          <p className="text-sm text-fg-muted">
            Distillation runs. The production gauge on a completed run is what puts new alcohol into the bulk ledger.
          </p>
        </div>
        <WriteOnly>
          <button
            onClick={() => setShowForm((s) => !s)}
            className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover"
          >
            {showForm ? "Cancel" : "New run"}
          </button>
        </WriteOnly>
      </div>

      {showForm && (
        <form
          onSubmit={submit}
          className="mb-6 grid grid-cols-2 gap-4 rounded-lg border border-border bg-surface-2 p-5 shadow-sm"
        >
          <div>
            <label className="mb-1 block text-xs font-medium text-fg-muted">Still label</label>
            <input name="still_label" placeholder="e.g. Pot Still #1" className="w-full rounded border border-border-strong px-3 py-2 text-sm" />
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-fg-muted">Run date</label>
            <input name="run_date" type="date" className="w-full rounded border border-border-strong px-3 py-2 text-sm" />
          </div>
          <div className="col-span-2">
            <label className="mb-1 block text-xs font-medium text-fg-muted">Notes</label>
            <textarea name="notes" rows={2} className="w-full rounded border border-border-strong px-3 py-2 text-sm" />
          </div>
          <div className="col-span-2 flex items-center gap-3">
            <button
              type="submit"
              disabled={createRun.isPending}
              className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
            >
              {createRun.isPending ? "Creating…" : "Create run"}
            </button>
            {createRun.error && (
              <span className="text-sm text-red-400">
                {createRun.error instanceof ConnectError
                  ? createRun.error.rawMessage
                  : String(createRun.error)}
              </span>
            )}
          </div>
        </form>
      )}

      <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs uppercase text-fg-muted">
            <tr>
              <th className="px-4 py-3">#</th>
              <th className="px-4 py-3">Date</th>
              <th className="px-4 py-3">Still</th>
              <th className="px-4 py-3">Status</th>
              {writeable && <th className="px-4 py-3"></th>}
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {list.isLoading && (
              <tr><td colSpan={writeable ? 5 : 4} className="px-4 py-3 text-fg-muted">Loading…</td></tr>
            )}
            {!list.isLoading && list.data?.runs.length === 0 && (
              <EmptyRow
                colSpan={writeable ? 5 : 4}
                title="No distillation runs yet"
                message="Each run captures a still session — its charges from fermenters, cuts (heads/hearts/tails), and the production gauge that puts new-make into a bulk container."
              />
            )}
            {list.data?.runs.map((r) => {
              const voided = !!r.voidedAt;
              return (
                <tr key={r.id} className={voided ? "bg-surface-3 text-fg-subtle" : ""}>
                  <td className="px-4 py-3 font-medium">
                    <Link to={`/distillations/${r.id}`} className="hover:underline">#{r.runNo}</Link>
                    {voided && (
                      <span className="ml-2 rounded bg-red-500/15 px-1.5 py-0.5 text-xs font-normal text-red-400">VOIDED</span>
                    )}
                  </td>
                  <td className="px-4 py-3">{r.runDate}</td>
                  <td className="px-4 py-3">
                    {r.stillLabel || "—"}
                    {voided && r.voidedReason && (
                      <div className="text-xs italic">{r.voidedReason}</div>
                    )}
                  </td>
                  <td className="px-4 py-3">{distillationStatusLabel(r.status)}</td>
                  {writeable && (
                    <td className="px-4 py-3 text-right">
                      {!voided && (
                        <button
                          onClick={() => onVoidRun(r.id, r.runNo)}
                          disabled={voidRun.isPending}
                          className="text-xs text-fg-muted hover:text-red-400 disabled:opacity-50"
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
