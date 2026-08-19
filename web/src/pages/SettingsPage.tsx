import { FormEvent, useState, useEffect } from "react";
import { useNavigate } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { Shell } from "@/components/Shell";
import { apiTokenClient, inviteClient, tenantClient, userClient } from "@/lib/clients";
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
      <h1 className="mb-1 text-3xl font-bold tracking-tight">Settings</h1>
      <p className="mb-6 text-sm text-fg-muted">Tenant metadata — what shows in the sidebar and what gets stamped on records.</p>

      <form onSubmit={submit} className="mb-10 grid grid-cols-1 sm:grid-cols-2 gap-3 sm:gap-4 rounded-lg border border-border bg-surface-2 p-4 sm:p-5 shadow-sm">
        <div className="col-span-2">
          <h2 className="text-sm font-semibold text-fg-muted">Distillery</h2>
        </div>
        <div>
          <label className="mb-2 block text-sm font-medium text-fg-muted">Distillery name</label>
          <input value={name} onChange={(e) => setName(e.target.value)} required className="w-full rounded border border-border-strong px-3 py-2 text-sm" />
        </div>
        <div>
          <label className="mb-2 block text-sm font-medium text-fg-muted">CRA spirits licence #</label>
          <input value={licence} onChange={(e) => setLicence(e.target.value)} required className="w-full rounded border border-border-strong px-3 py-2 text-sm" />
        </div>
        <div>
          <label className="mb-2 block text-sm font-medium text-fg-muted">Excise warehouse licence # (optional)</label>
          <input value={warehouse} onChange={(e) => setWarehouse(e.target.value)} className="w-full rounded border border-border-strong px-3 py-2 text-sm" />
        </div>
        <div>
          <label className="mb-2 block text-sm font-medium text-fg-muted">Default jurisdiction (ISO 3166-2)</label>
          <input value={jurisdiction} onChange={(e) => setJurisdiction(e.target.value)} required placeholder="CA-ON" className="w-full rounded border border-border-strong px-3 py-2 text-sm" />
        </div>
        <div className="col-span-2 flex items-center gap-3">
          {isOwner ? (
            <button
              type="submit"
              disabled={update.isPending}
              className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
            >
              {update.isPending ? "Saving…" : "Save changes"}
            </button>
          ) : (
            <span className="text-xs text-fg-muted">Owner-only — ask your distillery owner to update these fields.</span>
          )}
          {saved && <span className="text-sm text-success-fg">Saved.</span>}
          {update.error && (
            <span className="text-sm text-danger-fg">
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

      {isOwner && <InvitesPanel />}

      <APITokensPanel />

      {isOwner && (
        <section className="mt-10">
          <h2 className="mb-3 text-sm font-semibold text-fg-muted">Tenant data export</h2>
          <div className="rounded-lg border border-border bg-surface-2 p-5 shadow-sm">
            <p className="mb-3 text-sm text-fg">
              Download a zip containing one CSV per significant table — recipes through B266 history, plus the
              full audit log. Covers your Excise Act s.206 retention duty and your PIPEDA right-to-data.
            </p>
            <a
              href="/export/tenant.zip"
              className="inline-block rounded border border-border-strong px-3 py-2 text-sm text-fg hover:bg-surface-3"
            >
              Download tenant export (.zip)
            </a>
          </div>
        </section>
      )}

      <ChangePasswordPanel />

      {isOwner && <DangerZone tenantName={me.data?.tenant?.name ?? ""} />}
    </Shell>
  );
}

