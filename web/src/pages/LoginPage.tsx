import { FormEvent, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { authClient } from "@/lib/clients";
import { setLang, useLang, useT } from "@/lib/i18n";

export function LoginPage() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const lang = useLang();
  const t = useT();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  // One email address can hold an account at more than one distillery —
  // the outside bookkeeper case. When it does, the server verifies the
  // password against each, returns the ones that matched, and creates no
  // session until we come back naming one.
  const [choices, setChoices] = useState<{ tenantId: string; tenantName: string }[]>([]);
  // The second factor. The server never asks for one until the password
  // has already been verified, so reaching this step is itself only ever
  // reached by someone who supplied the right password.
  const [needsCode, setNeedsCode] = useState(false);
  const [code, setCode] = useState("");
  const [useRecovery, setUseRecovery] = useState(false);
  const [pickedTenant, setPickedTenant] = useState("");

  const login = useMutation({
    mutationFn: (tenantId: string) =>
      authClient.login({
        email,
        password,
        tenantId,
        totpCode: useRecovery ? "" : code,
        recoveryCode: useRecovery ? code : "",
      }),
    onSuccess: async (resp, tenantId) => {
      if (resp.choices.length > 0) {
        setChoices(resp.choices.map((c) => ({ tenantId: c.tenantId, tenantName: c.tenantName })));
        return;
      }
      if (resp.mfaRequired) {
        setPickedTenant(tenantId);
        setNeedsCode(true);
        return;
      }
      setChoices([]);
      setNeedsCode(false);
      await queryClient.invalidateQueries({ queryKey: ["getMe"] });
      navigate("/", { replace: true });
    },
  });

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    login.mutate(needsCode ? pickedTenant : "");
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-surface px-4">
      <form
        onSubmit={onSubmit}
        className="w-full max-w-sm space-y-5 rounded-xl border border-border bg-surface-2 p-8 shadow-card-dark"
      >
        <div className="flex items-start justify-between">
          <div>
            <h1 className="flex items-center gap-2 text-3xl font-bold tracking-tight tracking-tight text-fg">
              <span className="inline-block h-2.5 w-2.5 rounded-full bg-accent" />
              Stillhouse
            </h1>
            <p className="mt-1 text-sm text-fg-muted">
              {t("Sign in to continue.", "Connectez-vous pour continuer.")}
            </p>
          </div>
          <div className="flex items-center gap-1 text-xs text-fg-subtle">
            <button
              type="button"
              className={`rounded px-1.5 py-0.5 ${lang === "en" ? "bg-surface-3 text-fg" : "hover:text-fg"}`}
              onClick={() => setLang("en")}
            >
              EN
            </button>
            <button
              type="button"
              className={`rounded px-1.5 py-0.5 ${lang === "fr" ? "bg-surface-3 text-fg" : "hover:text-fg"}`}
              onClick={() => setLang("fr")}
            >
              FR
            </button>
          </div>
        </div>

        {needsCode ? (
          <div className="space-y-3">
            <p className="text-sm text-fg-muted">
              {useRecovery
                ? t(
                    "Enter one of the recovery codes you saved when you set this up. Each works once.",
                    "Entrez un des codes de récupération enregistrés lors de la configuration. Chacun ne sert qu'une fois.",
                  )
                : t(
                    "Enter the six-digit code from your authenticator app.",
                    "Entrez le code à six chiffres de votre application d'authentification.",
                  )}
            </p>
            <input
              autoFocus
              value={code}
              onChange={(e) => setCode(e.target.value)}
              inputMode={useRecovery ? "text" : "numeric"}
              autoComplete="one-time-code"
              placeholder={useRecovery ? "XXXX-XXXX-XXXX-XXXX" : "123456"}
              className="w-full rounded border border-border-strong px-3 py-2 text-center font-mono text-lg tracking-widest focus:border-accent focus:outline-none"
            />
            <button
              type="submit"
              disabled={login.isPending || code.trim() === ""}
              className="w-full rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
            >
              {login.isPending ? t("Checking…", "Vérification…") : t("Verify", "Vérifier")}
            </button>
            <div className="flex justify-between text-xs text-fg-subtle">
              <button
                type="button"
                onClick={() => { setUseRecovery((r) => !r); setCode(""); }}
                className="hover:text-fg"
              >
                {useRecovery
                  ? t("Use my authenticator app", "Utiliser mon application")
                  : t("I've lost my phone", "J'ai perdu mon téléphone")}
              </button>
              <button
                type="button"
                onClick={() => { setNeedsCode(false); setCode(""); setPassword(""); }}
                className="hover:text-fg"
              >
                {t("Start over", "Recommencer")}
              </button>
            </div>
          </div>
        ) : choices.length > 0 ? (
          <div className="space-y-3">
            <p className="text-sm text-fg-muted">
              {t(
                "That email has an account at more than one distillery. Which one?",
                "Ce courriel a un compte dans plus d'une distillerie. Laquelle ?",
              )}
            </p>
            <div className="space-y-2">
              {choices.map((c) => (
                <button
                  key={c.tenantId}
                  type="button"
                  disabled={login.isPending}
                  onClick={() => login.mutate(c.tenantId)}
                  className="w-full rounded border border-border-strong px-3 py-2 text-left text-sm text-fg hover:border-accent disabled:opacity-50"
                >
                  {c.tenantName}
                </button>
              ))}
            </div>
            <button
              type="button"
              onClick={() => { setChoices([]); setPassword(""); }}
              className="text-xs text-fg-subtle hover:text-fg"
            >
              {t("Use a different account", "Utiliser un autre compte")}
            </button>
          </div>
        ) : (
        <>
        <div className="space-y-1">
          <label htmlFor="email" className="block text-sm font-medium text-fg">
            {t("Email", "Courriel")}
          </label>
          <input
            id="email"
            type="email"
            required
            autoComplete="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            className="w-full rounded border border-border-strong px-3 py-2 text-sm focus:border-accent focus:outline-none"
          />
        </div>

        <div className="space-y-1">
          <label htmlFor="password" className="block text-sm font-medium text-fg">
            {t("Password", "Mot de passe")}
          </label>
          <input
            id="password"
            type="password"
            required
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            className="w-full rounded border border-border-strong px-3 py-2 text-sm focus:border-accent focus:outline-none"
          />
        </div>

        <button
          type="submit"
          disabled={login.isPending}
          className="w-full rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
        >
          {login.isPending ? t("Signing in…", "Connexion…") : t("Sign in", "Se connecter")}
        </button>
        </>
        )}

        {login.error && (
          <p className="text-sm text-danger-fg">
            {login.error instanceof ConnectError
              ? login.error.rawMessage
              : String(login.error)}
          </p>
        )}


        <div className="flex justify-between text-xs text-fg-subtle">
          <Link to="/forgot-password" className="hover:text-fg">
            {t("Forgot password?", "Mot de passe oublié ?")}
          </Link>
          <Link to="/signup" className="hover:text-fg">
            {t("Have an invite code?", "Vous avez un code d'invitation ?")}
          </Link>
        </div>
      </form>
    </div>
  );
}
