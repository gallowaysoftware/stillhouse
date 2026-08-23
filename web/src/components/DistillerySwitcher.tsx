import { FormEvent, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { authClient } from "@/lib/clients";

/**
 * DistillerySwitcher — move an existing session to another distillery the
 * same email holds an account at.
 *
 * It asks for the password, and that is not an oversight. Login verifies
 * credentials against each candidate account separately, because one
 * password may be right at one distillery and wrong at another — so a
 * session at one proves nothing about another, and a switcher that
 * skipped the check would be an authentication bypass dressed as a
 * convenience. What this saves is signing out, not proving who you are.
 *
 * If the destination account has a second factor, it is required here
 * too, whether or not the current session needed one.
 */
export function DistillerySwitcher() {
  const [open, setOpen] = useState(false);
  const list = useQuery({
    queryKey: ["myDistilleries"],
    queryFn: () => authClient.listMyDistilleries({}),
    enabled: open,
  });

  const others = (list.data?.distilleries ?? []).filter((d) => !d.current);

  return (
    <div className="mt-2">
      <button
        onClick={() => setOpen((v) => !v)}
        className="text-[11px] text-fg-subtle underline-offset-2 hover:text-fg-muted hover:underline"
      >
        Switch distillery
      </button>
      {open && (
        <div className="mt-2 rounded border border-border bg-surface-2 p-2">
          {list.isLoading && <p className="text-xs text-fg-muted">Loading…</p>}
          {list.data && others.length === 0 && (
            <p className="text-xs text-fg-muted">
              This account is only at one distillery.
            </p>
          )}
          {others.map((d) => (
            <SwitchTo key={d.tenantId} tenantId={d.tenantId} name={d.tenantName} role={d.role} />
          ))}
        </div>
      )}
    </div>
  );
}

function SwitchTo({ tenantId, name, role }: { tenantId: string; name: string; role: string }) {
  const qc = useQueryClient();
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [needCode, setNeedCode] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const go = useMutation({
    mutationFn: () =>
      authClient.switchDistillery({ tenantId, password, totpCode: code }),
    onSuccess: (r) => {
      if (r.mfaRequired) {
        setNeedCode(true);
        setErr(null);
        return;
      }
      // Everything cached belongs to the old distillery. Clearing rather
      // than invalidating: an invalidate leaves stale rows on screen
      // while refetches land, and those rows are another tenant's.
      qc.clear();
      window.location.assign("/");
    },
    onError: (e) => setErr(e instanceof ConnectError ? e.rawMessage : String(e)),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    setErr(null);
    go.mutate();
  }

  return (
    <form onSubmit={submit} className="mb-2 last:mb-0">
      <div className="text-xs font-medium text-fg">{name}</div>
      <div className="mb-1 text-[11px] text-fg-subtle">you are {role} there</div>
      <input
        type="password"
        value={password}
        onChange={(e) => setPassword(e.target.value)}
        placeholder="password at that distillery"
        autoComplete="off"
        className="w-full rounded border border-border-strong px-2 py-1 text-xs"
      />
      {needCode && (
        <input
          value={code}
          onChange={(e) => setCode(e.target.value)}
          placeholder="6-digit code"
          inputMode="numeric"
          autoComplete="one-time-code"
          className="mt-1 w-full rounded border border-border-strong px-2 py-1 text-xs"
        />
      )}
      <button
        type="submit"
        disabled={go.isPending || !password}
        className="mt-1 w-full rounded bg-accent px-2 py-1 text-xs font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
      >
        {go.isPending ? "Switching…" : needCode ? "Confirm code" : "Switch"}
      </button>
      {err && <p className="mt-1 text-[11px] text-danger-fg">{err}</p>}
    </form>
  );
}
