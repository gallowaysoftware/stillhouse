import { ReactNode } from "react";
import { Link, NavLink, useNavigate } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { authClient, userClient } from "@/lib/clients";

const navItems = [
  { to: "/", label: "Dashboard" },
  { to: "/materials", label: "Materials" },
  { to: "/recipes", label: "Recipes" },
];

export function Shell({ children }: { children: ReactNode }) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
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
      <aside className="flex w-56 flex-col border-r border-stone-200 bg-white">
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
        <nav className="flex-1 px-2 py-3">
          {navItems.map((item) => (
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
              {item.label}
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
          <button
            onClick={() => logout.mutate()}
            className="mt-2 text-stone-500 hover:text-stone-900"
            disabled={logout.isPending}
          >
            {logout.isPending ? "Signing out…" : "Sign out"}
          </button>
        </div>
      </aside>
      <main className="flex-1 overflow-x-auto">
        <div className="mx-auto max-w-5xl p-8">{children}</div>
      </main>
    </div>
  );
}
