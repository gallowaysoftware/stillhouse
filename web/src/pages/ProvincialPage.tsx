import { useState } from "react";
import { DepositReportPanel } from "@/components/DepositReportPanel";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { EmptyRow } from "@/components/EmptyState";
import { Shell } from "@/components/Shell";
import { provincialClient } from "@/lib/clients";
import {
  ReportingCadence,
  RequirementProvenance,
} from "@/gen/stillhouse/v1/provincial_pb";
import { formatCAD, formatLAA, formatQty } from "@/lib/format";
import { OwnerOnly } from "@/lib/role";

const cadenceLabel: Record<number, string> = {
  [ReportingCadence.UNSPECIFIED]: "—",
  [ReportingCadence.MONTHLY]: "Monthly",
  [ReportingCadence.QUARTERLY]: "Quarterly",
  [ReportingCadence.SEMI_ANNUAL]: "Semi-annual",
  [ReportingCadence.ANNUAL]: "Annual",
  [ReportingCadence.PER_SHIPMENT]: "With every shipment",
  [ReportingCadence.OTHER]: "Other",
};

const provenanceLabel: Record<number, { text: string; tone: string }> = {
  [RequirementProvenance.UNSPECIFIED]: { text: "unknown", tone: "text-warning-fg" },
  [RequirementProvenance.UNKNOWN]: { text: "unknown", tone: "text-warning-fg" },
  [RequirementProvenance.INDICATIVE]: { text: "indicative", tone: "text-warning-fg" },
  [RequirementProvenance.SOURCED]: { text: "sourced", tone: "text-success-fg" },
};

function errText(e: unknown): string {
  return e instanceof ConnectError ? e.rawMessage : String(e);
}

export function ProvincialPage() {
  const [tab, setTab] = useState<"due" | "figures" | "setup">("due");
  return (
    <Shell>
      <div className="mb-6">
        <h1 className="text-3xl font-bold tracking-tight">Provincial reporting</h1>
        <p className="text-sm text-fg-muted">
          Every province wants something reported, and no two want the same thing
          on the same clock. Stillhouse holds the machinery and the figures;
          what each board actually requires is recorded here by you, with the
          source it came from — because a deadline half-remembered is worse than
          no deadline, since it looks like one.
        </p>
      </div>

      <div className="mb-4 flex gap-1 border-b border-border text-sm">
        {([["due", "What's owed"], ["figures", "Figures"], ["setup", "Registrations"]] as const).map(
          ([k, label]) => (
            <button
              key={k}
              onClick={() => setTab(k)}
              className={`-mb-px border-b-2 px-3 py-2 ${
                tab === k ? "border-accent text-fg" : "border-transparent text-fg-muted hover:text-fg"
              }`}
            >
              {label}
            </button>
          ),
        )}
      </div>

      {tab === "due" && <DueTab />}
      {tab === "figures" && <FiguresTab />}
      {tab === "setup" && <SetupTab />}
      <DepositReportPanel />
    </Shell>
  );
}

