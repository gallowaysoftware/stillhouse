import { FormEvent, useState } from "react";
import { useNavigate, useSearchParams, Link } from "react-router-dom";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { inviteClient } from "@/lib/clients";

// /signup — public landing for invite-code redemption. ?code= prefills the
// invite field so an owner can hand out a single URL.
export function SignupPage() {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [params] = useSearchParams();
  const [code, setCode] = useState(params.get("code") ?? "");
  const [email, setEmail] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [password, setPassword] = useState("");
  const [tenantName, setTenantName] = useState("");
  const [licence, setLicence] = useState("");
  const [jurisdiction, setJurisdiction] = useState("CA-ON");

  const signup = useMutation({
    mutationFn: () =>
      inviteClient.signupWithInvite({
        code, email, password, displayName,
        tenantName, craSpiritsLicenceNumber: licence, defaultJurisdiction: jurisdiction,
      }),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["getMe"] });
      navigate("/", { replace: true });
    },
  });

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    signup.mutate();
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-surface px-4 py-10">
      <form
        onSubmit={onSubmit}
        className="w-full max-w-lg space-y-5 rounded-xl border border-border bg-surface-2 p-8 shadow-card-dark"
      >
        <div>
          <h1 className="flex items-center gap-2 text-2xl font-semibold tracking-tight text-fg">
            <span className="inline-block h-2.5 w-2.5 rounded-full bg-accent" />
            Stillhouse
          </h1>
          <p className="mt-1 text-sm text-fg-muted">
            Start a new distillery tenant. You'll need an invite code from an existing owner.
          </p>
        </div>

        <Field label="Invite code" value={code} onChange={setCode} required autoFocus />
        <Field label="Distillery name" value={tenantName} onChange={setTenantName} required placeholder="ACME Distillery" />
        <Field label="CRA spirits licence #" value={licence} onChange={setLicence} required />
        <Field label="Default jurisdiction (ISO 3166-2)" value={jurisdiction} onChange={setJurisdiction} placeholder="CA-ON" />

        <div className="border-t border-border pt-5">
          <h2 className="mb-3 text-sm font-semibold uppercase text-fg-muted">Owner login</h2>
          <Field label="Your name" value={displayName} onChange={setDisplayName} required />
          <Field label="Email" type="email" autoComplete="email" value={email} onChange={setEmail} required />
          <Field label="Password (12+ chars)" type="password" autoComplete="new-password" value={password} onChange={setPassword} required minLength={12} />
        </div>

        {signup.error && (
          <p className="text-sm text-red-400">
            {signup.error instanceof ConnectError ? signup.error.rawMessage : String(signup.error)}
          </p>
        )}

        <button
          type="submit"
          disabled={signup.isPending}
          className="w-full rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
        >
          {signup.isPending ? "Creating tenant…" : "Create my distillery"}
        </button>

        <p className="text-center text-xs text-fg-subtle">
          Already have an account? <Link to="/login" className="text-fg hover:underline">Sign in</Link>
        </p>
      </form>
    </div>
  );
}

function Field({
  label, value, onChange, type = "text", required, placeholder, autoComplete, autoFocus, minLength,
}: {
  label: string; value: string; onChange: (v: string) => void;
  type?: string; required?: boolean; placeholder?: string;
  autoComplete?: string; autoFocus?: boolean; minLength?: number;
}) {
  return (
    <div className="mb-3 space-y-1">
      <label className="block text-xs font-medium text-fg-muted">{label}</label>
      <input
        type={type}
        required={required}
        placeholder={placeholder}
        autoComplete={autoComplete}
        autoFocus={autoFocus}
        minLength={minLength}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="w-full rounded border border-border-strong bg-surface px-3 py-2 text-sm text-fg"
      />
    </div>
  );
}
