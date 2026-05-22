import { useNavigate } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { authClient, userClient } from "@/lib/clients";

export function HomePage() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();

  // RequireAuth already fetched this once; useQuery will read from cache.
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
    <div className="mx-auto max-w-3xl p-8">
      <header className="mb-8 flex items-center justify-between">
        <h1 className="text-2xl font-semibold text-stone-900">Stillhouse</h1>
        <button
          onClick={() => logout.mutate()}
          disabled={logout.isPending}
          className="text-sm text-stone-600 hover:text-stone-900"
        >
          {logout.isPending ? "Signing out…" : "Sign out"}
        </button>
      </header>

      {data && (
        <section className="space-y-2 rounded-lg bg-white p-6 shadow">
          <p className="text-sm text-stone-500">Signed in as</p>
          <p className="text-lg font-medium text-stone-900">
            {data.user?.displayName}{" "}
            <span className="text-sm text-stone-500">({data.user?.email})</span>
          </p>
          <p className="text-sm text-stone-500">Tenant</p>
          <p className="text-lg font-medium text-stone-900">{data.tenant?.name}</p>
          <p className="text-sm text-stone-500">
            CRA spirits licence: {data.tenant?.craSpiritsLicenceNumber} ·{" "}
            Jurisdiction: {data.tenant?.defaultJurisdiction}
          </p>
        </section>
      )}
    </div>
  );
}