function DueTab() {
  const qc = useQueryClient();
  const [unfiledOnly, setUnfiledOnly] = useState(true);
  const periods = useQuery({
    queryKey: ["listProvincialReportPeriods", unfiledOnly],
    queryFn: () => provincialClient.listProvincialReportPeriods({ unfiledOnly }),
  });
  const file = useMutation({
    mutationFn: (m: Parameters<typeof provincialClient.markProvincialReportFiled>[0]) =>
      provincialClient.markProvincialReportFiled(m),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["listProvincialReportPeriods"] });
      qc.invalidateQueries({ queryKey: ["listAlerts"] });
    },
  });

  return (
    <div className="space-y-4">
      <label className="flex items-center gap-2 text-sm text-fg-muted">
        <input type="checkbox" checked={unfiledOnly} onChange={(e) => setUnfiledOnly(e.target.checked)} />
        Only what's still outstanding
      </label>

      <div className="overflow-hidden rounded-lg border border-border bg-surface-2">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-2">Where</th>
              <th className="px-4 py-2">Report</th>
              <th className="px-4 py-2">Period</th>
              <th className="px-4 py-2">Due</th>
              <th className="px-4 py-2">Status</th>
              <th className="px-4 py-2"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {periods.data?.periods.length === 0 && (
              <EmptyRow
                colSpan={6}
                title="Nothing scheduled"
                message="Register a jurisdiction, record what it expects, and generate the periods."
              />
            )}
            {periods.data?.periods.map((p) => (
              <tr key={p.id} className={p.overdue ? "bg-danger-bg" : undefined}>
                <td className="px-4 py-2 text-fg">{p.boardName || p.jurisdiction}</td>
                <td className="px-4 py-2 text-fg-muted">{p.definitionName}</td>
                <td className="px-4 py-2 text-fg-muted">
                  {p.periodStart} → {p.periodEnd}
                </td>
                <td className="px-4 py-2 text-fg-muted">
                  {p.dueOn || <span className="text-fg-subtle">none recorded</span>}
                </td>
                <td className="px-4 py-2">
                  {p.filedAt ? (
                    <span className="text-success-fg">filed</span>
                  ) : p.overdue ? (
                    <span className="text-danger-fg">{-p.daysUntilDue} d late</span>
                  ) : p.dueOn ? (
                    <span className="text-fg-muted">{p.daysUntilDue} d</span>
                  ) : (
                    <span className="text-fg-subtle">—</span>
                  )}
                </td>
                <td className="px-4 py-2 text-right">
                  {!p.filedAt && (
                    <OwnerOnly>
                      <button
                        onClick={() => {
                          const ack = window.prompt(
                            "What did the board give back? A confirmation number, a " +
                              "receipt, or the date and who you spoke to.",
                          );
                          if (ack) file.mutate({ id: p.id, acknowledgement: ack });
                        }}
                        className="text-xs text-accent hover:underline"
                      >
                        Mark filed
                      </button>
                    </OwnerOnly>
                  )}
                  {p.filedAt && p.acknowledgement && (
                    <span className="text-xs text-fg-subtle">{p.acknowledgement}</span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {file.error && <p className="text-sm text-danger-fg">{errText(file.error)}</p>}
    </div>
  );
}

function FiguresTab() {
  const today = new Date().toISOString().slice(0, 10);
  const [jurisdiction, setJurisdiction] = useState("");
  const [from, setFrom] = useState(today.slice(0, 8) + "01");
  const [to, setTo] = useState(today);

  const regs = useQuery({
    queryKey: ["listProvincialRegistrations"],
    queryFn: () => provincialClient.listProvincialRegistrations({}),
  });
  const report = useQuery({
    queryKey: ["provincialSalesReport", jurisdiction, from, to],
    queryFn: () =>
      provincialClient.provincialSalesReport({
        jurisdiction, periodStart: from, periodEnd: to,
      }),
  });
  const r = report.data;

  const href =
    `/export/provincial.csv?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}` +
    (jurisdiction ? `&jurisdiction=${encodeURIComponent(jurisdiction)}` : "");

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end gap-3">
        <div>
          <label className="mb-1 block text-xs text-fg-muted">Jurisdiction</label>
          <select
            value={jurisdiction}
            onChange={(e) => setJurisdiction(e.target.value)}
            className="rounded border border-border-strong px-2 py-1.5 text-sm"
          >
            <option value="">All</option>
            {regs.data?.registrations.map((g) => (
              <option key={g.id} value={g.jurisdiction}>
                {g.jurisdiction} {g.boardName && `— ${g.boardName}`}
              </option>
            ))}
          </select>
        </div>
        <div>
          <label className="mb-1 block text-xs text-fg-muted">From</label>
          <input type="date" value={from} onChange={(e) => setFrom(e.target.value)}
                 className="rounded border border-border-strong px-2 py-1.5 text-sm" />
        </div>
        <div>
          <label className="mb-1 block text-xs text-fg-muted">To</label>
          <input type="date" value={to} onChange={(e) => setTo(e.target.value)}
                 className="rounded border border-border-strong px-2 py-1.5 text-sm" />
        </div>
        <a href={href}
           className="rounded border border-border-strong px-3 py-2 text-sm text-fg hover:bg-surface-3">
          Download CSV
        </a>
      </div>

      {r && (
        <>
          <p className="text-xs text-fg-subtle">{r.basis}</p>
          {r.unattributedRemovals > 0 && (
            <p className="rounded border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-fg">
              {r.unattributedRemovals} removal{r.unattributedRemovals === 1 ? "" : "s"} in
              this period ({r.unattributedBottles} bottles, {formatLAA(r.unattributedLaa)} L LAA)
              name no customer, so they are in no jurisdiction's figures. If any of them
              went to a board, record the customer and they will appear here.
            </p>
          )}
          <section className="grid grid-cols-2 gap-4 sm:grid-cols-4">
            <Stat label="Bottles" value={r.totalBottles.toLocaleString()} />
            <Stat label="Litres" value={formatQty(r.totalLitres)} />
            <Stat label="LAA" value={formatLAA(r.totalLaa)} highlight />
            <Stat label="Federal duty" value={formatCAD(r.totalDutyCad)} />
          </section>

          <div className="overflow-hidden rounded-lg border border-border bg-surface-2">
            <table className="min-w-full divide-y divide-border text-sm">
              <thead className="bg-surface-3 text-left text-xs text-fg-muted">
                <tr>
                  <th className="px-4 py-2">Jurisdiction</th>
                  <th className="px-4 py-2">Product</th>
                  <th className="px-4 py-2">GTIN</th>
                  <th className="px-4 py-2 text-right">Bottles</th>
                  <th className="px-4 py-2 text-right">Litres</th>
                  <th className="px-4 py-2 text-right">LAA</th>
                  <th className="px-4 py-2 text-right">Federal duty</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {r.lines.length === 0 && (
                  <EmptyRow colSpan={7} title="Nothing left in this period"
                            message="No removals to a customer in this jurisdiction." />
                )}
                {r.lines.map((l, i) => (
                  <tr key={`${l.productId}-${i}`}>
                    <td className="px-4 py-2 text-fg-muted">{l.jurisdiction}</td>
                    <td className="px-4 py-2 text-fg">{l.productName}</td>
                    <td className="px-4 py-2 font-mono text-xs text-fg-muted">{l.gtin || "—"}</td>
                    <td className="px-4 py-2 text-right text-fg-muted">{l.bottles}</td>
                    <td className="px-4 py-2 text-right text-fg-muted">{formatQty(l.litres)}</td>
                    <td className="px-4 py-2 text-right font-medium text-fg">{formatLAA(l.laa)}</td>
                    <td className="px-4 py-2 text-right text-fg-muted">{formatCAD(l.dutyCad)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
    </div>
  );
}

function SetupTab() {
  const qc = useQueryClient();
  const [addingReg, setAddingReg] = useState(false);
  const [addingDef, setAddingDef] = useState<string | null>(null);
  const regs = useQuery({
    queryKey: ["listProvincialRegistrations"],
    queryFn: () => provincialClient.listProvincialRegistrations({}),
  });
  const defs = useQuery({
    queryKey: ["listProvincialReportDefinitions"],
    queryFn: () => provincialClient.listProvincialReportDefinitions({}),
  });
  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["listProvincialRegistrations"] });
    qc.invalidateQueries({ queryKey: ["listProvincialReportDefinitions"] });
    qc.invalidateQueries({ queryKey: ["listProvincialReportPeriods"] });
  };
  const saveReg = useMutation({
    mutationFn: (m: Parameters<typeof provincialClient.saveProvincialRegistration>[0]) =>
      provincialClient.saveProvincialRegistration(m),
    onSuccess: () => { setAddingReg(false); invalidate(); },
  });
  const saveDef = useMutation({
    mutationFn: (m: Parameters<typeof provincialClient.saveProvincialReportDefinition>[0]) =>
      provincialClient.saveProvincialReportDefinition(m),
    onSuccess: () => { setAddingDef(null); invalidate(); },
  });
  const generate = useMutation({
    mutationFn: (m: Parameters<typeof provincialClient.generateProvincialPeriods>[0]) =>
      provincialClient.generateProvincialPeriods(m),
    onSuccess: invalidate,
  });

  return (
    <div className="space-y-6">
      <OwnerOnly>
        <button
          onClick={() => setAddingReg((v) => !v)}
          className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover"
        >
          {addingReg ? "Cancel" : "Register a jurisdiction"}
        </button>
      </OwnerOnly>

      {addingReg && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            const fd = new FormData(e.currentTarget);
            saveReg.mutate({
              jurisdiction: fd.get("jurisdiction")?.toString() ?? "",
              boardName: fd.get("board_name")?.toString() ?? "",
              registrationNo: fd.get("registration_no")?.toString() ?? "",
              portalUrl: fd.get("portal_url")?.toString() ?? "",
              contact: fd.get("contact")?.toString() ?? "",
              notes: fd.get("notes")?.toString() ?? "",
            });
          }}
          className="grid gap-3 rounded-lg border border-border bg-surface-2 p-5 sm:grid-cols-3"
        >
          <F label="Jurisdiction (CA-ON)" name="jurisdiction" required />
          <F label="Board" name="board_name" placeholder="LCBO" />
          <F label="Your number with them" name="registration_no" />
          <F label="Portal URL" name="portal_url" />
          <F label="Contact" name="contact" />
          <F label="Notes" name="notes" />
          <div className="sm:col-span-3">
            <button type="submit" disabled={saveReg.isPending}
                    className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50">
              Save
            </button>
            {saveReg.error && <span className="ml-3 text-sm text-danger-fg">{errText(saveReg.error)}</span>}
          </div>
        </form>
      )}

      {regs.data?.registrations.map((g) => {
        const mine = defs.data?.definitions.filter((d) => d.registrationId === g.id) ?? [];
        return (
          <section key={g.id} className="rounded-lg border border-border bg-surface-2 p-5">
            <div className="mb-2 flex flex-wrap items-baseline justify-between gap-2">
              <h2 className="text-sm font-semibold text-fg">
                {g.jurisdiction}
                {g.boardName && <span className="ml-2 text-fg-muted">{g.boardName}</span>}
                {g.registrationNo && (
                  <span className="ml-2 text-xs text-fg-subtle">#{g.registrationNo}</span>
                )}
              </h2>
              <OwnerOnly>
                <button
                  onClick={() => setAddingDef(addingDef === g.id ? null : g.id)}
                  className="text-xs text-accent hover:underline"
                >
                  {addingDef === g.id ? "Cancel" : "Add a report they want"}
                </button>
              </OwnerOnly>
            </div>

            {mine.length === 0 && (
              <p className="text-xs text-fg-subtle">
                Nothing recorded yet. Until something is, Stillhouse has no idea what
                this board expects — and will not guess.
              </p>
            )}
            <ul className="space-y-2">
              {mine.map((d) => {
                const prov = provenanceLabel[d.provenance] ?? provenanceLabel[0];
                return (
                  <li key={d.id} className="text-sm">
                    <span className="text-fg">{d.name}</span>
                    <span className="ml-2 text-xs text-fg-muted">
                      {cadenceLabel[d.cadence]}
                      {d.dueDaysAfterPeriodEnd >= 0
                        ? ` · due ${d.dueDaysAfterPeriodEnd} d after period end`
                        : " · no due date recorded"}
                      {d.followsExciseClock && " · on your fiscal month"}
                    </span>
                    <span className={`ml-2 text-xs ${prov.tone}`}>{prov.text}</span>
                    {d.authority && (
                      <span className="ml-2 text-xs text-fg-subtle">{d.authority}</span>
                    )}
                    <OwnerOnly>
                      <button
                        onClick={() => {
                          const year = new Date().getFullYear();
                          generate.mutate({
                            definitionId: d.id,
                            from: `${year}-01-01`,
                            to: `${year + 1}-12-31`,
                          });
                        }}
                        className="ml-3 text-xs text-accent hover:underline"
                      >
                        Generate periods
                      </button>
                    </OwnerOnly>
                  </li>
                );
              })}
            </ul>
            {generate.error && (
              <p className="mt-2 text-sm text-danger-fg">{errText(generate.error)}</p>
            )}

            {addingDef === g.id && (
              <form
                onSubmit={(e) => {
                  e.preventDefault();
                  const fd = new FormData(e.currentTarget);
                  saveDef.mutate({
                    registrationId: g.id,
                    name: fd.get("name")?.toString() ?? "",
                    cadence: Number(fd.get("cadence") ?? 0) as ReportingCadence,
                    dueDaysAfterPeriodEnd: fd.get("due_days")?.toString()
                      ? Number(fd.get("due_days"))
                      : -1,
                    followsExciseClock: fd.get("excise_clock") === "on",
                    provenance: Number(fd.get("provenance") ?? 0) as RequirementProvenance,
                    authority: fd.get("authority")?.toString() ?? "",
                    confirmedOn: fd.get("confirmed_on")?.toString() ?? "",
                    notes: fd.get("notes")?.toString() ?? "",
                  });
                }}
                className="mt-4 grid gap-3 border-t border-border pt-4 sm:grid-cols-3"
              >
                <F label="What they call it" name="name" required />
                <div>
                  <label className="mb-1 block text-xs text-fg-muted">How often</label>
                  <select name="cadence" className="w-full rounded border border-border-strong px-2 py-1.5 text-sm">
                    <option value={ReportingCadence.MONTHLY}>Monthly</option>
                    <option value={ReportingCadence.QUARTERLY}>Quarterly</option>
                    <option value={ReportingCadence.SEMI_ANNUAL}>Semi-annual</option>
                    <option value={ReportingCadence.ANNUAL}>Annual</option>
                    <option value={ReportingCadence.PER_SHIPMENT}>With every shipment</option>
                    <option value={ReportingCadence.OTHER}>Other</option>
                  </select>
                </div>
                <F label="Due, days after period end" name="due_days" type="number"
                   placeholder="leave blank if unknown" />
                <label className="flex items-center gap-2 text-sm text-fg-muted sm:col-span-3">
                  <input type="checkbox" name="excise_clock" />
                  Their period follows your fiscal month rather than the calendar
                </label>
                <div>
                  <label className="mb-1 block text-xs text-fg-muted">How well do you know this?</label>
                  <select name="provenance" className="w-full rounded border border-border-strong px-2 py-1.5 text-sm">
                    <option value={RequirementProvenance.UNKNOWN}>Unknown — not confirmed</option>
                    <option value={RequirementProvenance.INDICATIVE}>Indicative — a secondary source</option>
                    <option value={RequirementProvenance.SOURCED}>Sourced — from the board itself</option>
                  </select>
                </div>
                <F label="Source (required if sourced)" name="authority"
                   placeholder="URL, policy number, letter" />
                <F label="Confirmed on" name="confirmed_on" type="date" />
                <F label="Notes" name="notes" className="sm:col-span-3" />
                <div className="sm:col-span-3">
                  <button type="submit" disabled={saveDef.isPending}
                          className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50">
                    Save
                  </button>
                  {saveDef.error && (
                    <span className="ml-3 text-sm text-danger-fg">{errText(saveDef.error)}</span>
                  )}
                </div>
              </form>
            )}
          </section>
        );
      })}
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

function F({ label, name, type = "text", placeholder, required, className }: {
  label: string; name: string; type?: string;
  placeholder?: string; required?: boolean; className?: string;
}) {
  return (
    <div className={className}>
      <label className="mb-1 block text-xs text-fg-muted">{label}</label>
      <input name={name} type={type} placeholder={placeholder} required={required}
             className="w-full rounded border border-border-strong px-2 py-1.5 text-sm" />
    </div>
  );
}
