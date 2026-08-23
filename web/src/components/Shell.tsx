import { ReactNode, useEffect, useState } from "react";
import { Link, NavLink, useLocation, useNavigate } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { authClient, userClient } from "@/lib/clients";
import { setLang, useLang, useT } from "@/lib/i18n";

type NavItem = { to: string; en: string; fr: string };
type NavGroup = { en: string; fr: string; items: NavItem[] };

// Groups follow the production timeline an operator actually walks:
// Setup (config you touch rarely) → Production (the daily flow from
// material to barrel to bottle) → Compliance (the reports you owe CRA
// at month-end). Putting "Dashboard" outside any group keeps the home
// link prominent at the top.
const homeItem: NavItem = { to: "/", en: "Dashboard", fr: "Tableau de bord" };

const navGroups: NavGroup[] = [
  {
    en: "Setup",
    fr: "Configuration",
    items: [
      { to: "/materials", en: "Materials", fr: "Matières premières" },
      { to: "/purchasing", en: "Purchasing", fr: "Achats" },
      { to: "/recipes", en: "Recipes", fr: "Recettes" },
      { to: "/products", en: "Products", fr: "Produits" },
      { to: "/stamps", en: "Excise stamps", fr: "Timbres d'accise" },
      { to: "/instruments", en: "Instruments", fr: "Instruments" },
      { to: "/customers", en: "Customers", fr: "Clients" },
      { to: "/import", en: "Import", fr: "Importer" },
      { to: "/pricing", en: "Provincial pricing", fr: "Prix provincial" },
    ],
  },
  {
    en: "Production",
    fr: "Production",
    items: [
      { to: "/work", en: "Work", fr: "Travaux" },
      { to: "/mashes", en: "Mashes", fr: "Brassins" },
      { to: "/fermentations", en: "Fermentations", fr: "Fermentations" },
      { to: "/distillations", en: "Distillations", fr: "Distillations" },
      { to: "/bulk", en: "Bulk inventory", fr: "Inventaire en vrac" },
      { to: "/barrels", en: "Barrels", fr: "Fûts" },
      { to: "/bottling", en: "Bottling", fr: "Embouteillage" },
      { to: "/sales", en: "Sales & shipping", fr: "Ventes et expéditions" },
      { to: "/removals", en: "Removals", fr: "Sorties" },
    ],
  },
  {
    en: "Compliance",
    fr: "Conformité",
    items: [
      { to: "/b266", en: "B266 returns", fr: "Déclarations B266" },
      { to: "/journal", en: "Accounting journal", fr: "Journal comptable" },
      { to: "/audit", en: "Audit log", fr: "Journal d'audit" },
      { to: "/settings", en: "Settings", fr: "Paramètres" },
    ],
  },
];

export function Shell({ children }: { children: ReactNode }) {
  const navigate = useNavigate();
  const location = useLocation();
  const queryClient = useQueryClient();
  const lang = useLang();
  const t = useT();
  const [mobileNavOpen, setMobileNavOpen] = useState(false);

  // Auto-close the drawer on route change so tapping a nav link doesn't
  // leave it lingering over the next page.
  useEffect(() => {
    setMobileNavOpen(false);
  }, [location.pathname]);

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

  const sidebar = (
    <>
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
        <NavItemLink item={homeItem} t={t} />
        {navGroups.map((group) => (
          <div key={group.en} className="mt-4">
            <p className="px-3 pb-1 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">
              {t(group.en, group.fr)}
            </p>
            {group.items.map((item) => (
              <NavItemLink key={item.to} item={item} t={t} />
            ))}
          </div>
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
    </>
  );

  return (
    <div className="flex min-h-screen bg-surface text-fg">
      {/* Desktop sidebar: always visible at md+. */}
      <aside
        data-print-hide
        className="hidden md:flex w-60 flex-col border-r border-border bg-surface-2"
      >
        {sidebar}
      </aside>

      {/* Mobile header: visible below md, hosts the hamburger trigger. */}
      <header
        data-print-hide
        className="md:hidden fixed top-0 inset-x-0 z-30 flex h-14 items-center justify-between border-b border-border bg-surface-2 px-4"
      >
        <Link to="/" className="flex items-center gap-2 text-sm font-semibold tracking-tight text-fg">
          <span className="inline-block h-2 w-2 rounded-full bg-accent" />
          Stillhouse
        </Link>
        <button
          onClick={() => setMobileNavOpen(true)}
          className="rounded-md p-2 text-fg-muted hover:bg-surface-3 hover:text-fg"
          aria-label={t("Open navigation", "Ouvrir la navigation")}
        >
          {/* Inline SVG so we don't add an icon-lib dep for one glyph. */}
          <svg width="20" height="20" viewBox="0 0 20 20" fill="none" aria-hidden="true">
            <path d="M3 5h14M3 10h14M3 15h14" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
          </svg>
        </button>
      </header>

      {/* Mobile drawer + scrim. Render only when open so it doesn't trap
          focus / steal pointer events otherwise. */}
      {mobileNavOpen && (
        <div
          data-print-hide
          className="md:hidden fixed inset-0 z-40 flex"
          role="dialog"
          aria-modal="true"
        >
          <div
            className="absolute inset-0 bg-black/60"
            onClick={() => setMobileNavOpen(false)}
            aria-hidden="true"
          />
          <aside className="relative ml-auto flex h-full w-72 flex-col border-l border-border bg-surface-2 shadow-card-dark">
            <button
              onClick={() => setMobileNavOpen(false)}
              className="absolute right-3 top-3 rounded-md p-2 text-fg-muted hover:bg-surface-3 hover:text-fg"
              aria-label={t("Close navigation", "Fermer la navigation")}
            >
              <svg width="20" height="20" viewBox="0 0 20 20" fill="none" aria-hidden="true">
                <path d="M5 5l10 10M15 5L5 15" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
              </svg>
            </button>
            {sidebar}
          </aside>
        </div>
      )}

      <main className="flex-1 overflow-x-auto">
        {/* Top padding on mobile accounts for the fixed header. */}
        <div className="mx-auto max-w-6xl px-4 pt-20 pb-10 md:px-8 md:pt-10">{children}</div>
      </main>
    </div>
  );
}

function NavItemLink({ item, t }: { item: NavItem; t: (en: string, fr: string) => string }) {
  return (
    <NavLink
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
  );
}
