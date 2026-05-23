import { ReactNode } from "react";
import { Link, NavLink, useNavigate } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { authClient, userClient } from "@/lib/clients";
import { setLang, useLang, useT } from "@/lib/i18n";

const navItemKeys: { to: string; en: string; fr: string }[] = [
  { to: "/", en: "Dashboard", fr: "Tableau de bord" },
  { to: "/materials", en: "Materials", fr: "Matières premières" },
  { to: "/recipes", en: "Recipes", fr: "Recettes" },
  { to: "/mashes", en: "Mashes", fr: "Brassins" },
  { to: "/fermentations", en: "Fermentations", fr: "Fermentations" },
  { to: "/distillations", en: "Distillations", fr: "Distillations" },
  { to: "/bulk", en: "Bulk inventory", fr: "Inventaire en vrac" },
  { to: "/barrels", en: "Barrels", fr: "Fûts" },
  { to: "/products", en: "Products", fr: "Produits" },
  { to: "/stamps", en: "Excise stamps", fr: "Timbres d'accise" },
  { to: "/bottling", en: "Bottling", fr: "Embouteillage" },
  { to: "/removals", en: "Removals", fr: "Sorties" },
  { to: "/b266", en: "B266 returns", fr: "Déclarations B266" },
  { to: "/audit", en: "Audit log", fr: "Journal d'audit" },
  { to: "/pricing", en: "Provincial pricing", fr: "Prix provincial" },
  { to: "/settings", en: "Settings", fr: "Paramètres" },
];

export function Shell({ children }: { children: ReactNode }) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const lang = useLang();
  const t = useT();
  const { data } = useQuery({
    queryKey: ["getMe"],
    queryFn: () => userClient.getMe({}),
  });
  const logout = useMutation({
    mutationFn: () => authClient.logout({}),
    onSuccess: () => {
      queryClient.clear();
      navigate("/login", { replace: true });
    },
  });

  return (
    <div className="flex min-h-screen bg-surface text-fg">
      <aside data-print-hide className="flex w-60 flex-col border-r border-border bg-surface-2">
        <div className="border-b border-border px-5 py-5">
          <Link to="/" className="flex items-center gap-2 text-base font-semibold tracking-tight text-fg">
            <span className="inline-block h-2 w-2 rounded-full bg-accent" />
            Stillhouse
          </Link>
          {data?.tenant && (
            <p className="mt-1 text-xs text-fg-muted" title={data.tenant.craSpiritsLicenceNumber}>
              {data.tenant.name}
            </p>
          )}
        </div>
        <nav className="flex-1 overflow-y-auto px-2 py-3">
          {navItemKeys.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.to === "/"}
              className={({ isActive }) =>
                `mb-0.5 block rounded-md px-3 py-1.5 text-sm transition-colors ${
                  isActive
                    ? "bg-accent/15 text-accent-hover font-medium"
                    : "text-fg-muted hover:bg-surface-3 hover:text-fg"
                }`
              }
            >
              {t(item.en, item.fr)}
            </NavLink>
          ))}
        </nav>
        <div className="border-t border-border px-4 py-4 text-xs text-fg-muted">
          {data?.user && (
            <>
              <p className="text-fg">{data.user.displayName}</p>
              <p>{data.user.email}</p>
            </>
          )}
          <div className="mt-2 flex items-center justify-between">
            <button
              onClick={() => logout.mutate()}
              className="text-fg-muted hover:text-fg"
              disabled={logout.isPending}
            >
              {logout.isPending ? t("Signing out…", "Déconnexion…") : t("Sign out", "Déconnexion")}
            </button>
            <div className="flex items-center gap-1 text-fg-subtle">
              <button
                className={`rounded px-1.5 py-0.5 ${lang === "en" ? "bg-surface-3 text-fg" : "hover:text-fg"}`}
                onClick={() => setLang("en")}
              >
                EN
              </button>
              <button
                className={`rounded px-1.5 py-0.5 ${lang === "fr" ? "bg-surface-3 text-fg" : "hover:text-fg"}`}
                onClick={() => setLang("fr")}
              >
                FR
              </button>
            </div>
          </div>
        </div>
      </aside>
      <main className="flex-1 overflow-x-auto">
        <div className="mx-auto max-w-6xl px-8 py-10">{children}</div>
      </main>
    </div>
  );
}
