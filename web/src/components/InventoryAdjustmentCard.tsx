import { FormEvent, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { Callout } from "@/components/Callout";
import { useToast } from "@/components/Toast";
import {
  StrengthReading,
  emptyReading,
  readingToRequest,
  type StrengthReadingValue,
} from "@/components/StrengthReading";
import { bulkClient } from "@/lib/clients";
import { formatLAA, formatQty } from "@/lib/format";
import { InventoryAdjustmentReason } from "@/gen/stillhouse/v1/bulk_pb";

/**
 * InventoryAdjustmentCard — reconcile a container's book balance to what
 * was physically found.
 *
 * Line D on B266 page 3 is a reason-coded entry, and until stage 145 there
 * was no way to make one: a barrel regauge refuses any upward variance
 * outright, tanks could not be reconciled at all, and a downward variance
 * was booked as evaporation whatever caused it.
 *
 * The variance is previewed against the book balance while the operator is
 * still standing at the tank, because a count that comes out 400 L short is
 * usually a decimal point rather than a leak, and that is worth seeing
 * before the entry lands on a return.
 */

export const adjustmentReasonLabel: Record<InventoryAdjustmentReason, string> = {
  [InventoryAdjustmentReason.UNSPECIFIED]: "—",
  [InventoryAdjustmentReason.PHYSICAL_COUNT]: "Physical count",
  [InventoryAdjustmentReason.MEASUREMENT_CORRECTION]: "Measurement correction",
  [InventoryAdjustmentReason.DATA_ENTRY_ERROR]: "Data entry error",
  [InventoryAdjustmentReason.OTHER]: "Other",
};

const reasonHelp: Partial<Record<InventoryAdjustmentReason, string>> = {
  [InventoryAdjustmentReason.PHYSICAL_COUNT]:
    "Stock was counted or gauged and differs from the ledger.",
  [InventoryAdjustmentReason.MEASUREMENT_CORRECTION]:
    "An earlier determination was wrong — instrument error, arithmetic, a reading taken at the wrong temperature.",
  [InventoryAdjustmentReason.DATA_ENTRY_ERROR]:
    "A keying mistake in Stillhouse itself.",
};

export function InventoryAdjustmentCard({
  containerId,
  containerName,
  bookVolumeL,
  bookAbvPct,
  bookLaa,
}: {
  containerId: string;
  containerName: string;
  bookVolumeL: number;
  bookAbvPct: number | null;
  bookLaa: number;
}) {
  const qc = useQueryClient();
  const toast = useToast();
  const [open, setOpen] = useState(false);
  const [reading, setReading] = useState<StrengthReadingValue>(() =>
    emptyReading({
      volumeL: String(bookVolumeL),
      abv: bookAbvPct == null ? "" : bookAbvPct.toFixed(2),
    }),
  );
  const [reason, setReason] = useState<InventoryAdjustmentReason>(
    InventoryAdjustmentReason.PHYSICAL_COUNT,
  );
  const [explanation, setExplanation] = useState("");
  const [err, setErr] = useState<string | null>(null);

  const history = useQuery({
    queryKey: ["listInventoryAdjustments", containerId],
    queryFn: () => bulkClient.listInventoryAdjustments({ containerId }),
  });

  const record = useMutation({
    mutationFn: (msg: Parameters<typeof bulkClient.recordInventoryAdjustment>[0]) =>
      bulkClient.recordInventoryAdjustment(msg),
    onSuccess: (r) => {
      setErr(null);
      setExplanation("");
      setOpen(false);
      toast("success", "Adjustment recorded.");
      r.warnings.forEach((w) => toast("warning", w));
      void qc.invalidateQueries({ queryKey: ["getBulkContainer", containerId] });
      void qc.invalidateQueries({ queryKey: ["listInventoryAdjustments", containerId] });
      void qc.invalidateQueries({ queryKey: ["listBulkContainers"] });
      void qc.invalidateQueries({ queryKey: ["listRecentBulkMovements"] });
    },
    onError: (e) => setErr(ConnectError.from(e).message),
  });

  // The preview is deliberately naive: it multiplies the typed figures
  // rather than asking the server, because the server's corrected answer
  // already shows up in the StrengthReading control above. What matters
  // here is the size of the gap, not its fourth decimal place.
  const countedVol = Number(reading.volumeL);
  const countedAbv = reading.mode === "abv" ? Number(reading.abv) : NaN;
  const previewLaa =
    Number.isFinite(countedVol) && Number.isFinite(countedAbv)
      ? (countedVol * countedAbv) / 100
      : null;
  const delta = previewLaa == null ? null : previewLaa - bookLaa;
  // Anything past a fifth of the book balance is far more often a decimal
  // point than a real variance.
  const looksLikeATypo =
    delta != null && bookLaa > 0 && Math.abs(delta) > bookLaa * 0.2;

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const { instruments, ...trio } = readingToRequest(reading);
    record.mutate({
      containerId,
      reason,
      explanation,
      countedVolumeL: countedVol,
      abvPct: trio.abvPct,
      temperatureC: trio.temperatureC,
      temperatureCSet: trio.temperatureCSet,
      densityKgM3: trio.densityKgM3,
      densityKgM3Set: trio.densityKgM3Set,
      instruments,
    });
  };

  const rows = history.data?.adjustments ?? [];

  return (
    <div className="rounded-lg border border-border bg-surface-2">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center justify-between px-4 py-3 text-left hover:bg-surface-3"
      >
        <span className="text-sm font-semibold text-fg">Inventory adjustment</span>
        <span className="text-xs text-fg-muted">{open ? "Close" : "Reconcile to a count"}</span>
      </button>

      {open && (
        <form onSubmit={submit} className="space-y-3 border-t border-border p-4">
          <p className="text-xs text-fg-muted">
            Book balance: {formatQty(bookVolumeL)} L
            {bookAbvPct != null && ` at ${bookAbvPct.toFixed(2)}%`} ={" "}
            {formatLAA(bookLaa)} L LAA. Enter what was actually found.
          </p>

          <StrengthReading
            value={reading}
            onChange={setReading}
            volumeLabel="Counted volume (L, as gauged)"
          />

          {delta != null && Math.abs(delta) > 1e-9 && (
            <div className={`rounded border p-2 text-xs ${looksLikeATypo ? "border-warning text-warning" : "border-border text-fg-muted"}`}>
              Variance: {delta > 0 ? "+" : ""}
              {formatLAA(delta)} L LAA against the book.
              {looksLikeATypo && " That is more than a fifth of the balance — check the decimal point before recording."}
            </div>
          )}

          <div>
            <label className="mb-1 block text-xs text-fg-muted">Reason</label>
            <select
              value={reason}
              onChange={(e) => setReason(Number(e.target.value) as InventoryAdjustmentReason)}
              className="w-full rounded border border-border-strong px-2 py-1 text-sm"
            >
              {[
                InventoryAdjustmentReason.PHYSICAL_COUNT,
                InventoryAdjustmentReason.MEASUREMENT_CORRECTION,
                InventoryAdjustmentReason.DATA_ENTRY_ERROR,
                InventoryAdjustmentReason.OTHER,
              ].map((r) => (
                <option key={r} value={r}>
                  {adjustmentReasonLabel[r]}
                </option>
              ))}
            </select>
            {reasonHelp[reason] && (
              <p className="mt-1 text-xs text-fg-subtle">{reasonHelp[reason]}</p>
            )}
          </div>

          <div>
            <label className="mb-1 block text-xs text-fg-muted">Explanation</label>
            <textarea
              value={explanation}
              onChange={(e) => setExplanation(e.target.value)}
              required
              rows={2}
              placeholder="what happened, in the words you'd use to an auditor"
              className="w-full rounded border border-border-strong px-2 py-1 text-sm"
            />
            {/* Not decoration: line D is what someone reads to find out why
                the books moved, and the reason code alone doesn't answer it. */}
            <p className="mt-1 text-xs text-fg-subtle">
              Required. This lands on line D of the return and is what an auditor reads.
            </p>
          </div>

          {err && <Callout tone="danger">{err}</Callout>}

          <button
            type="submit"
            disabled={record.isPending || explanation.trim() === ""}
            className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
          >
            {record.isPending ? "Recording…" : "Record adjustment"}
          </button>
        </form>
      )}

      {rows.length > 0 && (
        <div className="border-t border-border px-4 py-3">
          <h3 className="mb-2 text-xs font-semibold uppercase text-fg-muted">
            Adjustments on {containerName}
          </h3>
          <ul className="space-y-2 text-xs">
            {rows.map((a) => (
              <li key={a.id} className="border-l-2 border-border pl-2">
                <div className="tabular-nums">
                  {a.deltaLaa > 0 ? "+" : ""}
                  {formatLAA(a.deltaLaa)} L LAA · {adjustmentReasonLabel[a.reason]}
                </div>
                <div className="text-fg-muted">{a.explanation}</div>
                <div className="text-fg-subtle">
                  {a.adjustedByName}
                  {a.occurredAt &&
                    ` · ${new Date(Number(a.occurredAt.seconds) * 1000).toLocaleDateString()}`}
                </div>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
