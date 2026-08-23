import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { EmptyRow } from "@/components/EmptyState";
import { WIPProductionTab } from "@/components/WIPProductionTab";
import { Shell } from "@/components/Shell";
import { costingClient } from "@/lib/clients";
import { OverheadBasis } from "@/gen/stillhouse/v1/costing_pb";
import { formatCAD, formatLAA } from "@/lib/format";
import { OwnerOnly } from "@/lib/role";

const basisLabel: Record<number, string> = {
  [OverheadBasis.UNSPECIFIED]: "—",
  [OverheadBasis.PER_MATERIAL_DOLLAR]: "per material dollar",
  [OverheadBasis.PER_LABOUR_HOUR]: "per labour hour",
  [OverheadBasis.PER_LAA]: "per LAA",
};

function errText(e: unknown): string {
  return e instanceof ConnectError ? e.rawMessage : String(e);
}

export function CostingPage() {
  const [tab, setTab] = useState<"value" | "wip" | "rates">("value");
  return (
    <Shell>
      <div className="mb-6">
        <h1 className="text-3xl font-bold tracking-tight">Costing</h1>
        <p className="text-sm text-fg-muted">
          What a batch cost beyond the grain, and what the alcohol on hand is
          worth. The rates are your policy, not something Stillhouse knows —
          an unset rate makes its component unavailable and says so, rather
          than absorbing zero and calling a partial cost a full one.
        </p>
      </div>

      <div className="mb-4 flex gap-1 border-b border-border text-sm">
        {([["value", "Inventory value"], ["wip", "Into WIP"], ["rates", "Rates"]] as const).map(([k, label]) => (
          <button
            key={k}
            onClick={() => setTab(k)}
            className={`-mb-px border-b-2 px-3 py-2 ${
              tab === k ? "border-accent text-fg" : "border-transparent text-fg-muted hover:text-fg"
            }`}
          >
            {label}
          </button>
        ))}
      </div>

      {tab === "value" && <ValueTab />}
      {tab === "wip" && <WIPProductionTab />}
      {tab === "rates" && <RatesTab />}
    </Shell>
  );
}

function ValueTab() {
  const v = useQuery({
    queryKey: ["inventoryValue"],
    queryFn: () => costingClient.inventoryValue({}),
  });
  const d = v.data;
  if (v.isLoading) return <p className="text-sm text-fg-muted">Valuing…</p>;
  if (!d) return null;

  return (
    <div className="space-y-6">
      <section className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <Stat label="Work in progress" value={formatCAD(d.wip?.valueCad)} />
        <Stat label="Finished goods" value={formatCAD(d.finishedGoods?.valueCad)} />
        <Stat label="Total" value={formatCAD(d.totalCad)} highlight />
      </section>
      <p className="text-xs text-fg-subtle">{d.basis}</p>

      <Bucket title="Work in progress" sub="Made, not packaged. Casks included — a maturing cask is the largest WIP a whisky distillery has." b={d.wip} />
      <Bucket title="Finished goods" sub="Packaged, not sold." b={d.finishedGoods} />
    </div>
  );
}

type BucketT = NonNullable<Awaited<ReturnType<typeof costingClient.inventoryValue>>["wip"]>;

