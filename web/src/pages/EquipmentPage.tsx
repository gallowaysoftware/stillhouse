import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { EmptyRow } from "@/components/EmptyState";
import { Shell } from "@/components/Shell";
import { equipmentClient, locationClient } from "@/lib/clients";
import { EquipmentKind, EquipmentStatus } from "@/gen/stillhouse/v1/equipment_pb";
import { formatQty } from "@/lib/format";
import { OwnerOnly, WriteOnly } from "@/lib/role";

const kindLabel: Record<number, string> = {
  [EquipmentKind.UNSPECIFIED]: "—",
  [EquipmentKind.STILL]: "Still",
  [EquipmentKind.MASH_TUN]: "Mash tun",
  [EquipmentKind.FERMENTER_VESSEL]: "Fermenter",
  [EquipmentKind.FILLER]: "Filler",
  [EquipmentKind.PUMP]: "Pump",
  [EquipmentKind.CHILLER]: "Chiller",
  [EquipmentKind.BOILER]: "Boiler",
  [EquipmentKind.CONDENSER]: "Condenser",
  [EquipmentKind.BOTTLING_LINE]: "Bottling line",
  [EquipmentKind.OTHER]: "Other",
};

const statusLabel: Record<number, string> = {
  [EquipmentStatus.UNSPECIFIED]: "—",
  [EquipmentStatus.IN_SERVICE]: "In service",
  [EquipmentStatus.DOWN]: "Down",
  [EquipmentStatus.RETIRED]: "Retired",
};

function errText(e: unknown): string {
  return e instanceof ConnectError ? e.rawMessage : String(e);
}

