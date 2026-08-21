import { defineConfig } from "vitest/config";
import { fileURLToPath } from "node:url";

// Node environment on purpose: the tests worth having here are over pure
// arithmetic — the numbers an operator reads and acts on — not over
// rendered components. No jsdom, no testing-library, no assertions that
// TanStack Query works.
export default defineConfig({
  resolve: {
    alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) },
  },
  test: {
    environment: "node",
    include: ["src/**/*.test.ts"],
  },
});
