import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { EmptyRow } from "@/components/EmptyState";
import { Shell } from "@/components/Shell";
import {
  bulkClient, locationClient, productClient, recipeClient,
  userClient, workOrderClient,
} from "@/lib/clients";
import { WorkOrderKind, WorkOrderStatus } from "@/gen/stillhouse/v1/work_order_pb";
import { WriteOnly } from "@/lib/role";

const kinds: { v: WorkOrderKind; label: string }[] = [
  { v: WorkOrderKind.MASH, label: "Mash" },
  { v: WorkOrderKind.FERMENTATION, label: "Fermentation" },
  { v: WorkOrderKind.DISTILLATION, label: "Distillation" },
  { v: WorkOrderKind.BOTTLING, label: "Bottling" },
  { v: WorkOrderKind.BARREL_FILL, label: "Fill a cask" },
  { v: WorkOrderKind.BARREL_DUMP, label: "Dump a cask" },
  { v: WorkOrderKind.REGAUGE, label: "Regauge" },
  { v: WorkOrderKind.CLEANING, label: "Cleaning" },
  { v: WorkOrderKind.MAINTENANCE, label: "Maintenance" },
  { v: WorkOrderKind.OTHER, label: "Other" },
];
const kindLabel = (k: WorkOrderKind) => kinds.find((x) => x.v === k)?.label ?? "—";

const statusLabel: Record<number, string> = {
  [WorkOrderStatus.PLANNED]: "Planned",
  [WorkOrderStatus.IN_PROGRESS]: "In progress",
  [WorkOrderStatus.DONE]: "Done",
  [WorkOrderStatus.CANCELLED]: "Cancelled",
};

function isOverdue(dueOn: string, status: WorkOrderStatus): boolean {
  if (!dueOn) return false;
  if (status === WorkOrderStatus.DONE || status === WorkOrderStatus.CANCELLED) return false;
  return new Date(dueOn) < new Date(new Date().toDateString());
}

