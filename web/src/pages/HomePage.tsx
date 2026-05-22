import { Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";

import { Shell } from "@/components/Shell";
import {
  auditClient,
  b266Client,
  barrelClient,
  bottlingClient,
  bulkClient,
  exciseStampClient,
} from "@/lib/clients";
import { AuditAction } from "@/gen/stillhouse/v1/audit_pb";
import { formatLAA, formatQty } from "@/lib/format";

function monthBounds(): { start: string; end: string } {
  const d = new Date();
  const start = new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), 1));
  const end = new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth() + 1, 0));
  return { start: start.toISOString().slice(0, 10), end: end.toISOString().slice(0, 10) };
}

const actionLabels: Record<AuditAction, string> = {
  [AuditAction.UNSPECIFIED]: "—",
  [AuditAction.CREATE]: "Create",
  [AuditAction.UPDATE]: "Update",
  [AuditAction.DELETE]: "Delete",
  [AuditAction.SIGN]: "Sign",
  [AuditAction.LOGIN]: "Login",
  [AuditAction.LOGOUT]: "Logout",
};

export function HomePage() {
  const { start, end } = monthBounds();
  const bulk = useQuery({
    queryKey: ["listBulkContainers"],
    queryFn: () => bulkClient.listBulkContainers({}),
  });
  const barrels = useQuery({
    queryKey: ["listBarrels"],
    queryFn: () => barrelClient.listBarrels({}),
  });
  const packaged = useQuery({
    queryKey: ["listPackagedInventory"],
    queryFn: () => bottlingClient.listPackagedInventory({}),
  });
  const stamps = useQuery({
    queryKey: ["listStampOrders"],
    queryFn: () => exciseStampClient.listStampOrders({}),
  });
  const b266 = useQuery({
    queryKey: ["generateB266", "current"],
    queryFn: () => b266Client.generateB266({ periodStart: start, periodEnd: end }),
  });
  const audit = useQuery({
    queryKey: ["listAuditEvents", "home"],
    queryFn: () => auditClient.listAuditEvents({ limit: 8 }),
  });

  const totalBulk = bulk.data?.summary?.totalLaa ?? 0;
  const barrelLAA = barrels.data?.barrels.reduce((s, b) => s + b.currentLaa, 0) ?? 0;
  // Bulk LAA already includes barrels (they're a kind of bulk_container).
  const nonBarrelBulk = totalBulk - barrelLAA;
  const packagedLAA =
    packaged.data?.rows.reduce(
      (s, r) => s + (r.bottlesOnHand * r.bottleSizeMl * r.targetAbvPct) / 100000,
      0,
    ) ?? 0;
  const packagedBottles =
    packaged.data?.rows.reduce((s, r) => s + r.bottlesOnHand, 0) ?? 0;
  const eligibleBarrels =
    barrels.data?.barrels.filter((b) => b.canadianWhiskyEligible).length ?? 0;
  const totalBarrels = barrels.data?.barrels.length ?? 0;
  const totalStampsOnHand =
    stamps.data?.summaries.reduce((s, j) => s + j.totalOnHand, 0) ?? 0;

  return (
    <Shell>
      <h1 className="mb-1 text-2xl font-semibold">Dashboard</h1>
      <p className="mb-8 text-sm text-stone-500">Live snapshot of everything Stillhouse is currently tracking.</p>

      <section className="mb-8">
        <h2 className="mb-3 text-xs font-semibold uppercase text-stone-500">Alcohol on hand (LAA)</h2>
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
          <Stat to="/bulk" label="Bulk tanks" value={formatLAA(nonBarrelBulk)} suffix="L" />
          <Stat
            to="/barrels"
            label="Barrels"
            value={formatLAA(barrelLAA)}
            suffix="L"
            sub={`${totalBarrels} barrels · ${eligibleBarrels} CW eligible`}
            highlight={eligibleBarrels > 0}
          />
          <Stat
            to="/bottling"
            label="Packaged"
            value={formatLAA(packagedLAA)}
            suffix="L"
            sub={`${packagedBottles.toLocaleString()} bottles on hand`}
          />
          <Stat to="/stamps" label="Excise stamps on hand" value={totalStampsOnHand.toLocaleString()} />
        </div>
      </section>

      <section className="mb-8">
        <h2 className="mb-3 text-xs font-semibold uppercase text-stone-500">
          Current period · {start} → {end}
        </h2>
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-3">
          <Stat
            to="/bottling"
            label="Bottled this period"
            value={formatLAA(b266.data?.report?.packagedPackagedLaa ?? 0)}
            suffix="L"
            sub={`${(b266.data?.report?.packagedPackagedBottles ?? 0).toLocaleString()} bottles`}
          />
          <Stat
            to="/removals"
            label="Removed (duty-paid)"
            value={formatLAA(b266.data?.report?.packagedRemovedDutyPaidLaa ?? 0)}
            suffix="L"
            sub={`${(b266.data?.report?.packagedRemovedDutyPaidBottles ?? 0).toLocaleString()} bottles`}
          />
          <Stat
            to="/b266"
            label="Duty payable (CAD)"
            value={`$${formatQty(b266.data?.report?.dutyPayableCad ?? 0)}`}
            highlight
          />
        </div>
      </section>

      <section>
        <h2 className="mb-3 text-xs font-semibold uppercase text-stone-500">Recent activity</h2>
        <div className="overflow-hidden rounded-lg border border-stone-200 bg-white shadow-sm">
          <table className="min-w-full divide-y divide-stone-200 text-sm">
            <thead className="bg-stone-50 text-left text-xs uppercase text-stone-500">
              <tr>
                <th className="px-4 py-2">When</th>
                <th className="px-4 py-2">Who</th>
                <th className="px-4 py-2">Action</th>
                <th className="px-4 py-2">Entity</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-stone-100">
              {audit.data?.events.length === 0 && (
                <tr><td colSpan={4} className="px-4 py-2 text-stone-500">No activity yet.</td></tr>
              )}
              {audit.data?.events.map((e) => (
                <tr key={e.id}>
                  <td className="px-4 py-2 text-stone-600">
                    {e.occurredAt ? new Date(Number(e.occurredAt.seconds) * 1000).toLocaleString() : ""}
                  </td>
                  <td className="px-4 py-2 text-stone-600">{e.userDisplayName || e.userEmail || "system"}</td>
                  <td className="px-4 py-2 text-stone-900">{actionLabels[e.action]}</td>
                  <td className="px-4 py-2 font-mono text-xs text-stone-600">
                    {e.entityType} <span className="text-stone-400">{e.entityId.slice(0, 8)}</span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <p className="mt-2 text-right text-xs text-stone-500">
          <Link to="/audit" className="hover:text-stone-900">Full audit log →</Link>
        </p>
      </section>
    </Shell>
  );
}

function Stat({
  to, label, value, suffix, sub, highlight,
}: {
  to: string;
  label: string;
  value: string;
  suffix?: string;
  sub?: string;
  highlight?: boolean;
}) {
  return (
    <Link
      to={to}
      className={`block rounded-lg border bg-white p-5 shadow-sm transition hover:border-stone-400 ${
        highlight ? "border-emerald-200" : "border-stone-200"
      }`}
    >
      <p className="text-xs uppercase text-stone-500">{label}</p>
      <p className={`mt-2 text-2xl font-semibold ${highlight ? "text-emerald-700" : "text-stone-900"}`}>
        {value}
        {suffix && <span className="ml-1 text-base font-normal text-stone-500">{suffix}</span>}
      </p>
      {sub && <p className="mt-1 text-xs text-stone-500">{sub}</p>}
    </Link>
  );
}
