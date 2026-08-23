import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { EmptyRow } from "@/components/EmptyState";
import { materialClient, purchasingClient } from "@/lib/clients";
import { formatQty } from "@/lib/format";
import { OwnerOnly } from "@/lib/role";

function errText(e: unknown): string {
  return e instanceof ConnectError ? e.rawMessage : String(e);
}

// How long what is here lasts.
//
// Generalises what the excise stamp panel already does: usage a day over a
// window, divided into what is left. A material nothing has consumed has
// no rate, so its cover reads as unknown rather than as fine — it may be
// about to be used daily.
export function MaterialCoverPanel() {
  const qc = useQueryClient();
  const [windowDays, setWindowDays] = useState(90);
  const [editing, setEditing] = useState<string | null>(null);

  const cover = useQuery({
    queryKey: ["materialCover", windowDays],
    queryFn: () => materialClient.materialCover({ windowDays }),
  });
  const suppliers = useQuery({
    queryKey: ["listSuppliers"],
    queryFn: () => purchasingClient.listSuppliers({}),
  });
  const save = useMutation({
    mutationFn: (m: Parameters<typeof materialClient.setMaterialReorder>[0]) =>
      materialClient.setMaterialReorder(m),
    onSuccess: () => {
      setEditing(null);
      qc.invalidateQueries({ queryKey: ["materialCover"] });
      qc.invalidateQueries({ queryKey: ["listMaterials"] });
      qc.invalidateQueries({ queryKey: ["listAlerts"] });
    },
  });

  const d = cover.data;

  return (
    <section className="mt-10">
      <div className="mb-2 flex flex-wrap items-baseline justify-between gap-3">
        <h2 className="text-sm font-semibold text-fg">Cover</h2>
        <label className="flex items-center gap-2 text-xs text-fg-muted">
          Measured over
          <select
            value={windowDays}
            onChange={(e) => setWindowDays(Number(e.target.value))}
            className="rounded border border-border-strong px-2 py-1 text-xs"
          >
            <option value={30}>30 days</option>
            <option value={90}>90 days</option>
            <option value={180}>180 days</option>
            <option value={365}>a year</option>
          </select>
        </label>
      </div>
      {d && <p className="mb-3 text-xs text-fg-subtle">{d.basis}</p>}

      <div className="overflow-hidden rounded-lg border border-border bg-surface-2">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-2">Material</th>
              <th className="px-4 py-2 text-right">On hand</th>
              <th className="px-4 py-2 text-right">On order</th>
              <th className="px-4 py-2 text-right">Per day</th>
              <th className="px-4 py-2 text-right">Cover</th>
              <th className="px-4 py-2 text-right">Reorder at</th>
              <th className="px-4 py-2"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {d?.materials.length === 0 && (
              <EmptyRow colSpan={7} title="No materials" message="Add one to see its cover." />
            )}
            {d?.materials.map((m) => (
              <tr key={m.materialId} className={m.belowReorderPoint ? "bg-warning/10" : undefined}>
                <td className="px-4 py-2 text-fg">
                  {m.materialName}
                  {m.preferredSupplierName && (
                    <span className="ml-2 text-xs text-fg-subtle">{m.preferredSupplierName}</span>
                  )}
                </td>
                <td className="px-4 py-2 text-right text-fg-muted">
                  {formatQty(m.onHand)} {m.uom}
                </td>
                <td className="px-4 py-2 text-right text-fg-muted">
                  {m.onOrder > 0 ? formatQty(m.onOrder) : "—"}
                </td>
                <td className="px-4 py-2 text-right text-fg-muted">
                  {m.coverKnown ? formatQty(m.dailyRate) : "—"}
                </td>
                <td className="px-4 py-2 text-right">
                  {/* Unknown, not infinite. */}
                  {m.coverKnown ? (
                    <span className={m.shorterThanLeadTime ? "font-medium text-danger-fg" : "text-fg"}>
                      {Math.round(m.coverDays)} d
                      {m.shorterThanLeadTime && (
                        <span className="ml-1 text-xs">under lead time</span>
                      )}
                    </span>
                  ) : (
                    <span className="text-fg-subtle">nothing used yet</span>
                  )}
                </td>
                <td className="px-4 py-2 text-right text-fg-muted">
                  {m.reorderPointSet ? (
                    <>
                      {formatQty(m.reorderPoint)}
                      {m.belowReorderPoint && (
                        <span className="ml-1 text-xs text-warning-fg">below</span>
                      )}
                    </>
                  ) : (
                    <span className="text-fg-subtle">not set</span>
                  )}
                </td>
                <td className="px-4 py-2 text-right">
                  <OwnerOnly>
                    <button
                      onClick={() => setEditing(editing === m.materialId ? null : m.materialId)}
                      className="text-xs text-fg-muted hover:text-fg"
                    >
                      {editing === m.materialId ? "Cancel" : "Set"}
                    </button>
                  </OwnerOnly>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {editing && (
        <OwnerOnly>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              const fd = new FormData(e.currentTarget);
              const num = (k: string) => Number(fd.get(k) ?? 0) || 0;
              const has = (k: string) => String(fd.get(k) ?? "").trim() !== "";
              save.mutate({
                id: editing,
                reorderPoint: num("point"),
                reorderPointSet: has("point"),
                reorderQuantity: num("qty"),
                reorderQuantitySet: has("qty"),
                leadTimeDays: num("lead"),
                leadTimeDaysSet: has("lead"),
                preferredSupplierId: fd.get("supplier")?.toString() ?? "",
              });
            }}
            className="mt-3 grid gap-3 rounded-lg border border-border bg-surface-2 p-4 sm:grid-cols-4"
          >
            <p className="text-xs text-fg-subtle sm:col-span-4">
              Leave a field blank to clear it. There are no defaults: a threshold
              nobody chose fires at a level nobody chose, and an alert people did
              not ask for is one they learn to dismiss.
            </p>
            <F label="Reorder at" name="point" type="number" step="0.01" />
            <F label="Usual order quantity" name="qty" type="number" step="0.01" />
            <F label="Their lead time (days)" name="lead" type="number" />
            <div>
              <label className="mb-1 block text-xs text-fg-muted">Usual supplier</label>
              <select name="supplier" className="w-full rounded border border-border-strong px-2 py-1.5 text-sm">
                <option value="">— none —</option>
                {suppliers.data?.suppliers.map((s) => (
                  <option key={s.id} value={s.id}>{s.name}</option>
                ))}
              </select>
            </div>
            <div className="sm:col-span-4">
              <button type="submit" disabled={save.isPending}
                      className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50">
                Save
              </button>
              {save.error && <span className="ml-3 text-sm text-danger-fg">{errText(save.error)}</span>}
            </div>
          </form>
        </OwnerOnly>
      )}
    </section>
  );
}

function F({ label, name, type = "text", step }: {
  label: string; name: string; type?: string; step?: string;
}) {
  return (
    <div>
      <label className="mb-1 block text-xs text-fg-muted">{label}</label>
      <input name={name} type={type} step={step}
             className="w-full rounded border border-border-strong px-2 py-1.5 text-sm" />
    </div>
  );
}
