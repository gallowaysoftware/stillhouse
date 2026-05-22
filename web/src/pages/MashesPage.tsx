import { FormEvent, useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { Shell } from "@/components/Shell";
import { mashClient, recipeClient } from "@/lib/clients";
import { CreateMashRunRequestSchema } from "@/gen/stillhouse/v1/mash_pb";
import { mashStatusLabel } from "@/lib/format";

export function MashesPage() {
  const qc = useQueryClient();
  const mashes = useQuery({
    queryKey: ["listMashRuns"],
    queryFn: () => mashClient.listMashRuns({}),
  });
  const recipes = useQuery({
    queryKey: ["listRecipes"],
    queryFn: () => recipeClient.listRecipes({}),
  });
  const [showForm, setShowForm] = useState(false);

  const createMash = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof CreateMashRunRequestSchema>>) =>
      mashClient.createMashRun(msg),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["listMashRuns"] });
      setShowForm(false);
    },
  });

  function onSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const fd = new FormData(e.currentTarget);
    createMash.mutate(
      create(CreateMashRunRequestSchema, {
        recipeVersionId: fd.get("recipe_version_id")?.toString() ?? "",
        mashDate: fd.get("mash_date")?.toString() ?? "",
        notes: fd.get("notes")?.toString() ?? "",
      }),
    );
  }

  return (
    <Shell>
      <div className="mb-6 flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">Mashes</h1>
          <p className="text-sm text-stone-500">Mash runs against a recipe version.</p>
        </div>
        <button
          onClick={() => setShowForm((s) => !s)}
          className="rounded bg-stone-900 px-3 py-2 text-sm font-medium text-white hover:bg-stone-800"
        >
          {showForm ? "Cancel" : "New mash"}
        </button>
      </div>

      {showForm && (
        <form
          onSubmit={onSubmit}
          className="mb-6 grid grid-cols-2 gap-4 rounded-lg border border-stone-200 bg-white p-5 shadow-sm"
        >
          <div>
            <label className="mb-1 block text-xs font-medium text-stone-600">Recipe</label>
            <select
              name="recipe_version_id"
              required
              className="w-full rounded border border-stone-300 px-3 py-2 text-sm"
            >
              <option value="">Select recipe…</option>
              {recipes.data?.recipes
                .filter((r) => r.currentVersionId)
                .map((r) => (
                  <option key={r.id} value={r.currentVersionId}>
                    {r.name}
                  </option>
                ))}
            </select>
            {recipes.data &&
              recipes.data.recipes.filter((r) => r.currentVersionId).length === 0 && (
                <p className="mt-1 text-xs text-stone-500">
                  No recipe versions yet. Save a version on a recipe first.
                </p>
              )}
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-stone-600">Mash date</label>
            <input
              name="mash_date"
              type="date"
              className="w-full rounded border border-stone-300 px-3 py-2 text-sm"
            />
          </div>
          <div className="col-span-2">
            <label className="mb-1 block text-xs font-medium text-stone-600">Notes</label>
            <textarea
              name="notes"
              rows={2}
              className="w-full rounded border border-stone-300 px-3 py-2 text-sm"
            />
          </div>
          <div className="col-span-2 flex items-center gap-3">
            <button
              type="submit"
              disabled={createMash.isPending}
              className="rounded bg-stone-900 px-3 py-2 text-sm font-medium text-white hover:bg-stone-800 disabled:bg-stone-400"
            >
              {createMash.isPending ? "Creating…" : "Create mash"}
            </button>
            {createMash.error && (
              <span className="text-sm text-red-600">
                {createMash.error instanceof ConnectError
                  ? createMash.error.rawMessage
                  : String(createMash.error)}
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
              <th className="px-4 py-3">Recipe</th>
              <th className="px-4 py-3">Status</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-stone-100">
            {mashes.isLoading && (
              <tr>
                <td colSpan={4} className="px-4 py-3 text-stone-500">
                  Loading…
                </td>
              </tr>
            )}
            {!mashes.isLoading && mashes.data?.mashRuns.length === 0 && (
              <tr>
                <td colSpan={4} className="px-4 py-3 text-stone-500">
                  No mash runs yet.
                </td>
              </tr>
            )}
            {mashes.data?.mashRuns.map((m) => (
              <tr key={m.id}>
                <td className="px-4 py-3 font-medium text-stone-900">#{m.mashNo}</td>
                <td className="px-4 py-3 text-stone-600">{m.mashDate}</td>
                <td className="px-4 py-3">
                  <Link to={`/mashes/${m.id}`} className="text-stone-900 hover:underline">
                    {m.recipeName} v{m.recipeVersionNo}
                  </Link>
                </td>
                <td className="px-4 py-3 text-stone-600">{mashStatusLabel(m.status)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Shell>
  );
}
