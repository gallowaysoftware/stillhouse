import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { Callout } from "@/components/Callout";
import { useToast } from "@/components/Toast";
import { bulkClient } from "@/lib/clients";
import { WriteOnly, canWrite, useCurrentRole } from "@/lib/role";
import { bulkMovementReasonLabel, formatCAD, formatLAA } from "@/lib/format";
import { LossDutyTreatment } from "@/gen/stillhouse/v1/bulk_pb";

/**
 * LossClassificationCard — the list an operator works through at period
 * end, before the return can be filed.
 *
 * Under EDM3-4-1 a relieved loss and one that cannot be accounted for are
 * charged differently, and Stillhouse will not guess which a given
 * evaporation loss is — the barrel regauge that wrote it did not ask.
 *
 * Two things this deliberately does. It shows what each loss would cost if
 * ruled dutiable, so the decision is made with its price visible rather
 * than discovered on the return. And it rules on several at once, because
 * an operator resolving a period's evaporation losses is making one
 * decision about a dozen rows, not a dozen decisions.
 */
export function LossClassificationCard({
  periodStart,
  periodEnd,
}: {
  periodStart: string;
  periodEnd: string;
}) {
  const qc = useQueryClient();
  const toast = useToast();
  const role = useCurrentRole();
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [authority, setAuthority] = useState("");
  const [err, setErr] = useState<string | null>(null);

  const list = useQuery({
    queryKey: ["listLosses", periodStart, periodEnd],
    queryFn: () =>
      bulkClient.listLosses({ periodStart, periodEnd, unclassifiedOnly: true }),
    enabled: periodStart !== "" && periodEnd !== "",
  });

  const classify = useMutation({
    mutationFn: (msg: Parameters<typeof bulkClient.classifyLosses>[0]) =>
      bulkClient.classifyLosses(msg),
    onSuccess: (r) => {
      setErr(null);
      setSelected(new Set());
      setAuthority("");
      toast("success", `${r.losses.length} loss${r.losses.length === 1 ? "" : "es"} classified.`);
      void qc.invalidateQueries({ queryKey: ["listLosses"] });
      void qc.invalidateQueries({ queryKey: ["generateB266"] });
    },
    onError: (e) => setErr(ConnectError.from(e).message),
  });

  const losses = list.data?.losses ?? [];
  if (losses.length === 0) return null;

  const toggle = (id: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const ids = [...selected];
  const selectedDuty = losses
    .filter((l) => selected.has(l.movementId))
    .reduce((s, l) => s + l.dutyIfDutiableCad, 0);

  const rule = (treatment: LossDutyTreatment) => {
    classify.mutate({ movementIds: ids, treatment, authority });
  };

  return (
    <div data-print-hide className="mb-6 rounded-lg border border-warning bg-surface-2">
      <div className="border-b border-border px-4 py-3">
        <h2 className="text-sm font-semibold text-fg">
          {losses.length} loss{losses.length === 1 ? "" : "es"} need a duty treatment
        </h2>
        <p className="mt-1 text-xs text-fg-muted">
          Under EDM3-4-1 a relieved loss and one that cannot be accounted for are
          charged differently. Stillhouse will not guess which these are, so the
          period cannot be filed until somebody rules on them.
        </p>
      </div>

      <ul className="divide-y divide-border">
        {losses.map((l) => (
          <li key={l.movementId} className="flex items-start gap-3 px-4 py-2 text-sm">
            <input
              type="checkbox"
              checked={selected.has(l.movementId)}
              onChange={() => toggle(l.movementId)}
              disabled={!canWrite(role)}
              className="mt-1"
            />
            <div className="flex-1">
              <div className="tabular-nums">
                {formatLAA(l.laa)} L LAA · {bulkMovementReasonLabel(l.reason)}
                {l.containerName && ` · ${l.containerName}`}
              </div>
              <div className="text-xs text-fg-muted">
                {l.occurredAt &&
                  new Date(Number(l.occurredAt.seconds) * 1000).toLocaleDateString()}
                {l.notes && ` · ${l.notes}`}
              </div>
            </div>
            {/* The price of ruling this one dutiable, before the decision. */}
            <div className="text-xs tabular-nums text-fg-subtle">
              {formatCAD(l.dutyIfDutiableCad)} if dutiable
            </div>
          </li>
        ))}
      </ul>

      <WriteOnly>
        <div className="space-y-3 border-t border-border p-4">
          {err && <Callout tone="danger">{err}</Callout>}
          <div>
            <label className="mb-1 block text-xs text-fg-muted">
              Authority for relief
            </label>
            <input
              value={authority}
              onChange={(e) => setAuthority(e.target.value)}
              placeholder="CRA approval reference, or the provision relied on"
              className="w-full rounded border border-border-strong px-2 py-1 text-sm"
            />
            {/* Required for relief, and only for relief: relief that rests
                on nothing is not relief. */}
            <p className="mt-1 text-xs text-fg-subtle">
              Required to mark a loss relieved. Not needed to mark one dutiable.
            </p>
          </div>
          <div className="flex flex-wrap items-center gap-3">
            <button
              type="button"
              disabled={ids.length === 0 || classify.isPending}
              onClick={() => rule(LossDutyTreatment.DUTIABLE)}
              className="rounded border border-border-strong px-3 py-2 text-sm hover:bg-surface-3 disabled:opacity-50"
            >
              Mark {ids.length || ""} dutiable
              {selectedDuty > 0 && ` — ${formatCAD(selectedDuty)}`}
            </button>
            <button
              type="button"
              disabled={ids.length === 0 || authority.trim() === "" || classify.isPending}
              onClick={() => rule(LossDutyTreatment.RELIEVED)}
              className="rounded border border-border-strong px-3 py-2 text-sm hover:bg-surface-3 disabled:opacity-50"
            >
              Mark {ids.length || ""} relieved
            </button>
          </div>
        </div>
      </WriteOnly>
    </div>
  );
}
