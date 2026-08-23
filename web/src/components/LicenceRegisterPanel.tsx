import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { tenantClient } from "@/lib/clients";
import { ExciseLicence, ExciseLicenceKind } from "@/gen/stillhouse/v1/tenant_pb";
import { OwnerOnly } from "@/lib/role";

const kinds: { v: ExciseLicenceKind; label: string; hint: string }[] = [
  { v: ExciseLicenceKind.SPIRITS, label: "Spirits (L63A)", hint: "To produce or package spirits." },
  {
    v: ExciseLicenceKind.EXCISE_WAREHOUSE,
    label: "Excise warehouse (L63W)",
    hint: "To store non-duty-paid spirits. Holding one moves your duty point to removal.",
  },
  { v: ExciseLicenceKind.USERS, label: "User's licence", hint: "To use spirits in manufacture." },
  { v: ExciseLicenceKind.WINE, label: "Wine licence", hint: "" },
  { v: ExciseLicenceKind.OTHER, label: "Other", hint: "" },
];

const kindLabel = (k: ExciseLicenceKind) => kinds.find((x) => x.v === k)?.label ?? "—";

/**
 * What the licensee actually holds.
 *
 * This is the record the rest of the compliance surface hangs off: which
 * returns exist follows from which licences are held, so does where the
 * duty point falls, and so does whether a renewal reminder is possible
 * at all.
 *
 * A licence with no expiry date raises no reminder, on purpose — every
 * CRA licence expires, so a blank means nobody entered it, and a
 * reminder derived from a guess would be believed. The panel says how
 * many are blank rather than letting the register look finished.
 */
