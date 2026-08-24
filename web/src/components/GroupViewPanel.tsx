import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { Callout } from "@/components/Callout";
import { tenantClient } from "@/lib/clients";
import { CopyableReference } from "@/gen/stillhouse/v1/tenant_pb";
import { formatLAA } from "@/lib/format";

/**
 * GroupViewPanel — figures across every licence you hold an account at.
 *
 * The screen is shaped by one rule: a B266 is filed PER LICENCE. Two
 * distilleries under one owner file two returns, and a figure spanning
 * both is not a line on either. So the rows stay separate, each carries
 * its own licence number, and the totals say what they are rather than
 * sitting bare where somebody could copy one onto a return.
 *
 * It also shows the role you hold at each, because holding an owner's
 * account at one distillery does not make you an owner at another and a
 * screen that implied otherwise would be misleading about what you can
 * actually do there.
 */
export function GroupViewPanel() {
  const [open, setOpen] = useState(false);
  const g = useQuery({
    queryKey: ["groupView"],
    queryFn: () => tenantClient.groupView({}),
    enabled: open,
  });

  if (!open) {
    return (
      <button
        onClick={() => setOpen(true)}
        className="text-[11px] text-fg-subtle underline-offset-2 hover:text-fg-muted hover:underline"
      >
        All my distilleries
      </button>
    );
  }

  const d = g.data;
  return (
    <section className="mt-2 rounded border border-border bg-surface-2 p-3">
      <div className="mb-2 flex items-center justify-between">
        <h3 className="text-xs font-semibold uppercase text-fg-muted">Across your licences</h3>
        <button onClick={() => setOpen(false)} className="text-xs text-fg-muted hover:text-fg">
          close
        </button>
      </div>

      {g.isLoading && <p className="text-xs text-fg-muted">Loading…</p>}

      {d && (
        <>
          {d.returnsDueSoon > 0 && (
            <Callout tone="warning" title={`${d.returnsDueSoon} return(s) due within the week`}>
              Each licence files its own. The row below tells you which.
            </Callout>
          )}

          <table className="mt-2 w-full text-xs">
            <thead className="text-left uppercase text-fg-muted">
              <tr>
                <th className="py-1 pr-2">Distillery</th>
                <th className="py-1 pr-2">Licence</th>
                <th className="py-1 pr-2">You are</th>
                <th className="py-1 pr-2 text-right">Bulk LAA</th>
                <th className="py-1 pr-2 text-right">Bottles</th>
                <th className="py-1 pr-2 text-right">Casks</th>
                <th className="py-1">Its own return</th>
              </tr>
            </thead>
            <tbody>
              {d.entities.map((e) => (
                <tr key={e.tenantId} className="border-t border-border align-top">
                  <td className="py-1 pr-2 font-medium">{e.tenantName}</td>
                  <td className="py-1 pr-2 tabular-nums">{e.craSpiritsLicenceNumber}</td>
                  <td className="py-1 pr-2">{e.yourRole}</td>
                  {e.unavailable ? (
                    <td colSpan={4} className="py-1 text-fg-muted">{e.unavailable}</td>
                  ) : (
                    <>
                      <td className="py-1 pr-2 text-right tabular-nums">{formatLAA(e.bulkLaa)}</td>
                      <td className="py-1 pr-2 text-right tabular-nums">{e.packagedBottles}</td>
                      <td className="py-1 pr-2 text-right tabular-nums">{e.caskCount}</td>
                      <td className="py-1">
                        {e.nextDueOn ? (
                          <>
                            {e.nextPeriodStart} → {e.nextPeriodEnd}
                            <span
                              className={`block ${
                                e.daysUntilDue <= 7 && !e.periodSubmitted ? "text-warning-fg" : "text-fg-muted"
                              }`}
                            >
                              due {e.nextDueOn}
                              {e.periodSubmitted
                                ? " · submitted"
                                : e.periodGenerated
                                  ? " · generated"
                                  : " · not generated"}
                            </span>
                          </>
                        ) : (
                          <span className="text-fg-muted">calendar not set</span>
                        )}
                      </td>
                    </>
                  )}
                </tr>
              ))}
            </tbody>
            <tfoot>
              <tr className="border-t border-border">
                <td colSpan={3} className="py-1 text-fg-muted">
                  Across {d.entities.length} separate returns
                </td>
                <td className="py-1 pr-2 text-right tabular-nums">{formatLAA(d.totalBulkLaa)}</td>
                <td className="py-1 pr-2 text-right tabular-nums">{d.totalPackagedBottles}</td>
                <td colSpan={2}></td>
              </tr>
            </tfoot>
          </table>

          <p className="mt-2 text-[11px] text-fg-muted">{d.caution}</p>

          {/* Copying, not linking. A material's extract fraction feeds a
              conversion efficiency which feeds a yield, so each licence
              has to own the definitions its own figures came from. */}
          <CopyFrom entities={d.entities} />
        </>
      )}
    </section>
  );
}

function CopyFrom({ entities }: { entities: { tenantId: string; tenantName: string }[] }) {
  const qc = useQueryClient();
  const [from, setFrom] = useState("");
  const [result, setResult] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const copy = useMutation({
    mutationFn: () =>
      tenantClient.copyReferenceData({
        fromTenantId: from,
        what: [
          CopyableReference.MATERIALS,
          CopyableReference.SUPPLIERS,
        ],
      }),
    onSuccess: (r) => {
      setErr(null);
      setResult(
        `${r.materialsCopied} material(s) and ${r.suppliersCopied} supplier(s) copied` +
          (r.skipped.length ? `; already here: ${r.skipped.join(", ")}` : ""),
      );
      void qc.invalidateQueries();
    },
    onError: (e) => setErr(e instanceof ConnectError ? e.rawMessage : String(e)),
  });

  if (entities.length < 2) return null;

  return (
    <div className="mt-3 border-t border-border pt-2">
      <p className="mb-1 text-[11px] text-fg-muted">
        Copy materials and suppliers from another of your distilleries. They
        become this one&apos;s own definitions — editing them here changes
        nothing there.
      </p>
      <div className="flex flex-wrap items-center gap-2">
        <select
          value={from}
          onChange={(e) => setFrom(e.target.value)}
          className="rounded border border-border-strong px-2 py-1 text-xs"
        >
          <option value="">Copy from…</option>
          {entities.map((e) => (
            <option key={e.tenantId} value={e.tenantId}>{e.tenantName}</option>
          ))}
        </select>
        <button
          onClick={() => copy.mutate()}
          disabled={copy.isPending || !from}
          className="rounded border border-border-strong px-2 py-1 text-xs hover:bg-surface-3 disabled:opacity-50"
        >
          {copy.isPending ? "Copying…" : "Copy"}
        </button>
      </div>
      {result && <p className="mt-1 text-[11px] text-fg-muted">{result}</p>}
      {err && <p className="mt-1 text-[11px] text-danger-fg">{err}</p>}
    </div>
  );
}
