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
import { bulkClient, bottlingClient } from "@/lib/clients";
import { BulkExternalMovementKind } from "@/gen/stillhouse/v1/bulk_pb";

/**
 * ExternalMovementCard — bulk spirits arriving on or leaving the premises.
 *
 * These are the B266 page 3 lines that had no path. Four of them were on
 * the report from the beginning and structurally always zero, because
 * nothing in the application ever wrote the movement: received in bond,
 * transferred out in bond, destroyed, and unaccounted loss.
 */

type Kind = BulkExternalMovementKind;

const receipts: { kind: Kind; label: string; party: boolean }[] = [
  { kind: BulkExternalMovementKind.IMPORT, label: "Imported bulk spirits", party: true },
  { kind: BulkExternalMovementKind.IN_BOND_IN, label: "Received in bond", party: true },
  { kind: BulkExternalMovementKind.FROM_SPIRITS_LICENSEE, label: "Received from a spirits licensee", party: true },
  { kind: BulkExternalMovementKind.FROM_LICENSED_USER, label: "Received from a licensed user", party: true },
  { kind: BulkExternalMovementKind.PACKAGED_RETURNED_TO_BULK, label: "Packaged spirits returned to bulk", party: false },
];

const dispositions: { kind: Kind; label: string; party: boolean }[] = [
  { kind: BulkExternalMovementKind.IN_BOND_OUT, label: "Transferred out in bond", party: true },
  { kind: BulkExternalMovementKind.TO_SPIRITS_LICENSEE, label: "Delivered to a spirits licensee", party: true },
  { kind: BulkExternalMovementKind.TO_LICENSED_USER, label: "Delivered to a licensed user", party: true },
  { kind: BulkExternalMovementKind.EXPORT, label: "Exported", party: true },
  { kind: BulkExternalMovementKind.DENATURED_DA, label: "Denatured to DA", party: false },
  { kind: BulkExternalMovementKind.DENATURED_SDA, label: "Denatured to SDA", party: false },
  { kind: BulkExternalMovementKind.RETURNED_TO_PRODUCTION, label: "Returned to production", party: false },
  { kind: BulkExternalMovementKind.DESTRUCTION, label: "Destroyed", party: false },
  { kind: BulkExternalMovementKind.UNACCOUNTED_LOSS, label: "Unaccounted loss", party: false },
];

const allKinds = [...receipts, ...dispositions];