export function EquipmentPage() {
  const qc = useQueryClient();
  const [includeRetired, setIncludeRetired] = useState(false);
  const [adding, setAdding] = useState(false);
  const [open, setOpen] = useState<string | null>(null);

  const list = useQuery({
    queryKey: ["listEquipment", includeRetired],
    queryFn: () => equipmentClient.listEquipment({ includeRetired }),
  });
  const locations = useQuery({
    queryKey: ["listLocations"],
    queryFn: () => locationClient.listLocations({}),
  });
  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["listEquipment"] });
    qc.invalidateQueries({ queryKey: ["getEquipment"] });
    qc.invalidateQueries({ queryKey: ["listAlerts"] });
  };
  const save = useMutation({
    mutationFn: (m: Parameters<typeof equipmentClient.saveEquipment>[0]) =>
      equipmentClient.saveEquipment(m),
    onSuccess: () => { setAdding(false); invalidate(); },
  });

  const d = list.data;

  return (
    <Shell>
      <div className="mb-6">
        <h1 className="text-3xl font-bold tracking-tight">Equipment</h1>
        <p className="text-sm text-fg-muted">
          The stills, tuns, fillers and pumps — the plant a run is performed
          <em> on</em>, as against the vessels it goes <em>into</em>. Where a
          capacity or a service interval has not been recorded it is left
          blank, and anything that would depend on it says so rather than
          assuming a number.
        </p>
      </div>

      {d && (
        <section className="mb-6 grid grid-cols-2 gap-4 sm:grid-cols-4">
          <Stat label="In service" value={String(d.inService)} />
          <Stat label="Down" value={String(d.down)} tone={d.down > 0 ? "danger" : undefined} />
          <Stat label="Service due" value={String(d.serviceDue)} tone={d.serviceDue > 0 ? "warning" : undefined} />
          <Stat label="Capacity unknown" value={String(d.capacityUnknown)} />
        </section>
      )}

      <div className="mb-4 flex flex-wrap items-center gap-3">
        <label className="flex items-center gap-2 text-sm text-fg-muted">
          <input type="checkbox" checked={includeRetired}
                 onChange={(e) => setIncludeRetired(e.target.checked)} />
          Include retired
        </label>
        <OwnerOnly>
          <button
            onClick={() => setAdding((v) => !v)}
            className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover"
          >
            {adding ? "Cancel" : "Add equipment"}
          </button>
        </OwnerOnly>
      </div>

      {adding && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            const fd = new FormData(e.currentTarget);
            const num = (k: string) => Number(fd.get(k) ?? 0) || 0;
            const has = (k: string) => String(fd.get(k) ?? "").trim() !== "";
            save.mutate({
              name: fd.get("name")?.toString() ?? "",
              kind: Number(fd.get("kind") ?? 0) as EquipmentKind,
              status: EquipmentStatus.IN_SERVICE,
              locationId: fd.get("location")?.toString() ?? "",
              manufacturer: fd.get("manufacturer")?.toString() ?? "",
              model: fd.get("model")?.toString() ?? "",
              serialNo: fd.get("serial")?.toString() ?? "",
              commissionedOn: fd.get("commissioned")?.toString() ?? "",
              capacityL: num("capacity"),
              capacityLSet: has("capacity"),
              typicalRunHours: num("run_hours"),
              typicalRunHoursSet: has("run_hours"),
              serviceIntervalDays: num("interval_days"),
              serviceIntervalDaysSet: has("interval_days"),
              notes: fd.get("notes")?.toString() ?? "",
            });
          }}
          className="mb-6 grid gap-3 rounded-lg border border-border bg-surface-2 p-5 sm:grid-cols-3"
        >
          <F label="Name" name="name" required />
          <div>
            <label className="mb-1 block text-xs text-fg-muted">Kind</label>
            <select name="kind" className="w-full rounded border border-border-strong px-2 py-1.5 text-sm">
              {Object.entries(kindLabel)
                .filter(([k]) => Number(k) !== EquipmentKind.UNSPECIFIED)
                .map(([k, label]) => (
                  <option key={k} value={k}>{label}</option>
                ))}
            </select>
          </div>
          <div>
            <label className="mb-1 block text-xs text-fg-muted">Where</label>
            <select name="location" className="w-full rounded border border-border-strong px-2 py-1.5 text-sm">
              <option value="">— none —</option>
              {locations.data?.locations.map((l) => (
                <option key={l.id} value={l.id}>{l.name}</option>
              ))}
            </select>
          </div>
          <F label="Manufacturer" name="manufacturer" />
          <F label="Model" name="model" />
          <F label="Serial number" name="serial" />
          <F label="Commissioned" name="commissioned" type="date" />
          <F label="Capacity (L) — blank if unknown" name="capacity" type="number" step="0.1" />
          <F label="Typical run (hours) — blank if unknown" name="run_hours" type="number" step="0.25" />
          <F label="Service every (days) — blank for none" name="interval_days" type="number" />
          <F label="Notes" name="notes" className="sm:col-span-2" />
          <div className="sm:col-span-3">
            <button type="submit" disabled={save.isPending}
                    className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50">
              Save
            </button>
            {save.error && <span className="ml-3 text-sm text-danger-fg">{errText(save.error)}</span>}
          </div>
        </form>
      )}

      <div className="overflow-hidden rounded-lg border border-border bg-surface-2">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-2">Name</th>
              <th className="px-4 py-2">Kind</th>
              <th className="px-4 py-2">Where</th>
              <th className="px-4 py-2 text-right">Capacity</th>
              <th className="px-4 py-2">Status</th>
              <th className="px-4 py-2">Last serviced</th>
              <th className="px-4 py-2"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {d?.equipment.length === 0 && (
              <EmptyRow
                colSpan={7}
                title="Nothing in the register"
                message="Add the still first. Everything that schedules or costs a run needs to know what it ran on."
              />
            )}
            {d?.equipment.map((e) => (
              <tr key={e.id} className={e.status === EquipmentStatus.DOWN ? "bg-danger-bg" : undefined}>
                <td className="px-4 py-2 font-medium text-fg">
                  {e.name}
                  {e.model && <span className="ml-2 text-xs text-fg-subtle">{e.manufacturer} {e.model}</span>}
                </td>
                <td className="px-4 py-2 text-fg-muted">{kindLabel[e.kind]}</td>
                <td className="px-4 py-2 text-fg-muted">{e.locationName || "—"}</td>
                <td className="px-4 py-2 text-right text-fg-muted">
                  {/* Blank, not zero: nothing has been recorded, and a
                      scheduler must not plan against a number nobody set. */}
                  {e.capacityLSet ? `${formatQty(e.capacityL)} L` : <span className="text-fg-subtle">not recorded</span>}
                </td>
                <td className="px-4 py-2 text-fg-muted">{statusLabel[e.status]}</td>
                <td className="px-4 py-2 text-fg-muted">
                  {e.lastServicedOn || <span className="text-fg-subtle">never</span>}
                  {e.serviceDue && (
                    <span className="ml-2 text-xs text-warning-fg">
                      due ({e.daysSinceService} d)
                    </span>
                  )}
                </td>
                <td className="px-4 py-2 text-right">
                  <button
                    onClick={() => setOpen(open === e.id ? null : e.id)}
                    className="text-xs text-fg-muted hover:text-fg"
                  >
                    {open === e.id ? "Close" : "Open"}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {open && <EquipmentDetail id={open} onChanged={invalidate} />}
    </Shell>
  );
}

function EquipmentDetail({ id, onChanged }: { id: string; onChanged: () => void }) {
  const detail = useQuery({
    queryKey: ["getEquipment", id],
    queryFn: () => equipmentClient.getEquipment({ id }),
  });
  const record = useMutation({
    mutationFn: (m: Parameters<typeof equipmentClient.recordService>[0]) =>
      equipmentClient.recordService(m),
    onSuccess: () => { detail.refetch(); onChanged(); },
  });

  const e = detail.data?.equipment;
  if (!e) return null;

  return (
    <div className="mt-4 space-y-4 rounded-lg border border-border bg-surface-2 p-5">
      <div>
        <h2 className="text-sm font-semibold text-fg">{e.name}</h2>
        <p className="text-xs text-fg-muted">
          {kindLabel[e.kind]}
          {e.serialNo && <> · serial {e.serialNo}</>}
          {e.commissionedOn && <> · commissioned {e.commissionedOn}</>}
          {e.runCount > 0 && <> · {e.runCount} distillation run{e.runCount === 1 ? "" : "s"}</>}
        </p>
      </div>

      <p className="text-xs text-fg-subtle">
        {detail.data && detail.data.observedRuns > 0 ? (
          <>
            Runs on this have taken a median of{" "}
            <span className="font-medium text-fg">
              {detail.data.observedMedianHours.toFixed(1)} h
            </span>{" "}
            across {detail.data.observedRuns} work order
            {detail.data.observedRuns === 1 ? "" : "s"} that recorded a start and
            a finish — which is a better basis for a plan than a figure typed once.
            {e.typicalRunHoursSet && <> The recorded typical run is {e.typicalRunHours} h.</>}
          </>
        ) : (
          <>
            No work order on this has recorded both a start and a finish yet, so
            there is nothing observed to plan from.
            {e.typicalRunHoursSet
              ? ` The recorded typical run is ${e.typicalRunHours} h.`
              : " No typical run time is recorded either."}
          </>
        )}
      </p>

      {detail.data && detail.data.services.length > 0 && (
        <div>
          <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-fg-muted">
            Service history
          </h3>
          <ul className="space-y-1 text-sm text-fg-muted">
            {detail.data.services.map((s) => (
              <li key={s.id}>
                {s.performedOn} · {s.description}
                {s.performedBy && <> · {s.performedBy}</>}
                {s.costCad && <> · ${s.costCad}</>}
              </li>
            ))}
          </ul>
        </div>
      )}

      <WriteOnly>
        <form
          onSubmit={(ev) => {
            ev.preventDefault();
            const fd = new FormData(ev.currentTarget);
            record.mutate({
              equipmentId: id,
              performedOn: fd.get("performed_on")?.toString() ?? "",
              description: fd.get("description")?.toString() ?? "",
              performedBy: fd.get("performed_by")?.toString() ?? "",
              costCad: fd.get("cost")?.toString() ?? "",
            });
            ev.currentTarget.reset();
          }}
          className="grid gap-3 border-t border-border pt-4 sm:grid-cols-4"
        >
          <F label="Date" name="performed_on" type="date" />
          <F label="What was done" name="description" required className="sm:col-span-2" />
          <F label="By whom" name="performed_by" />
          <F label="Cost (CAD)" name="cost" />
          <div className="flex items-end">
            <button type="submit" disabled={record.isPending}
                    className="rounded border border-border-strong px-3 py-1.5 text-sm text-fg hover:bg-surface-3">
              Record service
            </button>
          </div>
          {record.error && (
            <p className="text-sm text-danger-fg sm:col-span-4">{errText(record.error)}</p>
          )}
        </form>
      </WriteOnly>
    </div>
  );
}

function Stat({ label, value, tone }: { label: string; value: string; tone?: "danger" | "warning" }) {
  const ring =
    tone === "danger" ? "bg-danger/10" : tone === "warning" ? "bg-warning/10" : "bg-surface-2";
  const text =
    tone === "danger" ? "text-danger-fg" : tone === "warning" ? "text-warning-fg" : "text-fg";
  return (
    <div className={`rounded-lg border border-border p-4 ${ring}`}>
      <div className="text-xs text-fg-muted">{label}</div>
      <div className={`mt-1 text-2xl font-bold tracking-tight ${text}`}>{value}</div>
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
