// How the dashboard talks to a Geass API. Standalone, it is the page's own origin and the session cookie. Mounted in
// another app (see GeassDashboard), the host says where the API is and how a request signs in, and every request, stream
// and terminal goes through here so that nothing else needs to know which of the two it is.

const defaults = Object.freeze({
  apiBaseUrl: "",
  getToken: undefined,
  fetch: undefined,
  credentials: "same-origin",
  publicUrl: "",
});

let config = defaults;

/** Sets how requests are made. Calling it with no options goes back to the page's own origin and its cookie. */
export function configureTransport(options = {}) {
  config = Object.freeze({ ...defaults, ...definedOnly(options) });
}

export function transportConfig() {
  return config;
}

function definedOnly(options) {
  return Object.fromEntries(Object.entries(options).filter(([, value]) => value !== undefined));
}

/** Joins the API's base URL and a path such as "/api/bootstrap". The base may be a path of the host ("/geass/c1") or a full URL. */
export function apiUrl(path) {
  const base = config.apiBaseUrl.replace(/\/+$/, "");
  return `${base}${path.startsWith("/") ? path : `/${path}`}`;
}

/** Where the API is, as an absolute URL: what a person is shown to copy (a deploy hook, a curl example). */
export function publicOrigin() {
  const origin = typeof window === "undefined" ? "" : window.location.origin;
  if (config.publicUrl) return config.publicUrl.replace(/\/+$/, "");
  if (!config.apiBaseUrl) return origin;
  try {
    return new URL(config.apiBaseUrl, origin || undefined).toString().replace(/\/+$/, "");
  } catch {
    return origin;
  }
}

/** The API token for a request, or "" when the page signs in some other way (the cookie, or a proxy of the host's). */
async function currentToken(refresh) {
  if (!config.getToken) return "";
  const token = await config.getToken(refresh ? { forceRefresh: true } : {});
  return typeof token === "string" ? token.trim() : "";
}

/** True when a request must carry its credential itself: a browser attaches a cookie, but never a bearer token. */
function usesToken() {
  return Boolean(config.getToken);
}

function withAuthorization(headers, token) {
  const next = new Headers(headers || {});
  if (token) next.set("Authorization", `Bearer ${token}`);
  return next;
}

/**
 * fetch for the Geass API: the path is joined to the base URL and the token, if the host gave a way to get one, is
 * sent as a bearer. A 401 is tried once more with a fresh token, since the one held may have just expired.
 */
export async function apiFetch(path, options = {}) {
  const doFetch = config.fetch || ((...args) => globalThis.fetch(...args));
  const send = async (refresh) => {
    const token = await currentToken(refresh);
    return doFetch(apiUrl(path), {
      credentials: config.credentials,
      ...options,
      headers: withAuthorization(options.headers, token),
    });
  };
  const response = await send(false);
  if (response.status === 401 && usesToken()) return send(true);
  return response;
}

// ---- event streams ----

/**
 * An EventSource for a path of the API. The browser's own EventSource sends the cookie and cannot send a header, so it
 * is used only when the page signs in with its cookie. With a token (or a fetch of the host's) the stream is read with
 * fetch instead, behind an object that answers the same calls (addEventListener, onmessage, onopen, onerror, close).
 */
export function openEventSource(path) {
  if (!usesToken() && !config.fetch) {
    return new EventSource(apiUrl(path), { withCredentials: true });
  }
  return new FetchEventSource(path);
}

/** How long a dropped stream waits before it is opened again, as an EventSource does by itself, and how long at most. */
const RECONNECT_DELAY_MS = 3000;
const RECONNECT_DELAY_MAX_MS = 30000;

/** An answer that no new try will change: like an EventSource, the stream gives up on it. */
const FINAL_STATUSES = new Set([401, 403, 404]);

export class FetchEventSource {
  constructor(path, { reconnectDelay = RECONNECT_DELAY_MS } = {}) {
    this.path = path;
    this.reconnectDelay = reconnectDelay;
    this.delay = reconnectDelay;
    this.listeners = new Map();
    this.onopen = null;
    this.onmessage = null;
    this.onerror = null;
    this.closed = false;
    this.controller = null;
    this.timer = null;
    this.lastEventId = "";
    this.run();
  }

