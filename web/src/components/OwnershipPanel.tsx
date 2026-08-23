import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { bulkClient, customerClient } from "@/lib/clients";
import { BulkPossession } from "@/gen/stillhouse/v1/bulk_pb";
import { formatLAA } from "@/lib/format";
import { OwnerOnly, WriteOnly } from "@/lib/role";

function errText(e: unknown): string {
  return e instanceof ConnectError ? e.rawMessage : String(e);
}

type Container = {
  id: string;
  name: string;
  currentLaa: number;
  ownerCustomerId: string;
  ownerName: string;
  possession: BulkPossession;
  heldByName: string;
  heldByLicenceNo: string;
};

// Whose spirits these are, and whether they are still here.
//
// The two are deliberately separate controls with separate consequences,
// spelled out where the operator makes the change rather than in a manual:
// ownership decides whether the alcohol is an asset, possession decides
// whether it goes on the B266, and moving possession writes a reportable
// in-bond transfer.
export function OwnershipPanel({ c }: { c: Container }) {
  const qc = useQueryClient();
  const [moving, setMoving] = useState(false);
  const customers = useQuery({
    queryKey: ["listCustomers"],
    queryFn: () => customerClient.listCustomers({}),
  });
  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["listBulkContainers"] });
    qc.invalidateQueries({ queryKey: ["getBulkContainer", c.id] });
    qc.invalidateQueries({ queryKey: ["listBarrels"] });
    qc.invalidateQueries({ queryKey: ["getBarrel", c.id] });
    qc.invalidateQueries({ queryKey: ["listThirdPartySpirits"] });
  };
  const setOwner = useMutation({
    mutationFn: (m: Parameters<typeof bulkClient.setBulkContainerOwner>[0]) =>
      bulkClient.setBulkContainerOwner(m),
    onSuccess: invalidate,
  });
  const setPossession = useMutation({
    mutationFn: (m: Parameters<typeof bulkClient.setBulkContainerPossession>[0]) =>
      bulkClient.setBulkContainerPossession(m),
    onSuccess: () => { setMoving(false); invalidate(); },
  });

  const elsewhere = c.possession === BulkPossession.HELD_ELSEWHERE;

  return (
    <section className="rounded-lg border border-border bg-surface-2 p-5">
      <h2 className="mb-1 text-sm font-semibold text-fg">Ownership and possession</h2>
      <p className="mb-4 text-xs text-fg-subtle">
        Two separate facts. Who owns the spirits decides whether they are your
        inventory to value and sell; whether you hold them decides whether they
        go on your B266. CRA asks for everything in your possession whoever owns
        it, and nothing you own but do not hold.
      </p>

      <div className="grid gap-4 sm:grid-cols-2">
        <OwnerOnly>
          <div>
            <label className="mb-1 block text-xs text-fg-muted">Owner</label>
            <select
              value={c.ownerCustomerId}
              disabled={setOwner.isPending}
              onChange={(e) =>
                setOwner.mutate({ id: c.id, ownerCustomerId: e.target.value })
              }
              className="w-full rounded border border-border-strong px-2 py-1.5 text-sm"
            >
              <option value="">Ours</option>
              {customers.data?.customers.map((cu) => (
                <option key={cu.id} value={cu.id}>{cu.name}</option>
              ))}
            </select>
            <p className="mt-1 text-xs text-fg-subtle">
              {c.ownerCustomerId
                ? "On your return while you hold it; not on your books."
                : "Yours. Counts as inventory and as cost of sales when it sells."}
            </p>
            {setOwner.error && (
              <p className="mt-1 text-sm text-danger-fg">{errText(setOwner.error)}</p>
            )}
          </div>
        </OwnerOnly>

        <div>
          <label className="mb-1 block text-xs text-fg-muted">Possession</label>
          <p className="text-sm text-fg">
            {elsewhere
              ? `Held by ${c.heldByName || "another licensee"}${
                  c.heldByLicenceNo ? ` (${c.heldByLicenceNo})` : ""
                }`
              : "Here, on your premises"}
          </p>
          <p className="mt-1 text-xs text-fg-subtle">
            {elsewhere
              ? "Not on your B266 — they report it. Nothing can be gauged or drawn " +
                "from it until it is back."
              : "On your B266."}
          </p>
          <WriteOnly>
            <button
              onClick={() => setMoving((v) => !v)}
              className="mt-2 text-xs text-accent hover:underline"
            >
              {moving ? "Cancel" : elsewhere ? "Record it back here" : "Send it elsewhere"}
            </button>
          </WriteOnly>
        </div>
      </div>

      {moving && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            const fd = new FormData(e.currentTarget);
            setPossession.mutate({
              id: c.id,
              possession: elsewhere ? BulkPossession.HELD : BulkPossession.HELD_ELSEWHERE,
              heldByName: fd.get("held_by_name")?.toString() ?? "",
              heldByLicenceNo: fd.get("held_by_licence_no")?.toString() ?? "",
              occurredOn: fd.get("occurred_on")?.toString() ?? "",
              documentReference: fd.get("document_reference")?.toString() ?? "",
              notes: fd.get("notes")?.toString() ?? "",
            });
          }}
          className="mt-4 grid gap-3 border-t border-border pt-4 sm:grid-cols-3"
        >
          <p className="text-sm text-fg-muted sm:col-span-3">
            This writes an in-bond transfer of{" "}
            <span className="font-medium text-fg">{formatLAA(c.currentLaa)} L LAA</span> onto
            the return for the period containing the date below. The cask keeps its
            contents — what changes is who is answerable for them.
          </p>
          {!elsewhere && (
            <>
              <Field label="Held by" name="held_by_name" required />
              <Field label="Their licence number" name="held_by_licence_no" />
            </>
          )}
          <Field label="Date (blank = today)" name="occurred_on" type="date" />
          <Field label="Document reference" name="document_reference" />
          <Field label="Notes" name="notes" />
          <div className="sm:col-span-3">
            <button
              type="submit"
              disabled={setPossession.isPending}
              className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
            >
              {setPossession.isPending ? "Recording…" : "Record the transfer"}
            </button>
            {setPossession.error && (
              <span className="ml-3 text-sm text-danger-fg">{errText(setPossession.error)}</span>
            )}
          </div>
        </form>
      )}
    </section>
  );
}

function Field({ label, name, type = "text", required }: {
  label: string; name: string; type?: string; required?: boolean;
}) {
  return (
    <div>
      <label className="mb-1 block text-xs text-fg-muted">{label}</label>
      <input name={name} type={type} required={required}
             className="w-full rounded border border-border-strong px-2 py-1.5 text-sm" />
    </div>
  );
}
