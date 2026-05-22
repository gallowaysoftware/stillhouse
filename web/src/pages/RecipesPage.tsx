import { FormEvent, useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { Shell } from "@/components/Shell";
import { recipeClient } from "@/lib/clients";
import {
  CreateRecipeRequestSchema,
  DuplicateRecipeRequestSchema,
  SpiritKind,
} from "@/gen/stillhouse/v1/recipe_pb";
import { create } from "@bufbuild/protobuf";
import { spiritKindLabel } from "@/lib/format";
import { WriteOnly, canWrite, useCurrentRole } from "@/lib/role";

const spiritOptions: { value: SpiritKind; label: string }[] = [
  { value: SpiritKind.CANADIAN_WHISKY, label: "Canadian Whisky" },
  { value: SpiritKind.RYE_WHISKY, label: "Rye Whisky" },
  { value: SpiritKind.WHISKY, label: "Whisky (other)" },
  { value: SpiritKind.GIN, label: "Gin" },
  { value: SpiritKind.VODKA, label: "Vodka" },
  { value: SpiritKind.RUM, label: "Rum" },
  { value: SpiritKind.BRANDY, label: "Brandy" },
  { value: SpiritKind.LIQUEUR, label: "Liqueur" },
  { value: SpiritKind.OTHER, label: "Other" },
];

export function RecipesPage() {
  const qc = useQueryClient();
  const role = useCurrentRole();
  const writeable = canWrite(role);
  const { data, isLoading } = useQuery({
    queryKey: ["listRecipes"],
    queryFn: () => recipeClient.listRecipes({}),
  });

  const [showForm, setShowForm] = useState(false);
  const createRecipe = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof CreateRecipeRequestSchema>>) =>
      recipeClient.createRecipe(msg),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["listRecipes"] });
      setShowForm(false);
    },
  });

  const duplicateRecipe = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof DuplicateRecipeRequestSchema>>) =>
      recipeClient.duplicateRecipe(msg),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["listRecipes"] }),
  });

  function onDuplicate(sourceId: string, sourceName: string) {
    const proposed = `${sourceName} (copy)`;
    const newName = window.prompt("Name for the duplicate:", proposed);
    if (!newName || !newName.trim()) return;
    duplicateRecipe.mutate(
      create(DuplicateRecipeRequestSchema, {
        sourceRecipeId: sourceId,
        newName: newName.trim(),
      }),
    );
  }

  function onSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const fd = new FormData(e.currentTarget);
    createRecipe.mutate(
      create(CreateRecipeRequestSchema, {
        name: fd.get("name")?.toString() ?? "",
        spiritKind: Number(fd.get("spirit_kind")) as SpiritKind,
        notes: fd.get("notes")?.toString() ?? "",
      }),
    );
  }

  return (
    <Shell>
      <div className="mb-6 flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">Recipes</h1>
          <p className="text-sm text-stone-500">
            Mash bills with versioned process assumptions and projected LAA yield.
          </p>
        </div>
        <WriteOnly>
          <button
            onClick={() => setShowForm((s) => !s)}
            className="rounded bg-stone-900 px-3 py-2 text-sm font-medium text-white hover:bg-stone-800"
          >
            {showForm ? "Cancel" : "New recipe"}
          </button>
        </WriteOnly>
      </div>

      {showForm && (
        <form
          onSubmit={onSubmit}
          className="mb-6 grid grid-cols-2 gap-4 rounded-lg border border-stone-200 bg-white p-5 shadow-sm"
        >
          <div>
            <label className="mb-1 block text-xs font-medium text-stone-600">Name</label>
            <input
              name="name"
              required
              className="w-full rounded border border-stone-300 px-3 py-2 text-sm focus:border-stone-500 focus:outline-none"
            />
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-stone-600">Spirit kind</label>
            <select
              name="spirit_kind"
              required
              defaultValue={SpiritKind.CANADIAN_WHISKY}
              className="w-full rounded border border-stone-300 px-3 py-2 text-sm focus:border-stone-500 focus:outline-none"
            >
              {spiritOptions.map((s) => (
                <option key={s.value} value={s.value}>
                  {s.label}
                </option>
              ))}
            </select>
          </div>
          <div className="col-span-2">
            <label className="mb-1 block text-xs font-medium text-stone-600">Notes</label>
            <textarea
              name="notes"
              rows={2}
              className="w-full rounded border border-stone-300 px-3 py-2 text-sm focus:border-stone-500 focus:outline-none"
            />
          </div>
          <div className="col-span-2 flex items-center gap-3">
            <button
              type="submit"
              disabled={createRecipe.isPending}
              className="rounded bg-stone-900 px-3 py-2 text-sm font-medium text-white hover:bg-stone-800 disabled:bg-stone-400"
            >
              {createRecipe.isPending ? "Creating…" : "Create recipe"}
            </button>
            {createRecipe.error && (
              <span className="text-sm text-red-600">
                {createRecipe.error instanceof ConnectError
                  ? createRecipe.error.rawMessage
                  : String(createRecipe.error)}
              </span>
            )}
          </div>
        </form>
      )}

      <div className="overflow-hidden rounded-lg border border-stone-200 bg-white shadow-sm">
        <table className="min-w-full divide-y divide-stone-200 text-sm">
          <thead className="bg-stone-50 text-left text-xs uppercase text-stone-500">
            <tr>
              <th className="px-4 py-3">Name</th>
              <th className="px-4 py-3">Spirit</th>
              <th className="px-4 py-3">Status</th>
              {writeable && <th className="px-4 py-3 text-right">Actions</th>}
            </tr>
          </thead>
          <tbody className="divide-y divide-stone-100">
            {isLoading && (
              <tr>
                <td className="px-4 py-3 text-stone-500" colSpan={writeable ? 4 : 3}>
                  Loading…
                </td>
              </tr>
            )}
            {!isLoading && data?.recipes.length === 0 && (
              <tr>
                <td className="px-4 py-3 text-stone-500" colSpan={writeable ? 4 : 3}>
                  No recipes yet. Click <b>New recipe</b> to begin.
                </td>
              </tr>
            )}
            {data?.recipes.map((r) => (
              <tr key={r.id}>
                <td className="px-4 py-3">
                  <Link to={`/recipes/${r.id}`} className="font-medium text-stone-900 underline-offset-2 hover:underline">
                    {r.name}
                  </Link>
                </td>
                <td className="px-4 py-3 text-stone-600">{spiritKindLabel(r.spiritKind)}</td>
                <td className="px-4 py-3 text-stone-600">
                  {r.archived ? "Archived" : r.currentVersionId ? "Versioned" : "No version yet"}
                </td>
                {writeable && (
                  <td className="px-4 py-3 text-right">
                    <button
                      onClick={() => onDuplicate(r.id, r.name)}
                      disabled={duplicateRecipe.isPending}
                      className="text-xs text-stone-600 underline-offset-2 hover:text-stone-900 hover:underline disabled:opacity-50"
                    >
                      Duplicate
                    </button>
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Shell>
  );
}
