import { ReactNode } from "react";
import { Navigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { ConnectError, Code } from "@connectrpc/connect";

import { userClient } from "@/lib/clients";

export function RequireAuth({ children }: { children: ReactNode }) {
  const { data, isLoading, error } = useQuery({
    queryKey: ["getMe"],
    queryFn: () => userClient.getMe({}),
  });

  if (isLoading) {
    return (
      <div className="flex min-h-screen items-center justify-center text-fg-muted">
        Loading…
      </div>
    );
  }

  if (error) {
    if (error instanceof ConnectError && error.code === Code.Unauthenticated) {
      return <Navigate to="/login" replace />;
    }
    return (
      <div className="flex min-h-screen items-center justify-center text-danger-fg">
        Error: {error instanceof Error ? error.message : String(error)}
      </div>
    );
  }

  if (!data) {
    return <Navigate to="/login" replace />;
  }

  return <>{children}</>;
}