export function ExternalMovementCard({
  containerId,
  containerName,
}: {
  containerId: string;
  containerName: string;
}) {
  const qc = useQueryClient();
  const toast = useToast();
  const [open, setOpen] = useState(false);
  const [kind, setKind] = useState<Kind>(BulkExternalMovementKind.IN_BOND_IN);
  const [reading, setReading] = useState<StrengthReadingValue>(() => emptyReading());
  const [party, setParty] = useState("");
  const [licence, setLicence] = useState("");
  const [doc, setDoc] = useState("");
  const [notes, setNotes] = useState("");
  const [lotId, setLotId] = useState("");
  const [bottles, setBottles] = useState("");
  const [err, setErr] = useState<string | null>(null);

  const spec = allKinds.find((k) => k.kind === kind);
  const isUnpackage = kind === BulkExternalMovementKind.PACKAGED_RETURNED_TO_BULK;

  // Only fetched for the one kind that needs it.
  const lots = useQuery({
    queryKey: ["listPackagedInventory"],
    queryFn: () => bottlingClient.listPackagedInventory({ includeEmpty: false }),
    enabled: isUnpackage,
  });

  const record = useMutation({
    mutationFn: (msg: Parameters<typeof bulkClient.recordBulkExternalMovement>[0]) =>
      bulkClient.recordBulkExternalMovement(msg),
    onSuccess: (r) => {
      setErr(null);
      setOpen(false);
      setReading(emptyReading());
      setNotes("");
      toast("success", "Movement recorded.");
      r.warnings.forEach((w) => toast("warning", w));
      void qc.invalidateQueries({ queryKey: ["getBulkContainer", containerId] });
      void qc.invalidateQueries({ queryKey: ["listBulkContainers"] });
      void qc.invalidateQueries({ queryKey: ["listRecentBulkMovements"] });
      void qc.invalidateQueries({ queryKey: ["listPackagedInventory"] });
    },
    onError: (e) => setErr(ConnectError.from(e).message),
  });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const { instruments, ...trio } = readingToRequest(reading);
    record.mutate({
      containerId,
      kind,
      // The unpackaging path takes its quantity from the bottles; the
      // server ignores these, and sending the lot's own figures would
      // only look like they mattered.
      volumeL: isUnpackage ? 1 : Number(reading.volumeL),
      abvPct: trio.abvPct,
      temperatureC: trio.temperatureC,
      temperatureCSet: trio.temperatureCSet,
      densityKgM3: trio.densityKgM3,
      densityKgM3Set: trio.densityKgM3Set,
      instruments,
      counterpartyName: party,
      counterpartyLicenceNo: licence,
      documentReference: doc,
      notes,
      packagedInventoryId: isUnpackage ? lotId : "",
      bottlesUnpackaged: isUnpackage ? Number(bottles) || 0 : 0,
    });
  };

  return (
    <div className="rounded-lg border border-border bg-surface-2">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center justify-between px-4 py-3 text-left hover:bg-surface-3"
      >
        <span className="text-sm font-semibold text-fg">Receipts and dispositions</span>
        <span className="text-xs text-fg-muted">
          {open ? "Close" : "Record spirits in or out"}
        </span>
      </button>

      {open && (
        <form onSubmit={submit} className="space-y-3 border-t border-border p-4">
          <div>
            <label className="mb-1 block text-xs text-fg-muted">Movement</label>
            <select
              value={kind}
              onChange={(e) => setKind(Number(e.target.value) as Kind)}
              className="w-full rounded border border-border-strong px-2 py-1 text-sm"
            >
              <optgroup label={`Into ${containerName}`}>
                {receipts.map((k) => (
                  <option key={k.kind} value={k.kind}>{k.label}</option>
                ))}
              </optgroup>
              <optgroup label={`Out of ${containerName}`}>
                {dispositions.map((k) => (
                  <option key={k.kind} value={k.kind}>{k.label}</option>
                ))}
              </optgroup>
            </select>
          </div>

          {isUnpackage ? (
            <>
              {/* The quantity comes from the bottles, not a typed volume:
                  letting the two disagree would credit bulk with more than
                  packaged gave up. */}
              <div>
                <label className="mb-1 block text-xs text-fg-muted">Lot</label>
                <select
                  value={lotId}
                  onChange={(e) => setLotId(e.target.value)}
                  required
                  className="w-full rounded border border-border-strong px-2 py-1 text-sm"
                >
                  <option value="">Select a lot…</option>
                  {(lots.data?.rows ?? []).map((l) => (
                    <option key={l.id} value={l.id}>
                      {l.productName} · lot {l.lotCode} · {l.bottlesOnHand.toLocaleString()} on hand
                    </option>
                  ))}
                </select>
              </div>
              <div>
                <label className="mb-1 block text-xs text-fg-muted">Bottles unpackaged</label>
                <input
                  type="number"
                  min={1}
                  value={bottles}
                  onChange={(e) => setBottles(e.target.value)}
                  required
                  className="w-full rounded border border-border-strong px-2 py-1 text-sm"
                />
                <p className="mt-1 text-xs text-fg-subtle">
                  The alcohol credited to bulk is whatever these bottles hold. Duty already
                  paid on them is recovered through a B256 refund application, separately.
                </p>
              </div>
            </>
          ) : (
            <StrengthReading value={reading} onChange={setReading} />
          )}

          {spec?.party && (
            <>
              <div>
                <label className="mb-1 block text-xs text-fg-muted">Counterparty</label>
                <input
                  value={party}
                  onChange={(e) => setParty(e.target.value)}
                  required
                  placeholder="who it came from or went to"
                  className="w-full rounded border border-border-strong px-2 py-1 text-sm"
                />
                {/* Not optional: the line is reportable at both ends, and a
                    bare quantity can't be reconciled against the other
                    party's return. */}
                <p className="mt-1 text-xs text-fg-subtle">
                  Required — this line is reported by both parties.
                </p>
              </div>
              <div>
                <label className="mb-1 block text-xs text-fg-muted">Their licence number</label>
                <input
                  value={licence}
                  onChange={(e) => setLicence(e.target.value)}
                  className="w-full rounded border border-border-strong px-2 py-1 text-sm"
                />
              </div>
            </>
          )}

          <div>
            <label className="mb-1 block text-xs text-fg-muted">Document reference</label>
            <input
              value={doc}
              onChange={(e) => setDoc(e.target.value)}
              placeholder="bill of lading, customs entry, CRA approval…"
              className="w-full rounded border border-border-strong px-2 py-1 text-sm"
            />
          </div>

          <div>
            <label className="mb-1 block text-xs text-fg-muted">Notes</label>
            <input
              value={notes}
              onChange={(e) => setNotes(e.target.value)}
              className="w-full rounded border border-border-strong px-2 py-1 text-sm"
            />
          </div>

          {err && <Callout tone="danger">{err}</Callout>}

          <button
            type="submit"
            disabled={record.isPending}
            className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
          >
            {record.isPending ? "Recording…" : "Record movement"}
          </button>
        </form>
      )}
    </div>
  );
}
