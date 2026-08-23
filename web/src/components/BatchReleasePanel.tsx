import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { labClient } from "@/lib/clients";
import { LabResultStatus } from "@/gen/stillhouse/v1/lab_pb";
import { OwnerOnly, WriteOnly } from "@/lib/role";

/**
 * Release and hold for one packaged lot, with whatever the lab found.
 *
 * The two acts are deliberately not one toggle. Releasing says a named
 * person decided this stock may go; holding says a named person decided
 * it must not. A lot held after release is a recall in its early form,
 * and erasing the release would remove the most important part of that
 * record — so a hold does not clear it.
 */
export function BatchReleasePanel({
  lotId, bottlingRunId, released, held, holdReason,
}: {
  lotId: string;
  bottlingRunId?: string;
  released?: boolean;
  held?: boolean;
  holdReason?: string;
}) {
  const qc = useQueryClient();
  const [notes, setNotes] = useState("");
  const [reason, setReason] = useState("");

  const results = useQuery({
    queryKey: ["listLabResults", bottlingRunId ?? lotId],
    queryFn: () => labClient.listLabResults({ bottlingRunId: bottlingRunId ?? "" }),
    enabled: !!bottlingRunId,
  });
  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["listPackagedInventory"] });
    qc.invalidateQueries({ queryKey: ["listLabResults"] });
  };
  const release = useMutation({
    mutationFn: () => labClient.releaseLot({ packagedInventoryId: lotId, notes }),
    onSuccess: () => { setNotes(""); invalidate(); },
  });
  const hold = useMutation({
    mutationFn: () => labClient.holdLot({ packagedInventoryId: lotId, reason }),
    onSuccess: () => { setReason(""); invalidate(); },
  });

  const failing = (results.data?.results ?? []).filter(
    (r) => r.status === LabResultStatus.FAIL,
  );

  return (
    <div className="rounded-lg border border-border bg-surface-2 p-5">
      <p className="mb-3 text-sm font-semibold text-fg">Release for sale</p>

      {held ? (
        <p className="mb-3 rounded border border-danger/40 bg-danger/10 px-3 py-2 text-sm">
          <span className="font-medium text-danger-fg">On hold.</span>{" "}
          <span className="text-fg-muted">{holdReason}</span>
        </p>
      ) : released ? (
        <p className="mb-3 text-sm text-success-fg">Released.</p>
      ) : (
        <p className="mb-3 text-sm text-fg-muted">Not yet released.</p>
      )}

      {failing.length > 0 && (
        <p className="mb-3 rounded border border-warning/40 bg-warning/10 px-3 py-2 text-sm text-fg-muted">
          {failing.length} failing lab result{failing.length === 1 ? "" : "s"} on this run
          ({failing.map((r) => r.analyte).join(", ")}). Releasing anyway is your call —
          it's recorded in the audit trail either way.
        </p>
      )}

      {!held && (
        <OwnerOnly>
          <div className="mb-3">
            <label className="mb-1 block text-xs text-fg-muted">
              What did you check? Required — &ldquo;approved&rdquo; answers nothing when
              somebody asks why this lot was let out.
            </label>
            <input
              value={notes}
              onChange={(e) => setNotes(e.target.value)}
              placeholder="Methanol 68 ppm within spec; sensory panel passed."
              className="w-full rounded border border-border-strong px-3 py-2 text-sm"
            />
            <button
              onClick={() => release.mutate()}
              disabled={release.isPending || !notes.trim()}
              className="mt-2 rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
            >
              {release.isPending ? "Releasing…" : released ? "Re-release" : "Release"}
            </button>
            {release.error && (
              <span className="ml-3 text-sm text-danger-fg">
                {release.error instanceof ConnectError
                  ? release.error.rawMessage
                  : String(release.error)}
              </span>
            )}
          </div>
        </OwnerOnly>
      )}

      {held ? (
        <OwnerOnly>
          <div>
            <label className="mb-1 block text-xs text-fg-muted">
              Lifting the hold means releasing again, with what you checked.
            </label>
            <input
              value={notes}
              onChange={(e) => setNotes(e.target.value)}
              placeholder="Re-tested after the complaint; within spec."
              className="w-full rounded border border-border-strong px-3 py-2 text-sm"
            />
            <button
              onClick={() => release.mutate()}
              disabled={release.isPending || !notes.trim()}
              className="mt-2 rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
            >
              {release.isPending ? "Releasing…" : "Lift the hold"}
            </button>
          </div>
        </OwnerOnly>
      ) : (
        <WriteOnly>
          <div className="border-t border-border pt-3">
            <label className="mb-1 block text-xs text-fg-muted">
              Or stop it leaving. Why?
            </label>
            <div className="flex flex-wrap items-center gap-2">
              <input
                value={reason}
                onChange={(e) => setReason(e.target.value)}
                placeholder="Customer complaint — off nose. Investigating."
                className="flex-1 rounded border border-border-strong px-3 py-2 text-sm"
              />
              <button
                onClick={() => hold.mutate()}
                disabled={hold.isPending || !reason.trim()}
                className="rounded border border-danger/40 px-3 py-2 text-sm text-danger-fg hover:bg-danger/10 disabled:opacity-50"
              >
                {hold.isPending ? "Holding…" : "Hold"}
              </button>
            </div>
            {hold.error && (
              <p className="mt-2 text-sm text-danger-fg">
                {hold.error instanceof ConnectError ? hold.error.rawMessage : String(hold.error)}
              </p>
            )}
          </div>
        </WriteOnly>
      )}

      {(results.data?.results.length ?? 0) > 0 && (
        <div className="mt-4 border-t border-border pt-3">
          <p className="mb-2 text-xs font-semibold uppercase tracking-wider text-fg-subtle">
            Lab results on this run
          </p>
          <ul className="space-y-1 text-sm">
            {results.data?.results.map((r) => (
              <li key={r.id} className="flex flex-wrap items-baseline gap-2">
                <span className="text-fg">{r.analyte}</span>
                {r.valueSet && (
                  <span className="font-mono text-xs text-fg-muted">
                    {r.value} {r.uom}
                  </span>
                )}
                <span
                  className={
                    r.status === LabResultStatus.FAIL
                      ? "text-danger-fg"
                      : r.status === LabResultStatus.PASS
                        ? "text-success-fg"
                        : "text-fg-subtle"
                  }
                >
                  {r.status === LabResultStatus.FAIL
                    ? "fail"
                    : r.status === LabResultStatus.PASS
                      ? "pass"
                      : "recorded"}
                </span>
                {r.laboratory && <span className="text-xs text-fg-subtle">{r.laboratory}</span>}
                {r.reference && <span className="text-xs text-fg-subtle">{r.reference}</span>}
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
