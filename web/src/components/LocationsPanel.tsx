import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { locationClient, tenantClient } from "@/lib/clients";
import { OwnerOnly } from "@/lib/role";

/**
 * Where, within a licensee.
 *
 * A location is a premises — the address on a licence — not a rack
 * position. Containers keep their free-text `location` for "Row 4, Level
 * 2", which is a finer question and would be lost if the two were folded
 * together.
 *
 * Every tenant has exactly one default, maintained by the database
 * rather than by whichever path created the tenant, so an install that
 * never adds a second location behaves exactly as it did before.
 */
export function LocationsPanel() {
  const qc = useQueryClient();
  const [editing, setEditing] = useState<string | null>(null);

  const list = useQuery({
    queryKey: ["listLocations"],
    queryFn: () => locationClient.listLocations({}),
  });
  const licences = useQuery({
    queryKey: ["listExciseLicences"],
    queryFn: () => tenantClient.listExciseLicences({}),
  });
  const save = useMutation({
    mutationFn: (m: Parameters<typeof locationClient.saveLocation>[0]) =>
      locationClient.saveLocation(m),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["listLocations"] });
      setEditing(null);
    },
  });

  const locations = list.data?.locations ?? [];

  return (
    <section className="mt-10">
      <h2 className="mb-3 text-sm font-semibold text-fg-muted">Locations</h2>
      <div className="rounded-lg border border-border bg-surface-2 p-5 shadow-sm">
        <p className="mb-4 text-sm text-fg-muted">
          Your premises. An excise warehouse licence can cover several, and the 30%
          single-retail-store rule is worked out per premises — so which site a movement
          happened at is part of the record, not a note.
        </p>

        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="text-left text-xs text-fg-muted">
            <tr>
              <th className="px-2 py-2">Name</th>
              <th className="px-2 py-2">Address</th>
              <th className="px-2 py-2">Licence</th>
              <th className="px-2 py-2">Retail</th>
              <th className="px-2 py-2 text-right">Containers</th>
              <th className="px-2 py-2 text-right"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {locations.map((l) => (
              <tr key={l.id}>
                <td className="px-2 py-2 text-fg">
                  {l.name}
                  {l.isDefault && <span className="ml-2 text-xs text-fg-subtle">default</span>}
                </td>
                <td className="px-2 py-2 text-fg-muted">{l.address || "—"}</td>
                <td className="px-2 py-2 font-mono text-xs text-fg-muted">
                  {l.licenceNumber || "—"}
                </td>
                <td className="px-2 py-2 text-fg-muted">{l.retailStore ? "yes" : ""}</td>
                <td className="px-2 py-2 text-right text-fg-muted">{l.containerCount || "—"}</td>
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
            ))}
          </tbody>
        </table>

        {editing && (
          <LocationForm
            location={editing === "new" ? undefined : locations.find((l) => l.id === editing)}
            licences={licences.data?.licences ?? []}
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
              Add a location
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

function LocationForm({
  location, licences, onSubmit, pending,
}: {
  location?: { id: string; name: string; address: string; exciseLicenceId: string; retailStore: boolean; isDefault: boolean; notes: string };
  licences: { id: string; licenceNumber: string; kind: number }[];
  onSubmit: (v: Parameters<typeof locationClient.saveLocation>[0]) => void;
  pending: boolean;
}) {
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        const fd = new FormData(e.currentTarget);
        onSubmit({
          id: location?.id ?? "",
          name: fd.get("name")?.toString() ?? "",
          address: fd.get("address")?.toString() ?? "",
          exciseLicenceId: fd.get("licence")?.toString() ?? "",
          retailStore: fd.get("retail") === "on",
          notes: fd.get("notes")?.toString() ?? "",
          makeDefault: fd.get("make_default") === "on",
        });
      }}
      className="mt-4 grid gap-3 rounded border border-border bg-surface p-4 sm:grid-cols-2"
    >
      <LField label="Name" name="name" defaultValue={location?.name} required />
      <LField label="Address on the licence" name="address" defaultValue={location?.address} />
      <div>
        <label className="mb-1 block text-xs text-fg-muted">Licence covering it</label>
        <select name="licence" defaultValue={location?.exciseLicenceId ?? ""}
                className="w-full rounded border border-border-strong px-2 py-1.5 text-sm">
          <option value="">— none / not licensed —</option>
          {licences.map((l) => (
            <option key={l.id} value={l.id}>{l.licenceNumber}</option>
          ))}
        </select>
      </div>
      <LField label="Notes" name="notes" defaultValue={location?.notes} />
      <label className="flex items-center gap-2 text-sm text-fg-muted">
        <input type="checkbox" name="retail" defaultChecked={location?.retailStore} />
        Packaged spirits are sold to the public here
      </label>
      {!location?.isDefault && (
        <label className="flex items-center gap-2 text-sm text-fg-muted">
          <input type="checkbox" name="make_default" />
          Make this the default
        </label>
      )}
      <div className="sm:col-span-2">
        <button type="submit" disabled={pending}
                className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50">
          {pending ? "Saving…" : "Save location"}
        </button>
      </div>
    </form>
  );
}

function LField({ label, name, defaultValue, required }: {
  label: string; name: string; defaultValue?: string; required?: boolean;
}) {
  return (
    <div>
      <label className="mb-1 block text-xs text-fg-muted">{label}</label>
      <input key={defaultValue} name={name} defaultValue={defaultValue ?? ""} required={required}
             className="w-full rounded border border-border-strong px-2 py-1.5 text-sm" />
    </div>
  );
}
