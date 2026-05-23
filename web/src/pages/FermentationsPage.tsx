import { Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";

import { Shell } from "@/components/Shell";
import { fermentationClient } from "@/lib/clients";
import { fermentationStatusLabel, formatQty } from "@/lib/format";

export function FermentationsPage() {
  const { data, isLoading } = useQuery({
    queryKey: ["listFermentationRuns", "all"],
    queryFn: () => fermentationClient.listFermentationRuns({}),
  });

  return (
    <Shell>
      <div className="mb-6">
        <h1 className="text-2xl font-semibold">Fermentations</h1>
        <p className="text-sm text-fg-muted">All fermentation runs. Create new ones from a mash detail page.</p>
      </div>

      <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs uppercase text-fg-muted">
            <tr>
              <th className="px-4 py-3">Fermenter</th>
              <th className="px-4 py-3">Mash</th>
              <th className="px-4 py-3">Recipe</th>
              <th className="px-4 py-3">Pitched</th>
              <th className="px-4 py-3 text-right">Volume (L)</th>
              <th className="px-4 py-3">Status</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {isLoading && (
              <tr>
                <td colSpan={6} className="px-4 py-3 text-fg-muted">
                  Loading…
                </td>
              </tr>
            )}
            {!isLoading && data?.runs.length === 0 && (
              <tr>
                <td colSpan={6} className="px-4 py-3 text-fg-muted">
                  No fermentation runs yet.
                </td>
              </tr>
            )}
            {data?.runs.map((f) => (
              <tr key={f.id}>
                <td className="px-4 py-3">
                  <Link to={`/fermentations/${f.id}`} className="text-fg hover:underline">
                    {f.fermenterLabel}
                  </Link>
                </td>
                <td className="px-4 py-3 text-fg-muted">#{f.mashNo} · {f.mashDate}</td>
                <td className="px-4 py-3 text-fg-muted">{f.recipeName}</td>
                <td className="px-4 py-3 text-fg-muted">
                  {f.pitchAt ? new Date(Number(f.pitchAt.seconds) * 1000).toLocaleDateString() : ""}
                </td>
                <td className="px-4 py-3 text-right text-fg-muted">
                  {f.initialVolumeLSet ? formatQty(f.initialVolumeL) : "—"}
                </td>
                <td className="px-4 py-3 text-fg-muted">{fermentationStatusLabel(f.status)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Shell>
  );
}
