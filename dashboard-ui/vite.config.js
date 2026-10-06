import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

const dashboardProxyTarget = resolveDashboardProxyTarget();

export default defineConfig({
  plugins: [react(), geassDashboardBackendPlugin(dashboardProxyTarget)],
  resolve: {
    alias: { "@": path.resolve(__dirname, "./src") },
  },
  build: {
    outDir: "../internal/dashboard/frontend/dist",
    emptyOutDir: true,
  },
  server: {
    port: 5176,
    proxy: {
      "/api": dashboardApiProxy(dashboardProxyTarget),
      "/projects": pageOrMutationProxy(dashboardProxyTarget),
      "/apps": pageOrMutationProxy(dashboardProxyTarget),
      "/databases": pageOrMutationProxy(dashboardProxyTarget),
      "/logical-databases": pageOrMutationProxy(dashboardProxyTarget),
      "/object-stores": pageOrMutationProxy(dashboardProxyTarget),
      "/settings": pageOrMutationProxy(dashboardProxyTarget),
      "/ha-readiness": pageOrMutationProxy(dashboardProxyTarget),
      "/cluster": pageOrMutationProxy(dashboardProxyTarget),
      "/cloud-connections": pageOrMutationProxy(dashboardProxyTarget),
      "/object-storage": pageOrMutationProxy(dashboardProxyTarget),
    },
  },
});

function resolveDashboardProxyTarget() {
  const raw = process.env.GEASS_DASHBOARD_URL || process.env.GEASS_DASHBOARD_PROXY || "http://127.0.0.1:8085";
  try {
    return new URL(raw).origin;
  } catch {
    return "http://127.0.0.1:8085";
  }
}

function dashboardApiProxy(target) {
  return {
    target,
    changeOrigin: false,
    configure(proxy) {
      proxy.on("error", (_error, _req, res) => {
        if (res.writeHead) {
          res.writeHead(502, { "Content-Type": "application/json" });
          res.end(JSON.stringify({
            error: `Geass API unreachable at ${target}. Run: make dashboard-backend`,
          }));
        }
      });
    },
  };
}

function pageOrMutationProxy(target) {
  return {
    target,
    changeOrigin: false,
    bypass(req) {
      return req.method === "GET" ? req.url : undefined;
    },
  };
}

function geassDashboardBackendPlugin(target) {
  return {
    name: "geass-dashboard-backend",
    configureServer(server) {
      const check = () => {
        fetch(`${target}/geass-probe`, { signal: AbortSignal.timeout(2000) })
          .then(async (response) => {
            if (!response.ok) {
              console.warn(
                `\n[geass] ${target} responded ${response.status} to /geass-probe — not the Geass dashboard.\n` +
                  "        Start the API: make dashboard-backend   (or: make run)\n" +
                  `        Or set GEASS_DASHBOARD_URL to your dashboard origin.\n`,
              );
              return;
            }
            const probe = await response.json().catch(() => null);
            if (!probe?.api?.cloudflareConnector) {
              console.warn(
                `\n[geass] ${target} is an older Geass dashboard without Cloudflare connector API.\n` +
                  "        Run make dashboard-backend from this repo, or rebuild and rollout the controller image.\n",
              );
            }
          })
          .catch(() => {
            console.warn(
              `\n[geass] No dashboard at ${target}.\n` +
                "        Start the API: make dashboard-backend   (or: make run)\n" +
                `        Or set GEASS_DASHBOARD_URL before pnpm dev.\n`,
            );
          });
      };
      server.httpServer?.once("listening", check);
    },
  };
}
