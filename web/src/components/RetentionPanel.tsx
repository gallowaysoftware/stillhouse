import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { retentionClient } from "@/lib/clients";
import { OwnerOnly } from "@/lib/role";

function errText(e: unknown): string {
  return e instanceof ConnectError ? e.rawMessage : String(e);
}

// What is kept, for how long, and whether anything says not to delete.
//
// Stillhouse records the policy; it does not set one. An unstated window
// reads as unstated rather than defaulting to six years, because a stated
// policy nobody stated is not one.
export function RetentionPanel() {
  const qc = useQueryClient();
  const status = useQuery({
    queryKey: ["retentionStatus"],
    queryFn: () => retentionClient.retentionStatus({}),
  });
  const invalidate = () => qc.invalidateQueries({ queryKey: ["retentionStatus"] });
  const save = useMutation({
    mutationFn: (m: Parameters<typeof retentionClient.saveRetentionPolicy>[0]) =>
      retentionClient.saveRetentionPolicy(m),
    onSuccess: invalidate,
  });
  const place = useMutation({
    mutationFn: (m: Parameters<typeof retentionClient.placeLegalHold>[0]) =>
      retentionClient.placeLegalHold(m),
    onSuccess: invalidate,
  });
  const release = useMutation({
    mutationFn: (m: Parameters<typeof retentionClient.releaseLegalHold>[0]) =>
      retentionClient.releaseLegalHold(m),
    onSuccess: invalidate,
  });

  const d = status.data;
  if (!d) return null;
  const p = d.policy;

  return (
    <section className="mt-8">
      <h2 className="mb-1 text-sm font-semibold text-fg">Records retention</h2>
      <p className="mb-3 text-xs text-fg-subtle">{d.basis}</p>

      {d.openHolds > 0 && (
        <p className="mb-3 rounded border border-danger/40 bg-danger/10 px-3 py-2 text-sm text-fg">
          {d.openHolds} legal hold{d.openHolds === 1 ? " is" : "s are"} open. Nothing
          that can normally be deleted may be, until they are lifted.
        </p>
      )}

      <div className="rounded-lg border border-border bg-surface-2 p-5">
        <dl className="grid gap-3 sm:grid-cols-3">
          <div>
            <dt className="text-xs text-fg-muted">Retention window</dt>
            <dd className="mt-0.5 text-sm">
              {p?.retentionYearsSet ? (
                <span className="font-medium text-fg">{p.retentionYears} years</span>
              ) : (
                <span className="text-warning-fg">not stated</span>
              )}
            </dd>
          </div>
          <div>
            <dt className="text-xs text-fg-muted">Last reviewed</dt>
            <dd className="mt-0.5 text-sm">
              {p?.reviewed ? (
                <span className={p.daysSinceReview > 365 ? "text-warning-fg" : "text-fg"}>
                  {p.reviewedOn} ({p.daysSinceReview} d ago)
                </span>
              ) : (
                <span className="text-warning-fg">never</span>
              )}
            </dd>
          </div>
          <div>
            <dt className="text-xs text-fg-muted">Backups</dt>
            <dd className="mt-0.5 text-sm text-fg-muted">
              {p?.backupCadence || <span className="text-warning-fg">not described</span>}
            </dd>
          </div>
        </dl>
        {p?.restoreNotes && (
          <p className="mt-3 text-sm text-fg-muted">{p.restoreNotes}</p>
        )}

        <OwnerOnly>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              const fd = new FormData(e.currentTarget);
              const years = Number(fd.get("years") ?? 0) || 0;
              save.mutate({
                retentionYears: years,
                retentionYearsSet: String(fd.get("years") ?? "").trim() !== "",
                backupCadence: fd.get("cadence")?.toString() ?? "",
                restoreNotes: fd.get("restore")?.toString() ?? "",
                reviewedOn: fd.get("reviewed")?.toString() ?? "",
                notes: fd.get("notes")?.toString() ?? "",
              });
            }}
            className="mt-4 grid gap-3 border-t border-border pt-4 sm:grid-cols-3"
          >
            <p className="text-xs text-fg-subtle sm:col-span-3">
              Your policy, in your words. Leaving the review date blank keeps the
              previous one — editing the window is not a review, and re-dating one
              silently would make the date meaningless.
            </p>
            <F label="Keep records for (years)" name="years" type="number" />
            <F label="Mark reviewed on" name="reviewed" type="date" />
            <F label="Backup cadence" name="cadence" />
            <F label="What a restore actually returns" name="restore" className="sm:col-span-2" />
            <F label="Notes" name="notes" />
            <div className="sm:col-span-3">
              <button type="submit" disabled={save.isPending}
                      className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50">
                Save the policy
              </button>
              {save.error && <span className="ml-3 text-sm text-danger-fg">{errText(save.error)}</span>}
            </div>
          </form>
        </OwnerOnly>
      </div>

      <h3 className="mb-2 mt-6 text-xs font-semibold uppercase tracking-wide text-fg-muted">
        What is still held
      </h3>
      <div className="overflow-hidden rounded-lg border border-border bg-surface-2">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-2">Record class</th>
              <th className="px-4 py-2 text-right">Rows</th>
              <th className="px-4 py-2">Oldest</th>
              <th className="px-4 py-2 text-right">History</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {d.coverage.map((c) => (
              <tr key={c.recordClass}>
                <td className="px-4 py-2 text-fg">{c.recordClass}</td>
                <td className="px-4 py-2 text-right text-fg-muted">{c.rows.toLocaleString()}</td>
                <td className="px-4 py-2 text-fg-muted">
                  {/* Empty is an answer: "we have nothing" and "our oldest
                      is from this morning" are not the same. */}
                  {c.oldest || <span className="text-fg-subtle">nothing held</span>}
                </td>
                <td className="px-4 py-2 text-right text-fg-muted">
                  {c.rows > 0 ? `${c.yearsHeld.toFixed(1)} y` : "—"}
                  {c.shorterThanPolicy && (
                    <span className="ml-2 text-xs text-fg-subtle">under the window</span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="mt-2 text-xs text-fg-subtle">
        Holding less history than your window is not a fault on its own — a
        distillery three years old cannot hold six years of anything.
      </p>

      <h3 className="mb-2 mt-6 text-xs font-semibold uppercase tracking-wide text-fg-muted">
        Legal holds
      </h3>
      <OwnerOnly>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            const fd = new FormData(e.currentTarget);
            place.mutate({
              reason: fd.get("reason")?.toString() ?? "",
              instructedBy: fd.get("by")?.toString() ?? "",
              reference: fd.get("ref")?.toString() ?? "",
            });
            e.currentTarget.reset();
          }}
          className="mb-3 grid gap-3 sm:grid-cols-4"
        >
          <F label="Why" name="reason" required className="sm:col-span-2" />
          <F label="Instructed by" name="by" />
          <F label="Reference" name="ref" />
          <div className="sm:col-span-4">
            <button type="submit" disabled={place.isPending}
                    className="rounded border border-border-strong px-3 py-1.5 text-sm text-fg hover:bg-surface-3">
              Place a hold
            </button>
            {place.error && <span className="ml-3 text-sm text-danger-fg">{errText(place.error)}</span>}
          </div>
        </form>
      </OwnerOnly>

      {d.holds.length === 0 ? (
        <p className="text-sm text-fg-muted">None placed.</p>
      ) : (
        <ul className="space-y-2">
          {d.holds.map((h) => (
            <li key={h.id} className={`rounded border px-3 py-2 text-sm ${
              h.open ? "border-danger/40 bg-danger/5" : "border-border bg-surface-2"
            }`}>
              <div className="flex flex-wrap items-baseline justify-between gap-2">
                <span className="text-fg">{h.reason}</span>
                <span className="text-xs text-fg-muted">
                  {h.open ? `open since ${h.placedOn}` : `released ${h.releasedOn}`}
                </span>
              </div>
              <p className="text-xs text-fg-subtle">
                {[h.instructedBy, h.reference, h.placedByName].filter(Boolean).join(" · ")}
                {h.releaseReason && ` — lifted: ${h.releaseReason}`}
              </p>
              {h.open && (
                <OwnerOnly>
                  <button
                    onClick={() => {
                      const reason = window.prompt("Why is the hold being lifted?");
                      if (reason) release.mutate({ id: h.id, reason });
                    }}
                    className="mt-1 text-xs text-accent hover:underline"
                  >
                    Lift it
                  </button>
                </OwnerOnly>
              )}
            </li>
          ))}
        </ul>
      )}
      {release.error && <p className="mt-2 text-sm text-danger-fg">{errText(release.error)}</p>}
    </section>
  );
}

function F({ label, name, type = "text", required, className }: {
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
