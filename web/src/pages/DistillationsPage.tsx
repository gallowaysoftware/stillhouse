import { FormEvent, useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { Shell } from "@/components/Shell";
import { distillationClient } from "@/lib/clients";
import { CreateDistillationRunRequestSchema } from "@/gen/stillhouse/v1/distillation_pb";
import { distillationStatusLabel } from "@/lib/format";

export function DistillationsPage() {
  const qc = useQueryClient();
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
        <button
          onClick={() => setShowForm((s) => !s)}
          className="rounded bg-stone-900 px-3 py-2 text-sm font-medium text-white hover:bg-stone-800"
        >
          {showForm ? "Cancel" : "New run"}
        </button>
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
            </tr>
          </thead>
          <tbody className="divide-y divide-stone-100">
            {list.isLoading && (
              <tr><td colSpan={4} className="px-4 py-3 text-stone-500">Loading…</td></tr>
            )}
            {!list.isLoading && list.data?.runs.length === 0 && (
              <tr><td colSpan={4} className="px-4 py-3 text-stone-500">No runs yet.</td></tr>
            )}
            {list.data?.runs.map((r) => (
              <tr key={r.id}>
                <td className="px-4 py-3 font-medium text-stone-900">
                  <Link to={`/distillations/${r.id}`} className="hover:underline">#{r.runNo}</Link>
                </td>
                <td className="px-4 py-3 text-stone-600">{r.runDate}</td>
                <td className="px-4 py-3 text-stone-600">{r.stillLabel || "—"}</td>
                <td className="px-4 py-3 text-stone-600">{distillationStatusLabel(r.status)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Shell>
  );
}
