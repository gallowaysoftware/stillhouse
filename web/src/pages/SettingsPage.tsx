import { FormEvent, useState, useEffect } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { Shell } from "@/components/Shell";
import { tenantClient } from "@/lib/clients";
import { UpdateTenantRequestSchema } from "@/gen/stillhouse/v1/tenant_pb";

export function SettingsPage() {
  const qc = useQueryClient();
  const tenant = useQuery({
    queryKey: ["getTenant"],
    queryFn: () => tenantClient.getTenant({}),
  });

  const [name, setName] = useState("");
  const [licence, setLicence] = useState("");
  const [warehouse, setWarehouse] = useState("");
  const [jurisdiction, setJurisdiction] = useState("");
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    if (tenant.data?.tenant) {
      setName(tenant.data.tenant.name);
      setLicence(tenant.data.tenant.craSpiritsLicenceNumber);
      setWarehouse(tenant.data.tenant.exciseWarehouseLicenceNumber);
      setJurisdiction(tenant.data.tenant.defaultJurisdiction);
    }
  }, [tenant.data]);

  const update = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof UpdateTenantRequestSchema>>) =>
      tenantClient.updateTenant(msg),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["getTenant"] });
      qc.invalidateQueries({ queryKey: ["getMe"] });
      setSaved(true);
      setTimeout(() => setSaved(false), 3000);
    },
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    update.mutate(
      create(UpdateTenantRequestSchema, {
        name,
        craSpiritsLicenceNumber: licence,
        exciseWarehouseLicenceNumber: warehouse,
        defaultJurisdiction: jurisdiction,
      }),
    );
  }

  return (
    <Shell>
      <h1 className="mb-1 text-2xl font-semibold">Settings</h1>
      <p className="mb-6 text-sm text-stone-500">Tenant metadata — what shows in the sidebar and what gets stamped on records.</p>

      <form onSubmit={submit} className="grid grid-cols-2 gap-4 rounded-lg border border-stone-200 bg-white p-5 shadow-sm">
        <div>
          <label className="mb-1 block text-xs font-medium text-stone-600">Distillery name</label>
          <input value={name} onChange={(e) => setName(e.target.value)} required className="w-full rounded border border-stone-300 px-3 py-2 text-sm" />
        </div>
        <div>
          <label className="mb-1 block text-xs font-medium text-stone-600">CRA spirits licence #</label>
          <input value={licence} onChange={(e) => setLicence(e.target.value)} required className="w-full rounded border border-stone-300 px-3 py-2 text-sm" />
        </div>
        <div>
          <label className="mb-1 block text-xs font-medium text-stone-600">Excise warehouse licence # (optional)</label>
          <input value={warehouse} onChange={(e) => setWarehouse(e.target.value)} className="w-full rounded border border-stone-300 px-3 py-2 text-sm" />
        </div>
        <div>
          <label className="mb-1 block text-xs font-medium text-stone-600">Default jurisdiction (ISO 3166-2)</label>
          <input value={jurisdiction} onChange={(e) => setJurisdiction(e.target.value)} required placeholder="CA-ON" className="w-full rounded border border-stone-300 px-3 py-2 text-sm" />
        </div>
        <div className="col-span-2 flex items-center gap-3">
          <button
            type="submit"
            disabled={update.isPending}
            className="rounded bg-stone-900 px-3 py-2 text-sm font-medium text-white hover:bg-stone-800 disabled:bg-stone-400"
          >
            {update.isPending ? "Saving…" : "Save changes"}
          </button>
          {saved && <span className="text-sm text-emerald-700">Saved.</span>}
          {update.error && (
            <span className="text-sm text-red-600">
              {update.error instanceof ConnectError ? update.error.rawMessage : String(update.error)}
            </span>
          )}
        </div>
      </form>
    </Shell>
  );
}
