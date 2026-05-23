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
  materialClient,
  recipeClient,
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
  const materials = useQuery({ queryKey: ["listMaterials"], queryFn: () => materialClient.listMaterials({}) });
  const recipes = useQuery({ queryKey: ["listRecipes"], queryFn: () => recipeClient.listRecipes({}) });
  const bulk = useQuery({ queryKey: ["listBulkContainers"], queryFn: () => bulkClient.listBulkContainers({}) });
  const barrels = useQuery({ queryKey: ["listBarrels"], queryFn: () => barrelClient.listBarrels({}) });
  const packaged = useQuery({ queryKey: ["listPackagedInventory"], queryFn: () => bottlingClient.listPackagedInventory({}) });
  const stamps = useQuery({ queryKey: ["listStampOrders"], queryFn: () => exciseStampClient.listStampOrders({}) });
  const b266 = useQuery({
    queryKey: ["generateB266", "current"],
    queryFn: () => b266Client.generateB266({ periodStart: start, periodEnd: end }),
  });
  const audit = useQuery({ queryKey: ["listAuditEvents", "home"], queryFn: () => auditClient.listAuditEvents({ limit: 8 }) });

  const allLoaded = !materials.isLoading && !recipes.isLoading && !bulk.isLoading
    && !packaged.isLoading && !stamps.isLoading;
  const hasMaterials = (materials.data?.materials.length ?? 0) > 0;
  const hasRecipes = (recipes.data?.recipes.filter((r) => r.currentVersionId).length ?? 0) > 0;
  const hasBulkContainers = (bulk.data?.containers.length ?? 0) > 0;
  const hasStamps = (stamps.data?.summaries.reduce((s, j) => s + j.totalReceived, 0) ?? 0) > 0;
  const hasBottling = (packaged.data?.rows.length ?? 0) > 0;
  const completedAll = hasMaterials && hasRecipes && hasBulkContainers && hasStamps && hasBottling;
  const isFreshInstall = allLoaded && !hasMaterials && !hasRecipes && !hasBulkContainers && !hasStamps && !hasBottling;

  const totalBulk = bulk.data?.summary?.totalLaa ?? 0;
  const barrelLAA = barrels.data?.barrels.reduce((s, b) => s + b.currentLaa, 0) ?? 0;
  const nonBarrelBulk = totalBulk - barrelLAA;
  const packagedLAA = packaged.data?.rows.reduce((s, r) => s + (r.bottlesOnHand * r.bottleSizeMl * r.targetAbvPct) / 100000, 0) ?? 0;
  const packagedBottles = packaged.data?.rows.reduce((s, r) => s + r.bottlesOnHand, 0) ?? 0;
  const eligibleBarrels = barrels.data?.barrels.filter((b) => b.canadianWhiskyEligible).length ?? 0;
  const totalBarrels = barrels.data?.barrels.length ?? 0;
  const totalStampsOnHand = stamps.data?.summaries.reduce((s, j) => s + j.totalOnHand, 0) ?? 0;

  return (
    <Shell>
      <h1 className="mb-1 text-3xl font-bold tracking-tight">Dashboard</h1>
      <p className="mb-8 text-sm text-fg-muted">
        {isFreshInstall
          ? "Welcome to Stillhouse. Walk through the steps below to get from a fresh install to your first B266."
          : "Live snapshot of everything Stillhouse is currently tracking."}
      </p>

      <ReadyToDumpCallout barrels={barrels.data?.barrels ?? []} />
      <B266DueCallout periodEnd={end} hasBottling={hasBottling} />
      <StampLowStockCallout summaries={stamps.data?.summaries ?? []} />
      <StagnantBulkCallout containers={bulk.data?.containers ?? []} />
      <CWForecastSection barrels={barrels.data?.barrels ?? []} />

      {!completedAll && allLoaded && (
        <section className="mb-8 rounded-lg border border-border bg-surface-2 p-5 shadow-sm">
          <h2 className="mb-3 text-sm font-semibold text-fg-muted">Getting started</h2>
          <ol className="space-y-2 text-sm">
            <Step n={1} done={hasMaterials} to="/materials" label="Add your raw materials (grain, malt, water)." />
            <Step n={2} done={hasRecipes} to="/recipes" label="Save at least one recipe with a saved version." />
            <Step n={3} done={hasBulkContainers} to="/bulk" label="Define your bulk containers (spirit receivers, tanks)." />
            <Step n={4} done={hasStamps} to="/stamps" label="Order CRA excise stamps for the provinces you'll sell in." />
            <Step n={5} done={hasBottling} to="/bottling" label="Run a bottling to produce packaged inventory." />
          </ol>
          <p className="mt-3 text-xs text-fg-muted">
            You can also use the full nav at left to skip ahead. Mash, fermentation, distillation, and barrel pages are ready
            whenever you start tracking production.
          </p>
        </section>
      )}

      {(hasBulkContainers || hasBottling || hasStamps) && (
        <section className="mb-8">
          <h2 className="mb-3 text-xs font-semibold text-fg-muted">Alcohol on hand (LAA)</h2>
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
      )}

      {hasBottling && (
        <section className="mb-8">
          <h2 className="mb-3 text-xs font-semibold text-fg-muted">
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
      )}

      {(audit.data?.events.length ?? 0) > 0 && (
        <section>
          <h2 className="mb-3 text-xs font-semibold text-fg-muted">Recent activity</h2>
          <div className="overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
            <table className="min-w-full divide-y divide-border text-sm">
              <thead className="bg-surface-3 text-left text-xs text-fg-muted">
                <tr>
                  <th className="px-4 py-2">When</th>
                  <th className="px-4 py-2">Who</th>
                  <th className="px-4 py-2">Action</th>
                  <th className="px-4 py-2">Entity</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {audit.data?.events.map((e) => (
                  <tr key={e.id}>
                    <td className="px-4 py-2 text-fg-muted">
                      {e.occurredAt ? new Date(Number(e.occurredAt.seconds) * 1000).toLocaleString() : ""}
                    </td>
                    <td className="px-4 py-2 text-fg-muted">{e.userDisplayName || e.userEmail || "system"}</td>
                    <td className="px-4 py-2 text-fg">{actionLabels[e.action]}</td>
                    <td className="px-4 py-2 font-mono text-xs text-fg-muted">
                      {e.entityType} <span className="text-fg-subtle">{e.entityId.slice(0, 8)}</span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <p className="mt-2 text-right text-xs text-fg-muted">
            <Link to="/audit" className="hover:text-fg">Full audit log →</Link>
          </p>
        </section>
      )}
    </Shell>
  );
}

// CWForecastSection projects when in-cask LAA will cross the Canadian
// Whisky 3-year eligibility threshold, binned by calendar quarter. Eligible
// LAA today shows in the first bucket; a barrel 200 days from CW shows in
// whatever quarter that lands in. Non-small-wood barrels are excluded —
// they can age forever without becoming CW.
function CWForecastSection({ barrels }: { barrels: { currentLaa: number; canadianWhiskyEligible: boolean; smallWood: boolean; daysToCanadianWhiskyEligible: number }[] }) {
  const eligible = barrels.filter((b) => b.smallWood && b.currentLaa > 0);
  if (eligible.length === 0) return null;
  // 6 quarter-buckets ahead, plus a "ready now" bucket.
  const buckets: { label: string; laa: number; count: number }[] = [
    { label: "Ready now", laa: 0, count: 0 },
  ];
  const now = new Date();
  for (let i = 0; i < 6; i++) {
    const start = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth() + i * 3, 1));
    buckets.push({ label: `${["Q1","Q2","Q3","Q4"][Math.floor(start.getUTCMonth() / 3)]} ${start.getUTCFullYear()}`, laa: 0, count: 0 });
  }
  for (const b of eligible) {
    if (b.canadianWhiskyEligible) {
      buckets[0].laa += b.currentLaa;
      buckets[0].count++;
      continue;
    }
    const target = new Date(now.getTime() + b.daysToCanadianWhiskyEligible * 86_400_000);
    const monthsFromNow = (target.getUTCFullYear() - now.getUTCFullYear()) * 12 + (target.getUTCMonth() - now.getUTCMonth());
    const idx = 1 + Math.floor(monthsFromNow / 3);
    if (idx >= 1 && idx < buckets.length) {
      buckets[idx].laa += b.currentLaa;
      buckets[idx].count++;
    }
  }
  // Drop trailing zero buckets so a small operation doesn't waste real estate.
  while (buckets.length > 1 && buckets[buckets.length - 1].count === 0) {
    buckets.pop();
  }
  return (
    <section className="mb-8">
      <h2 className="mb-3 text-xs font-semibold text-fg-muted">Canadian Whisky maturation forecast</h2>
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-7">
        {buckets.map((b, i) => (
          <div key={b.label} className={`rounded-lg border p-3 ${i === 0 ? "border-emerald-500/30 bg-emerald-500/10" : "border-border bg-surface-2"}`}>
            <p className="text-xs text-fg-muted">{b.label}</p>
            <p className={`mt-1 text-base font-semibold ${i === 0 ? "text-emerald-400" : "text-fg"}`}>
              {b.laa.toFixed(1)} L
            </p>
            <p className="text-xs text-fg-muted">{b.count} barrel{b.count === 1 ? "" : "s"}</p>
          </div>
        ))}
      </div>
      <p className="mt-2 text-xs text-fg-muted">
        Only small-wood barrels (≤700 L) shown — FDR B.02.020 excludes larger casks from the CW class.
      </p>
    </section>
  );
}

