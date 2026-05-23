import { QueryClient } from "@tanstack/react-query";
import { ConnectError, Code } from "@connectrpc/connect";

// Codes that warrant a single automatic retry — transient infrastructure
// blips that the operator would otherwise have to manually re-submit.
// Anything semantic (FailedPrecondition, InvalidArgument, AlreadyExists,
// PermissionDenied) must NOT retry — the second attempt would just
// surface the same error and confuse the operator.
const retryableCodes = new Set<Code>([Code.Unavailable, Code.DeadlineExceeded, Code.Aborted]);

function shouldRetry(failureCount: number, error: unknown): boolean {
  if (failureCount >= 1) return false;
  if (error instanceof ConnectError) return retryableCodes.has(error.code);
  // Plain network error (fetch threw) — retry once.
  return true;
}

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: false,
      refetchOnWindowFocus: false,
      staleTime: 60_000,
    },
    mutations: {
      retry: shouldRetry,
      retryDelay: 800,
    },
  },
});
