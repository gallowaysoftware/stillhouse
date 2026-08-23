import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { certificateClient } from "@/lib/clients";
import { formatLAA } from "@/lib/format";

function errText(e: unknown): string {
  return e instanceof ConnectError ? e.rawMessage : String(e);
}

// The evidence behind a certificate of age and origin.
//
// Not a certificate: one is signed by a Canadian official (EDM3-1-1
// ¶43–46). This assembles what Stillhouse's own records support, names
// every cask it cannot account for, and leaves the signing to a person —
// a packet that quietly rounded up over a gap in the record is the one
// failure that matters here.
export function AgeCertificatePanel({ bottlingRunId }: { bottlingRunId: string }) {
  const [open, setOpen] = useState(false);
  const build = useMutation({
    mutationFn: (m: Parameters<typeof certificateClient.ageCertificate>[0]) =>
      certificateClient.ageCertificate(m),
  });
  const c = build.data;

  return (
    <section className="mb-8 rounded-lg border border-border bg-surface-2 p-5">
      <div data-print-hide className="flex flex-wrap items-baseline justify-between gap-3">
        <h2 className="text-sm font-semibold text-fg">Age and origin</h2>
        <button
          onClick={() => setOpen((v) => !v)}
          className="text-xs text-accent hover:underline"
        >
          {open ? "Hide" : "Build the export packet"}
        </button>
      </div>

      {open && (
        <>
          <form
            data-print-hide
            onSubmit={(e) => {
              e.preventDefault();
              const fd = new FormData(e.currentTarget);
              build.mutate({
                bottlingRunId,
                consignee: fd.get("consignee")?.toString() ?? "",
                destinationCountry: fd.get("country")?.toString() ?? "",
                reference: fd.get("reference")?.toString() ?? "",
              });
            }}
            className="mt-3 grid gap-3 sm:grid-cols-4"
          >
            <F label="Consignee" name="consignee" className="sm:col-span-2" />
            <F label="Destination country" name="country" />
            <F label="Reference" name="reference" />
            <div className="sm:col-span-4">
              <button type="submit" disabled={build.isPending}
                      className="rounded border border-border-strong px-3 py-1.5 text-sm text-fg hover:bg-surface-3">
                {build.isPending ? "Assembling…" : "Assemble"}
              </button>
              {build.error && (
                <span className="ml-3 text-sm text-danger-fg">{errText(build.error)}</span>
              )}
            </div>
          </form>

          {c && (
            <div className="mt-4 border-t border-border pt-4">
              <div className="flex flex-wrap items-baseline justify-between gap-3">
                <div>
                  <p className="text-sm font-medium text-fg">{c.producerName}</p>
                  <p className="text-xs text-fg-muted">
                    Spirits licence {c.producerLicenceNo || "— not on file —"}
                  </p>
                </div>
                <div className="text-right text-xs text-fg-muted">
                  <p>Lot {c.lotCode} · {c.bottleCount.toLocaleString()} bottles at {c.bottleAbvPct} %</p>
                  <p>Bottled {c.bottledOn}</p>
                  {c.consignee && <p>To {c.consignee}{c.destinationCountry && `, ${c.destinationCountry}`}</p>}
                </div>
              </div>

              <p className={`mt-3 text-sm ${c.ageSupportable ? "text-fg" : "text-warning-fg"}`}>
                {c.ageSupportable
                  ? `Stillhouse's records support an age of ${c.supportableAgeYears} year${c.supportableAgeYears === 1 ? "" : "s"}.`
                  : "Stillhouse's records do not support an age claim for this run."}
              </p>

              {c.casks.length > 0 && (
                <table className="mt-3 min-w-full divide-y divide-border text-sm">
                  <thead className="text-left text-xs text-fg-muted">
                    <tr>
                      <th className="px-2 py-1.5">Cask</th>
                      <th className="px-2 py-1.5">Wood</th>
                      <th className="px-2 py-1.5">Emptied</th>
                      <th className="px-2 py-1.5 text-right">LAA</th>
                      <th className="px-2 py-1.5 text-right">Age</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-border">
                    {c.casks.map((k) => (
                      <tr key={k.containerId}>
                        <td className="px-2 py-1.5 text-fg">
                          {k.caskName}
                          {k.serialBurnin && (
                            <span className="ml-2 text-xs text-fg-subtle">{k.serialBurnin}</span>
                          )}
                        </td>
                        <td className="px-2 py-1.5 text-xs text-fg-muted">
                          {[k.woodSpecies, k.priorUse].filter(Boolean).join(" · ") || "—"}
                          {k.capacityL > 0 && ` · ${k.capacityL} L`}
                          {!k.smallWood && k.capacityL > 0 && (
                            <span className="ml-1 text-warning-fg">not small wood</span>
                          )}
                        </td>
                        <td className="px-2 py-1.5 text-fg-muted">{k.dumpedOn || "—"}</td>
                        <td className="px-2 py-1.5 text-right text-fg-muted">
                          {k.dumpedLaa > 0 ? formatLAA(k.dumpedLaa) : "—"}
                        </td>
                        <td className="px-2 py-1.5 text-right">
                          {k.daysAgedKnown ? (
                            <span className="text-fg">
                              {Math.floor(k.daysAged / 365)} y {k.daysAged % 365} d
                            </span>
                          ) : (
                            <span className="text-xs text-warning-fg">{k.whyNot}</span>
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}

              {c.caveats.map((cv, i) => (
                <p key={i} className="mt-2 rounded border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-fg">
                  {cv}
                </p>
              ))}
              <p className="mt-3 text-xs text-fg-subtle">{c.basis}</p>
              <button
                data-print-hide
                onClick={() => window.print()}
                className="mt-3 rounded border border-border-strong px-3 py-1.5 text-sm text-fg hover:bg-surface-3"
              >
                Print the packet
              </button>
            </div>
          )}
        </>
      )}
    </section>
  );
}

function F({ label, name, className }: { label: string; name: string; className?: string }) {
  return (
    <div className={className}>
      <label className="mb-1 block text-xs text-fg-muted">{label}</label>
      <input name={name} className="w-full rounded border border-border-strong px-2 py-1.5 text-sm" />
    </div>
  );
}
