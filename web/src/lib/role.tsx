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

// canWrite gates create/update/delete actions on the *production*
// surface. Mirrors the backend's procedureMinRole: viewer cannot mutate,
// and neither can an accountant — someone who both books a movement and
// rules on its treatment is the conflict that role exists to avoid. The
// compliance actions an accountant does reach are gated by canFile.
export function canWrite(role: UserRole | undefined): boolean {
  return role === UserRole.OPERATOR || role === UserRole.OWNER;
}

// canFile gates the return: generate, submit, reopen, set the reporting
// calendar, rule on a loss. Mirrors accountantAlso in role_gate.go.
export function canFile(role: UserRole | undefined): boolean {
  return role === UserRole.OWNER || role === UserRole.ACCOUNTANT;
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

// FilingOnly shows children to whoever may act on a return — the owner
// and the accountant.
export function FilingOnly({ children }: { children: ReactNode }) {
  const role = useCurrentRole();
  if (!canFile(role)) return null;
  return <>{children}</>;
}

export function OwnerOnly({ children }: { children: ReactNode }) {
  const role = useCurrentRole();
  if (!canOwn(role)) return null;
  return <>{children}</>;
}
