import { FormEvent, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { useMutation } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { authClient } from "@/lib/clients";

// /reset-password?token=... — public. Token comes from the password-reset
// email; consumed single-use server-side.
export function ResetPasswordPage() {
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const token = params.get("token") ?? "";
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const mismatch = password !== "" && password !== confirm;
  const tooShort = password !== "" && password.length < 12;

  const submit = useMutation({
    mutationFn: () => authClient.resetPassword({ token, newPassword: password }),
    onSuccess: () => navigate("/login?reset=1", { replace: true }),
  });

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (mismatch || tooShort || !token) return;
    submit.mutate();
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-surface px-4">
      <form
        onSubmit={onSubmit}
        className="w-full max-w-sm space-y-5 rounded-xl border border-border bg-surface-2 p-8 shadow-card-dark"
      >
        <div>
          <h1 className="flex items-center gap-2 text-3xl font-bold tracking-tight tracking-tight text-fg">
            <span className="inline-block h-2.5 w-2.5 rounded-full bg-accent" />
            Set a new password
          </h1>
        </div>
        {!token && (
          <p className="rounded border border-red-500/40 bg-red-500/10 px-3 py-3 text-sm text-red-300">
            Missing token. Open the link from your email exactly as sent.
          </p>
        )}
        <div className="space-y-1">
          <label className="block text-sm font-medium text-fg-muted">New password (12+ chars)</label>
          <input
            type="password"
            required
            autoComplete="new-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            className="w-full rounded border border-border-strong bg-surface px-3 py-2 text-sm text-fg"
          />
        </div>
        <div className="space-y-1">
          <label className="block text-sm font-medium text-fg-muted">Confirm new password</label>
          <input
            type="password"
            required
            autoComplete="new-password"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            className="w-full rounded border border-border-strong bg-surface px-3 py-2 text-sm text-fg"
          />
        </div>
        {mismatch && <p className="text-sm text-red-400">Passwords don't match.</p>}
        {tooShort && <p className="text-sm text-red-400">Must be at least 12 characters.</p>}
        {submit.error && (
          <p className="text-sm text-red-400">
            {submit.error instanceof ConnectError ? submit.error.rawMessage : String(submit.error)}
          </p>
        )}
        <button
          type="submit"
          disabled={submit.isPending || mismatch || tooShort || !token}
          className="w-full rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
        >
          {submit.isPending ? "Resetting…" : "Reset password"}
        </button>
        <p className="text-center text-xs text-fg-subtle">
          <Link to="/login" className="text-fg hover:underline">← Back to sign in</Link>
        </p>
      </form>
    </div>
  );
}
