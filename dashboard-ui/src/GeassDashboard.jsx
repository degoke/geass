import { useMemo, useState } from "react";
import { MantineThemeProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { normalizeBasePath } from "@/lib/base-path";
import { configureTransport } from "@/lib/transport";
import { DashboardOptionsProvider } from "@/lib/options";
import { createDashboardRouter } from "@/router";
import { geassTheme } from "@/theme";
import logoDark from "../public/geass-logo-dark.svg?inline";
import logoLight from "../public/geass-logo-light.svg?inline";
import symbolDark from "../public/geass-symbol.svg?inline";
import symbolLight from "../public/geass-symbol-light.svg?inline";

const LOGOS = {
  "geass-logo-dark": logoDark,
  "geass-logo-light": logoLight,
  "geass-symbol": symbolDark,
  "geass-symbol-light": symbolLight,
};

let mounts = 0;

/**
 * The Geass dashboard, as a part of another React app.
 *
 * It needs, above it, a Mantine provider (the app's own: see `geassTheme`), and the app's `Notifications` for the messages
 * it shows. It brings its own router, query client and styles, all kept inside the element it renders.
 *
 * Props
 * - basePath: where the host mounts it in the page's URL, e.g. "/acme/clusters/c1". Every link and navigation stays below it.
 * - history: the host router's history (TanStack Router's `router.history`), so the host and the dashboard both see every
 *   change of the address. Without it the dashboard uses the browser's history by itself.
 * - apiBaseUrl: where the Geass API is. A path of the host ("/api/clusters/c1/geass") or a full URL; "" is the page's own origin.
 *   A request for "/api/bootstrap" goes to `${apiBaseUrl}/api/bootstrap`.
 * - getToken: how a request signs in. Returns the API token (or a promise of it); it is sent as `Authorization: Bearer`. It is
 *   called again with `{ forceRefresh: true }` after a 401. Leave it out when the host's own proxy adds the credential.
 * - sessionKey: who the requests are for (a user id, say). When it changes, what was fetched for the last person is dropped.
 *   Give it whenever one host page can sign another person in to the same cluster without a reload.
 * - fetch: replaces window.fetch for every request of the dashboard, for a host that signs requests itself.
 * - credentials: the `credentials` of every request; "same-origin" by default.
 * - publicUrl: the address people are shown for the API (a deploy hook's URL); apiBaseUrl made absolute when left out.
 * - hide: names of parts to leave out; see SELF_HOSTED_ONLY.
 * - onUnauthorized: called when the API does not accept the token and the person asks to reconnect.
 * - className, style: for the element the dashboard fills. Give it a height; the dashboard fills it.
 *
 * One dashboard is shown at a time: the way requests are made is a setting of the page, not of the element.
 */
export function GeassDashboard({
  basePath = "/",
  history,
  apiBaseUrl = "",
  getToken,
  sessionKey,
  fetch: fetchOverride,
  credentials,
  publicUrl,
  hide,
  onUnauthorized,
  className,
  style,
}) {
  // Set while rendering, so that it is in place before the first request of any page below. The same props set the same values.
  configureTransport({ apiBaseUrl, getToken, fetch: fetchOverride, credentials, publicUrl });

  const base = normalizeBasePath(basePath);
  // A router is not given another history or base path once it runs, so a new one is a new element, with its own state.
  const mounted = useMemo(() => ({ router: createDashboardRouter({ basepath: base, history }), key: ++mounts }), [base, history]);
  // What one cluster answered is not shown for another, nor what it answered for one person for the next.
  const queryClient = useMemo(() => new QueryClient(), [apiBaseUrl, sessionKey]);

  // Menus, dialogs and drawers open in the dashboard's own element, where its styles reach them, and not in the host's body.
  const [root, setRoot] = useState(null);
  const theme = useMemo(
    () => ({
      components: {
        ...geassTheme.components,
        Portal: { defaultProps: { target: root } },
        // The element clips what overflows it, and a dropdown placed as `absolute` inside it would be cut at its edge.
        Popover: { defaultProps: { floatingStrategy: "fixed" } },
        Tooltip: { defaultProps: { floatingStrategy: "fixed" } },
      },
    }),
    [root],
  );

  return (
    <div ref={setRoot} className={["geass-dashboard", "is-embedded", className].filter(Boolean).join(" ")} style={style}>
      {root ? (
        <MantineThemeProvider theme={theme}>
          <DashboardOptionsProvider hide={hide} logos={LOGOS} onUnauthorized={onUnauthorized}>
            <QueryClientProvider client={queryClient}>
              <RouterProvider key={mounted.key} router={mounted.router} />
            </QueryClientProvider>
          </DashboardOptionsProvider>
        </MantineThemeProvider>
      ) : null}
    </div>
  );
}