function ReadyToDumpCallout({ barrels }: { barrels: { id: string; name: string; currentLaa: number; canadianWhiskyEligible: boolean; daysAged: number }[] }) {
  const ready = barrels.filter((b) => b.currentLaa > 0 && b.canadianWhiskyEligible);
  if (ready.length === 0) return null;
  return (
    <section className="mb-6 rounded-lg border border-emerald-500/30 bg-emerald-500/10 p-4">
      <p className="text-sm text-emerald-300">
        <span className="font-semibold">{ready.length} barrel{ready.length > 1 ? "s" : ""}</span> hit Canadian Whisky eligibility
        and still hold alcohol — ready to dump for bottling.{" "}
        <Link to="/barrels" className="underline">Open barrels →</Link>
      </p>
    </section>
  );
}

function B266DueCallout({ periodEnd, hasBottling }: { periodEnd: string; hasBottling: boolean }) {
  if (!hasBottling) return null;
  // B266 is due by the last day of the month following the reporting period.
  const end = new Date(periodEnd + "T00:00:00Z");
  const dueDate = new Date(Date.UTC(end.getUTCFullYear(), end.getUTCMonth() + 2, 0));
  const today = new Date();
  const daysToDue = Math.ceil((dueDate.getTime() - today.getTime()) / (1000 * 60 * 60 * 24));
  const overdue = daysToDue < 0;
  const urgent = daysToDue <= 7 && !overdue;
  const colour = overdue
    ? "border-red-500/40 bg-red-500/10 text-red-300"
    : urgent
      ? "border-amber-500/40 bg-amber-500/10 text-amber-300"
      : "border-border bg-surface-2 text-fg";
  return (
    <section className={`mb-6 rounded-lg border p-4 ${colour}`}>
      <p className="text-sm">
        {overdue && <span className="mr-2 rounded bg-red-700 px-1.5 py-0.5 text-xs font-semibold text-white">OVERDUE</span>}
        <span className="font-semibold">B266 for {periodEnd}</span> is due {dueDate.toISOString().slice(0, 10)}
        {" "}({daysToDue >= 0 ? `${daysToDue} day${daysToDue === 1 ? "" : "s"} from now` : `${-daysToDue} day${-daysToDue === 1 ? "" : "s"} overdue — file as soon as possible`}).
        {" "}<Link to="/b266" className="underline">Open the return →</Link>
      </p>
    </section>
  );
}

