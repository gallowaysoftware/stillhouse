import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { Shell } from "@/components/Shell";
import { journalClient } from "@/lib/clients";
import { JournalEventKind } from "@/gen/stillhouse/v1/journal_pb";
import { formatCAD } from "@/lib/format";
import { OwnerOnly } from "@/lib/role";

const kinds: { v: JournalEventKind; label: string; what: string }[] = [
  {
    v: JournalEventKind.DUTY_PAYABLE,
    label: "Duty payable",
    what: "Excise duty as it crystallises — at packaging or on removal, whichever your duty point is.",
  },
  {
    v: JournalEventKind.MATERIAL_RECEIPT,
    label: "Material receipt",
    what: "Raw material in, at the lot cost you recorded.",
  },
  {
    v: JournalEventKind.MATERIAL_CONSUMPTION,
    label: "Material consumption",
    what: "Raw material into a mash, at the cost of the lot it came from.",
  },
  {
    v: JournalEventKind.COGS_ON_REMOVAL,
    label: "Cost of sales",
    what: "Packaged stock leaving, at its bottling run's direct-material cost.",
  },
];

const kindLabel = (k: JournalEventKind) => kinds.find((x) => x.v === k)?.label ?? "—";

function monthToDate() {
  const now = new Date();
  const first = new Date(now.getFullYear(), now.getMonth(), 1);
  const iso = (d: Date) => d.toISOString().slice(0, 10);
  return { from: iso(first), to: iso(now) };
}

