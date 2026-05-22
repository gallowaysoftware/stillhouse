import { FormEvent, useState, useEffect } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { Shell } from "@/components/Shell";
import { tenantClient, userClient } from "@/lib/clients";
import { UpdateTenantRequestSchema } from "@/gen/stillhouse/v1/tenant_pb";
import { ChangeMyPasswordRequestSchema, CreateUserRequestSchema, UserRole } from "@/gen/stillhouse/v1/user_pb";

const roleLabels: Record<UserRole, string> = {
  [UserRole.UNSPECIFIED]: "—",
  [UserRole.OWNER]: "Owner",
  [UserRole.OPERATOR]: "Operator",
  [UserRole.VIEWER]: "Viewer",
};

export function SettingsPage() {
  const qc = useQueryClient();
  const tenant = useQuery({
    queryKey: ["getTenant"],
    queryFn: () => tenantClient.getTenant({}),
  });
  const me = useQuery({
    queryKey: ["getMe"],
    queryFn: () => userClient.getMe({}),
  });
  const users = useQuery({
    queryKey: ["listUsers"],
    queryFn: () => userClient.listUsers({}),
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

  const isOwner = me.data?.user?.role === UserRole.OWNER;

  return (
    <Shell>
      <h1 className="mb-1 text-2xl font-semibold">Settings</h1>
      <p className="mb-6 text-sm text-stone-500">Tenant metadata — what shows in the sidebar and what gets stamped on records.</p>

      <form onSubmit={submit} className="mb-10 grid grid-cols-2 gap-4 rounded-lg border border-stone-200 bg-white p-5 shadow-sm">
        <div className="col-span-2">
          <h2 className="text-sm font-semibold uppercase text-stone-500">Distillery</h2>
        </div>
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
          {isOwner ? (
            <button
              type="submit"
              disabled={update.isPending}
              className="rounded bg-stone-900 px-3 py-2 text-sm font-medium text-white hover:bg-stone-800 disabled:bg-stone-400"
            >
              {update.isPending ? "Saving…" : "Save changes"}
            </button>
          ) : (
            <span className="text-xs text-stone-500">Owner-only — ask your distillery owner to update these fields.</span>
          )}
          {saved && <span className="text-sm text-emerald-700">Saved.</span>}
          {update.error && (
            <span className="text-sm text-red-600">
              {update.error instanceof ConnectError ? update.error.rawMessage : String(update.error)}
            </span>
          )}
        </div>
      </form>

      <UsersPanel
        isOwner={isOwner}
        users={users.data?.users ?? []}
        onCreated={() => qc.invalidateQueries({ queryKey: ["listUsers"] })}
      />

      <ChangePasswordPanel />
    </Shell>
  );
}

function ChangePasswordPanel() {
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [done, setDone] = useState(false);

  const change = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof ChangeMyPasswordRequestSchema>>) =>
      userClient.changeMyPassword(msg),
    onSuccess: () => {
      setDone(true);
      setCurrentPassword(""); setNewPassword(""); setConfirm("");
      setTimeout(() => setDone(false), 5000);
    },
  });
  const mismatch = newPassword !== "" && newPassword !== confirm;
  const tooShort = newPassword !== "" && newPassword.length < 12;
  function submit(e: FormEvent) {
    e.preventDefault();
    if (mismatch || tooShort) return;
    change.mutate(
      create(ChangeMyPasswordRequestSchema, { currentPassword, newPassword }),
    );
  }
  return (
    <section className="mt-10">
      <h2 className="mb-3 text-sm font-semibold uppercase text-stone-500">Change my password</h2>
      <form onSubmit={submit} className="grid grid-cols-3 gap-3 rounded-lg border border-stone-200 bg-white p-5 shadow-sm">
        <div>
          <label className="mb-1 block text-xs font-medium text-stone-600">Current password</label>
          <input value={currentPassword} onChange={(e) => setCurrentPassword(e.target.value)} type="password" autoComplete="current-password" required className="w-full rounded border border-stone-300 px-3 py-2 text-sm" />
        </div>
        <div>
          <label className="mb-1 block text-xs font-medium text-stone-600">New password (12+ chars)</label>
          <input value={newPassword} onChange={(e) => setNewPassword(e.target.value)} type="password" autoComplete="new-password" required className="w-full rounded border border-stone-300 px-3 py-2 text-sm" />
        </div>
        <div>
          <label className="mb-1 block text-xs font-medium text-stone-600">Confirm new password</label>
          <input value={confirm} onChange={(e) => setConfirm(e.target.value)} type="password" autoComplete="new-password" required className="w-full rounded border border-stone-300 px-3 py-2 text-sm" />
        </div>
        <div className="col-span-3 flex items-center gap-3">
          <button
            type="submit"
            disabled={change.isPending || mismatch || tooShort}
            className="rounded bg-stone-900 px-3 py-2 text-sm font-medium text-white hover:bg-stone-800 disabled:bg-stone-400"
          >
            {change.isPending ? "Updating…" : "Update password"}
          </button>
          {done && <span className="text-sm text-emerald-700">Password updated.</span>}
          {mismatch && <span className="text-sm text-red-600">Passwords don't match.</span>}
          {tooShort && !mismatch && <span className="text-sm text-red-600">Must be at least 12 characters.</span>}
          {change.error && (
            <span className="text-sm text-red-600">
              {change.error instanceof ConnectError ? change.error.rawMessage : String(change.error)}
            </span>
          )}
        </div>
      </form>
    </section>
  );
}