export function LicenceRegisterPanel() {
  const qc = useQueryClient();
  const [editing, setEditing] = useState<string | null>(null);
  const list = useQuery({
    queryKey: ["listExciseLicences"],
    queryFn: () => tenantClient.listExciseLicences({}),
  });
  const save = useMutation({
    mutationFn: (m: Parameters<typeof tenantClient.saveExciseLicence>[0]) =>
      tenantClient.saveExciseLicence(m),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["listExciseLicences"] });
      qc.invalidateQueries({ queryKey: ["listAlerts"] });
      setEditing(null);
    },
  });

  const licences = list.data?.licences ?? [];
  const missing = list.data?.missingExpiryCount ?? 0;

  return (
    <section className="mt-10">
      <h2 className="mb-3 text-sm font-semibold text-fg-muted">Licence register</h2>
      <div className="rounded-lg border border-border bg-surface-2 p-5 shadow-sm">
        <p className="mb-4 text-sm text-fg-muted">
          Every excise licence you hold, with its dates and the security behind it.
          Licences run two years and CRA wants the renewal 30 days before expiry —
          recording the expiry here is what turns that into a reminder rather than a
          surprise.
        </p>

        {missing > 0 && (
          <p className="mb-4 rounded border border-warning/40 bg-warning/10 px-3 py-2 text-sm text-fg-muted">
            {missing} licence{missing === 1 ? " has" : "s have"} no expiry date recorded, so
            no renewal reminder is possible for {missing === 1 ? "it" : "them"}. Stillhouse
            won't guess a date — a reminder for the wrong day is worse than none.
          </p>
        )}

        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="text-left text-xs text-fg-muted">
            <tr>
              <th className="px-2 py-2">Licence</th>
              <th className="px-2 py-2">Number</th>
              <th className="px-2 py-2">Effective</th>
              <th className="px-2 py-2">Expires</th>
              <th className="px-2 py-2">Premises</th>
              <th className="px-2 py-2">Security</th>
              <th className="px-2 py-2 text-right"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {licences.length === 0 && !list.isLoading && (
              <tr><td colSpan={7} className="px-2 py-3 text-fg-muted">No licences recorded.</td></tr>
            )}
            {licences.map((l) => {
              const ceased = !!l.ceasedOn;
              return (
                <tr key={l.id} className={ceased ? "opacity-60" : ""}>
                  <td className="px-2 py-2 text-fg">
                    {kindLabel(l.kind)}
                    {ceased && <span className="ml-2 text-xs text-fg-subtle">ceased {l.ceasedOn}</span>}
                  </td>
                  <td className="px-2 py-2 font-mono text-xs text-fg-muted">{l.licenceNumber}</td>
                  <td className="px-2 py-2 text-fg-muted">{l.effectiveFrom || "—"}</td>
                  <td className="px-2 py-2 text-fg-muted">
                    {l.expiresOn || <span className="text-warning-fg">not recorded</span>}
                  </td>
                  <td className="px-2 py-2 text-fg-muted">{l.premises || "—"}</td>
                  <td className="px-2 py-2 text-fg-muted">
                    {l.securityAmountCad ? `$${l.securityAmountCad}` : "—"}
                    {l.securityExpiresOn && (
                      <span className="ml-1 text-xs text-fg-subtle">to {l.securityExpiresOn}</span>
                    )}
                  </td>
                  <td className="px-2 py-2 text-right">
                    <OwnerOnly>
                      <button
                        onClick={() => setEditing(editing === l.id ? null : l.id)}
                        className="text-xs text-fg-muted hover:text-fg"
                      >
                        {editing === l.id ? "Cancel" : "Edit"}
                      </button>
                    </OwnerOnly>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>

        {editing && (
          <LicenceForm
            licence={licences.find((l) => l.id === editing)}
            onSubmit={(v) => save.mutate(v)}
            pending={save.isPending}
          />
        )}

        <OwnerOnly>
          {!editing && (
            <button
              onClick={() => setEditing("new")}
              className="mt-4 rounded border border-border-strong px-3 py-2 text-sm text-fg hover:border-accent"
            >
              Add a licence
            </button>
          )}
        </OwnerOnly>

        {save.error && (
          <p className="mt-2 text-sm text-danger-fg">
            {save.error instanceof ConnectError ? save.error.rawMessage : String(save.error)}
          </p>
        )}
      </div>
    </section>
  );
}

function LicenceForm({
  licence, onSubmit, pending,
}: {
  licence?: ExciseLicence;
  onSubmit: (v: Parameters<typeof tenantClient.saveExciseLicence>[0]) => void;
  pending: boolean;
}) {
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        const fd = new FormData(e.currentTarget);
        onSubmit({
          id: licence?.id ?? "",
          kind: Number(fd.get("kind")) as ExciseLicenceKind,
          licenceNumber: fd.get("licence_number")?.toString() ?? "",
          effectiveFrom: fd.get("effective_from")?.toString() ?? "",
          expiresOn: fd.get("expires_on")?.toString() ?? "",
          premises: fd.get("premises")?.toString() ?? "",
          securityAmountCad: fd.get("security_amount")?.toString() ?? "",
          securityExpiresOn: fd.get("security_expires_on")?.toString() ?? "",
          notes: fd.get("notes")?.toString() ?? "",
          ceasedOn: fd.get("ceased_on")?.toString() ?? "",
        });
      }}
      className="mt-4 grid gap-3 rounded border border-border bg-surface p-4 sm:grid-cols-3"
    >
      <div>
        <label className="mb-1 block text-xs text-fg-muted">Licence</label>
        <select
          name="kind"
          defaultValue={String(licence?.kind ?? ExciseLicenceKind.SPIRITS)}
          className="w-full rounded border border-border-strong px-2 py-1.5 text-sm"
        >
          {kinds.map((k) => <option key={k.v} value={k.v}>{k.label}</option>)}
        </select>
      </div>
      <F label="Number" name="licence_number" defaultValue={licence?.licenceNumber} required />
      <F label="Effective from" name="effective_from" type="date"
         defaultValue={licence?.effectiveFrom || new Date().toISOString().slice(0, 10)} required />
      <F label="Expires on" name="expires_on" type="date" defaultValue={licence?.expiresOn} />
      <F label="Premises" name="premises" defaultValue={licence?.premises} />
      <F label="Security posted (CAD)" name="security_amount" defaultValue={licence?.securityAmountCad} />
      <F label="Security expires" name="security_expires_on" type="date"
         defaultValue={licence?.securityExpiresOn} />
      <F label="Ceased on" name="ceased_on" type="date" defaultValue={licence?.ceasedOn} />
      <F label="Notes" name="notes" defaultValue={licence?.notes} />
      <div className="sm:col-span-3">
        <button
          type="submit"
          disabled={pending}
          className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
        >
          {pending ? "Saving…" : "Save licence"}
        </button>
      </div>
    </form>
  );
}

function F({ label, name, type = "text", defaultValue, required }: {
  label: string; name: string; type?: string; defaultValue?: string; required?: boolean;
}) {
  return (
    <div>
      <label className="mb-1 block text-xs text-fg-muted">{label}</label>
      <input
        key={defaultValue}
        name={name}
        type={type}
        required={required}
        defaultValue={defaultValue ?? ""}
        className="w-full rounded border border-border-strong px-2 py-1.5 text-sm"
      />
    </div>
  );
}
