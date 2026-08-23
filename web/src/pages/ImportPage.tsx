import { useRef, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { Shell } from "@/components/Shell";
import { importClient } from "@/lib/clients";
import { ImportKind } from "@/gen/stillhouse/v1/importer_pb";
import { OwnerOnly } from "@/lib/role";

// The order a distillery would sensibly work through: later kinds refer
// to earlier ones by name, so importing out of order produces a file of
// "no material called X" and a bad first impression.
const kinds: { v: ImportKind; label: string }[] = [
  { v: ImportKind.MATERIALS, label: "1 · Materials" },
  { v: ImportKind.MATERIAL_LOTS, label: "2 · Material deliveries" },
  { v: ImportKind.PRODUCTS, label: "3 · Products" },
  { v: ImportKind.CUSTOMERS, label: "4 · Customers" },
  { v: ImportKind.BARRELS, label: "5 · Casks" },
  { v: ImportKind.PACKAGED_INVENTORY, label: "6 · Bottled stock" },
];

export function ImportPage() {
  const [kind, setKind] = useState<ImportKind>(ImportKind.MATERIALS);
  const [csv, setCsv] = useState("");
  const fileInput = useRef<HTMLInputElement>(null);

  const describe = useQuery({
    queryKey: ["describeImport", kind],
    queryFn: () => importClient.describeImport({ kind }),
  });
  const run = useMutation({
    mutationFn: (commit: boolean) => importClient.runImport({ kind, csv, commit }),
  });

  const result = run.data;

  return (
    <Shell>
      <div className="mb-6">
        <h1 className="text-3xl font-bold tracking-tight">Import</h1>
        <p className="text-sm text-fg-muted">
          What you already have, in a spreadsheet. Check it first — the check does
          everything a real import does and then throws it away, so it finds the
          collisions a spelling check can't. Nothing is written unless the whole file
          is good.
        </p>
      </div>

      <div className="mb-4 flex flex-wrap gap-2">
        {kinds.map((k) => (
          <button
            key={k.v}
            onClick={() => { setKind(k.v); run.reset(); }}
            className={`rounded border px-3 py-1.5 text-sm ${
              kind === k.v
                ? "border-accent bg-accent/10 text-fg"
                : "border-border-strong text-fg-muted hover:text-fg"
            }`}
          >
            {k.label}
          </button>
        ))}
      </div>

      {describe.data && (
        <div className="mb-4 rounded-lg border border-border bg-surface-2 p-5">
          <p className="text-sm text-fg-muted">{describe.data.help}</p>
          <table className="mt-3 min-w-full divide-y divide-border text-sm">
            <thead className="text-left text-xs text-fg-muted">
              <tr>
                <th className="px-2 py-1.5">Column</th>
                <th className="px-2 py-1.5">Required</th>
                <th className="px-2 py-1.5">What it is</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {describe.data.columns.map((c) => (
                <tr key={c.name}>
                  <td className="px-2 py-1.5 font-mono text-xs text-fg">{c.name}</td>
                  <td className="px-2 py-1.5 text-xs text-fg-muted">{c.required ? "yes" : ""}</td>
                  <td className="px-2 py-1.5 text-fg-muted">{c.help}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <button
            onClick={() => setCsv(describe.data.templateCsv)}
            className="mt-3 text-xs text-accent hover:underline"
          >
            Put the header row in the box below
          </button>
        </div>
      )}

      <div className="mb-4 rounded-lg border border-border bg-surface-2 p-5">
        <div className="mb-3 flex flex-wrap items-center gap-3">
          <input
            ref={fileInput}
            type="file"
            accept=".csv,text/csv"
            onChange={async (e) => {
              const file = e.target.files?.[0];
              if (!file) return;
              setCsv(await file.text());
              run.reset();
            }}
            className="text-sm text-fg-muted"
          />
          {csv && (
            <button
              onClick={() => {
                setCsv("");
                run.reset();
                if (fileInput.current) fileInput.current.value = "";
              }}
              className="text-xs text-fg-muted hover:text-fg"
            >
              Clear
            </button>
          )}
        </div>
        <textarea
          value={csv}
          onChange={(e) => { setCsv(e.target.value); run.reset(); }}
          rows={10}
          spellCheck={false}
          placeholder="Choose a file above, or paste CSV here."
          className="w-full rounded border border-border-strong bg-surface px-3 py-2 font-mono text-xs text-fg"
        />
        <OwnerOnly>
          <div className="mt-3 flex flex-wrap items-center gap-3">
            <button
              onClick={() => run.mutate(false)}
              disabled={run.isPending || !csv.trim()}
              className="rounded border border-border-strong px-3 py-2 text-sm text-fg hover:border-accent disabled:opacity-50"
            >
              {run.isPending ? "Checking…" : "Check without importing"}
            </button>
            <button
              onClick={() => run.mutate(true)}
              disabled={run.isPending || !csv.trim()}
              className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
            >
              Import
            </button>
            {run.error && (
              <span className="text-sm text-danger-fg">
                {run.error instanceof ConnectError ? run.error.rawMessage : String(run.error)}
              </span>
            )}
          </div>
        </OwnerOnly>
      </div>

      {result && (
        <div className="space-y-3">
          <div
            className={`rounded-lg border p-4 ${
              result.committed
                ? "border-success/40 bg-success/10"
                : result.problems.length > 0
                  ? "border-danger/40 bg-danger/10"
                  : "border-border bg-surface-2"
            }`}
          >
            <p className="text-sm font-medium text-fg">
              {result.committed
                ? `Imported ${result.rowsAccepted} of ${result.rowsRead} rows.`
                : result.problems.length > 0
                  ? `Nothing was imported — ${result.problems.length} problem${
                      result.problems.length === 1 ? "" : "s"
                    } in ${result.rowsRead} rows.`
                  : `${result.rowsAccepted} of ${result.rowsRead} rows would import cleanly. Nothing has been written.`}
            </p>
            {!result.committed && result.problems.length === 0 && (
              <p className="mt-1 text-sm text-fg-muted">
                Press Import to write them.
              </p>
            )}
          </div>

          {result.problems.length > 0 && (
            <div className="overflow-hidden rounded-lg border border-border bg-surface-2">
              <table className="min-w-full divide-y divide-border text-sm">
                <thead className="bg-surface-3 text-left text-xs text-fg-muted">
                  <tr>
                    <th className="px-4 py-2">Row</th>
                    <th className="px-4 py-2">Column</th>
                    <th className="px-4 py-2">What's wrong</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-border">
                  {result.problems.map((p, i) => (
                    <tr key={i}>
                      <td className="px-4 py-2 text-fg-muted">{p.row}</td>
                      <td className="px-4 py-2 font-mono text-xs text-fg-muted">{p.column || "—"}</td>
                      <td className="px-4 py-2 text-fg">{p.detail}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          {result.notes.length > 0 && (
            <div className="rounded-lg border border-warning/40 bg-warning/10 p-4">
              <p className="text-sm font-medium text-fg">Worth knowing</p>
              <ul className="mt-1 space-y-1 text-sm text-fg-muted">
                {result.notes.map((n, i) => <li key={i}>{n}</li>)}
              </ul>
            </div>
          )}
        </div>
      )}
    </Shell>
  );
}
