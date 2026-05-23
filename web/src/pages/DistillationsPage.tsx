import { FormEvent, useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { Shell } from "@/components/Shell";
import { distillationClient } from "@/lib/clients";
import { CreateDistillationRunRequestSchema, VoidDistillationRunRequestSchema } from "@/gen/stillhouse/v1/distillation_pb";
import { distillationStatusLabel } from "@/lib/format";
import { WriteOnly, canWrite, useCurrentRole } from "@/lib/role";

export function DistillationsPage() {
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
  function onVoidRun(id: string, runNo: number) {
    const reason = window.prompt(
      `Void distillation run #${runNo}? Production-gauge LAA will be refunded from the destination tank. ` +
        `Fails if downstream movements have drained the tank below the gauged volume.`,
      "recorded in error",
    );
    if (!reason || !reason.trim()) return;
    voidRun.mutate(create(VoidDistillationRunRequestSchema, { id, reason: reason.trim() }));
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
          <p className="text-sm text-stone-500">
            Distillation runs. The production gauge on a completed run is what puts new alcohol into the bulk ledger.
          </p>
        </div>
        <WriteOnly>
          <button
            onClick={() => setShowForm((s) => !s)}
            className="rounded bg-stone-900 px-3 py-2 text-sm font-medium text-white hover:bg-stone-800"
          >
            {showForm ? "Cancel" : "New run"}
          </button>
        </WriteOnly>
      </div>

      {showForm && (
        <form
          onSubmit={submit}
          className="mb-6 grid grid-cols-2 gap-4 rounded-lg border border-stone-200 bg-white p-5 shadow-sm"
        >
          <div>
            <label className="mb-1 block text-xs font-medium text-stone-600">Still label</label>
            <input name="still_label" placeholder="e.g. Pot Still #1" className="w-full rounded border border-stone-300 px-3 py-2 text-sm" />
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-stone-600">Run date</label>
            <input name="run_date" type="date" className="w-full rounded border border-stone-300 px-3 py-2 text-sm" />
          </div>
          <div className="col-span-2">
            <label className="mb-1 block text-xs font-medium text-stone-600">Notes</label>
            <textarea name="notes" rows={2} className="w-full rounded border border-stone-300 px-3 py-2 text-sm" />
          </div>
          <div className="col-span-2 flex items-center gap-3">
            <button
              type="submit"
              disabled={createRun.isPending}
              className="rounded bg-stone-900 px-3 py-2 text-sm font-medium text-white hover:bg-stone-800 disabled:bg-stone-400"
            >
              {createRun.isPending ? "Creating…" : "Create run"}
            </button>
            {createRun.error && (
              <span className="text-sm text-red-600">
                {createRun.error instanceof ConnectError
                  ? createRun.error.rawMessage
                  : String(createRun.error)}
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
              <th className="px-4 py-3">Still</th>
              <th className="px-4 py-3">Status</th>
              {writeable && <th className="px-4 py-3"></th>}
            </tr>
          </thead>
          <tbody className="divide-y divide-stone-100">
            {list.isLoading && (
              <tr><td colSpan={writeable ? 5 : 4} className="px-4 py-3 text-stone-500">Loading…</td></tr>
            )}
            {!list.isLoading && list.data?.runs.length === 0 && (
              <tr><td colSpan={writeable ? 5 : 4} className="px-4 py-3 text-stone-500">No runs yet.</td></tr>
            )}
            {list.data?.runs.map((r) => {
              const voided = !!r.voidedAt;
              return (
                <tr key={r.id} className={voided ? "bg-stone-50 text-stone-400" : ""}>
                  <td className="px-4 py-3 font-medium">
                    <Link to={`/distillations/${r.id}`} className="hover:underline">#{r.runNo}</Link>
                    {voided && (
                      <span className="ml-2 rounded bg-red-100 px-1.5 py-0.5 text-xs font-normal text-red-700">VOIDED</span>
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
