import type { CSSProperties, JSX } from "react";
import type { MantineThemeOverride } from "@mantine/core";

/** Parts of the dashboard a host can leave out, by name (see `hide`). */
export type HideablePart =
  /** The Version section of Usage: which Geass and K3s run, and the upgrade of the workers. */
  | "upgrade"
  /** Adding and removing the cluster's servers. */
  | "servers"
  /** Sign-in settings (local users and passwords, the identity provider), Organizations, and the page that approves a command-line sign-in. */
  | "signin"
  /** The dashboard's domain and DNS, and the banner that asks for it. */
  | "domain"
  /** API tokens. A token cannot manage tokens, so the page cannot work with one. */
  | "tokens"
  /** Alert settings. The server answers only a signed-in admin, never a token. */
  | "alerts"
  /** An organization's container registry. The server answers only a signed-in admin, never a token. */
  | "registry"
  /** The account menu (theme, sign out) and the switch between organizations and workspaces. */
  | "account"
  /** The Geass logo in the top bar. */
  | "brand";

export interface GetTokenOptions {
  /** True after the API answered 401 with the token this function gave last: return a new one, not the one held. */
  forceRefresh?: boolean;
}

export interface GeassDashboardProps {
  /**
   * Where the host mounts the dashboard in the page's URL, e.g. "/acme/clusters/c1". Every link and navigation of the
   * dashboard stays below it. "/" (the default) is the whole site.
   */
  basePath?: string;
  /**
   * The host router's history (TanStack Router: `router.history`), so that the host and the dashboard both see every
   * change of the address. Without it the dashboard uses the browser's history by itself, and the host's router does not
   * hear about the dashboard's navigation.
   */
  history?: unknown;
  /**
   * Where the Geass API is: a path of the host ("/api/clusters/c1/geass") or a full URL. A request for "/api/bootstrap"
   * goes to `${apiBaseUrl}/api/bootstrap`. The default, "", is the page's own origin.
   */
  apiBaseUrl?: string;
  /**
   * The API token for a request, sent as `Authorization: Bearer <token>` (also on the event streams, which are read with
   * fetch, and on the terminal's WebSocket, where it is offered as a subprotocol). Called for every request, so it should
   * answer from memory; called again with `{ forceRefresh: true }` after a 401. Leave it out when the host's own proxy adds
   * the credential.
   */
  getToken?: (options?: GetTokenOptions) => string | undefined | Promise<string | undefined>;
  /** Replaces `window.fetch` for every request of the dashboard, for a host that signs requests itself. */
  fetch?: typeof fetch;
  /** The `credentials` of every request. "same-origin" by default. */
  credentials?: RequestCredentials;
  /** The address people are shown for the API (the URL of a deploy hook). `apiBaseUrl` made absolute when left out. */
  publicUrl?: string;
  /** Parts to leave out. `SELF_HOSTED_ONLY` is what a host that runs Geass for its people leaves out. */
  hide?: readonly HideablePart[];
  /** Called when the person asks to reconnect after the API refused the token for good. */
  onUnauthorized?: () => void;
  className?: string;
  /** Give the element a height (or place it in a flex or grid cell): the dashboard fills it. */
  style?: CSSProperties;
}

/**
 * The Geass dashboard as a part of another React app. It needs a Mantine provider above it (use `geassTheme`), the app's
 * `Notifications` for the messages it shows, and `@geass/dashboard-ui/style.css` loaded once.
 */
export function GeassDashboard(props: GeassDashboardProps): JSX.Element;

/** The Mantine theme the dashboard is designed for: pass it to the host's MantineProvider, or merge it into the host's theme. */
export const geassTheme: MantineThemeOverride;

/** Every name `hide` takes. */
export const HIDEABLE: readonly HideablePart[];

/** What a host that runs Geass for its people leaves out: everything in `HIDEABLE` except "brand". */
export const SELF_HOSTED_ONLY: readonly HideablePart[];

/** "/a/b/" and "a/b" are both "/a/b"; the whole site is "/". */
export function normalizeBasePath(basePath: string): string;

/** What the dashboard throws when the API answers with an error. */
export class ApiError extends Error {
  status: number;
  constructor(message: string, status: number);
}