export function JournalPage() {
  const initial = monthToDate();
  const [from, setFrom] = useState(initial.from);
  const [to, setTo] = useState(initial.to);
  const [tab, setTab] = useState<"journal" | "accounts">("journal");

  const preview = useQuery({
    queryKey: ["previewJournal", from, to],
    queryFn: () => journalClient.previewJournal({ periodStart: from, periodEnd: to }),
    enabled: !!from && !!to,
  });

  return (
    <Shell>
      <div className="mb-6">
        <h1 className="text-3xl font-bold tracking-tight">Accounting journal</h1>
        <p className="text-sm text-fg-muted">
          The seam between Stillhouse and your books. It knows what happened and what it
          was worth; which account each belongs in is yours to say. Anything it can't
          price or can't map is reported, never guessed — a journal line in the wrong
          account reconciles, and then nobody looks again.
        </p>
      </div>

      <div className="mb-4 flex gap-1 border-b border-border text-sm">
        {(["journal", "accounts"] as const).map((t) => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className={`-mb-px border-b-2 px-3 py-2 ${
              tab === t ? "border-accent text-fg" : "border-transparent text-fg-muted hover:text-fg"
            }`}
          >
            {t === "journal" ? "Journal" : "Account mapping"}
          </button>
        ))}
      </div>

      {tab === "accounts" ? (
        <AccountMappingTab />
      ) : (
        <>
          <div className="mb-4 flex flex-wrap items-end gap-3">
            <div>
              <label className="mb-2 block text-sm font-medium text-fg-muted">From</label>
              <input
                type="date" value={from} onChange={(e) => setFrom(e.target.value)}
                className="rounded border border-border-strong px-3 py-2 text-sm"
              />
            </div>
            <div>
              <label className="mb-2 block text-sm font-medium text-fg-muted">To</label>
              <input
                type="date" value={to} onChange={(e) => setTo(e.target.value)}
                className="rounded border border-border-strong px-3 py-2 text-sm"
              />
            </div>
            <a
              href={`/export/journal.csv?from=${from}&to=${to}`}
              className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover"
            >
              Download CSV
            </a>
          </div>

          {preview.error && (
            <p className="mb-4 text-sm text-danger-fg">
              {preview.error instanceof ConnectError
                ? preview.error.rawMessage
                : String(preview.error)}
            </p>
          )}

          {preview.data && preview.data.warnings.length > 0 && (
            <div className="mb-4 rounded-lg border border-warning/40 bg-warning/10 p-4">
              <p className="text-sm font-medium text-fg">
                What this export could not do
              </p>
              <ul className="mt-2 space-y-1 text-sm text-fg-muted">
                {preview.data.warnings.map((w, i) => (
                  <li key={i}>
                    <span className="text-fg">{kindLabel(kindFromString(w.kind))}</span> — {w.detail}
                  </li>
                ))}
              </ul>
              <p className="mt-2 text-xs text-fg-subtle">
                These ride along in the CSV too, above the header, so whoever imports it
                sees them without being told.
              </p>
            </div>
          )}

          {preview.data && preview.data.totals.length > 0 && (
            <div className="mb-4 grid gap-3 sm:grid-cols-4">
              {preview.data.totals.map((t) => (
                <div key={t.kind} className="rounded-lg border border-border bg-surface-2 p-4">
                  <p className="text-xs text-fg-muted">{kindLabel(t.kind)}</p>
                  <p className="mt-1 text-xl font-semibold text-fg">{formatCAD(t.amountCad)}</p>
                  <p className="text-xs text-fg-subtle">
                    {t.lineCount} line{t.lineCount === 1 ? "" : "s"}
                  </p>
                </div>
              ))}
            </div>
          )}

          <div className="overflow-x-auto rounded-lg border border-border bg-surface-2 shadow-sm">
            <table className="min-w-full divide-y divide-border text-sm">
              <thead className="bg-surface-3 text-left text-xs text-fg-muted">
                <tr>
                  <th className="px-4 py-3">Date</th>
                  <th className="px-4 py-3">Kind</th>
                  <th className="px-4 py-3">Description</th>
                  <th className="px-4 py-3 text-right">Amount</th>
                  <th className="px-4 py-3">Debit</th>
                  <th className="px-4 py-3">Credit</th>
                  <th className="px-4 py-3">Basis</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {preview.isLoading && (
                  <tr><td colSpan={7} className="px-4 py-3 text-fg-muted">Loading…</td></tr>
                )}
                {preview.data?.lines.length === 0 && (
                  <tr>
                    <td colSpan={7} className="px-4 py-6 text-center text-fg-muted">
                      Nothing to post in this period.
                    </td>
                  </tr>
                )}
                {preview.data?.lines.map((l, i) => (
                  <tr key={i}>
                    <td className="px-4 py-3 text-fg-muted">{l.date}</td>
                    <td className="px-4 py-3 text-fg-muted">{kindLabel(l.kind)}</td>
                    <td className="px-4 py-3 text-fg">
                      {l.description}
                      {l.reference && <span className="ml-2 text-xs text-fg-subtle">{l.reference}</span>}
                    </td>
                    <td className="px-4 py-3 text-right font-medium text-fg">{formatCAD(l.amountCad)}</td>
                    <td className="px-4 py-3 font-mono text-xs text-fg-muted">
                      {l.debitAccount || <span className="text-warning-fg">unmapped</span>}
                    </td>
                    <td className="px-4 py-3 font-mono text-xs text-fg-muted">
                      {l.creditAccount || <span className="text-warning-fg">unmapped</span>}
                    </td>
                    <td className="px-4 py-3 text-xs text-fg-subtle">{l.basis}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
    </Shell>
  );
}

function AccountMappingTab() {
  const qc = useQueryClient();
  const list = useQuery({
    queryKey: ["listJournalAccounts"],
    queryFn: () => journalClient.listJournalAccounts({}),
  });
  const save = useMutation({
    mutationFn: (m: Parameters<typeof journalClient.setJournalAccount>[0]) =>
      journalClient.setJournalAccount(m),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["listJournalAccounts"] }),
  });

  return (
    <div className="space-y-4">
      <p className="text-sm text-fg-muted">
        Your account codes, as they appear in your own chart of accounts. Stillhouse
        never validates these against anything — they're yours. What it does do is
        refuse to post an event you haven't mapped.
      </p>
      {kinds.map((k) => {
        const existing = list.data?.mappings.find((m) => m.kind === k.v);
        return (
          <form
            key={k.v}
            onSubmit={(e) => {
              e.preventDefault();
              const fd = new FormData(e.currentTarget);
              save.mutate({
                mapping: {
                  $typeName: "stillhouse.v1.JournalAccountMapping",
                  kind: k.v,
                  debitAccount: fd.get("debit_account")?.toString() ?? "",
                  debitName: fd.get("debit_name")?.toString() ?? "",
                  creditAccount: fd.get("credit_account")?.toString() ?? "",
                  creditName: fd.get("credit_name")?.toString() ?? "",
                  memoPrefix: fd.get("memo_prefix")?.toString() ?? "",
                },
              });
            }}
            className="rounded-lg border border-border bg-surface-2 p-5 shadow-sm"
          >
            <p className="text-sm font-medium text-fg">{k.label}</p>
            <p className="mb-3 text-xs text-fg-muted">{k.what}</p>
            <div className="grid gap-3 sm:grid-cols-5">
              <Field label="Debit account" name="debit_account" defaultValue={existing?.debitAccount} />
              <Field label="Debit name" name="debit_name" defaultValue={existing?.debitName} />
              <Field label="Credit account" name="credit_account" defaultValue={existing?.creditAccount} />
              <Field label="Credit name" name="credit_name" defaultValue={existing?.creditName} />
              <Field label="Memo prefix" name="memo_prefix" defaultValue={existing?.memoPrefix} />
            </div>
            <OwnerOnly>
              <button
                type="submit"
                disabled={save.isPending}
                className="mt-3 rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
              >
                {save.isPending ? "Saving…" : "Save"}
              </button>
            </OwnerOnly>
          </form>
        );
      })}
      {save.error && (
        <p className="text-sm text-danger-fg">
          {save.error instanceof ConnectError ? save.error.rawMessage : String(save.error)}
        </p>
      )}
    </div>
  );
}

function Field({ label, name, defaultValue }: { label: string; name: string; defaultValue?: string }) {
  return (
    <div>
      <label className="mb-1 block text-xs text-fg-muted">{label}</label>
      <input
        key={defaultValue}
        name={name}
        defaultValue={defaultValue ?? ""}
        className="w-full rounded border border-border-strong px-2 py-1.5 text-sm"
      />
    </div>
  );
}

// The warnings carry the database's own enum string; map it back for
// display rather than plumbing a second representation through the API.
function kindFromString(s: string): JournalEventKind {
  switch (s) {
    case "duty_payable": return JournalEventKind.DUTY_PAYABLE;
    case "material_receipt": return JournalEventKind.MATERIAL_RECEIPT;
    case "material_consumption": return JournalEventKind.MATERIAL_CONSUMPTION;
    case "cogs_on_removal": return JournalEventKind.COGS_ON_REMOVAL;
    default: return JournalEventKind.UNSPECIFIED;
  }
}