export function WorkOrdersPage() {
  const qc = useQueryClient();
  const [mineOnly, setMineOnly] = useState(false);
  const [openOnly, setOpenOnly] = useState(true);
  const [showForm, setShowForm] = useState(false);

  const list = useQuery({
    queryKey: ["listWorkOrders", mineOnly, openOnly],
    queryFn: () => workOrderClient.listWorkOrders({
      openOnly, assignedTo: mineOnly ? "me" : "",
    }),
  });
  const users = useQuery({ queryKey: ["listUsers"], queryFn: () => userClient.listUsers({}) });
  const locations = useQuery({ queryKey: ["listLocations"], queryFn: () => locationClient.listLocations({}) });
  const containers = useQuery({ queryKey: ["listBulkContainers"], queryFn: () => bulkClient.listBulkContainers({}) });
  const products = useQuery({ queryKey: ["listProducts"], queryFn: () => productClient.listProducts({}) });
  const recipes = useQuery({ queryKey: ["listRecipes"], queryFn: () => recipeClient.listRecipes({}) });

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["listWorkOrders"] });
    qc.invalidateQueries({ queryKey: ["listAlerts"] });
  };
  const save = useMutation({
    mutationFn: (m: Parameters<typeof workOrderClient.saveWorkOrder>[0]) =>
      workOrderClient.saveWorkOrder(m),
    onSuccess: () => { setShowForm(false); invalidate(); },
  });
  const setStatus = useMutation({
    mutationFn: (m: Parameters<typeof workOrderClient.setWorkOrderStatus>[0]) =>
      workOrderClient.setWorkOrderStatus(m),
    onSuccess: invalidate,
  });

  return (
    <Shell>
      <div className="mb-6 flex items-start justify-between">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">Work</h1>
          <p className="text-sm text-fg-muted">
            What's planned, who has it, and when it's due. Deliberately thin — a work
            order points at what it produced rather than holding its own copy of the
            numbers, so there's one place a batch is recorded and it isn't here.
          </p>
        </div>
        <WriteOnly>
          <button
            onClick={() => setShowForm((v) => !v)}
            className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover"
          >
            {showForm ? "Cancel" : "New job"}
          </button>
        </WriteOnly>
      </div>

      <div className="mb-4 flex flex-wrap gap-4 text-sm text-fg-muted">
        <label className="flex items-center gap-2">
          <input type="checkbox" checked={mineOnly} onChange={(e) => setMineOnly(e.target.checked)} />
          Only mine
        </label>
        <label className="flex items-center gap-2">
          <input type="checkbox" checked={openOnly} onChange={(e) => setOpenOnly(e.target.checked)} />
          Only what's open
        </label>
      </div>

      {showForm && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            const fd = new FormData(e.currentTarget);
            const subject = fd.get("subject")?.toString() ?? "";
            const [kindOfSubject, subjectId] = subject ? subject.split(":") : ["", ""];
            save.mutate({
              kind: Number(fd.get("kind")) as WorkOrderKind,
              title: fd.get("title")?.toString() ?? "",
              detail: fd.get("detail")?.toString() ?? "",
              assignedTo: fd.get("assigned_to")?.toString() ?? "",
              locationId: fd.get("location_id")?.toString() ?? "",
              scheduledFor: fd.get("scheduled_for")?.toString() ?? "",
              dueOn: fd.get("due_on")?.toString() ?? "",
              containerId: kindOfSubject === "container" ? subjectId : "",
              productId: kindOfSubject === "product" ? subjectId : "",
              recipeId: kindOfSubject === "recipe" ? subjectId : "",
            });
          }}
          className="mb-6 grid gap-3 rounded-lg border border-border bg-surface-2 p-5 sm:grid-cols-3"
        >
          <WField label="What needs doing" name="title" required className="sm:col-span-2" />
          <div>
            <label className="mb-1 block text-xs text-fg-muted">Kind</label>
            <select name="kind" defaultValue={String(WorkOrderKind.OTHER)}
                    className="w-full rounded border border-border-strong px-2 py-1.5 text-sm">
              {kinds.map((k) => <option key={k.v} value={k.v}>{k.label}</option>)}
            </select>
          </div>
          <div>
            <label className="mb-1 block text-xs text-fg-muted">Who</label>
            <select name="assigned_to" className="w-full rounded border border-border-strong px-2 py-1.5 text-sm">
              <option value="">— nobody yet —</option>
              {users.data?.users.map((u) => (
                <option key={u.id} value={u.id}>{u.displayName}</option>
              ))}
            </select>
          </div>
          <WField label="Scheduled for" name="scheduled_for" type="date" />
          <WField label="Due by" name="due_on" type="date" />
          <div>
            <label className="mb-1 block text-xs text-fg-muted">Where</label>
            <select name="location_id" className="w-full rounded border border-border-strong px-2 py-1.5 text-sm">
              <option value="">— any —</option>
              {locations.data?.locations.map((l) => (
                <option key={l.id} value={l.id}>{l.name}</option>
              ))}
            </select>
          </div>
          <div className="sm:col-span-2">
            <label className="mb-1 block text-xs text-fg-muted">
              About (one thing, or nothing)
            </label>
            <select name="subject" className="w-full rounded border border-border-strong px-2 py-1.5 text-sm">
              <option value="">— nothing in particular —</option>
              <optgroup label="Cask or tank">
                {containers.data?.containers.map((c) => (
                  <option key={c.id} value={`container:${c.id}`}>{c.name}</option>
                ))}
              </optgroup>
              <optgroup label="Product">
                {products.data?.products.map((p) => (
                  <option key={p.id} value={`product:${p.id}`}>{p.name}</option>
                ))}
              </optgroup>
              <optgroup label="Recipe">
                {recipes.data?.recipes.map((r) => (
                  <option key={r.id} value={`recipe:${r.id}`}>{r.name}</option>
                ))}
              </optgroup>
            </select>
          </div>
          <WField label="Detail" name="detail" className="sm:col-span-3" />
          <div className="sm:col-span-3 flex items-center gap-3">
            <button type="submit" disabled={save.isPending}
                    className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50">
              {save.isPending ? "Saving…" : "Add to the board"}
            </button>
            {save.error && (
              <span className="text-sm text-danger-fg">
                {save.error instanceof ConnectError ? save.error.rawMessage : String(save.error)}
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
              <th className="px-4 py-3">What</th>
              <th className="px-4 py-3">Kind</th>
              <th className="px-4 py-3">Who</th>
              <th className="px-4 py-3">When</th>
              <th className="px-4 py-3">Status</th>
              <th className="px-4 py-3 text-right"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {list.data?.workOrders.length === 0 && (
              <EmptyRow
                colSpan={7}
                title="Nothing on the board"
                message="What's planned, who has it, and when it's due — the thing a second person needs before the system is usable by a team rather than an owner."
              />
            )}
            {list.data?.workOrders.map((w) => {
              const late = isOverdue(w.dueOn, w.status);
              const subject = w.containerName || w.productName || w.recipeName;
              return (
                <tr key={w.id} className={late ? "bg-danger/5" : ""}>
                  <td className="px-4 py-3 text-fg-muted">{w.workOrderNo}</td>
                  <td className="px-4 py-3">
                    <span className="font-medium text-fg">{w.title}</span>
                    {subject && <span className="ml-2 text-xs text-fg-subtle">{subject}</span>}
                    {w.detail && <p className="text-xs text-fg-muted">{w.detail}</p>}
                  </td>
                  <td className="px-4 py-3 text-fg-muted">{kindLabel(w.kind)}</td>
                  <td className="px-4 py-3 text-fg-muted">
                    {w.assignedToName || <span className="text-fg-subtle">unassigned</span>}
                  </td>
                  <td className="px-4 py-3 text-fg-muted">
                    {w.scheduledFor || "—"}
                    {w.dueOn && (
                      <span className={late ? "ml-2 text-danger-fg" : "ml-2 text-fg-subtle"}>
                        due {w.dueOn}
                      </span>
                    )}
                  </td>
                  <td className="px-4 py-3 text-fg-muted">{statusLabel[w.status]}</td>
                  <td className="px-4 py-3 text-right">
                    <WriteOnly>
                      {w.status === WorkOrderStatus.PLANNED && (
                        <button
                          onClick={() => setStatus.mutate({ id: w.id, status: WorkOrderStatus.IN_PROGRESS })}
                          className="text-xs text-accent hover:underline"
                        >
                          Start
                        </button>
                      )}
                      {w.status === WorkOrderStatus.IN_PROGRESS && (
                        <button
                          onClick={() => setStatus.mutate({ id: w.id, status: WorkOrderStatus.DONE })}
                          className="text-xs text-accent hover:underline"
                        >
                          Done
                        </button>
                      )}
                      {(w.status === WorkOrderStatus.PLANNED ||
                        w.status === WorkOrderStatus.IN_PROGRESS) && (
                        <button
                          onClick={() => {
                            const reason = window.prompt("Why is it being cancelled?");
                            if (reason) {
                              setStatus.mutate({
                                id: w.id, status: WorkOrderStatus.CANCELLED, cancelReason: reason,
                              });
                            }
                          }}
                          className="ml-3 text-xs text-fg-muted hover:text-danger-fg"
                        >
                          Cancel
                        </button>
                      )}
                    </WriteOnly>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </Shell>
  );
}

function WField({ label, name, type = "text", required, className }: {
  label: string; name: string; type?: string; required?: boolean; className?: string;
}) {
  return (
    <div className={className}>
      <label className="mb-1 block text-xs text-fg-muted">{label}</label>
      <input name={name} type={type} required={required}
             className="w-full rounded border border-border-strong px-2 py-1.5 text-sm" />
    </div>
  );
}
