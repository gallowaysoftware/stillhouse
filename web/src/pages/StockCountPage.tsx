import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { EmptyRow } from "@/components/EmptyState";
import { Shell } from "@/components/Shell";
import { locationClient, stockCountClient } from "@/lib/clients";
import { StockCountScope, StockCountStatus } from "@/gen/stillhouse/v1/stockcount_pb";
import { InventoryAdjustmentReason } from "@/gen/stillhouse/v1/bulk_pb";
import { OwnerOnly, WriteOnly } from "@/lib/role";

const statusLabel: Record<number, string> = {
  [StockCountStatus.UNSPECIFIED]: "—",
  [StockCountStatus.OPEN]: "Counting",
  [StockCountStatus.COUNTED]: "Counted",
  [StockCountStatus.POSTED]: "Posted",
  [StockCountStatus.CANCELLED]: "Abandoned",
};

const reasonLabel: Record<number, string> = {
  [InventoryAdjustmentReason.UNSPECIFIED]: "— why? —",
  [InventoryAdjustmentReason.PHYSICAL_COUNT]: "Counted and differs",
  [InventoryAdjustmentReason.MEASUREMENT_CORRECTION]: "Earlier measurement was wrong",
  [InventoryAdjustmentReason.DATA_ENTRY_ERROR]: "Keying mistake",
  [InventoryAdjustmentReason.OTHER]: "Other (explain)",
};

function errText(e: unknown): string {
  return e instanceof ConnectError ? e.rawMessage : String(e);
}

