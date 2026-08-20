import { useQuery } from "@tanstack/react-query";

import { Callout } from "@/components/Callout";
import { alcoholometryClient } from "@/lib/clients";

/**
 * AlcoholometricTablesPanel — provenance for the numbers on the B266.
 *
 * Every corrected strength in the ledger traces back to one published
 * table, and an auditor is entitled to know which. When it's installed
 * this panel is the receipt: the file, its SHA-256, the row count, the
 * range it covers.
 *
 * When it isn't, this is the install page. The tables aren't shipped with
 * Stillhouse — they're Crown material, and the Government of Canada's
 * terms don't extend to commercial redistribution — so each operator
 * downloads their own copy once. Nothing else in the app is blocked
 * meanwhile; readings just record uncorrected.
 */
export function AlcoholometricTablesPanel({ dataDirHint }: { dataDirHint?: string }) {
  const info = useQuery({
    queryKey: ["tablesInfo"],
    queryFn: () => alcoholometryClient.tablesInfo({}),
    staleTime: 5 * 60 * 1000,
  });

  const t = info.data;
  const path = dataDirHint ?? "/data/alcoholometric-tables";

  return (
    <section className="mt-10">
      <h2 className="mb-3 text-sm font-semibold text-fg-muted">Alcoholometric tables</h2>
      <div className="rounded-lg border border-border bg-surface-2 p-5 shadow-sm">
        {info.isLoading && <p className="text-sm text-fg-muted">Checking…</p>}

        {t && !t.loaded && (
          <>
            <Callout tone="warning" title="Not installed — temperature correction is off">
              Strengths are being recorded exactly as read. Everything else — barrels, bottling,
              B266 — works normally, but a hydrometer indication taken away from 20 °C will carry
              its error into your return.
            </Callout>
            <ol className="mt-4 space-y-2 text-sm text-fg">
              <li>
                <span className="mr-2 text-fg-subtle">1.</span>
                Download the {t.name} from{" "}
                <a
                  href={t.sourceUrl}
                  target="_blank"
                  rel="noreferrer"
                  className="text-accent hover:text-accent-hover underline underline-offset-2"
                >
                  canada.ca
                </a>{" "}
                — the ZIP at the bottom of the page.
              </li>
              <li>
                <span className="mr-2 text-fg-subtle">2.</span>
                Put it on the server at <code className="rounded bg-surface-3 px-1 py-0.5 text-xs">{path}</code>.
                The ZIP as downloaded is fine; so is the <code className="rounded bg-surface-3 px-1 py-0.5 text-xs">ALC_TAB.TXT</code>{" "}
                inside it.
              </li>
              <li>
                <span className="mr-2 text-fg-subtle">3.</span>
                Restart Stillhouse. This panel will show the file&apos;s checksum once it loads.
              </li>
            </ol>
            <p className="mt-4 text-xs text-fg-subtle">
              Why the manual step: the tables are Crown copyright, reproducible for non-commercial
              use but not redistributable with software. Downloading them yourself keeps your
              install unambiguously in the clear.
            </p>
          </>
        )}

        {t?.loaded && (
          <dl className="grid grid-cols-1 gap-x-8 gap-y-3 sm:grid-cols-2">
            <Field label="Table">
              <a
                href={t.sourceUrl}
                target="_blank"
                rel="noreferrer"
                className="text-accent hover:text-accent-hover underline underline-offset-2"
              >
                {t.name}
              </a>
            </Field>
            <Field label="Rows loaded">{t.rowCount.toLocaleString()}</Field>
            <Field label="Temperature range">
              {t.temperatureMinC} °C to {t.temperatureMaxC} °C, referred to{" "}
              {t.referenceTemperatureC} °C
            </Field>
            <Field label="File">
              <span className="break-all">{t.fileName}</span>
            </Field>
            <div className="sm:col-span-2">
              <Field label="SHA-256 of the published table">
                <code className="break-all text-xs">{t.sourceSha256}</code>
              </Field>
            </div>
          </dl>
        )}
      </div>
    </section>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <dt className="text-xs uppercase tracking-wide text-fg-subtle">{label}</dt>
      <dd className="mt-0.5 text-sm text-fg">{children}</dd>
    </div>
  );
}
