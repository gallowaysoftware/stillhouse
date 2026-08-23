import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { costingClient } from "@/lib/clients";
import { LabourSubject } from "@/gen/stillhouse/v1/costing_pb";
import { WriteOnly } from "@/lib/role";

function errText(e: unknown): string {
  return e instanceof ConnectError ? e.rawMessage : String(e);
}

// Hours worked on this batch.
//
// Recorded rather than derived from a run's start and end, because
// elapsed time is not effort: a fermentation runs for five days and
// nobody is standing over it. Without hours, labour and any overhead
// absorbing per hour stay unavailable — which the cost screen says
// outright rather than quietly absorbing nothing.
export function LabourPanel({ subject, what }: { subject: LabourSubject; what: string }) {
  const qc = useQueryClient();
  const key = JSON.stringify(subject);
  const list = useQuery({
    queryKey: ["listLabour", key],
    queryFn: () => costingClient.listLabour({ subject }),
  });
  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["listLabour", key] });
    qc.invalidateQueries({ queryKey: ["bottlingRunFullCost"] });
    qc.invalidateQueries({ queryKey: ["inventoryValue"] });
  };
  const record = useMutation({
    mutationFn: (m: Parameters<typeof costingClient.recordLabour>[0]) =>
      costingClient.recordLabour(m),
    onSuccess: invalidate,
  });
  const remove = useMutation({
    mutationFn: (m: Parameters<typeof costingClient.deleteLabourEntry>[0]) =>
      costingClient.deleteLabourEntry(m),
    onSuccess: invalidate,
  });

  return (
    <section className="rounded-lg border border-border bg-surface-2 p-5">
      <h2 className="mb-1 text-sm font-semibold text-fg">Hours on this {what}</h2>
      <p className="mb-3 text-xs text-fg-subtle">
        {list.data && list.data.totalHours > 0
          ? `${list.data.totalHours.toFixed(2)} h recorded.`
          : "None recorded, so labour and any per-hour overhead stay out of this batch's cost."}
      </p>

      {list.data && list.data.entries.length > 0 && (
        <ul className="mb-3 space-y-1 text-sm">
          {list.data.entries.map((e) => (
            <li key={e.id} className="flex items-center justify-between gap-3">
              <span className="text-fg-muted">
                {e.workedOn} · {e.hours.toFixed(2)} h
                {e.workedByName && <> · {e.workedByName}</>}
                {e.rateCadPerHour && (
                  <span className="ml-1 text-xs text-fg-subtle">at ${e.rateCadPerHour}/h</span>
                )}
                {e.notes && <span className="ml-1 text-xs text-fg-subtle">— {e.notes}</span>}
              </span>
              <WriteOnly>
                <button
                  onClick={() => remove.mutate({ id: e.id })}
                  className="text-xs text-fg-muted hover:text-danger-fg"
                >
                  Remove
                </button>
              </WriteOnly>
            </li>
          ))}
        </ul>
      )}

      <WriteOnly>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            const fd = new FormData(e.currentTarget);
            record.mutate({
              subject,
              workedOn: fd.get("worked_on")?.toString() ?? "",
              hours: Number(fd.get("hours") ?? 0) || 0,
              workedByName: fd.get("worked_by_name")?.toString() ?? "",
              rateCadPerHour: fd.get("rate")?.toString() ?? "",
              notes: fd.get("notes")?.toString() ?? "",
            });
            e.currentTarget.reset();
          }}
          className="grid gap-3 border-t border-border pt-3 sm:grid-cols-5"
        >
          <F label="Date" name="worked_on" type="date" />
          <F label="Hours" name="hours" type="number" step="0.25" required />
          <F label="Who" name="worked_by_name" />
          <F label="Rate override" name="rate" placeholder="blank = standard" />
          <div className="flex items-end">
            <button
              type="submit"
              disabled={record.isPending}
              className="rounded border border-border-strong px-3 py-1.5 text-sm text-fg hover:bg-surface-3"
            >
              Add
            </button>
          </div>
          <F label="Notes" name="notes" className="sm:col-span-5" />
          {record.error && (
            <p className="text-sm text-danger-fg sm:col-span-5">{errText(record.error)}</p>
          )}
        </form>
      </WriteOnly>
    </section>
  );
}

function F({ label, name, type = "text", step, placeholder, required, className }: {
  label: string; name: string; type?: string; step?: string;
  placeholder?: string; required?: boolean; className?: string;
}) {
  return (
    <div className={className}>
      <label className="mb-1 block text-xs text-fg-muted">{label}</label>
      <input name={name} type={type} step={step} placeholder={placeholder} required={required}
             className="w-full rounded border border-border-strong px-2 py-1.5 text-sm" />
    </div>
  );
}
