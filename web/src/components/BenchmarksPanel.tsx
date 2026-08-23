import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { Callout } from "@/components/Callout";
import { benchmarkClient } from "@/lib/clients";
import { OwnerOnly } from "@/lib/role";

/**
 * BenchmarksPanel — how this distillery compares, and the rules that keep
 * the comparison from identifying anybody.
 *
 * This is the one read in Stillhouse that crosses the tenant boundary, so
 * the panel leads with what is shared rather than burying it. Opting in
 * is off by default, only participants can read, nothing appears below
 * five contributing distilleries, and what appears is quartiles — never a
 * maximum, because a maximum is one participant's exact number.
 */
export function BenchmarksPanel() {
  const qc = useQueryClient();
  const b = useQuery({
    queryKey: ["benchmarks"],
    queryFn: () => benchmarkClient.benchmarks({}),
  });
  const optIn = useMutation({
    mutationFn: (v: boolean) => benchmarkClient.setBenchmarkOptIn({ optIn: v }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["benchmarks"] }),
  });

  const d = b.data;
  return (
    <section className="mb-8">
      <h2 className="mb-1 text-sm font-semibold text-fg-muted">Benchmarks</h2>
      <p className="mb-3 text-sm text-fg-muted">
        How your figures sit against other Canadian craft distillers using
        Stillhouse. Everything here is voluntary in both directions.
      </p>

      {d?.refused ? (
        <Callout tone="info" title="Not participating">
          <p>{d.refused}</p>
          <OwnerOnly>
            <button
              onClick={() => optIn.mutate(true)}
              disabled={optIn.isPending}
              className="mt-3 rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
            >
              {optIn.isPending ? "Saving…" : "Opt in"}
            </button>
          </OwnerOnly>
          <p className="mt-3 text-xs">{d.privacyNote}</p>
        </Callout>
      ) : d ? (
        <>
          <Callout tone="success" title={`Participating since ${d.optedInAt || "today"}`}>
            <p className="text-xs">{d.privacyNote}</p>
            <OwnerOnly>
              <button
                onClick={() => optIn.mutate(false)}
                className="mt-2 text-xs underline"
              >
                opt out
              </button>
            </OwnerOnly>
          </Callout>

          <div className="mt-3 space-y-3">
            {d.metrics.map((m) => (
              <div key={m.key} className="rounded-lg border border-border bg-surface-2 p-4">
                <div className="flex items-baseline justify-between">
                  <h3 className="text-sm font-semibold">{m.name}</h3>
                  <span className="text-xs text-fg-muted">{m.unit}</span>
                </div>
                <p className="mt-1 text-xs text-fg-muted">{m.basis}</p>

                {m.cohort?.available ? (
                  <table className="mt-3 w-full text-sm">
                    <thead className="text-left text-xs uppercase text-fg-muted">
                      <tr>
                        <th className="py-1">You</th>
                        <th className="py-1 text-right">Lower quartile</th>
                        <th className="py-1 text-right">Median</th>
                        <th className="py-1 text-right">Upper quartile</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr className="border-t border-border">
                        <td className="py-1 font-medium tabular-nums">
                          {m.youSet ? m.you.toFixed(2) : "—"}
                        </td>
                        <td className="py-1 text-right tabular-nums">{m.cohort.p25.toFixed(2)}</td>
                        <td className="py-1 text-right tabular-nums">{m.cohort.median.toFixed(2)}</td>
                        <td className="py-1 text-right tabular-nums">{m.cohort.p75.toFixed(2)}</td>
                      </tr>
                    </tbody>
                  </table>
                ) : (
                  <p className="mt-3 text-sm text-fg-muted">{m.cohort?.missing}</p>
                )}

                {m.cohort?.available && (
                  <p className="mt-2 text-xs text-fg-subtle">
                    {m.cohort.tenants} distilleries, {m.cohort.observations} measurements
                    {m.youSet && <> · yours from {m.yourObservations}</>}
                  </p>
                )}
              </div>
            ))}
          </div>
        </>
      ) : null}
    </section>
  );
}