// StagnantBulkCallout warns when a non-barrel container (tank, blend tank,
// spirit receiver, etc.) has had no movement for 90+ days AND holds
// non-trivial alcohol. Barrels intentionally excluded — long stagnation
// IS the goal for aging spirit.
function StagnantBulkCallout({ containers }: { containers: { id: string; name: string; kind: number; currentLaa: number; lastMovementAt?: { seconds: bigint }; createdAt?: { seconds: bigint }; archived: boolean }[] }) {
  const STAGNANT_DAYS = 90;
  const flagged: { id: string; name: string; days: number; currentLaa: number }[] = [];
  for (const c of containers) {
    if (c.archived || c.kind === 7 /* BARREL */ || c.currentLaa < 1) continue;
    const ts = c.lastMovementAt ?? c.createdAt;
    if (!ts) continue;
    const days = Math.floor((Date.now() - Number(ts.seconds) * 1000) / 86_400_000);
    if (days >= STAGNANT_DAYS) flagged.push({ id: c.id, name: c.name, days, currentLaa: c.currentLaa });
  }
  flagged.sort((a, b) => b.days - a.days);
  flagged.splice(5);
  if (flagged.length === 0) return null;
  return (
    <section className="mb-6 rounded-lg border border-amber-500/40 bg-amber-500/10 p-4">
      <p className="text-sm text-amber-300">
        <span className="font-semibold">Stagnant bulk inventory:</span>{" "}
        {flagged.map((c, i) => (
          <span key={c.id}>
            {i > 0 && ", "}
            {c.name} ({c.days}d, {c.currentLaa.toFixed(0)} L LAA)
          </span>
        ))} — no movement in 90+ days. Evap losses accrue silently; consider
        gauging or transferring.{" "}
        <Link to="/bulk" className="underline">Open bulk →</Link>
      </p>
    </section>
  );
}