// DangerZone — irreversible owner actions live here, visually quarantined.
// Currently just delete-my-tenant; account-deletion is hard delete (every
// FK to tenants cascades). Type-the-name confirmation matches the GitHub /
// Stripe pattern for destructive operations.
function DangerZone({ tenantName }: { tenantName: string }) {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [confirm, setConfirm] = useState("");
  const [open, setOpen] = useState(false);
  const del = useMutation({
    mutationFn: () => tenantClient.deleteMyTenant({ confirmName: confirm }),
    onSuccess: async () => {
      // Tenant + session both gone server-side; clear local state and
      // bounce to the login screen with a "tenant deleted" note.
      qc.clear();
      navigate("/login?deleted=1", { replace: true });
    },
  });

  return (
    <section className="mt-10">
      <h2 className="mb-3 text-sm font-semibold uppercase text-danger-fg">Danger zone</h2>
      <div className="rounded-lg border border-danger/40 bg-danger/5 p-5">
        <h3 className="text-sm font-semibold text-fg">Delete this distillery</h3>
        <p className="mt-1 text-sm text-fg-muted">
          Wipes every record under <span className="font-medium text-fg">{tenantName}</span> —
          users, materials, recipes, mashes, ferments, distillations, barrels, bulk movements,
          bottling runs, removals, B266 history, audit log. Cannot be undone. Export your data
          first if you want a copy.
        </p>
        {!open ? (
          <button
            onClick={() => setOpen(true)}
            className="mt-4 rounded border border-danger/40 px-3 py-2 text-sm font-medium text-danger-fg hover:bg-danger/10"
          >
            Delete tenant…
          </button>
        ) : (
          <div className="mt-4 space-y-3 rounded border border-danger/40 bg-surface-2 p-4">
            <p className="text-sm text-fg">
              Retype <span className="font-mono text-danger-fg">{tenantName}</span> below to confirm.
            </p>
            <input
              autoFocus
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              className="w-full rounded border border-border-strong bg-surface px-3 py-2 text-sm text-fg"
            />
            {del.error && (
              <p className="text-sm text-danger-fg">
                {del.error instanceof ConnectError ? del.error.rawMessage : String(del.error)}
              </p>
            )}
            <div className="flex gap-2">
              <button
                onClick={() => del.mutate()}
                disabled={confirm !== tenantName || del.isPending}
                className="rounded bg-danger px-3 py-2 text-sm font-medium text-white hover:bg-danger/80 disabled:opacity-50"
              >
                {del.isPending ? "Deleting…" : "I understand, delete it"}
              </button>
              <button
                onClick={() => { setOpen(false); setConfirm(""); }}
                className="rounded border border-border-strong px-3 py-2 text-sm text-fg hover:bg-surface-3"
              >
                Cancel
              </button>
            </div>
          </div>
        )}
      </div>
    </section>
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
      <h2 className="mb-3 text-sm font-semibold text-fg-muted">Change my password</h2>
      <form onSubmit={submit} className="grid grid-cols-3 gap-3 rounded-lg border border-border bg-surface-2 p-5 shadow-sm">
        <div>
          <label className="mb-2 block text-sm font-medium text-fg-muted">Current password</label>
          <input value={currentPassword} onChange={(e) => setCurrentPassword(e.target.value)} type="password" autoComplete="current-password" required className="w-full rounded border border-border-strong px-3 py-2 text-sm" />
        </div>
        <div>
          <label className="mb-2 block text-sm font-medium text-fg-muted">New password (12+ chars)</label>
          <input value={newPassword} onChange={(e) => setNewPassword(e.target.value)} type="password" autoComplete="new-password" required className="w-full rounded border border-border-strong px-3 py-2 text-sm" />
        </div>
        <div>
          <label className="mb-2 block text-sm font-medium text-fg-muted">Confirm new password</label>
          <input value={confirm} onChange={(e) => setConfirm(e.target.value)} type="password" autoComplete="new-password" required className="w-full rounded border border-border-strong px-3 py-2 text-sm" />
        </div>
        <div className="col-span-3 flex items-center gap-3">
          <button
            type="submit"
            disabled={change.isPending || mismatch || tooShort}
            className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
          >
            {change.isPending ? "Updating…" : "Update password"}
          </button>
          {done && <span className="text-sm text-success-fg">Password updated.</span>}
          {mismatch && <span className="text-sm text-danger-fg">Passwords don't match.</span>}
          {tooShort && !mismatch && <span className="text-sm text-danger-fg">Must be at least 12 characters.</span>}
          {change.error && (
            <span className="text-sm text-danger-fg">
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
      <h2 className="mb-3 text-sm font-semibold text-fg-muted">Users</h2>
      <div className="mb-6 overflow-hidden rounded-lg border border-border bg-surface-2 shadow-sm">
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="bg-surface-3 text-left text-xs text-fg-muted">
            <tr>
              <th className="px-4 py-3">Display name</th>
              <th className="px-4 py-3">Email</th>
              <th className="px-4 py-3">Role</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {users.length === 0 && (
              <tr><td colSpan={3} className="px-4 py-3 text-fg-muted">No users.</td></tr>
            )}
            {users.map((u) => (
              <tr key={u.id}>
                <td className="px-4 py-3 text-fg">{u.displayName}</td>
                <td className="px-4 py-3 text-fg-muted">{u.email}</td>
                <td className="px-4 py-3 text-fg-muted">{roleLabels[u.role]}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {!isOwner ? (
        <p className="text-sm text-fg-muted">Only owners can invite new users.</p>
      ) : (
        <form onSubmit={submit} className="grid grid-cols-3 gap-3 rounded-lg border border-border bg-surface-2 p-5 shadow-sm">
          <div>
            <label className="mb-2 block text-sm font-medium text-fg-muted">Email</label>
            <input value={email} onChange={(e) => setEmail(e.target.value)} type="email" required className="w-full rounded border border-border-strong px-3 py-2 text-sm" />
          </div>
          <div>
            <label className="mb-2 block text-sm font-medium text-fg-muted">Display name</label>
            <input value={displayName} onChange={(e) => setDisplayName(e.target.value)} required className="w-full rounded border border-border-strong px-3 py-2 text-sm" />
          </div>
          <div>
            <label className="mb-2 block text-sm font-medium text-fg-muted">Role</label>
            <select value={role} onChange={(e) => setRole(Number(e.target.value) as UserRole)} className="w-full rounded border border-border-strong px-3 py-2 text-sm">
              <option value={UserRole.OPERATOR}>Operator</option>
              <option value={UserRole.OWNER}>Owner</option>
              <option value={UserRole.VIEWER}>Viewer</option>
            </select>
          </div>
          <div className="col-span-3 flex items-center gap-3">
            <button
              type="submit"
              disabled={create_.isPending}
              className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
            >
              {create_.isPending ? "Creating…" : "Invite user"}
            </button>
            {create_.error && (
              <span className="text-sm text-danger-fg">
                {create_.error instanceof ConnectError ? create_.error.rawMessage : String(create_.error)}
              </span>
            )}
          </div>
          {lastCreated && (
            <div className="col-span-3 rounded border border-success/30 bg-success/10 p-3 text-sm text-success-fg">
              <p className="font-medium">User created.</p>
              <p>Deliver this initial password to {lastCreated.email} through a secure channel — it will not be shown again:</p>
              <p className="mt-2 font-mono text-fg">{lastCreated.password}</p>
            </div>
          )}
        </form>
      )}
    </section>
  );
}

// InvitesPanel — owner-only. Generate codes to hand out to prospective
// distillery owners. Each redemption creates a brand new tenant; the
// invite tracks the redeemed_email + tenant_id afterward for attribution.
function InvitesPanel() {
  const qc = useQueryClient();
  const list = useQuery({
    queryKey: ["listMyInviteCodes"],
    queryFn: () => inviteClient.listMyInviteCodes({}),
  });
  const [note, setNote] = useState("");
  const [expiresInDays, setExpiresInDays] = useState("30");
  const [justCreated, setJustCreated] = useState<string | null>(null);

  const createInvite = useMutation({
    mutationFn: () => inviteClient.createInviteCode({ note, expiresInDays: Number(expiresInDays) || 0 }),
    onSuccess: (resp) => {
      setJustCreated(resp.invite?.code ?? null);
      setNote("");
      qc.invalidateQueries({ queryKey: ["listMyInviteCodes"] });
    },
  });
  const revoke = useMutation({
    mutationFn: (code: string) => inviteClient.revokeInviteCode({ code }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["listMyInviteCodes"] }),
  });

  function copySignupURL(code: string) {
    const url = `${window.location.origin}/signup?code=${encodeURIComponent(code)}`;
    void navigator.clipboard.writeText(url);
  }

  return (
    <section className="mt-10">
      <h2 className="mb-3 text-sm font-semibold text-fg-muted">Distillery invites</h2>
      <div className="rounded-lg border border-border bg-surface-2 p-5 shadow-sm">
        <p className="mb-4 text-sm text-fg-muted">
          Generate a one-time code for someone to create their own distillery tenant on this Stillhouse install.
          They'll get a signup URL pre-filled with the code; redeeming it creates their tenant + their owner login
          and uses up the code.
        </p>
        <form
          onSubmit={(e) => { e.preventDefault(); createInvite.mutate(); }}
          className="mb-4 flex flex-wrap items-end gap-3 border-b border-border pb-4"
        >
          <div className="flex-1 min-w-[12rem]">
            <label className="mb-2 block text-sm font-medium text-fg-muted">Note (who's this for?)</label>
            <input
              value={note}
              onChange={(e) => setNote(e.target.value)}
              placeholder="ACME Distillery"
              className="w-full rounded border border-border-strong bg-surface px-3 py-2 text-sm text-fg"
            />
          </div>
          <div>
            <label className="mb-2 block text-sm font-medium text-fg-muted">Expires in (days)</label>
            <input
              type="number"
              min="0"
              value={expiresInDays}
              onChange={(e) => setExpiresInDays(e.target.value)}
              className="w-24 rounded border border-border-strong bg-surface px-3 py-2 text-sm text-fg"
            />
          </div>
          <button
            type="submit"
            disabled={createInvite.isPending}
            className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
          >
            {createInvite.isPending ? "Generating…" : "Generate code"}
          </button>
          {createInvite.error && (
            <span className="text-sm text-danger-fg">
              {createInvite.error instanceof ConnectError ? createInvite.error.rawMessage : String(createInvite.error)}
            </span>
          )}
        </form>
        {justCreated && (
          <div className="mb-4 rounded border border-success/40 bg-success/10 p-3 text-sm text-success-fg">
            <p className="font-medium">New invite ready.</p>
            <p className="mt-1">
              Share this signup URL with the recipient — it expires after one redemption.
            </p>
            <p className="mt-2 break-all rounded bg-surface px-2 py-1 font-mono text-xs text-fg">
              {window.location.origin}/signup?code={justCreated}
            </p>
            <button
              onClick={() => copySignupURL(justCreated)}
              className="mt-2 text-xs text-fg-muted hover:text-fg"
            >
              Copy URL to clipboard
            </button>
          </div>
        )}
        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="text-left text-xs text-fg-muted">
            <tr>
              <th className="px-2 py-2">Note</th>
              <th className="px-2 py-2">Code</th>
              <th className="px-2 py-2">Status</th>
              <th className="px-2 py-2 text-right"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {list.data?.invites.length === 0 && (
              <tr><td colSpan={4} className="px-2 py-3 text-fg-muted">No invites yet.</td></tr>
            )}
            {list.data?.invites.map((i) => {
              const status = i.redeemedAt
                ? `Redeemed by ${i.redeemedEmail}`
                : i.revokedAt
                  ? "Revoked"
                  : i.expiresAt && Number(i.expiresAt.seconds) * 1000 < Date.now()
                    ? "Expired"
                    : "Active";
              const active = !i.redeemedAt && !i.revokedAt && (!i.expiresAt || Number(i.expiresAt.seconds) * 1000 > Date.now());
              return (
                <tr key={i.code}>
                  <td className="px-2 py-2 text-fg">{i.note || <span className="text-fg-subtle">—</span>}</td>
                  <td className="px-2 py-2 font-mono text-xs text-fg-muted">{i.code.slice(0, 12)}…</td>
                  <td className="px-2 py-2 text-fg-muted">{status}</td>
                  <td className="px-2 py-2 text-right">
                    {active && (
                      <>
                        <button
                          onClick={() => copySignupURL(i.code)}
                          className="mr-3 text-xs text-fg-muted hover:text-fg"
                        >
                          Copy URL
                        </button>
                        <button
                          onClick={() => revoke.mutate(i.code)}
                          disabled={revoke.isPending}
                          className="text-xs text-fg-muted hover:text-danger-fg disabled:opacity-50"
                        >
                          Revoke
                        </button>
                      </>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </section>
  );
}

// APITokensPanel — every user manages their own personal access tokens.
// Tokens are how non-browser clients (the MCP server, future scripts)
// authenticate. The plaintext value is shown once at issue time and
// never again; the row in the table only carries the hash-derived id.
function APITokensPanel() {
  const qc = useQueryClient();
  const list = useQuery({
    queryKey: ["listAPITokens"],
    queryFn: () => apiTokenClient.listAPITokens({}),
  });
  const [name, setName] = useState("");
  const [justIssued, setJustIssued] = useState<{ name: string; plaintext: string } | null>(null);
  const [copied, setCopied] = useState(false);

  const issue = useMutation({
    mutationFn: (n: string) => apiTokenClient.issueAPIToken({ name: n }),
    onSuccess: (resp) => {
      setJustIssued({ name: resp.token?.name ?? "", plaintext: resp.plaintext });
      setName("");
      qc.invalidateQueries({ queryKey: ["listAPITokens"] });
    },
  });
  const revoke = useMutation({
    mutationFn: (id: string) => apiTokenClient.revokeAPIToken({ id }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["listAPITokens"] }),
  });

  function copyPlaintext() {
    if (!justIssued) return;
    void navigator.clipboard.writeText(justIssued.plaintext);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  }

  const mcpURL = `${window.location.origin}/mcp`;

  return (
    <section className="mt-10">
      <h2 className="mb-3 text-sm font-semibold text-fg-muted">API tokens (MCP &amp; scripts)</h2>
      <div className="rounded-lg border border-border bg-surface-2 p-5 shadow-sm">
        <p className="mb-4 text-sm text-fg-muted">
          Personal access tokens authenticate non-browser clients — most importantly the
          built-in Model Context Protocol server, so an LLM like Claude (on your phone or
          desktop) can read the ledger and capture activity from the still floor. Tokens
          are per-user; revoke them when a device is lost.
        </p>
        <div className="mb-4 rounded border border-border bg-surface p-3 text-sm">
          <p className="mb-1 font-medium text-fg">Connect Claude to this Stillhouse install</p>
          <p className="text-fg-muted">
            In Claude's MCP server settings, add a remote server at:
          </p>
          <p className="mt-1 break-all rounded bg-surface-3 px-2 py-1 font-mono text-xs text-fg">{mcpURL}</p>
          <p className="mt-2 text-fg-muted">
            With header <span className="font-mono text-fg">Authorization: Bearer &lt;your token&gt;</span>.
          </p>
        </div>

        <form
          onSubmit={(e) => { e.preventDefault(); if (name.trim()) issue.mutate(name.trim()); }}
          className="mb-4 flex flex-wrap items-end gap-3 border-b border-border pb-4"
        >
          <div className="flex-1 min-w-[12rem]">
            <label className="mb-2 block text-sm font-medium text-fg-muted">Token name</label>
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="phone, laptop, MCP, …"
              required
              maxLength={100}
              className="w-full rounded border border-border-strong bg-surface px-3 py-2 text-sm text-fg"
            />
          </div>
          <button
            type="submit"
            disabled={issue.isPending || !name.trim()}
            className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
          >
            {issue.isPending ? "Issuing…" : "Issue token"}
          </button>
          {issue.error && (
            <span className="text-sm text-danger-fg">
              {issue.error instanceof ConnectError ? issue.error.rawMessage : String(issue.error)}
            </span>
          )}
        </form>

        {justIssued && (
          <div className="mb-4 rounded border border-warning/40 bg-warning/10 p-3 text-sm">
            <p className="font-medium text-fg">New token "{justIssued.name}" — shown once.</p>
            <p className="mt-1 text-fg-muted">
              Copy it now. We only store the hash; if you lose it you'll have to issue a new one.
            </p>
            <p className="mt-2 break-all rounded bg-surface px-2 py-1 font-mono text-xs text-fg">
              {justIssued.plaintext}
            </p>
            <div className="mt-2 flex items-center gap-3">
              <button onClick={copyPlaintext} className="text-xs text-fg-muted hover:text-fg">
                Copy to clipboard
              </button>
              {copied && <span className="text-xs text-success-fg">Copied.</span>}
              <button
                onClick={() => { setJustIssued(null); setCopied(false); }}
                className="ml-auto text-xs text-fg-muted hover:text-fg"
              >
                I've saved it
              </button>
            </div>
          </div>
        )}

        <table className="min-w-full divide-y divide-border text-sm">
          <thead className="text-left text-xs text-fg-muted">
            <tr>
              <th className="px-2 py-2">Name</th>
              <th className="px-2 py-2">Created</th>
              <th className="px-2 py-2">Last used</th>
              <th className="px-2 py-2">Status</th>
              <th className="px-2 py-2 text-right"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {list.data?.tokens.length === 0 && (
              <tr><td colSpan={5} className="px-2 py-3 text-fg-muted">No tokens yet.</td></tr>
            )}
            {list.data?.tokens.map((t) => {
              const created = t.createdAt ? new Date(Number(t.createdAt.seconds) * 1000) : null;
              const lastUsed = t.lastUsedAt ? new Date(Number(t.lastUsedAt.seconds) * 1000) : null;
              const revoked = !!t.revokedAt;
              return (
                <tr key={t.id} className={revoked ? "opacity-60" : ""}>
                  <td className="px-2 py-2 text-fg">{t.name}</td>
                  <td className="px-2 py-2 text-fg-muted">{created ? created.toLocaleDateString() : "—"}</td>
                  <td className="px-2 py-2 text-fg-muted">{lastUsed ? lastUsed.toLocaleString() : <span className="text-fg-subtle">never</span>}</td>
                  <td className="px-2 py-2 text-fg-muted">{revoked ? "Revoked" : "Active"}</td>
                  <td className="px-2 py-2 text-right">
                    {!revoked && (
                      <button
                        onClick={() => revoke.mutate(t.id)}
                        disabled={revoke.isPending}
                        className="text-xs text-fg-muted hover:text-danger-fg disabled:opacity-50"
                      >
                        Revoke
                      </button>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </section>
  );
}
