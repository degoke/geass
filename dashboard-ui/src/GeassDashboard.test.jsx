import { afterEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { createMemoryHistory } from "@tanstack/react-router";
import { GeassDashboard } from "@/GeassDashboard";
import { SELF_HOSTED_ONLY } from "@/lib/options";
import { configureTransport } from "@/lib/transport";
import { geassTheme } from "@/theme";

const project = {
  metadata: { name: "proj-1", creationTimestamp: "2026-10-01T00:00:00Z" },
  spec: { displayName: "Payments", environments: ["production"] },
};

const bootstrap = (overrides = {}) => ({
  projects: { items: [project] },
  platform: { session: { username: "token:rungeass", role: "admin", canMutate: true, platformAdmin: true } },
  ...overrides,
});

const json = (body, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });

/** The Geass API, as far as the pages under test ask for it. */
function geassApi(handlers = {}, prefix = "/proxy/c1") {
  const requests = [];
  const fetchStub = vi.fn(async (url, init = {}) => {
    const headers = new Headers(init.headers);
    requests.push({ url: String(url), method: init.method || "GET", authorization: headers.get("Authorization") });
    const path = String(url).replace(prefix, "");
    if (handlers[path]) return handlers[path](url, init);
    if (path === "/api/bootstrap") return json(bootstrap());
    // The event stream: open, and silent.
    if (path === "/api/events") return new Response(new ReadableStream({ start() {} }), { status: 200 });
    return json({}, 200);
  });
  return { fetchStub, requests };
}

function mount({ path = "/acme/clusters/c1/projects", props = {}, fetchStub } = {}) {
  const history = createMemoryHistory({ initialEntries: [path] });
  const view = render(
    <MantineProvider theme={geassTheme}>
      <div style={{ height: 600 }}>
        <GeassDashboard
          basePath="/acme/clusters/c1"
          history={history}
          apiBaseUrl="/proxy/c1"
          getToken={() => "gsw_test"}
          fetch={fetchStub}
          {...props}
        />
      </div>
    </MantineProvider>,
  );
  return { history, ...view };
}

afterEach(() => {
  configureTransport();
});

describe("GeassDashboard", () => {
  it("asks the API under apiBaseUrl, with the token, and lists the projects below basePath", async () => {
    const { fetchStub, requests } = geassApi();
    mount({ fetchStub });
    const link = await screen.findByRole("link", { name: /Payments/ });
    expect(link.getAttribute("href")).toBe("/acme/clusters/c1/projects/proj-1");
    expect(requests[0]).toMatchObject({ url: "/proxy/c1/api/bootstrap", authorization: "Bearer gsw_test" });
    // The event stream is read with fetch, so that it carries the token too.
    await waitFor(() => expect(requests.some((request) => request.url === "/proxy/c1/api/events" && request.authorization === "Bearer gsw_test")).toBe(true));
  });

  it("keeps every link of the shell inside basePath, and moves the host's history when one is followed", async () => {
    const { fetchStub } = geassApi();
    const { history, container } = mount({ fetchStub });
    await screen.findByRole("link", { name: /Payments/ });
    const hrefs = [...container.querySelectorAll("a[href]")].map((a) => a.getAttribute("href")).filter((href) => href.startsWith("/"));
    expect(hrefs.length).toBeGreaterThan(2);
    for (const href of hrefs) expect(href, href).toMatch(/^\/acme\/clusters\/c1(\/|$)/);

    fireEvent.click(within(container).getByRole("link", { name: "Templates" }));
    await waitFor(() => expect(history.location.pathname).toBe("/acme/clusters/c1/templates"));
  });

  it("follows the host's history when the host navigates", async () => {
    const { fetchStub } = geassApi();
    const { history } = mount({ fetchStub });
    await screen.findByRole("link", { name: /Payments/ });
    act(() => history.push("/acme/clusters/c1/settings"));
    await screen.findByText("General settings");
  });

  it("leaves out what the host hides, from the menu and from the page that is asked for", async () => {
    const { fetchStub } = geassApi();
    const { container } = mount({ fetchStub, path: "/acme/clusters/c1/settings", props: { hide: SELF_HOSTED_ONLY } });
    await screen.findByText("General settings");
    const labels = [...container.querySelectorAll(".rw-settings-platform-link")].map((node) => node.textContent);
    expect(labels).toEqual(expect.arrayContaining(["Connectors"]));
    for (const hidden of ["Domain", "Tokens", "Sign-in", "Organizations", "Alerts"]) expect(labels).not.toContain(hidden);
    expect(container.querySelector(".rw-top-avatar")).toBeNull();
  });

  it("says a hidden page is not available, even for a link that was kept", async () => {
    const { fetchStub } = geassApi();
    mount({ fetchStub, path: "/acme/clusters/c1/settings/auth", props: { hide: SELF_HOSTED_ONLY } });
    await screen.findByText("Not available here");
  });

  it("shows everything, and the account menu, when nothing is hidden", async () => {
    const { fetchStub } = geassApi();
    const { container } = mount({ fetchStub, path: "/acme/clusters/c1/settings" });
    await screen.findByText("General settings");
    const labels = [...container.querySelectorAll(".rw-settings-platform-link")].map((node) => node.textContent);
    expect(labels).toEqual(expect.arrayContaining(["Domain", "Tokens", "Sign-in"]));
    expect(container.querySelector(".rw-top-avatar")).not.toBeNull();
  });

  it("asks the host for a new token when the API turns the token away, instead of showing a sign-in page", async () => {
    const onUnauthorized = vi.fn();
    const { fetchStub, requests } = geassApi({ "/api/bootstrap": () => json({ error: "invalid or expired API token" }, 401) });
    mount({ fetchStub, props: { onUnauthorized } });
    const button = await screen.findByRole("button", { name: "Reconnect" });
    expect(screen.queryByText(/username/i)).toBeNull();
    // The token is asked for again before the page gives up.
    expect(requests.filter((request) => request.url === "/proxy/c1/api/bootstrap").length).toBeGreaterThanOrEqual(2);
    fireEvent.click(button);
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
  });

  it("shows the new cluster, and nothing of the last, after apiBaseUrl and basePath change", async () => {
    const first = geassApi();
    const { rerender } = mount({ fetchStub: first.fetchStub });
    await screen.findByRole("link", { name: /Payments/ });
    const second = geassApi({
      "/api/bootstrap": () => json(bootstrap({ projects: { items: [{ ...project, metadata: { name: "proj-2" }, spec: { displayName: "Ledger", environments: ["production"] } }] } })),
    }, "/proxy/c2");
    rerender(
      <MantineProvider theme={geassTheme}>
        <div style={{ height: 600 }}>
          <GeassDashboard basePath="/acme/clusters/c2" history={createMemoryHistory({ initialEntries: ["/acme/clusters/c2/projects"] })} apiBaseUrl="/proxy/c2" getToken={() => "gsw_two"} fetch={second.fetchStub} />
        </div>
      </MantineProvider>,
    );
    const link = await screen.findByRole("link", { name: /Ledger/ });
    expect(link.getAttribute("href")).toBe("/acme/clusters/c2/projects/proj-2");
    expect(screen.queryByRole("link", { name: /Payments/ })).toBeNull();
  });
});
