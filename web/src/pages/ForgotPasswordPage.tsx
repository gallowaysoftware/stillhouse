import { FormEvent, useState } from "react";
import { Link } from "react-router-dom";
import { useMutation } from "@tanstack/react-query";

import { authClient } from "@/lib/clients";

// /forgot-password — public. Always tells the user "check your email" even
// when the address isn't on file. The server silently no-ops in that case.
export function ForgotPasswordPage() {
  const [email, setEmail] = useState("");
  const submit = useMutation({
    mutationFn: () => authClient.requestPasswordReset({ email }),
  });
  function onSubmit(e: FormEvent) {
    e.preventDefault();
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
            Reset password
          </h1>
          <p className="mt-1 text-sm text-fg-muted">
            Enter the email tied to your Stillhouse account; we'll send a reset link if it's on file.
          </p>
        </div>
        {submit.isSuccess ? (
          <p className="rounded border border-border bg-surface-3 px-3 py-3 text-sm text-fg">
            Check your inbox. The link expires in 1 hour.
          </p>
        ) : (
          <>
            <div className="space-y-1">
              <label className="block text-sm font-medium text-fg-muted">Email</label>
              <input
                type="email"
                required
                autoComplete="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                className="w-full rounded border border-border-strong bg-surface px-3 py-2 text-sm text-fg"
              />
            </div>
            <button
              type="submit"
              disabled={submit.isPending}
              className="w-full rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
            >
              {submit.isPending ? "Sending…" : "Send reset link"}
            </button>
          </>
        )}
        <p className="text-center text-xs text-fg-subtle">
          <Link to="/login" className="text-fg hover:underline">← Back to sign in</Link>
        </p>
      </form>
    </div>
  );
}
