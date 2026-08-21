import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "node:path";

const BACKEND = process.env.STILLHOUSE_BACKEND ?? "http://localhost:8080";

// Connect routes everything under /<package>.<Service>/<Method>. Proxy
// stillhouse.* and the /healthz endpoint through to the Go backend in dev so
// that the browser sees same-origin (and cookies flow without CORS hoops).
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@": path.resolve(import.meta.dirname, "src"),
    },
  },
  server: {
    port: 5173,
    proxy: {
      "/stillhouse.v1.": { target: BACKEND, changeOrigin: false },
      "/healthz": { target: BACKEND, changeOrigin: false },
      "/export": { target: BACKEND, changeOrigin: false },
    },
  },
});
