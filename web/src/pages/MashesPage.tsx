import { FormEvent, useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { EmptyRow } from "@/components/EmptyState";
import { Shell } from "@/components/Shell";
import { mashClient, recipeClient } from "@/lib/clients";
import { CreateMashRunRequestSchema } from "@/gen/stillhouse/v1/mash_pb";
import { mashStatusLabel } from "@/lib/format";
import { WriteOnly } from "@/lib/role";

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
          <h1 className="text-3xl font-bold tracking-tight">Mashes</h1>
          <p className="text-sm text-fg-muted">Mash runs against a recipe version.</p>
        </div>
        <WriteOnly>
          <button
            onClick={() => setShowForm((s) => !s)}
            className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover"
          >
            {showForm ? "Cancel" : "New mash"}
          </button>
        </WriteOnly>
      </div>

      {showForm && (
        <form
          onSubmit={onSubmit}
          className="mb-6 grid grid-cols-1 sm:grid-cols-2 gap-3 sm:gap-4 rounded-lg border border-border bg-surface-2 p-4 sm:p-5 shadow-sm"
        >
          <div>
            <label className="mb-2 block text-sm font-medium text-fg-muted">Recipe</label>
            <select
              name="recipe_version_id"
              required
              className="w-full rounded border border-border-strong px-3 py-2 text-sm"
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
                <p className="mt-1 text-xs text-fg-muted">
                  No recipe versions yet. Save a version on a recipe first.
                </p>
              )}
          </div>
          <div>
            <label className="mb-2 block text-sm font-medium text-fg-muted">Mash date</label>
            <input
              name="mash_date"
              type="date"
              className="w-full rounded border border-border-strong px-3 py-2 text-sm"
            />
          </div>
          <div className="col-span-2">
            <label className="mb-2 block text-sm font-medium text-fg-muted">Notes</label>
            <textarea
              name="notes"
              rows={2}
              className="w-full rounded border border-border-strong px-3 py-2 text-sm"
            />
          </div>
          <div className="col-span-2 flex items-center gap-3">
            <button
              type="submit"
              disabled={createMash.isPending}
              className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
            >
              {createMash.isPending ? "Creating…" : "Create mash"}
            </button>
            {createMash.error && (
              <span className="text-sm text-danger-fg">
                {createMash.error instanceof ConnectError
                  ? createMash.error.rawMessage
                  : String(createMash.error)}
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
              <th className="px-4 py-3">Recipe</th>
              <th className="px-4 py-3">Status</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {mashes.isLoading && (
              <tr>
                <td colSpan={4} className="px-4 py-3 text-fg-muted">
                  Loading…
                </td>
              </tr>
            )}
            {!mashes.isLoading && mashes.data?.mashRuns.length === 0 && (
              <EmptyRow
                colSpan={4}
                title="No mashes yet"
                message="A mash binds a recipe version to a date and tracks ingredients + metrics through to fermentation. Start one when you fire up the tun."
                action={
                  <WriteOnly>
                    <button
                      onClick={() => setShowForm(true)}
                      className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover"
                    >
                      New mash
                    </button>
                  </WriteOnly>
                }
              />
            )}
            {mashes.data?.mashRuns.map((m) => (
              <tr key={m.id}>
                <td className="px-4 py-3 font-medium text-fg">#{m.mashNo}</td>
                <td className="px-4 py-3 text-fg-muted">{m.mashDate}</td>
                <td className="px-4 py-3">
                  <Link to={`/mashes/${m.id}`} className="text-fg hover:underline">
                    {m.recipeName} v{m.recipeVersionNo}
                  </Link>
                </td>
                <td className="px-4 py-3 text-fg-muted">{mashStatusLabel(m.status)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Shell>
  );
}
