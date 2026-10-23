import { afterEach, describe, expect, it, vi } from "vitest";
import {
  EventStreamParser,
  FetchEventSource,
  SOCKET_BEARER_PREFIX,
  SOCKET_PROTOCOL,
  apiFetch,
  apiUrl,
  configureTransport,
  openEventSource,
  openSocket,
  publicOrigin,
} from "@/lib/transport";
import { api, action, ApiError } from "@/lib/api";

const json = (body, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });

afterEach(() => {
  configureTransport();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("apiUrl", () => {
  it("is the path itself on the page's own origin", () => {
    expect(apiUrl("/api/bootstrap")).toBe("/api/bootstrap");
  });

  it("joins the base URL and the path, whatever the slashes", () => {
    configureTransport({ apiBaseUrl: "/api/clusters/c1/geass/" });
    expect(apiUrl("/api/bootstrap")).toBe("/api/clusters/c1/geass/api/bootstrap");
    configureTransport({ apiBaseUrl: "https://geass.example.com" });
    expect(apiUrl("api/events")).toBe("https://geass.example.com/api/events");
  });
});

describe("publicOrigin", () => {
  it("is the page's origin, then the base URL made absolute, then what the host says", () => {
    expect(publicOrigin()).toBe(window.location.origin);
    configureTransport({ apiBaseUrl: "/proxy/c1/" });
    expect(publicOrigin()).toBe(`${window.location.origin}/proxy/c1`);
    configureTransport({ apiBaseUrl: "/proxy/c1", publicUrl: "https://c1.example.com/" });
    expect(publicOrigin()).toBe("https://c1.example.com");
  });
});

describe("apiFetch", () => {
  it("sends the cookie and no Authorization by default, as the standalone dashboard always has", async () => {
    const fetchMock = vi.fn(async () => json({ ok: true }));
    vi.stubGlobal("fetch", fetchMock);
    await api("/api/bootstrap");
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/api/bootstrap");
    expect(init.credentials).toBe("same-origin");
    expect(new Headers(init.headers).has("Authorization")).toBe(false);
  });

  it("sends the token as a bearer, on reads and on form posts alike", async () => {
    const fetchMock = vi.fn(async () => json({ ok: true }));
    vi.stubGlobal("fetch", fetchMock);
    configureTransport({ apiBaseUrl: "/geass", getToken: async () => " gsw_abc " });
    await api("/api/bootstrap");
    await action("/apps/web/scale", { replicas: 2 });
    const [read, post] = fetchMock.mock.calls;
    expect(read[0]).toBe("/geass/api/bootstrap");
    expect(new Headers(read[1].headers).get("Authorization")).toBe("Bearer gsw_abc");
    expect(post[0]).toBe("/geass/api/apps/web/scale");
    const headers = new Headers(post[1].headers);
    expect(headers.get("Authorization")).toBe("Bearer gsw_abc");
    expect(headers.get("Content-Type")).toContain("application/x-www-form-urlencoded");
    expect(post[1].method).toBe("POST");
    expect(String(post[1].body)).toBe("replicas=2");
  });

  it("asks for a new token after a 401 and tries once more", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(json({ error: "invalid or expired API token" }, 401))
      .mockResolvedValueOnce(json({ ok: true }));
    vi.stubGlobal("fetch", fetchMock);
    const getToken = vi.fn(async ({ forceRefresh } = {}) => (forceRefresh ? "fresh" : "stale"));
    configureTransport({ getToken });
    await expect(api("/api/bootstrap")).resolves.toEqual({ ok: true });
    expect(getToken).toHaveBeenNthCalledWith(1, {});
    expect(getToken).toHaveBeenNthCalledWith(2, { forceRefresh: true });
    expect(new Headers(fetchMock.mock.calls[1][1].headers).get("Authorization")).toBe("Bearer fresh");
  });

  it("gives up after one more try, and says why", async () => {
    const fetchMock = vi.fn(async () => json({ error: "invalid or expired API token" }, 401));
    vi.stubGlobal("fetch", fetchMock);
    configureTransport({ getToken: () => "gsw_x" });
    const failure = await api("/api/bootstrap").catch((error) => error);
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(failure).toBeInstanceOf(ApiError);
    expect(failure.status).toBe(401);
    expect(failure.message).toBe("invalid or expired API token");
  });

  it("does not retry a 401 when the page signs in some other way", async () => {
    const fetchMock = vi.fn(async () => json({ error: "authentication required" }, 401));
    vi.stubGlobal("fetch", fetchMock);
    await expect(api("/api/bootstrap")).rejects.toMatchObject({ status: 401 });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("uses the host's fetch instead of the window's, and its credentials mode", async () => {
    const windowFetch = vi.fn();
    vi.stubGlobal("fetch", windowFetch);
    const hostFetch = vi.fn(async () => json({ ok: true }));
    configureTransport({ fetch: hostFetch, credentials: "include" });
    await apiFetch("/api/bootstrap");
    expect(windowFetch).not.toHaveBeenCalled();
    expect(hostFetch.mock.calls[0][1].credentials).toBe("include");
  });
});

describe("EventStreamParser", () => {
  const parse = (...chunks) => {
    const messages = [];
    const parser = new EventStreamParser((message) => messages.push(message));
    chunks.forEach((chunk) => parser.feed(chunk));
    return messages;
  };

  it("reads named events, comments and the lines of a message", () => {
    expect(parse(": hello\n\nevent: connected\ndata: {}\n\nevent: change\ndata: a\ndata: b\nid: 7\n\n")).toEqual([
      { event: "connected", id: "", data: "{}" },
      { event: "change", id: "7", data: "a\nb" },
    ]);
  });

  it("reads a message that arrives in pieces, whatever the line ends", () => {
    expect(parse("event: chan", "ge\r\nda", "ta: x\r\n\r", "\n")).toEqual([{ event: "change", id: "", data: "x" }]);
  });

  it("skips a message with no data", () => {
    expect(parse("event: ping\n\n")).toEqual([]);
  });
});

function streamOf(...chunks) {
  const encoder = new TextEncoder();
  return new ReadableStream({
    start(controller) {
      chunks.forEach((chunk) => controller.enqueue(encoder.encode(chunk)));
      controller.close();
    },
  });
}

describe("openEventSource", () => {
  it("is the browser's EventSource, with the cookie, when the page signs in with its cookie", () => {
    const created = [];
    class FakeEventSource {
      constructor(url, init) {
        created.push([url, init]);
      }
    }
    vi.stubGlobal("EventSource", FakeEventSource);
    configureTransport({ apiBaseUrl: "/geass" });
    expect(openEventSource("/api/events")).toBeInstanceOf(FakeEventSource);
    expect(created).toEqual([["/geass/api/events", { withCredentials: true }]]);
  });

  it("reads the stream with fetch, and the token, when there is one", async () => {
    const fetchMock = vi.fn(async () => new Response(streamOf("event: connected\ndata: {}\n\n", "data: line\n\n"), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    configureTransport({ apiBaseUrl: "/geass", getToken: () => "gsw_abc" });
    const source = openEventSource("/api/events");
    expect(source).toBeInstanceOf(FetchEventSource);
    const seen = [];
    source.addEventListener("connected", (event) => seen.push(["connected", event.data]));
    source.onmessage = (event) => seen.push(["message", event.data]);
    source.onopen = () => seen.push(["open"]);
    try {
      await vi.waitFor(() => expect(seen.slice(0, 3)).toEqual([["open"], ["connected", "{}"], ["message", "line"]]));
    } finally {
      source.close();
    }
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/geass/api/events");
    expect(new Headers(init.headers).get("Authorization")).toBe("Bearer gsw_abc");
    expect(new Headers(init.headers).get("Accept")).toBe("text/event-stream");
  });

  it("opens the stream again when it drops, and reports the drop, as EventSource does", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response("denied", { status: 503 }))
      .mockImplementation(async () => new Response(streamOf("data: back\n\n"), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    configureTransport({ getToken: () => "gsw_abc" });
    const source = new FetchEventSource("/api/events", { reconnectDelay: 5 });
    const seen = [];
    source.onerror = () => seen.push("error");
    source.onmessage = (event) => seen.push(event.data);
    try {
      await vi.waitFor(() => expect(seen.slice(0, 3)).toEqual(["error", "back", "back"]));
    } finally {
      source.close();
    }
  });

  it("gives up on an answer that trying again will not change, as EventSource does, and does not ask the host for tokens for ever", async () => {
    const fetchMock = vi.fn(async () => json({ error: "invalid or expired API token" }, 401));
    vi.stubGlobal("fetch", fetchMock);
    const getToken = vi.fn(() => "gsw_abc");
    configureTransport({ getToken });
    const source = new FetchEventSource("/api/events", { reconnectDelay: 5 });
    const errors = [];
    source.onerror = (event) => errors.push(event.status);
    try {
      await vi.waitFor(() => expect(errors).toEqual([401]));
      await new Promise((resolve) => setTimeout(resolve, 60));
      // One try, and the one more the transport makes with a fresh token.
      expect(fetchMock).toHaveBeenCalledTimes(2);
      expect(getToken).toHaveBeenCalledTimes(2);
      expect(errors).toEqual([401]);
    } finally {
      source.close();
    }
  });

  it("stops for good when closed", async () => {
    const fetchMock = vi.fn(async () => new Response(streamOf("data: x\n\n"), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    configureTransport({ getToken: () => "gsw_abc" });
    const source = new FetchEventSource("/api/events", { reconnectDelay: 5 });
    source.close();
    await new Promise((resolve) => setTimeout(resolve, 40));
    expect(fetchMock.mock.calls.length).toBeLessThanOrEqual(1);
  });
});

describe("openSocket", () => {
  const sockets = [];
  class FakeWebSocket {
    constructor(url, protocols) {
      this.url = String(url);
      this.protocols = protocols;
      sockets.push(this);
    }
  }

  it("is a plain socket, with no protocols, when the page signs in with its cookie", async () => {
    vi.stubGlobal("WebSocket", FakeWebSocket);
    await openSocket("/api/apps/web/console/ws?session=s");
    const socket = sockets.at(-1);
    expect(socket.url).toBe(`${window.location.origin.replace("http", "ws")}/api/apps/web/console/ws?session=s`);
    expect(socket.protocols).toBeUndefined();
  });

  it("offers the token as a subprotocol, next to the one the terminal speaks", async () => {
    vi.stubGlobal("WebSocket", FakeWebSocket);
    configureTransport({ apiBaseUrl: "https://geass.example.com/proxy", getToken: () => "gsw_abc" });
    await openSocket("/api/apps/web/console/ws?session=s");
    const socket = sockets.at(-1);
    expect(socket.url).toBe("wss://geass.example.com/proxy/api/apps/web/console/ws?session=s");
    expect(socket.protocols).toEqual([SOCKET_PROTOCOL, `${SOCKET_BEARER_PREFIX}gsw_abc`]);
  });
});
