import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { "@": path.resolve(__dirname, "./src") },
  },
  build: {
    outDir: "../internal/dashboard/frontend/dist",
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    proxy: {
      "/api": {
        target: "http://127.0.0.1:8082",
        // Keep the browser-facing host so the dashboard's same-origin
        // mutation check sees the same origin the browser sent.
        changeOrigin: false,
      },
      "/projects": pageOrMutationProxy(),
      "/apps": pageOrMutationProxy(),
      "/databases": pageOrMutationProxy(),
      "/logical-databases": pageOrMutationProxy(),
      "/caches": pageOrMutationProxy(),
      "/object-stores": pageOrMutationProxy(),
      "/settings": pageOrMutationProxy(),
      "/ha-readiness": pageOrMutationProxy(),
      "/cloud-connections": pageOrMutationProxy(),
    },
  },
});

function pageOrMutationProxy() {
  return {
    target: "http://127.0.0.1:8082",
    // Preserve the browser-facing host so the dashboard's same-origin mutation
    // check accepts POST requests made through the local dev proxy.
    changeOrigin: false,
    bypass(req) {
      return req.method === "GET" ? req.url : undefined;
    },
  };
}
