import { useQuery } from "@tanstack/react-query";

import { tenantClient } from "@/lib/clients";
import { formatCAD, formatLAA } from "@/lib/format";

// What the licensee would owe, beside what they have posted.
//
// Deliberately not a verdict. What security s.23 requires is CRA's
// determination and turns on things outside Stillhouse; printing a pass
// or a fail would be inventing a threshold, which is the same mistake as
// inventing a rate.
export function SecurityPanel() {
  const s = useQuery({
    queryKey: ["securitySufficiency"],
    queryFn: () => tenantClient.securitySufficiency({}),
  });
  const rows = s.data?.licences ?? [];
  if (rows.length === 0) return null;

  return (
    <section className="mt-8">
      <h2 className="mb-1 text-sm font-semibold text-fg">Security against exposure</h2>
      <div className="space-y-4">
        {rows.map((l) => (
          <div key={l.licenceId} className="rounded-lg border border-border bg-surface-2 p-5">
            <div className="mb-3 flex flex-wrap items-baseline justify-between gap-3">
              <span className="text-sm font-medium text-fg">
                {l.licenceNumber}
                {l.securityExpiresOn && (
                  <span className="ml-2 text-xs text-fg-muted">
                    security expires {l.securityExpiresOn}
                  </span>
                )}
              </span>
              <span className="text-sm">
                {l.securityAmountSet ? (
                  <>
                    <span className="text-fg-muted">posted </span>
                    <span className="font-medium text-fg">${l.securityAmountCad}</span>
                  </>
                ) : (
                  <span className="text-warning-fg">no security amount recorded</span>
                )}
              </span>
            </div>

            <dl className="grid gap-2 sm:grid-cols-4">
              <Fig label="Filed, treated as owing" v={formatCAD(l.filedDutyCad)} />
              <Fig label="Crystallised, unfiled" v={formatCAD(l.unfiledDutyCad)} />
              <Fig
                label="Would fall on stock held"
                v={l.contingentPriced ? formatCAD(l.contingentDutyCad) : `${formatLAA(l.contingentLaa)} L LAA`}
              />
              <Fig label="Total exposure" v={formatCAD(l.totalExposureCad)} strong />
            </dl>

            {l.headroomKnown && (
              <p className={`mt-3 text-sm ${l.headroomCad < 0 ? "text-danger-fg" : "text-fg-muted"}`}>
                {l.headroomCad >= 0 ? (
                  <>Posted security exceeds the exposure by {formatCAD(l.headroomCad)}.</>
                ) : (
                  <>
                    The exposure is {formatCAD(-l.headroomCad)} larger than the posted
                    security. Whether that means the security is insufficient is CRA's
                    call, not this screen's.
                  </>
                )}
              </p>
            )}

            <p className="mt-3 text-xs text-fg-subtle">{l.basis}</p>
            {l.caveats.map((c, i) => (
              <p key={i} className="mt-2 rounded border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-fg">
                {c}
              </p>
            ))}
          </div>
        ))}
      </div>
    </section>
  );
}

function Fig({ label, v, strong }: { label: string; v: string; strong?: boolean }) {
  return (
    <div>
      <dt className="text-xs text-fg-muted">{label}</dt>
      <dd className={`mt-0.5 font-mono ${strong ? "font-semibold text-fg" : "text-fg-muted"}`}>{v}</dd>
    </div>
  );
}
