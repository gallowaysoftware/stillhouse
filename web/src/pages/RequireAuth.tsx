import { ReactNode } from "react";
import { Navigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { ConnectError, Code } from "@connectrpc/connect";

import { LoadError } from "@/components/LoadError";
import { userClient } from "@/lib/clients";
import { mayHaveBeenSaved } from "@/lib/errors";

export function RequireAuth({ children }: { children: ReactNode }) {
  const { data, isLoading, error, refetch } = useQuery({
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

  if (error && !(data && mayHaveBeenSaved(error))) {
    if (error instanceof ConnectError && error.code === Code.Unauthenticated) {
      return <Navigate to="/login" replace />;
    }
    return (
      <div className="flex min-h-screen items-center justify-center px-4">
        <LoadError error={error} onRetry={() => void refetch()} />
      </div>
    );
  }

  if (!data) {
    return <Navigate to="/login" replace />;
  }

  return <>{children}</>;
}
