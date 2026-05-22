import { Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";

import { materialClient, recipeClient, userClient } from "@/lib/clients";
import { Shell } from "@/components/Shell";

export function HomePage() {
  const me = useQuery({ queryKey: ["getMe"], queryFn: () => userClient.getMe({}) });
  const mats = useQuery({
    queryKey: ["listMaterials"],
    queryFn: () => materialClient.listMaterials({}),
  });
  const recs = useQuery({
    queryKey: ["listRecipes"],
    queryFn: () => recipeClient.listRecipes({}),
  });

  return (
    <Shell>
      <h1 className="mb-1 text-2xl font-semibold">Dashboard</h1>
      <p className="mb-8 text-sm text-stone-500">
        Welcome{me.data?.user ? `, ${me.data.user.displayName}` : ""}.
      </p>

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <Link
          to="/materials"
          className="rounded-lg border border-stone-200 bg-white p-5 shadow-sm hover:border-stone-400"
        >
          <p className="text-sm font-medium text-stone-500">Materials</p>
          <p className="mt-2 text-3xl font-semibold text-stone-900">
            {mats.data?.materials.length ?? "—"}
          </p>
          <p className="mt-1 text-sm text-stone-500">grain, malt, water, packaging…</p>
        </Link>
        <Link
          to="/recipes"
          className="rounded-lg border border-stone-200 bg-white p-5 shadow-sm hover:border-stone-400"
        >
          <p className="text-sm font-medium text-stone-500">Recipes</p>
          <p className="mt-2 text-3xl font-semibold text-stone-900">
            {recs.data?.recipes.length ?? "—"}
          </p>
          <p className="mt-1 text-sm text-stone-500">mash bills + projected LAA</p>
        </Link>
      </div>
    </Shell>
  );
}