function StampLowStockCallout({ summaries }: { summaries: { jurisdiction: string; totalOnHand: number; bottlesPerDay30d: number }[] }) {
  // "Days of stock" = on-hand / 30-day bottling rate. Tighter signal than a
  // static threshold — a 500-stamp safety net is plenty for a quiet province
  // and dangerously low for a busy one.
  const WARN_DAYS = 14;
  const flagged = summaries
    .filter((s) => s.totalOnHand > 0 && s.bottlesPerDay30d > 0)
    .map((s) => ({ ...s, daysLeft: s.totalOnHand / s.bottlesPerDay30d }))
    .filter((s) => s.daysLeft < WARN_DAYS);
  if (flagged.length === 0) return null;
  return (
    <section className="mb-6 rounded-lg border border-amber-500/40 bg-amber-500/10 p-4">
      <p className="text-sm text-amber-300">
        <span className="font-semibold">Stamps running low:</span>{" "}
        {flagged.map((s, i) => (
          <span key={s.jurisdiction}>
            {i > 0 && ", "}
            {s.jurisdiction} (~{Math.floor(s.daysLeft)} day{Math.floor(s.daysLeft) === 1 ? "" : "s"} left at current bottling rate)
          </span>
        ))}. CRA orders take weeks — place a replenishment order now.{" "}
        <Link to="/stamps" className="underline">Open stamps →</Link>
      </p>
    </section>
  );
}

function Step({ n, done, to, label }: { n: number; done: boolean; to: string; label: string }) {
  return (
    <li className="flex items-start gap-3">
      <span className={`mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-full text-xs font-medium ${
        done ? "bg-emerald-600 text-white" : "bg-stone-200 text-fg-muted"
      }`}>
        {done ? "✓" : n}
      </span>
      <Link to={to} className={done ? "text-fg-muted line-through" : "text-fg hover:underline"}>
        {label}
      </Link>
    </li>
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
      className={`block rounded-lg border bg-surface-2 p-5 shadow-sm transition hover:border-border-strong ${
        highlight ? "border-emerald-500/30" : "border-border"
      }`}
    >
      <p className="text-xs text-fg-muted">{label}</p>
      <p className={`mt-2 text-3xl font-bold tracking-tight ${highlight ? "text-emerald-400" : "text-fg"}`}>
        {value}
        {suffix && <span className="ml-1 text-base font-normal text-fg-muted">{suffix}</span>}
      </p>
      {sub && <p className="mt-1 text-xs text-fg-muted">{sub}</p>}
    </Link>
  );
}
