import { useQuery } from "@tanstack/react-query";

import { b266Client } from "@/lib/clients";
import { B266Status } from "@/gen/stillhouse/v1/b266_pb";

/**
 * LockedPeriodHint — show a small warning when `date` falls inside a
 * submitted (locked) B266 period. The backend will reject the mutation
 * with FailedPrecondition anyway, but surfacing the conflict before
 * submit saves the operator a round-trip and explains *why* the date
 * is bad.
 *
 * Pass an empty string while the field is blank — nothing renders.
 */
export function LockedPeriodHint({ date }: { date: string }) {
  // Cached by listB266Periods — pages already use this key.
  const periods = useQuery({
    queryKey: ["listB266Periods"],
    queryFn: () => b266Client.listB266Periods({}),
  });
  if (!date) return null;
  const submitted = (periods.data?.periods ?? []).filter((p) => p.status === B266Status.SUBMITTED);
  // Date comparison is lexicographic on YYYY-MM-DD strings — no need to parse.
  const hit = submitted.find((p) => date >= p.periodStart && date <= p.periodEnd);
  if (!hit) return null;
  return (
    <p className="mt-1 text-xs text-warning-fg">
      {date} falls in submitted B266 period {hit.periodStart} → {hit.periodEnd}.
      Reopen the period or pick a date after {hit.periodEnd}.
    </p>
  );
}
