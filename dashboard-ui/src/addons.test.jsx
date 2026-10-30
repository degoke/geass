import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { createMemoryHistory } from "@tanstack/react-router";
import { GeassDashboard } from "@/GeassDashboard";
import { addonsAreMoving } from "@/components/AddonSettings";
import { configureTransport } from "@/lib/transport";
import { geassTheme } from "@/theme";

// Settings > Add-ons: which add-ons are on, how each is doing, and the few things an admin can do to one. The server decides what
// is allowed; these tests are about what the page shows and what it sends.

const json = (body, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });

const addon = (fields) => ({ enabled: false, state: "Off", installed: false, canRetry: false, canDisable: true, keptWhenOff: false, ...fields });

const settings = (overrides = {}) => ({
  addons: [
    addon({ name: "serverless", title: "Sleep when idle", keptWhenOff: true }),
    addon({ name: "longhorn", title: "Replicated storage", keptWhenOff: true }),
    addon({ name: "monitoring", title: "Metrics", enabled: true, state: "Ready", installed: true, profile: "lite", profiles: ["lite", "full"] }),
    addon({ name: "logging", title: "Logs", enabled: true, state: "Ready", installed: true, retentionDays: 7 }),
  ],
  ...overrides,
});

const bootstrap = {
  projects: { items: [] },
  platformConfig: { items: [{ metadata: { name: "platform" }, spec: {} }] },
  platform: { session: { username: "admin", role: "admin", canMutate: true, platformAdmin: true }, release: { geass: "v0.1.0", k3s: "v1.31.0+k3s1" } },
};

/** The Geass API as far as this page asks for it, with a record of every write. */
function geassApi(current) {
  const writes = [];
  const fetchStub = vi.fn(async (url, init = {}) => {
    const path = String(url).replace("/proxy/c1", "");
    const method = init.method || "GET";
    if (method !== "GET") {
      writes.push({ method, path, body: Object.fromEntries(new URLSearchParams(String(init.body || ""))) });
      return json({ ok: true });
    }
    switch (path) {
      case "/api/bootstrap": return json(bootstrap);
      case "/api/settings/addons": return json(current);
      case "/api/events": return new Response(new ReadableStream({ start() {} }), { status: 200 });
      default: return json({}, 200);
    }
  });
  return { fetchStub, writes };
}

function mount(current) {
  const { fetchStub, writes } = geassApi(current);
  const history = createMemoryHistory({ initialEntries: ["/acme/clusters/c1/settings/addons"] });
  render(
    <MantineProvider theme={geassTheme}>
      <div style={{ height: 600 }}>
        <GeassDashboard basePath="/acme/clusters/c1" history={history} apiBaseUrl="/proxy/c1" getToken={() => "gsw_test"} fetch={fetchStub} />
      </div>
    </MantineProvider>,
  );
  return writes;
}

const card = async (name) => {
  const element = await waitFor(() => {
    const found = document.querySelector(`[data-addon="${name}"]`);
    if (!found) throw new Error(`no card for ${name}`);
    return found;
  });
  return within(element);
};

afterEach(() => {
  configureTransport();
});

describe("addonsAreMoving", () => {
  it("reads the list again soon while something is installing or a retry waits, and not otherwise", () => {
    expect(addonsAreMoving(undefined)).toBe(false);
    expect(addonsAreMoving(settings())).toBe(false);
    expect(addonsAreMoving({ addons: [addon({ state: "Installing", enabled: true })] })).toBe(true);
    expect(addonsAreMoving(settings({ retryPending: "monitoring" }))).toBe(true);
  });
});

describe("Add-ons", () => {
  it("turns an add-on on with one click, and sends only that", async () => {
    const writes = mount(settings());
    const serverless = await card("serverless");

    fireEvent.click(await serverless.findByRole("button", { name: "Turn on" }));

    await waitFor(() => expect(writes).toHaveLength(1));
    expect(writes[0]).toEqual({ method: "POST", path: "/api/settings/addons/serverless/save", body: { enabled: "true" } });
  });

  it("shows why an add-on is not up, and lets an admin retry a stuck install", async () => {
    const writes = mount(settings({
      addons: settings().addons.map((item) => item.name === "monitoring"
        ? { ...item, state: "Failed", reason: "InstallSuspended", message: "The install job is suspended.", canRetry: true }
        : item),
    }));
    const metrics = await card("monitoring");

    expect(await metrics.findByText("Failed")).toBeTruthy();
    expect(metrics.getByText("The install job is suspended.")).toBeTruthy();
    fireEvent.click(metrics.getByRole("button", { name: "Retry install" }));

    await waitFor(() => expect(writes).toHaveLength(1));
    expect(writes[0].path).toBe("/api/settings/addons/monitoring/retry");
  });

  it("does not offer a retry for an add-on that is not stuck", async () => {
    mount(settings());
    const logs = await card("logging");

    expect(await logs.findByText("Ready")).toBeTruthy();
    expect(logs.queryByRole("button", { name: "Retry install" })).toBeNull();
  });

  it("asks before it removes metrics, and says what goes", async () => {
    const writes = mount(settings());
    const metrics = await card("monitoring");

    fireEvent.click(await metrics.findByRole("button", { name: "Turn off" }));

    expect(writes).toHaveLength(0);
    expect(metrics.getByText(/Removes Prometheus/)).toBeTruthy();
    fireEvent.click(metrics.getByRole("button", { name: "Turn off Metrics" }));
    await waitFor(() => expect(writes).toHaveLength(1));
    expect(writes[0].body).toEqual({ enabled: "false" });
  });

  it("has no way to turn replicated storage off once it is on", async () => {
    mount(settings({
      addons: settings().addons.map((item) => item.name === "longhorn" ? { ...item, enabled: true, state: "Ready", installed: true, canDisable: false } : item),
    }));
    const storage = await card("longhorn");

    expect(await storage.findByText(/Cannot be turned off/)).toBeTruthy();
    expect(storage.queryByRole("button", { name: "Turn off" })).toBeNull();
  });

  it("warns about the iSCSI initiator before it turns replicated storage on", async () => {
    const writes = mount(settings());
    const storage = await card("longhorn");

    fireEvent.click(await storage.findByRole("button", { name: "Turn on" }));

    expect(writes).toHaveLength(0);
    expect(storage.getByText(/iSCSI initiator/)).toBeTruthy();
    fireEvent.click(storage.getByRole("button", { name: "Turn on Replicated storage" }));
    await waitFor(() => expect(writes).toHaveLength(1));
    expect(writes[0]).toEqual({ method: "POST", path: "/api/settings/addons/longhorn/save", body: { enabled: "true" } });
  });

  it("saves how long logs are kept", async () => {
    const writes = mount(settings());
    const logs = await card("logging");

    fireEvent.change(await logs.findByLabelText("Keep logs for (days)"), { target: { value: "30" } });
    fireEvent.click(logs.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(writes).toHaveLength(1));
    expect(writes[0]).toEqual({ method: "POST", path: "/api/settings/addons/logging/save", body: { retentionDays: "30" } });
  });
});