function UsersPanel({ isOwner, users, onCreated }: {
  isOwner: boolean;
  users: { id: string; email: string; displayName: string; role: UserRole }[];
  onCreated: () => void;
}) {
  const [email, setEmail] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [role, setRole] = useState<UserRole>(UserRole.OPERATOR);
  const [lastCreated, setLastCreated] = useState<{ email: string; password: string } | null>(null);

  const create_ = useMutation({
    mutationFn: (msg: ReturnType<typeof create<typeof CreateUserRequestSchema>>) =>
      userClient.createUser(msg),
    onSuccess: (resp) => {
      onCreated();
      setLastCreated({ email: resp.user!.email, password: resp.initialPassword });
      setEmail("");
      setDisplayName("");
    },
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    create_.mutate(
      create(CreateUserRequestSchema, { email, displayName, role }),
    );
  }

  return (
    <section>
      <h2 className="mb-3 text-sm font-semibold uppercase text-stone-500">Users</h2>
      <div className="mb-6 overflow-hidden rounded-lg border border-stone-200 bg-white shadow-sm">
        <table className="min-w-full divide-y divide-stone-200 text-sm">
          <thead className="bg-stone-50 text-left text-xs uppercase text-stone-500">
            <tr>
              <th className="px-4 py-3">Display name</th>
              <th className="px-4 py-3">Email</th>
              <th className="px-4 py-3">Role</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-stone-100">
            {users.length === 0 && (
              <tr><td colSpan={3} className="px-4 py-3 text-stone-500">No users.</td></tr>
            )}
            {users.map((u) => (
              <tr key={u.id}>
                <td className="px-4 py-3 text-stone-900">{u.displayName}</td>
                <td className="px-4 py-3 text-stone-600">{u.email}</td>
                <td className="px-4 py-3 text-stone-600">{roleLabels[u.role]}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {!isOwner ? (
        <p className="text-sm text-stone-500">Only owners can invite new users.</p>
      ) : (
        <form onSubmit={submit} className="grid grid-cols-3 gap-3 rounded-lg border border-stone-200 bg-white p-5 shadow-sm">
          <div>
            <label className="mb-1 block text-xs font-medium text-stone-600">Email</label>
            <input value={email} onChange={(e) => setEmail(e.target.value)} type="email" required className="w-full rounded border border-stone-300 px-3 py-2 text-sm" />
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-stone-600">Display name</label>
            <input value={displayName} onChange={(e) => setDisplayName(e.target.value)} required className="w-full rounded border border-stone-300 px-3 py-2 text-sm" />
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-stone-600">Role</label>
            <select value={role} onChange={(e) => setRole(Number(e.target.value) as UserRole)} className="w-full rounded border border-stone-300 px-3 py-2 text-sm">
              <option value={UserRole.OPERATOR}>Operator</option>
              <option value={UserRole.OWNER}>Owner</option>
              <option value={UserRole.VIEWER}>Viewer</option>
            </select>
          </div>
          <div className="col-span-3 flex items-center gap-3">
            <button
              type="submit"
              disabled={create_.isPending}
              className="rounded bg-stone-900 px-3 py-2 text-sm font-medium text-white hover:bg-stone-800 disabled:bg-stone-400"
            >
              {create_.isPending ? "Creating…" : "Invite user"}
            </button>
            {create_.error && (
              <span className="text-sm text-red-600">
                {create_.error instanceof ConnectError ? create_.error.rawMessage : String(create_.error)}
              </span>
            )}
          </div>
          {lastCreated && (
            <div className="col-span-3 rounded border border-emerald-200 bg-emerald-50 p-3 text-sm text-emerald-900">
              <p className="font-medium">User created.</p>
              <p>Deliver this initial password to {lastCreated.email} through a secure channel — it will not be shown again:</p>
              <p className="mt-2 font-mono text-stone-900">{lastCreated.password}</p>
            </div>
          )}
        </form>
      )}
    </section>
  );
}
