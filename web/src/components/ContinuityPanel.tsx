import { Callout } from "@/components/Callout";
import { formatLAA } from "@/lib/format";
import type { B266Continuity } from "@/gen/stillhouse/v1/b266_pb";

/**
 * ContinuityPanel — whether this return continues the last one filed.
 *
 * Worth understanding why this is the only check on the page that can
 * actually fail. Every other figure on a B266 is derived from the same
 * ledger, and the opening balance in particular is reverse-walked from
 * closing — so the return balances against itself no matter what is
 * missing from it. A movement nobody recorded doesn't show up as a
 * discrepancy; it quietly becomes part of the opening balance.
 *
 * The previous return's closing balance is different in kind. It is a
 * number already sent to CRA, and it does not move when the ledger moves
 * underneath it. Comparing against it is the one way to notice.
 *
 * Three states, and the difference between the first two matters: not
 * checked (nothing to compare against — a first return) is not the same
 * as checked and clean, and showing them the same way would tell an
 * operator their books tie out when nothing looked at them.
 */
export function ContinuityPanel({ c }: { c: B266Continuity | undefined }) {
  if (!c) return null;

  if (!c.checked) {
    return (
      <Callout tone="info" title="No filed return to check this against">
        <p>
          Opening balances here are walked back from what is on hand now. That
          arithmetic always ties out, so it cannot tell you whether anything is
          missing — only a previously filed return can, and there isn't one yet.
        </p>
        <p className="mt-2 text-xs">
          Once this period is filed, the next one will be compared against its
          closing balance of {formatLAA(c.bulkOpeningLaa)}.
        </p>
      </Callout>
    );
  }

  const tolerance = 0.0001;
  const bulkOff = Math.abs(c.bulkDiscrepancyLaa) > tolerance;
  const packagedOff = Math.abs(c.packagedDiscrepancyLaa) > tolerance;
  const clean = !bulkOff && !packagedOff;

  if (clean) {
    return (
      <Callout tone="success" title="Continues the last filed return">
        <p>
          Opening balances match what the return for {c.priorPeriodStart} to{" "}
          {c.priorPeriodEnd} closed at — {formatLAA(c.priorBulkClosingLaa)} bulk,{" "}
          {formatLAA(c.priorPackagedClosingLaa)} packaged. Nothing has been
          recorded against that period since it was filed.
        </p>
        {c.gap && <p className="mt-2 text-xs">{c.gapNote}</p>}
      </Callout>
    );
  }

  return (
    <Callout tone="danger" title="This return does not continue the last one filed">
      <table className="w-full text-sm">
        <thead>
          <tr className="text-left text-xs uppercase opacity-70">
            <th className="py-1 pr-3 font-medium">&nbsp;</th>
            <th className="py-1 pr-3 font-medium">
              Filed {c.priorPeriodStart} → {c.priorPeriodEnd}
            </th>
            <th className="py-1 pr-3 font-medium">Opening here</th>
            <th className="py-1 font-medium">Difference</th>
          </tr>
        </thead>
        <tbody>
          <Line
            label="Bulk"
            prior={c.priorBulkClosingLaa}
            opening={c.bulkOpeningLaa}
            diff={c.bulkDiscrepancyLaa}
            off={bulkOff}
          />
          <Line
            label="Packaged"
            prior={c.priorPackagedClosingLaa}
            opening={c.packagedOpeningLaa}
            diff={c.packagedDiscrepancyLaa}
            off={packagedOff}
          />
        </tbody>
      </table>

      {c.gap && <p className="mt-3 text-xs">{c.gapNote}</p>}

      {c.backdated.length > 0 ? (
        <div className="mt-3">
          <p className="text-sm">
            Recorded against the filed period after it was filed
            {c.backdatedTruncated > 0 && (
              <>
                {" "}
                — showing the largest {c.backdated.length} of{" "}
                {c.backdated.length + c.backdatedTruncated}
              </>
            )}
            , {formatLAA(c.backdatedNetLaa)} in total:
          </p>
          <table className="mt-2 w-full text-sm">
            <thead>
              <tr className="text-left text-xs uppercase opacity-70">
                <th className="py-1 pr-3 font-medium">Dated</th>
                <th className="py-1 pr-3 font-medium">Entered</th>
                <th className="py-1 pr-3 font-medium">Container</th>
                <th className="py-1 pr-3 font-medium">Reason</th>
                <th className="py-1 text-right font-medium">Effect</th>
              </tr>
            </thead>
            <tbody>
              {c.backdated.map((e) => (
                <tr key={e.id} className="border-t border-current/10">
                  <td className="py-1 pr-3 tabular-nums">{e.occurredAt}</td>
                  <td className="py-1 pr-3 tabular-nums">{e.createdAt}</td>
                  <td className="py-1 pr-3">{e.container || "—"}</td>
                  <td className="py-1 pr-3">{e.reason}</td>
                  <td className="py-1 text-right tabular-nums">
                    {e.laa > 0 ? "+" : ""}
                    {formatLAA(e.laa)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          <p className="mt-2 text-xs">
            Either amend the filed return so it accounts for these, or move them
            to the period they belong in. Stillhouse won't do either for you —
            which one is right depends on what actually happened.
          </p>
        </div>
      ) : (
        <p className="mt-3 text-xs">
          Nothing was recorded against the filed period after it was filed, so a
          late entry doesn't explain this. Check the closing balance on the
          filed return itself.
        </p>
      )}
    </Callout>
  );
}

function Line({
  label,
  prior,
  opening,
  diff,
  off,
}: {
  label: string;
  prior: number;
  opening: number;
  diff: number;
  off: boolean;
}) {
  return (
    <tr className="border-t border-current/10">
      <td className="py-1 pr-3">{label}</td>
      <td className="py-1 pr-3 tabular-nums">{formatLAA(prior)}</td>
      <td className="py-1 pr-3 tabular-nums">{formatLAA(opening)}</td>
      <td className={`py-1 tabular-nums ${off ? "font-semibold" : "opacity-60"}`}>
        {off ? `${diff > 0 ? "+" : ""}${formatLAA(diff)}` : "—"}
      </td>
    </tr>
  );
}