function Bucket({ title, sub, b }: { title: string; sub: string; b?: BucketT }) {
  if (!b) return null;
  const covered = b.totalLaa > 0 ? (b.valuedLaa / b.totalLaa) * 100 : 100;
  return (
    <section>
      <h2 className="text-sm font-semibold text-fg">{title}</h2>
      <p className="mb-2 text-xs text-fg-subtle">{sub}</p>
      {b.unvalued > 0 && (
        <p className="mb-2 rounded border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-fg">
          {b.unvalued} line{b.unvalued === 1 ? "" : "s"} could not be valued, so this
          figure covers {covered.toFixed(0)} % of the {formatLAA(b.totalLaa)} L LAA
          here. A valuation that quietly omits what it could not price reads as a
          smaller inventory rather than an incomplete one.
        </p>
      )}
      <div className="overflow-hidden rounded-lg border border-border bg-surface-2">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-2">What</th>
              <th className="px-4 py-2 text-right">LAA</th>
              <th className="px-4 py-2 text-right">Bottles</th>
              <th className="px-4 py-2 text-right">Unit</th>
              <th className="px-4 py-2 text-right">Value</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {b.lines.length === 0 && (
              <EmptyRow colSpan={5} title="Nothing here" message="No stock in this bucket." />
            )}
            {b.lines.map((l, i) => (
              <tr key={`${l.name}-${i}`}>
                <td className="px-4 py-2">
                  <span className="text-fg">{l.name}</span>
                  <span className="ml-2 text-xs text-fg-muted">{l.detail}</span>
                  {/* `why` now covers both "could not be valued" and
                      "valued at an incomplete cost" — a figure short by
                      its materials is worth more than nothing and less
                      than it looks. */}
                  {l.why && <div className="text-xs text-warning-fg">{l.why}</div>}
                </td>
                <td className="px-4 py-2 text-right text-fg-muted">{formatLAA(l.laa)}</td>
                <td className="px-4 py-2 text-right text-fg-muted">{l.bottles || "—"}</td>
                <td className="px-4 py-2 text-right text-fg-muted">
                  {l.valued ? formatCAD(l.unitCad) : "—"}
                </td>
                <td className="px-4 py-2 text-right font-medium text-fg">
                  {l.valued ? formatCAD(l.valueCad) : "—"}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function RatesTab() {
  const qc = useQueryClient();
  const [adding, setAdding] = useState(false);
  const rates = useQuery({
    queryKey: ["listCostRates"],
    queryFn: () => costingClient.listCostRates({}),
  });
  const save = useMutation({
    mutationFn: (m: Parameters<typeof costingClient.saveCostRates>[0]) =>
      costingClient.saveCostRates(m),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["listCostRates"] });
      qc.invalidateQueries({ queryKey: ["inventoryValue"] });
      setAdding(false);
    },
  });
  const remove = useMutation({
    mutationFn: (m: Parameters<typeof costingClient.deleteCostRates>[0]) =>
      costingClient.deleteCostRates(m),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["listCostRates"] });
      qc.invalidateQueries({ queryKey: ["inventoryValue"] });
    },
  });

  return (
    <div className="space-y-4">
      <p className="text-sm text-fg-muted">
        Effective-dated on purpose. A rate is a fact about a period; changing one
        retroactively restates every batch already costed, including those an
        accountant has taken into a set of books. A March run is costed at March's
        rates however long afterwards you ask.
      </p>
      <OwnerOnly>
        <button
          onClick={() => setAdding((v) => !v)}
          className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover"
        >
          {adding ? "Cancel" : "Set rates from a date"}
        </button>
      </OwnerOnly>

      {adding && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            const fd = new FormData(e.currentTarget);
            save.mutate({
              effectiveFrom: fd.get("effective_from")?.toString() ?? "",
              labourRateCadPerHour: fd.get("labour")?.toString() ?? "",
              overheadBasis: Number(fd.get("basis") ?? 0) as OverheadBasis,
              overheadRate: fd.get("overhead")?.toString() ?? "",
              notes: fd.get("notes")?.toString() ?? "",
            });
          }}
          className="grid gap-3 rounded-lg border border-border bg-surface-2 p-5 sm:grid-cols-3"
        >
          <Field label="Effective from (blank = today)" name="effective_from" type="date" />
          <Field label="Labour, CAD per hour" name="labour" placeholder="32.50" />
          <div>
            <label className="mb-1 block text-xs text-fg-muted">Overhead basis</label>
            <select name="basis" className="w-full rounded border border-border-strong px-2 py-1.5 text-sm">
              <option value={OverheadBasis.UNSPECIFIED}>Not set</option>
              <option value={OverheadBasis.PER_MATERIAL_DOLLAR}>Fraction of direct materials</option>
              <option value={OverheadBasis.PER_LABOUR_HOUR}>CAD per labour hour</option>
              <option value={OverheadBasis.PER_LAA}>CAD per LAA</option>
            </select>
            <p className="mt-1 text-xs text-fg-subtle">
              There is no correct answer, only a stated one.
            </p>
          </div>
          <Field label="Overhead rate" name="overhead" placeholder="0.35 or 18.00" />
          <Field label="Notes" name="notes" className="sm:col-span-2" />
          <div className="sm:col-span-3">
            <button
              type="submit"
              disabled={save.isPending}
              className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
            >
              {save.isPending ? "Saving…" : "Save"}
            </button>
            {save.error && <span className="ml-3 text-sm text-danger-fg">{errText(save.error)}</span>}
          </div>
        </form>
      )}

      <div className="overflow-hidden rounded-lg border border-border bg-surface-2">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-2">From</th>
              <th className="px-4 py-2 text-right">Labour / h</th>
              <th className="px-4 py-2">Overhead basis</th>
              <th className="px-4 py-2 text-right">Overhead rate</th>
              <th className="px-4 py-2">Notes</th>
              <th className="px-4 py-2"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {rates.data?.rates.length === 0 && (
              <EmptyRow
                colSpan={6}
                title="No rates set"
                message="Without them, a bottle costs the price of its barley and nothing else — which is what every cost figure in Stillhouse will say."
              />
            )}
            {rates.data?.rates.map((r) => (
              <tr key={r.id}>
                <td className="px-4 py-2 text-fg">{r.effectiveFrom}</td>
                <td className="px-4 py-2 text-right text-fg-muted">
                  {r.labourRateCadPerHour || <span className="text-warning-fg">not set</span>}
                </td>
                <td className="px-4 py-2 text-fg-muted">{basisLabel[r.overheadBasis] ?? "—"}</td>
                <td className="px-4 py-2 text-right text-fg-muted">
                  {r.overheadRate || <span className="text-warning-fg">not set</span>}
                </td>
                <td className="px-4 py-2 text-xs text-fg-muted">{r.notes}</td>
                <td className="px-4 py-2 text-right">
                  <OwnerOnly>
                    <button
                      onClick={() => remove.mutate({ id: r.id })}
                      className="text-xs text-fg-muted hover:text-danger-fg"
                    >
                      Remove
                    </button>
                  </OwnerOnly>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {remove.error && <p className="text-sm text-danger-fg">{errText(remove.error)}</p>}
    </div>
  );
}

function Stat({ label, value, highlight }: { label: string; value: string; highlight?: boolean }) {
  return (
    <div className={`rounded-lg border border-border p-4 ${highlight ? "bg-success/10" : "bg-surface-2"}`}>
      <div className="text-xs text-fg-muted">{label}</div>
      <div className={`mt-1 text-2xl font-bold tracking-tight ${highlight ? "text-success-fg" : "text-fg"}`}>
        {value}
      </div>
    </div>
  );
}

function Field({ label, name, type = "text", placeholder, className }: {
  label: string; name: string; type?: string; placeholder?: string; className?: string;
}) {
  return (
    <div className={className}>
      <label className="mb-1 block text-xs text-fg-muted">{label}</label>
      <input name={name} type={type} placeholder={placeholder}
             className="w-full rounded border border-border-strong px-2 py-1.5 text-sm" />
    </div>
  );
}