  addEventListener(type, listener) {
    if (!this.listeners.has(type)) this.listeners.set(type, new Set());
    this.listeners.get(type).add(listener);
  }

  removeEventListener(type, listener) {
    this.listeners.get(type)?.delete(listener);
  }

  close() {
    this.closed = true;
    window.clearTimeout(this.timer);
    this.controller?.abort();
  }

  dispatch(type, event) {
    if (type === "message") this.onmessage?.(event);
    if (type === "open") this.onopen?.(event);
    if (type === "error") this.onerror?.(event);
    this.listeners.get(type)?.forEach((listener) => listener(event));
  }

  async run() {
    while (!this.closed) {
      this.controller = new AbortController();
      try {
        const headers = { Accept: "text/event-stream" };
        if (this.lastEventId) headers["Last-Event-ID"] = this.lastEventId;
        const response = await apiFetch(this.path, { headers, signal: this.controller.signal });
        if (FINAL_STATUSES.has(response.status)) {
          this.closed = true;
          this.dispatch("error", { type: "error", error: new Error(`Event stream refused (${response.status})`), status: response.status });
          return;
        }
        if (!response.ok || !response.body) throw new Error(`Event stream refused (${response.status})`);
        this.delay = this.reconnectDelay;
        this.dispatch("open", { type: "open" });
        await this.read(response.body);
      } catch (error) {
        if (this.closed) return;
        this.dispatch("error", { type: "error", error });
      }
      if (this.closed) return;
      await new Promise((resolve) => {
        this.timer = window.setTimeout(resolve, this.delay);
      });
      this.delay = Math.min(this.delay * 2, RECONNECT_DELAY_MAX_MS);
    }
  }

  async read(body) {
    const reader = body.getReader();
    const decoder = new TextDecoder();
    const parser = new EventStreamParser((message) => {
      if (message.id) this.lastEventId = message.id;
      this.dispatch(message.event || "message", { type: message.event || "message", data: message.data, lastEventId: this.lastEventId });
    });
    for (;;) {
      const { done, value } = await reader.read();
      if (done) return;
      parser.feed(decoder.decode(value, { stream: true }));
    }
  }
}

/** Reads "text/event-stream": fields on lines, a message ending at a blank line, lines starting with ":" ignored. */
export class EventStreamParser {
  constructor(onMessage) {
    this.onMessage = onMessage;
    this.buffer = "";
    this.reset();
  }

  reset() {
    this.event = "";
    this.id = "";
    this.data = [];
  }

  feed(chunk) {
    this.buffer += chunk;
    const lines = this.buffer.split(/\r\n|\n|\r/);
    // The last piece may be the start of a line that has not finished arriving.
    this.buffer = lines.pop();
    lines.forEach((line) => this.line(line));
  }

  line(line) {
    if (line === "") {
      if (this.data.length) this.onMessage({ event: this.event, id: this.id, data: this.data.join("\n") });
      this.reset();
      return;
    }
    if (line.startsWith(":")) return;
    const colon = line.indexOf(":");
    const field = colon === -1 ? line : line.slice(0, colon);
    let value = colon === -1 ? "" : line.slice(colon + 1);
    if (value.startsWith(" ")) value = value.slice(1);
    if (field === "event") this.event = value;
    else if (field === "data") this.data.push(value);
    else if (field === "id") this.id = value;
  }
}

// ---- sockets ----

/** The subprotocol the terminal speaks, and the prefix of the one that carries the token (the server reads it, see websocketBearer). */
export const SOCKET_PROTOCOL = "geass.v1";
export const SOCKET_BEARER_PREFIX = "geass.bearer.";

/**
 * A WebSocket to a path of the API. A browser cannot set a header on a WebSocket, so with a token it is offered as a
 * subprotocol next to the protocol the terminal speaks; with the cookie, nothing is added.
 */
export async function openSocket(path) {
  const url = new URL(apiUrl(path), window.location.href);
  url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
  const token = await currentToken(false);
  return token ? new WebSocket(url, [SOCKET_PROTOCOL, `${SOCKET_BEARER_PREFIX}${token}`]) : new WebSocket(url);
}
