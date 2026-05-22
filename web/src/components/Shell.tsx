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
    <div className="flex min-h-screen bg-stone-50 text-stone-900">
      <aside data-print-hide className="flex w-56 flex-col border-r border-stone-200 bg-white">
        <div className="border-b border-stone-200 px-5 py-4">
          <Link to="/" className="text-lg font-semibold tracking-tight">
            Stillhouse
          </Link>
          {data?.tenant && (
            <p className="mt-1 text-xs text-stone-500" title={data.tenant.craSpiritsLicenceNumber}>
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
                `block rounded px-3 py-2 text-sm ${
                  isActive
                    ? "bg-stone-900 text-white"
                    : "text-stone-700 hover:bg-stone-100"
                }`
              }
            >
              {t(item.en, item.fr)}
            </NavLink>
          ))}
        </nav>
        <div className="border-t border-stone-200 px-3 py-3 text-xs text-stone-500">
          {data?.user && (
            <>
              <p className="text-stone-900">{data.user.displayName}</p>
              <p>{data.user.email}</p>
            </>
          )}
          <div className="mt-2 flex items-center justify-between">
            <button
              onClick={() => logout.mutate()}
              className="text-stone-500 hover:text-stone-900"
              disabled={logout.isPending}
            >
              {logout.isPending ? t("Signing out…", "Déconnexion…") : t("Sign out", "Déconnexion")}
            </button>
            <div className="flex items-center gap-1 text-stone-400">
              <button
                className={`rounded px-1.5 py-0.5 ${lang === "en" ? "bg-stone-200 text-stone-900" : "hover:text-stone-700"}`}
                onClick={() => setLang("en")}
              >
                EN
              </button>
              <button
                className={`rounded px-1.5 py-0.5 ${lang === "fr" ? "bg-stone-200 text-stone-900" : "hover:text-stone-700"}`}
                onClick={() => setLang("fr")}
              >
                FR
              </button>
            </div>
          </div>
        </div>
      </aside>
      <main className="flex-1 overflow-x-auto">
        <div className="mx-auto max-w-5xl p-8">{children}</div>
      </main>
    </div>
  );
}
