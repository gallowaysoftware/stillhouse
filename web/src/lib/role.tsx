import { ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";

import { userClient } from "@/lib/clients";
import { UserRole } from "@/gen/stillhouse/v1/user_pb";

// useCurrentUser reads the cached getMe response shared with RequireAuth.
// Pages mount under RequireAuth, so the query has already resolved by the
// time these hooks fire; staleTime keeps it from re-fetching on every render.
export function useCurrentUser() {
  return useQuery({
    queryKey: ["getMe"],
    queryFn: () => userClient.getMe({}),
    staleTime: 60_000,
  });
}

export function useCurrentRole(): UserRole | undefined {
  return useCurrentUser().data?.user?.role;
}

// canWrite gates create/update/delete actions. Mirrors the backend's
// procedureMinRole: viewer cannot mutate.
export function canWrite(role: UserRole | undefined): boolean {
  return role === UserRole.OPERATOR || role === UserRole.OWNER;
}

// canOwn gates owner-only actions (UpdateTenant, CreateUser, SubmitB266).
export function canOwn(role: UserRole | undefined): boolean {
  return role === UserRole.OWNER;
}

// WriteOnly hides children when the current user is a viewer. Renders
// nothing while the getMe query is still resolving so viewers don't
// briefly see actionable buttons.
export function WriteOnly({ children }: { children: ReactNode }) {
  const role = useCurrentRole();
  if (!canWrite(role)) return null;
  return <>{children}</>;
}

export function OwnerOnly({ children }: { children: ReactNode }) {
  const role = useCurrentRole();
  if (!canOwn(role)) return null;
  return <>{children}</>;
}