export function StockCountPage() {
  const qc = useQueryClient();
  const [open, setOpen] = useState<string | null>(null);
  const [starting, setStarting] = useState(false);

  const counts = useQuery({
    queryKey: ["listStockCounts"],
    queryFn: () => stockCountClient.listStockCounts({}),
  });
  const locations = useQuery({
    queryKey: ["listLocations"],
    queryFn: () => locationClient.listLocations({}),
  });
  const start = useMutation({
    mutationFn: (m: Parameters<typeof stockCountClient.openStockCount>[0]) =>
      stockCountClient.openStockCount(m),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["listStockCounts"] });
      setOpen(r.count?.id ?? null);
      setStarting(false);
    },
  });

  return (
    <Shell>
      <div className="mb-6">
        <h1 className="text-3xl font-bold tracking-tight">Stock counts</h1>
        <p className="text-sm text-fg-muted">
          A sheet, worked through in order, with the book figure beside a blank.
          The book figures are taken when the count opens — a count that takes a
          morning while somebody else is shipping would otherwise measure the
          shipping rather than the discrepancy. Differences post as reason-coded
          adjustments, never as a silent correction.
        </p>
      </div>

      <WriteOnly>
        <button
          onClick={() => setStarting((v) => !v)}
          className="mb-4 rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover"
        >
          {starting ? "Cancel" : "Start a count"}
        </button>
      </WriteOnly>

      {starting && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            const fd = new FormData(e.currentTarget);
            start.mutate({
              name: fd.get("name")?.toString() ?? "",
              scope: Number(fd.get("scope") ?? 0) as StockCountScope,
              locationId: fd.get("location")?.toString() ?? "",
              notes: fd.get("notes")?.toString() ?? "",
            });
          }}
          className="mb-6 grid gap-3 rounded-lg border border-border bg-surface-2 p-5 sm:grid-cols-3"
        >
          <F label="Name it" name="name" placeholder="August cycle count" />
          <div>
            <label className="mb-1 block text-xs text-fg-muted">What to count</label>
            <select name="scope" className="w-full rounded border border-border-strong px-2 py-1.5 text-sm">
              <option value={StockCountScope.ALL}>Everything</option>
              <option value={StockCountScope.BULK}>Bulk vessels</option>
              <option value={StockCountScope.PACKAGED}>Packaged stock</option>
              <option value={StockCountScope.MATERIALS}>Materials</option>
            </select>
          </div>
          <div>
            <label className="mb-1 block text-xs text-fg-muted">Where</label>
            <select name="location" className="w-full rounded border border-border-strong px-2 py-1.5 text-sm">
              <option value="">Everywhere</option>
              {locations.data?.locations.map((l) => (
                <option key={l.id} value={l.id}>{l.name}</option>
              ))}
            </select>
          </div>
          <F label="Notes" name="notes" className="sm:col-span-3" />
          <div className="sm:col-span-3">
            <button type="submit" disabled={start.isPending}
                    className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50">
              Take the sheet
            </button>
            {start.error && <span className="ml-3 text-sm text-danger-fg">{errText(start.error)}</span>}
          </div>
        </form>
      )}

      <div className="overflow-hidden rounded-lg border border-border bg-surface-2">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-2">#</th>
              <th className="px-4 py-2">Name</th>
              <th className="px-4 py-2">Where</th>
              <th className="px-4 py-2">Opened</th>
              <th className="px-4 py-2 text-right">Counted</th>
              <th className="px-4 py-2">Status</th>
              <th className="px-4 py-2"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {counts.data?.counts.length === 0 && (
              <EmptyRow colSpan={7} title="No counts yet"
                        message="A count sheet is how a discrepancy gets a reason instead of a correction." />
            )}
            {counts.data?.counts.map((c) => (
              <tr key={c.id}>
                <td className="px-4 py-2 font-medium text-fg">{c.countNo}</td>
                <td className="px-4 py-2 text-fg-muted">{c.name || "—"}</td>
                <td className="px-4 py-2 text-fg-muted">{c.locationName || "everywhere"}</td>
                <td className="px-4 py-2 text-fg-muted">
                  {c.openedAt ? new Date(Number(c.openedAt.seconds) * 1000).toLocaleDateString() : "—"}
                </td>
                <td className="px-4 py-2 text-right text-fg-muted">
                  {c.countedLines}/{c.lineCount}
                </td>
                <td className="px-4 py-2 text-fg-muted">{statusLabel[c.status]}</td>
                <td className="px-4 py-2 text-right">
                  <button onClick={() => setOpen(open === c.id ? null : c.id)}
                          className="text-xs text-fg-muted hover:text-fg">
                    {open === c.id ? "Close" : "Open"}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {open && <CountSheet id={open} />}
    </Shell>
  );
}

function CountSheet({ id }: { id: string }) {
  const qc = useQueryClient();
  const sheet = useQuery({
    queryKey: ["getStockCount", id],
    queryFn: () => stockCountClient.getStockCount({ id }),
  });
  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["getStockCount", id] });
    qc.invalidateQueries({ queryKey: ["listStockCounts"] });
    qc.invalidateQueries({ queryKey: ["listPackagedInventory"] });
    qc.invalidateQueries({ queryKey: ["listMaterialLots"] });
  };
  const record = useMutation({
    mutationFn: (m: Parameters<typeof stockCountClient.recordCount>[0]) =>
      stockCountClient.recordCount(m),
    onSuccess: invalidate,
  });
  const post = useMutation({
    mutationFn: (m: Parameters<typeof stockCountClient.postStockCount>[0]) =>
      stockCountClient.postStockCount(m),
    onSuccess: invalidate,
  });

  const c = sheet.data?.count;
  if (!c) return null;
  const editable = c.status === StockCountStatus.OPEN || c.status === StockCountStatus.COUNTED;

  return (
    <div className="mt-4 space-y-4 rounded-lg border border-border bg-surface-2 p-5">
      <div className="flex flex-wrap items-baseline justify-between gap-3">
        <h2 className="text-sm font-semibold text-fg">
          Count {c.countNo} {c.name && <span className="text-fg-muted">— {c.name}</span>}
          <span className="ml-2 text-xs text-fg-muted">{statusLabel[c.status]}</span>
        </h2>
        <span className="text-xs text-fg-muted">
          {c.countedLines} of {c.lineCount} counted · {c.varianceLines} differ
        </span>
      </div>

      <div className="overflow-x-auto">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="text-left text-xs text-fg-muted">
            <tr>
              <th className="px-2 py-1.5">What</th>
              <th className="px-2 py-1.5 text-right">Book</th>
              <th className="px-2 py-1.5 text-right">Counted</th>
              <th className="px-2 py-1.5 text-right">Difference</th>
              <th className="px-2 py-1.5">Why</th>
              <th className="px-2 py-1.5"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {c.lines.map((l) => (
              <tr key={l.id} className={l.counted && l.variance !== 0 ? "bg-warning/5" : undefined}>
                <td className="px-2 py-1.5 text-fg">
                  {l.subject}
                  <span className="ml-2 text-xs text-fg-subtle">{l.detail}</span>
                </td>
                <td className="px-2 py-1.5 text-right text-fg-muted">
                  {l.bookQuantity} {l.uom}
                </td>
                <td className="px-2 py-1.5 text-right text-fg">
                  {/* Blank, not zero: an uncounted line is not a line
                      saying the shelf was empty. */}
                  {l.counted ? l.countedQuantity : <span className="text-fg-subtle">—</span>}
                </td>
                <td className={`px-2 py-1.5 text-right ${l.variance !== 0 ? "font-medium text-warning-fg" : "text-fg-subtle"}`}>
                  {l.counted ? (l.variance > 0 ? `+${l.variance}` : l.variance) : "—"}
                </td>
                <td className="px-2 py-1.5 text-xs text-fg-muted">
                  {l.explanation || (l.reason ? reasonLabel[l.reason] : "")}
                </td>
                <td className="px-2 py-1.5 text-right">
                  {l.posted && <span className="text-xs text-success-fg">posted</span>}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {editable && (
        <WriteOnly>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              const fd = new FormData(e.currentTarget);
              record.mutate({
                lineId: fd.get("line")?.toString() ?? "",
                countedQuantity: Number(fd.get("counted") ?? 0) || 0,
                countedAbvPct: Number(fd.get("abv") ?? 0) || 0,
                countedAbvPctSet: String(fd.get("abv") ?? "").trim() !== "",
                reason: Number(fd.get("reason") ?? 0) as InventoryAdjustmentReason,
                explanation: fd.get("explanation")?.toString() ?? "",
                countedBy: fd.get("counted_by")?.toString() ?? "",
              });
              e.currentTarget.reset();
            }}
            className="grid gap-3 border-t border-border pt-4 sm:grid-cols-6"
          >
            <div className="sm:col-span-2">
              <label className="mb-1 block text-xs text-fg-muted">Line</label>
              <select name="line" required className="w-full rounded border border-border-strong px-2 py-1.5 text-sm">
                <option value="">— choose —</option>
                {c.lines.filter((l) => !l.posted).map((l) => (
                  <option key={l.id} value={l.id}>
                    {l.subject} (book {l.bookQuantity} {l.uom})
                  </option>
                ))}
              </select>
            </div>
            <F label="Counted" name="counted" type="number" step="0.01" required />
            <F label="Strength % (vessels)" name="abv" type="number" step="0.01" />
            <div>
              <label className="mb-1 block text-xs text-fg-muted">Why, if it differs</label>
              <select name="reason" className="w-full rounded border border-border-strong px-2 py-1.5 text-sm">
                {Object.entries(reasonLabel).map(([k, label]) => (
                  <option key={k} value={k}>{label}</option>
                ))}
              </select>
            </div>
            <F label="Counted by" name="counted_by" />
            <F label="Explanation" name="explanation" className="sm:col-span-5" />
            <div className="flex items-end">
              <button type="submit" disabled={record.isPending}
                      className="rounded border border-border-strong px-3 py-1.5 text-sm text-fg hover:bg-surface-3">
                Record
              </button>
            </div>
            {record.error && (
              <p className="text-sm text-danger-fg sm:col-span-6">{errText(record.error)}</p>
            )}
          </form>
        </WriteOnly>
      )}

      {editable && (
        <OwnerOnly>
          <div className="border-t border-border pt-4">
            <button
              onClick={() => post.mutate({ id, occurredOn: "" })}
              disabled={post.isPending}
              className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
            >
              {post.isPending ? "Posting…" : "Post the differences"}
            </button>
            <p className="mt-2 text-xs text-fg-subtle">
              Writes a reason-coded adjustment for every line that differs. The
              date decides which return they land on, so it cannot fall inside a
              period already filed.
            </p>
            {post.error && <p className="mt-2 text-sm text-danger-fg">{errText(post.error)}</p>}
          </div>
        </OwnerOnly>
      )}

      {post.data && (
        <div className="border-t border-border pt-4">
          <p className="text-sm text-fg">
            {post.data.adjustmentsWritten} adjustment
            {post.data.adjustmentsWritten === 1 ? "" : "s"} written.
          </p>
          {post.data.skipped.map((sk, i) => (
            <p key={i} className="mt-1 rounded border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-fg">
              {sk}
            </p>
          ))}
        </div>
      )}
    </div>
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
