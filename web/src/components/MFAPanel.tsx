import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { authClient } from "@/lib/clients";

/**
 * Two-factor authentication, for the account you are signed in as.
 *
 * Enrolment is two steps because it has to be: handing back a secret and
 * turning enforcement on in the same breath means a mistyped secret
 * locks the account at the next sign-in, and the failure lands on the
 * person who did everything right. Nothing is enforced until the app has
 * produced a code that matches.
 *
 * There is no QR image. Rendering one needs an encoder this project
 * would have to take as a dependency, and the two things that replace it
 * work everywhere: on a phone, tapping the enrolment link opens the
 * authenticator app directly, and on a desktop the grouped secret is
 * made for typing in. Worth revisiting if manual entry proves to be a
 * papercut.
 */
export function MFAPanel() {
  const qc = useQueryClient();
  const status = useQuery({
    queryKey: ["mfaStatus"],
    queryFn: () => authClient.mFAStatus({}),
  });
  const [enrolling, setEnrolling] = useState<{ uri: string; secret: string } | null>(null);
  const [code, setCode] = useState("");
  const [codes, setCodes] = useState<string[] | null>(null);
  const [disablePassword, setDisablePassword] = useState("");
  const [showDisable, setShowDisable] = useState(false);

  const begin = useMutation({
    mutationFn: () => authClient.beginMFAEnrolment({}),
    onSuccess: (r) => setEnrolling({ uri: r.enrolmentUri, secret: r.secret }),
  });
  const confirm = useMutation({
    mutationFn: () => authClient.confirmMFAEnrolment({ code }),
    onSuccess: (r) => {
      setCodes(r.recoveryCodes);
      setEnrolling(null);
      setCode("");
      qc.invalidateQueries({ queryKey: ["mfaStatus"] });
    },
  });
  const disable = useMutation({
    mutationFn: () => authClient.disableMFA({ currentPassword: disablePassword }),
    onSuccess: () => {
      setShowDisable(false);
      setDisablePassword("");
      setCodes(null);
      qc.invalidateQueries({ queryKey: ["mfaStatus"] });
    },
  });

  const s = status.data;

  return (
    <section className="mt-10">
      <h2 className="mb-3 text-sm font-semibold text-fg-muted">Two-factor authentication</h2>
      <div className="rounded-lg border border-border bg-surface-2 p-5 shadow-sm">
        <p className="mb-4 text-sm text-fg-muted">
          A code from an authenticator app, on top of your password. Stillhouse holds the
          records behind a filed excise return; a password on its own is one phishing email
          away from all of them.
        </p>

        {s && !s.available && (
          <p className="rounded border border-warning/40 bg-warning/10 px-3 py-2 text-sm text-fg-muted">
            Not available on this install: {s.unavailableReason} Secrets are encrypted at
            rest, so Stillhouse refuses to set up a second factor rather than store one in
            the clear.
          </p>
        )}

        {s?.available && s.enabled && !codes && (
          <div className="space-y-3">
            <p className="text-sm text-success-fg">Enabled.</p>
            <p className="text-sm text-fg-muted">
              {s.recoveryCodesRemaining} recovery code
              {s.recoveryCodesRemaining === 1 ? "" : "s"} left.
              {s.recoveryCodesRemaining <= 2 && (
                <span className="ml-1 text-warning-fg">
                  Running low — turn it off and set it up again to get a fresh set.
                </span>
              )}
            </p>
            {!showDisable ? (
              <button
                onClick={() => setShowDisable(true)}
                className="rounded border border-danger/40 px-3 py-2 text-sm text-danger-fg hover:bg-danger/10"
              >
                Turn off
              </button>
            ) : (
              <form
                onSubmit={(e) => { e.preventDefault(); disable.mutate(); }}
                className="flex flex-wrap items-end gap-3"
              >
                <div>
                  <label className="mb-2 block text-sm font-medium text-fg-muted">
                    Confirm your password
                  </label>
                  <input
                    type="password"
                    autoComplete="current-password"
                    value={disablePassword}
                    onChange={(e) => setDisablePassword(e.target.value)}
                    required
                    className="rounded border border-border-strong px-3 py-2 text-sm"
                  />
                </div>
                <button
                  type="submit"
                  disabled={disable.isPending}
                  className="rounded border border-danger/40 px-3 py-2 text-sm text-danger-fg hover:bg-danger/10 disabled:opacity-50"
                >
                  {disable.isPending ? "Turning off…" : "Turn off"}
                </button>
                <button
                  type="button"
                  onClick={() => { setShowDisable(false); setDisablePassword(""); }}
                  className="text-sm text-fg-muted hover:text-fg"
                >
                  Cancel
                </button>
                {disable.error && (
                  <span className="text-sm text-danger-fg">
                    {disable.error instanceof ConnectError
                      ? disable.error.rawMessage
                      : String(disable.error)}
                  </span>
                )}
              </form>
            )}
          </div>
        )}

        {s?.available && !s.enabled && !enrolling && (
          <button
            onClick={() => begin.mutate()}
            disabled={begin.isPending}
            className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
          >
            {begin.isPending ? "Starting…" : s.pending ? "Start again" : "Set up"}
          </button>
        )}
        {begin.error && (
          <p className="mt-2 text-sm text-danger-fg">
            {begin.error instanceof ConnectError ? begin.error.rawMessage : String(begin.error)}
          </p>
        )}

        {enrolling && (
          <div className="space-y-4">
            <ol className="list-decimal space-y-3 pl-5 text-sm text-fg-muted">
              <li>
                On your phone,{" "}
                <a href={enrolling.uri} className="text-accent underline">
                  tap here to add it to your authenticator app
                </a>
                .
                <p className="mt-2">
                  On a desktop, add an account by hand and type this key:
                </p>
                <p className="mt-1 select-all break-all rounded bg-surface-3 px-2 py-1 font-mono text-sm text-fg">
                  {enrolling.secret}
                </p>
              </li>
              <li>
                Enter the six-digit code it shows, so we know it works before anything
                starts depending on it.
                <form
                  onSubmit={(e) => { e.preventDefault(); confirm.mutate(); }}
                  className="mt-2 flex flex-wrap items-center gap-3"
                >
                  <input
                    value={code}
                    onChange={(e) => setCode(e.target.value)}
                    inputMode="numeric"
                    autoComplete="one-time-code"
                    placeholder="123456"
                    className="w-32 rounded border border-border-strong px-3 py-2 text-center font-mono tracking-widest"
                  />
                  <button
                    type="submit"
                    disabled={confirm.isPending || code.trim() === ""}
                    className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
                  >
                    {confirm.isPending ? "Checking…" : "Turn on"}
                  </button>
                  <button
                    type="button"
                    onClick={() => { setEnrolling(null); setCode(""); }}
                    className="text-sm text-fg-muted hover:text-fg"
                  >
                    Cancel
                  </button>
                  {confirm.error && (
                    <span className="text-sm text-danger-fg">
                      {confirm.error instanceof ConnectError
                        ? confirm.error.rawMessage
                        : String(confirm.error)}
                    </span>
                  )}
                </form>
              </li>
            </ol>
          </div>
        )}

        {codes && (
          <div className="mt-4 rounded border border-warning/40 bg-warning/10 p-3">
            <p className="text-sm font-medium text-fg">
              Recovery codes — shown once. Print them or put them somewhere that isn't
              the phone.
            </p>
            <p className="mt-1 text-sm text-fg-muted">
              Each works once, instead of a code from the app. They are the way back if
              the phone is lost, and there is no other one.
            </p>
            <div className="mt-3 grid grid-cols-2 gap-1 font-mono text-sm text-fg sm:grid-cols-3">
              {codes.map((c) => <span key={c} className="select-all">{c}</span>)}
            </div>
            <button
              onClick={() => setCodes(null)}
              className="mt-3 text-xs text-fg-muted hover:text-fg"
            >
              I've saved them
            </button>
          </div>
        )}
      </div>
    </section>
  );
}
