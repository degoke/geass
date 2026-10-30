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

  it("leaves out the Version section and adding a server on Usage when the host hides them, and shows them otherwise", async () => {
    const release = { platform: { session: { username: "t", role: "admin", canMutate: true, platformAdmin: true }, release: { geass: "v0.1.0", k3s: "v1.31.0" } } };
    const api = () => geassApi({ "/api/bootstrap": () => json(bootstrap(release)) });
    const shown = mount({ fetchStub: api().fetchStub, path: "/acme/clusters/c1/cluster" });
    await screen.findByRole("button", { name: "Add server" });
    expect(screen.getByText("K3s (control plane)")).toBeTruthy();
    shown.unmount();

    mount({ fetchStub: api().fetchStub, path: "/acme/clusters/c1/cluster", props: { hide: ["upgrade", "servers"] } });
    await screen.findByText("Nodes");
    expect(screen.queryByRole("button", { name: "Add server" })).toBeNull();
    expect(screen.queryByText("K3s (control plane)")).toBeNull();
  });

  it("lists a server that did not join with its error, and retries it from the table", async () => {
    const capacity = {
      known: true,
      nodes: [
        { name: "main", role: "control-plane", ready: true, schedulable: true, cpuAllocatable: "4", memoryAllocatable: "8Gi" },
        { name: "worker-2", role: "worker", ready: true, schedulable: false, cpuAllocatable: "2", memoryAllocatable: "4Gi" },
        { name: "upgrading", role: "worker", ready: true, schedulable: false, cordonedBy: "upgrade", cpuAllocatable: "2", memoryAllocatable: "4Gi" },
      ],
    };
    const failure = "ssh dial 198.51.100.7:22: connection refused";
    let retried = 0;
    let cordoned = 0;
    const { fetchStub, requests } = geassApi({
      "/api/bootstrap": () => json(bootstrap({ platform: { session: { username: "t", role: "admin", canMutate: true, platformAdmin: true }, capacity } })),
      "/api/v1/servers": () => json({
        items: [
          { name: "main", role: "control-plane", phase: "Ready", joined: true, node: "main", canRemove: false },
          { name: "edge-1", role: "worker", phase: "Failed", message: failure, joined: false, canRetry: true, canUpdateHostKeys: true, canRemove: true, address: "198.51.100.7" },
        ],
      }),
      "/api/servers/edge-1/retry": () => { retried += 1; return json({ ok: true }); },
      "/api/nodes/worker-2/uncordon": () => { cordoned += 1; return json({ ok: true, cordoned: false }); },
    });
    mount({ fetchStub, path: "/acme/clusters/c1/cluster" });

    await screen.findByText(failure);
    const row = screen.getByText("edge-1").closest("tr");
    expect(within(row).getByText("Failed")).toBeTruthy();
    // What is next for a failed server is in the row. The rest is in its menu.
    expect(screen.getByRole("button", { name: "Retry edge-1" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Actions for edge-1" }));
    const menuOf = async (name) => {
      fireEvent.click(screen.getByRole("button", { name: `Actions for ${name}` }));
      const items = await screen.findAllByRole("menuitem");
      const labels = items.map((item) => item.textContent);
      const disabled = items.filter((item) => item.hasAttribute("data-disabled") || item.disabled).map((item) => item.textContent);
      fireEvent.keyDown(items[0], { key: "Escape" });
      await waitFor(() => expect(screen.queryAllByRole("menuitem")).toHaveLength(0));
      return { labels, disabled };
    };
    expect((await menuOf("edge-1")).labels).toEqual(["Update host keys", "Remove"]);
    // The control plane has no Remove.
    expect((await menuOf("main")).labels).toEqual(["Edit", "Cordon"]);
    // A node that Geass cordoned cannot be uncordoned by hand.
    expect(await menuOf("upgrading")).toEqual({ labels: ["Edit", "Uncordon", "Remove"], disabled: ["Uncordon"] });
    expect(screen.getByText("Cordoned by Geass to upgrade it")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Retry edge-1" }));
    await waitFor(() => expect(retried).toBe(1));
    fireEvent.click(screen.getByRole("button", { name: "Actions for worker-2" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Uncordon" }));
    await waitFor(() => expect(cordoned).toBe(1));
    expect(requests.filter((request) => request.method === "POST").every((request) => request.authorization === "Bearer gsw_test")).toBe(true);
  });

  it("sends new host keys for a server from the table", async () => {
    let sent = "";
    const { fetchStub } = geassApi({
      "/api/bootstrap": () => json(bootstrap({ platform: { session: { username: "t", role: "admin", canMutate: true, platformAdmin: true }, capacity: { known: true, nodes: [] } } })),
      "/api/v1/servers": () => json({ items: [{ name: "edge-1", role: "worker", phase: "Failed", message: "host key", joined: false, canRetry: true, canUpdateHostKeys: true, canRemove: true, address: "198.51.100.7" }] }),
      "/api/servers/edge-1/host-keys": (_url, init) => { sent = new URLSearchParams(init.body).get("knownHosts"); return json({ ok: true, failed: true }); },
    });
    mount({ fetchStub, path: "/acme/clusters/c1/cluster" });

    fireEvent.click(await screen.findByRole("button", { name: "Actions for edge-1" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Update host keys" }));
    fireEvent.change(await screen.findByLabelText("Host keys"), { target: { value: "198.51.100.7 ssh-ed25519 AAAA" } });
    fireEvent.click(screen.getByRole("button", { name: "Save host keys" }));
    await waitFor(() => expect(sent).toBe("198.51.100.7 ssh-ed25519 AAAA"));
  });

  it("shows every check of the HA readiness report, and which one did not pass", async () => {
    const conditions = [
      { type: "NodesReady", status: "False", reason: "HealthyNodes", message: "At least three healthy schedulable nodes are required", lastTransitionTime: "2026-10-30T10:00:00Z" },
      { type: "StorageReady", status: "True", reason: "StorageClass", message: "A usable persistent storage class is required", lastTransitionTime: "2026-10-30T10:00:00Z" },
      { type: "Ready", status: "False", reason: "HAReadiness", message: "HA prerequisites are satisfied", lastTransitionTime: "2026-10-30T10:00:00Z" },
    ];
    const { fetchStub } = geassApi({
      "/api/bootstrap": () => json(bootstrap({
        platform: { session: { username: "t", role: "admin", canMutate: true, platformAdmin: true }, haReady: false },
        haReadiness: { items: [{ metadata: { name: "platform" }, status: { healthyNodes: 2, conditions } }] },
      })),
    });
    mount({ fetchStub, path: "/acme/clusters/c1/cluster" });

    await screen.findByText("At least three healthy schedulable nodes are required. Now: 2.");
    const nodes = screen.getByText("Healthy nodes", { selector: "td" }).closest("tr");
    expect(within(nodes).getByText("Not passed")).toBeTruthy();
    expect(screen.getByText("Every check above must pass.")).toBeTruthy();
    const storage = screen.getByText("Storage for databases").closest("tr");
    expect(within(storage).getByText("Passed")).toBeTruthy();
  });

  it("leaves out a project's Tokens section when the host hides tokens, and shows it otherwise", async () => {
    const path = "/acme/clusters/c1/projects/proj-1/settings";
    const shown = mount({ fetchStub: geassApi().fetchStub, path });
    await screen.findByText("Compliance");
    expect(screen.queryByText("Tokens")).not.toBeNull();
    shown.unmount();

    mount({ fetchStub: geassApi().fetchStub, path, props: { hide: ["tokens"] } });
    await screen.findByText("Compliance");
    expect(screen.queryByText("Tokens")).toBeNull();
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
