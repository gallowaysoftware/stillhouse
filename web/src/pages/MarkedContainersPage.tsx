import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { EmptyRow } from "@/components/EmptyState";
import { Shell } from "@/components/Shell";
import { bulkClient, customerClient, markedClient, productClient } from "@/lib/clients";
import { MarkedContainerStatus } from "@/gen/stillhouse/v1/marked_pb";
import { formatCAD, formatLAA, formatQty } from "@/lib/format";
import { WriteOnly } from "@/lib/role";

const statusLabel: Record<number, string> = {
  [MarkedContainerStatus.UNSPECIFIED]: "—",
  [MarkedContainerStatus.MARKED]: "On the premises",
  [MarkedContainerStatus.DELIVERED]: "Delivered",
  [MarkedContainerStatus.UNMARKED]: "Unmarked (s.156)",
  [MarkedContainerStatus.DESTROYED]: "Destroyed",
};

function errText(e: unknown): string {
  return e instanceof ConnectError ? e.rawMessage : String(e);
}

export function MarkedContainersPage() {
  const qc = useQueryClient();
  const [onHandOnly, setOnHandOnly] = useState(true);
  const [filling, setFilling] = useState(false);

  const containers = useQuery({
    queryKey: ["listMarkedContainers", onHandOnly],
    queryFn: () => markedClient.listMarkedContainers({ onHandOnly }),
  });
  const deliveries = useQuery({
    queryKey: ["listMarkedDeliveries"],
    queryFn: () => markedClient.listMarkedDeliveries({}),
  });
  const vessels = useQuery({
    queryKey: ["listBulkContainers"],
    queryFn: () => bulkClient.listBulkContainers({}),
  });
  const products = useQuery({
    queryKey: ["listProducts"],
    queryFn: () => productClient.listProducts({}),
  });
  const customers = useQuery({
    queryKey: ["listCustomers"],
    queryFn: () => customerClient.listCustomers({}),
  });

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["listMarkedContainers"] });
    qc.invalidateQueries({ queryKey: ["listMarkedDeliveries"] });
    qc.invalidateQueries({ queryKey: ["listBulkContainers"] });
    qc.invalidateQueries({ queryKey: ["b266"] });
  };
  const mark = useMutation({
    mutationFn: (m: Parameters<typeof markedClient.markContainer>[0]) =>
      markedClient.markContainer(m),
    onSuccess: () => { setFilling(false); invalidate(); },
  });
  const deliver = useMutation({
    mutationFn: (m: Parameters<typeof markedClient.deliverMarkedContainer>[0]) =>
      markedClient.deliverMarkedContainer(m),
    onSuccess: invalidate,
  });
  const unmark = useMutation({
    mutationFn: (m: Parameters<typeof markedClient.unmarkContainer>[0]) =>
      markedClient.unmarkContainer(m),
    onSuccess: invalidate,
  });

  const list = containers.data;

  return (
    <Shell>
      <div className="mb-6">
        <h1 className="text-3xl font-bold tracking-tight">Marked special containers</h1>
        <p className="text-sm text-fg-muted">
          Containers of 100 to 1,500 litres, marked rather than stamped, for
          delivery to a registered user or to bottle-your-own premises
          (EDM3-8-1). They are packaging — the alcohol has left bulk — and they
          have their own line on the B266. What the mark has to say is
          EDM3-8-1's to specify; Stillhouse records what you applied.
        </p>
      </div>

      {list && (
        <section className="mb-6 grid grid-cols-2 gap-4 sm:grid-cols-3">
          <Stat label="On the premises" value={String(list.onHandCount)} />
          <Stat label="LAA in them" value={`${formatLAA(list.onHandLaa)} L`} highlight />
          <Stat label="Delivered" value={String(deliveries.data?.deliveries.length ?? 0)} />
        </section>
      )}

      <div className="mb-4 flex flex-wrap items-center gap-3">
        <label className="flex items-center gap-2 text-sm text-fg-muted">
          <input type="checkbox" checked={onHandOnly} onChange={(e) => setOnHandOnly(e.target.checked)} />
          Only what's still here
        </label>
        <WriteOnly>
          <button
            onClick={() => setFilling((v) => !v)}
            className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover"
          >
            {filling ? "Cancel" : "Fill and mark one"}
          </button>
        </WriteOnly>
      </div>

      {filling && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            const fd = new FormData(e.currentTarget);
            const num = (k: string) => Number(fd.get(k) ?? 0) || 0;
            mark.mutate({
              mark: fd.get("mark")?.toString() ?? "",
              capacityL: num("capacity"),
              sourceContainerId: fd.get("source")?.toString() ?? "",
              productId: fd.get("product")?.toString() ?? "",
              description: fd.get("description")?.toString() ?? "",
              volumeL: num("volume"),
              abvPct: num("abv"),
              densityKgM3: num("density"),
              densityKgM3Set: Boolean(fd.get("density")),
              temperatureC: num("temperature"),
              temperatureCSet: Boolean(fd.get("temperature")),
              filledOn: fd.get("filled_on")?.toString() ?? "",
              notes: fd.get("notes")?.toString() ?? "",
            });
          }}
          className="mb-6 grid gap-3 rounded-lg border border-border bg-surface-2 p-5 sm:grid-cols-3"
        >
          <F label="The mark applied" name="mark" required className="sm:col-span-2" />
          <F label="Capacity (L), 100–1500" name="capacity" type="number" step="0.1" required />
          <div>
            <label className="mb-1 block text-xs text-fg-muted">Drawn from</label>
            <select name="source" required className="w-full rounded border border-border-strong px-2 py-1.5 text-sm">
              <option value="">— choose —</option>
              {vessels.data?.containers
                .filter((c) => !c.archived && c.currentLaa > 0)
                .map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name} ({formatLAA(c.currentLaa)} LAA)
                  </option>
                ))}
            </select>
          </div>
          <div>
            <label className="mb-1 block text-xs text-fg-muted">Product (optional)</label>
            <select name="product" className="w-full rounded border border-border-strong px-2 py-1.5 text-sm">
              <option value="">— none —</option>
              {products.data?.products.map((p) => (
                <option key={p.id} value={p.id}>{p.name}</option>
              ))}
            </select>
          </div>
          <F label="Filled on" name="filled_on" type="date" />
          <F label="Volume (L)" name="volume" type="number" step="0.01" required />
          <F label="Density (kg/m³)" name="density" type="number" step="0.01" />
          <F label="Temperature (°C)" name="temperature" type="number" step="0.1" />
          <F label="Or strength at 20 °C (%)" name="abv" type="number" step="0.01" />
          <F label="Description" name="description" />
          <F label="Notes" name="notes" />
          <div className="sm:col-span-3">
            <button type="submit" disabled={mark.isPending}
                    className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50">
              {mark.isPending ? "Filling…" : "Fill and mark"}
            </button>
            {mark.error && <span className="ml-3 text-sm text-danger-fg">{errText(mark.error)}</span>}
          </div>
        </form>
      )}

      {mark.data && mark.data.warnings.length > 0 && (
        <div className="mb-4 space-y-1">
          {mark.data.warnings.map((w, i) => (
            <p key={i} className="rounded border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-fg">{w}</p>
          ))}
        </div>
      )}

      <div className="overflow-hidden rounded-lg border border-border bg-surface-2">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-2">#</th>
              <th className="px-4 py-2">Mark</th>
              <th className="px-4 py-2">Contents</th>
              <th className="px-4 py-2 text-right">LAA</th>
              <th className="px-4 py-2">Filled</th>
              <th className="px-4 py-2">Status</th>
              <th className="px-4 py-2 text-right">Duty at fill</th>
              <th className="px-4 py-2"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {list?.containers.length === 0 && (
              <EmptyRow
                colSpan={8}
                title="None yet"
                message="A marked special container is 100–1,500 L, marked rather than stamped, and goes to a registered user or bottle-your-own premises."
              />
            )}
            {list?.containers.map((c) => (
              <tr key={c.id}>
                <td className="px-4 py-2 font-medium text-fg">{c.containerNo}</td>
                <td className="px-4 py-2 font-mono text-xs text-fg-muted">{c.mark}</td>
                <td className="px-4 py-2 text-fg-muted">
                  {c.productName || c.description || "—"}
                  <span className="ml-2 text-xs text-fg-subtle">
                    {formatQty(c.volumeL)} L @ {c.abvPct.toFixed(2)} % · {c.capacityL} L vessel
                  </span>
                </td>
                <td className="px-4 py-2 text-right font-medium text-fg">{formatLAA(c.laa)}</td>
                <td className="px-4 py-2 text-fg-muted">{c.filledOn}</td>
                <td className="px-4 py-2 text-fg-muted">
                  {statusLabel[c.status]}
                  {c.unmarkedReason && (
                    <div className="text-xs text-fg-subtle">{c.unmarkedReason}</div>
                  )}
                </td>
                <td className="px-4 py-2 text-right text-fg-muted">
                  {/* Absent is not zero: a container that was not a duty
                      event and one that cost nothing say different things. */}
                  {c.dutySet ? formatCAD(c.dutyAmountCad) : <span className="text-fg-subtle">at delivery</span>}
                </td>
                <td className="px-4 py-2 text-right">
                  {c.status === MarkedContainerStatus.MARKED && (
                    <WriteOnly>
                      <div className="flex justify-end gap-2">
                        <button
                          onClick={() => {
                            const who = window.prompt("Deliver to which customer? Leave blank to type a name.");
                            const match = customers.data?.customers.find(
                              (x) => x.name.toLowerCase() === (who ?? "").toLowerCase(),
                            );
                            deliver.mutate({
                              containerId: c.id,
                              customerId: match?.id ?? "",
                              destinationName: match ? "" : (who ?? ""),
                            });
                          }}
                          className="text-xs text-accent hover:underline"
                        >
                          Deliver
                        </button>
                        <button
                          onClick={() => {
                            const reason = window.prompt(
                              "Unmarking returns the contents to bulk (s.156). Why?",
                            );
                            if (!reason) return;
                            const dest = vessels.data?.containers.find((v) => !v.archived);
                            if (!dest) return;
                            unmark.mutate({
                              id: c.id,
                              destinationContainerId: dest.id,
                              reason,
                            });
                          }}
                          className="text-xs text-fg-muted hover:text-fg"
                        >
                          Unmark
                        </button>
                      </div>
                    </WriteOnly>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {(deliver.error || unmark.error) && (
        <p className="mt-2 text-sm text-danger-fg">{errText(deliver.error ?? unmark.error)}</p>
      )}

      {deliveries.data && deliveries.data.deliveries.length > 0 && (
        <section className="mt-8">
          <h2 className="mb-2 text-sm font-semibold text-fg">Deliveries</h2>
          <div className="overflow-hidden rounded-lg border border-border bg-surface-2">
            <table className="min-w-full divide-y divide-border text-sm">
              <thead className="bg-surface-3 text-left text-xs text-fg-muted">
                <tr>
                  <th className="px-4 py-2">#</th>
                  <th className="px-4 py-2">Container</th>
                  <th className="px-4 py-2">To</th>
                  <th className="px-4 py-2">Date</th>
                  <th className="px-4 py-2 text-right">LAA</th>
                  <th className="px-4 py-2 text-right">Duty</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {deliveries.data.deliveries.map((d) => (
                  <tr key={d.id}>
                    <td className="px-4 py-2 font-medium text-fg">{d.deliveryNo}</td>
                    <td className="px-4 py-2 text-fg-muted">#{d.containerNo}</td>
                    <td className="px-4 py-2 text-fg-muted">{d.customerName || d.destinationName}</td>
                    <td className="px-4 py-2 text-fg-muted">{d.deliveryDate}</td>
                    <td className="px-4 py-2 text-right text-fg">{formatLAA(d.laa)}</td>
                    <td className="px-4 py-2 text-right text-fg-muted">
                      {d.dutyAmountCad > 0 ? formatCAD(d.dutyAmountCad) : "at packaging"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      )}
    </Shell>
  );
}

function Stat({ label, value, highlight }: { label: string; value: string; highlight?: boolean }) {
  return (
    <div className={`rounded-lg border border-border p-4 ${highlight ? "bg-success/10" : "bg-surface-2"}`}>
      <div className="text-xs text-fg-muted">{label}</div>
      <div className={`mt-1 text-2xl font-bold tracking-tight ${highlight ? "text-success-fg" : "text-fg"}`}>
        {value}
      </div>
    </div>
  );
}

function F({ label, name, type = "text", step, required, className }: {
  label: string; name: string; type?: string; step?: string;
  required?: boolean; className?: string;
}) {
  return (
    <div className={className}>
      <label className="mb-1 block text-xs text-fg-muted">{label}</label>
      <input name={name} type={type} step={step} required={required}
             className="w-full rounded border border-border-strong px-2 py-1.5 text-sm" />
    </div>
  );
}
