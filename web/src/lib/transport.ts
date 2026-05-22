import { createConnectTransport } from "@connectrpc/connect-web";

// JSON over HTTP to the same origin. credentials: 'include' lets the session
// cookie ride along on every Connect call.
export const transport = createConnectTransport({
  baseUrl: "/",
  useBinaryFormat: false,
  fetch: (input, init) => fetch(input, { ...init, credentials: "include" }),
});
