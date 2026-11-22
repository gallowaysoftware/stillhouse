import { MutationCache, QueryClient } from "@tanstack/react-query";

import { mayHaveBeenSaved, retryRead } from "./errors";

export const queryClient: QueryClient = new QueryClient({
  mutationCache: new MutationCache({
    // The write may be on the record, so what is on screen is suspect.
    onError: (error) => {
      if (mayHaveBeenSaved(error)) void queryClient.invalidateQueries();
    },
  }),
  defaultOptions: {
    queries: {
      retry: retryRead,
      retryDelay: 800,
      refetchOnWindowFocus: false,
      staleTime: 60_000,
    },
    // Never retried: no write is idempotent, and an error from between here
    // and the server does not say whether it committed.
    // "always": offline, a write would otherwise wait unseen, be stamped when
    // the signal came back, and be lost with the page.
    mutations: { retry: false, networkMode: "always" },
  },
});
