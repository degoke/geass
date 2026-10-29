import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { createMemoryHistory } from "@tanstack/react-router";
import { GeassDashboard } from "@/GeassDashboard";
import { MANAGED_AREAS } from "@/lib/managed";
import { configureTransport } from "@/lib/transport";
import { geassTheme } from "@/theme";

// Managed mode: a platform that runs this Geass keeps sign-in, the dashboard's domain, the installer, updates and the K3s
// version. The server says so in the bootstrap (platform.managed) and refuses the writes; these tests are about what the
// pages do with it, which is show what is there, with the reason, and offer nothing to change it with. Without it every page
// is as it was, and each test says so first.

const MANAGED = { enabled: true, provider: "rungeass", locked: [...MANAGED_AREAS] };

const json = (body, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });

const authSettings = {
  mode: "local", configured: false, displayName: "", issuer: "", clientID: "", hasSecret: false, scopes: [], roleClaim: "", roleMap: {},
  requireClaim: {}, sessionMinutes: 60, refreshMinutes: 0, staleGraceMinutes: 0, callbackURL: "https://geass.example.com/auth/oidc/callback",
  backchannelLogoutURL: "https://geass.example.com/auth/oidc/logout", revokeTokensOnLogout: true, test: null, hasPasswordAdmin: true, canEnable: false,
  tenancy: { enabled: false, orgsClaim: "", platformClaim: "", platformValues: [], organizations: 0, autoProvision: "off", autoProvisionAllow: [], autoProvisionMax: 25 },
};

const etcdSettings = { configured: false, state: "off", policy: "{}" };

const organizations = {
  items: [{ name: "acme", displayName: "Acme", idpID: "org_acme", workspaces: [], projects: 0, suspended: false, idp: null }],
};

function bootstrap(managed) {
  return {
    projects: { items: [{ metadata: { name: "proj-1", creationTimestamp: "2026-10-01T00:00:00Z" }, spec: { displayName: "Payments", environments: ["production"] } }] },
    platformConfig: { items: [{ metadata: { name: "platform" }, spec: { rootDomain: "example.com", dashboardURL: "https://geass.example.com" } }] },
    platform: {
      session: { username: "admin", role: "admin", canMutate: true, platformAdmin: true },
      hasDashboardURL: true,
      release: { geass: "v0.1.0", k3s: "v1.31.0+k3s1" },
      ...(managed ? { managed } : {}),
    },
  };
}

/** The Geass API as far as these pages ask for it, with a record of every write. */
function geassApi(managed) {
  const writes = [];
  const fetchStub = vi.fn(async (url, init = {}) => {
    const path = String(url).replace("/proxy/c1", "");
    const method = init.method || "GET";
    if (method !== "GET") {
      writes.push({ method, path });
      return json({ ok: true });
    }
    switch (path) {
      case "/api/bootstrap": return json(bootstrap(managed));
      case "/api/settings/auth": return json(authSettings);
      case "/api/settings/etcd-snapshots": return json(etcdSettings);
      case "/api/orgs": return json(organizations);
      case "/api/events": return new Response(new ReadableStream({ start() {} }), { status: 200 });
      default: return json({}, 200);
    }
  });
  return { fetchStub, writes };
}

function mount(path, managed) {
  const { fetchStub, writes } = geassApi(managed);
  const history = createMemoryHistory({ initialEntries: [`/acme/clusters/c1${path}`] });
  const view = render(
    <MantineProvider theme={geassTheme}>
      <div style={{ height: 600 }}>
        <GeassDashboard basePath="/acme/clusters/c1" history={history} apiBaseUrl="/proxy/c1" getToken={() => "gsw_test"} fetch={fetchStub} />
      </div>
    </MantineProvider>,
  );
  return { writes, ...view };
}

/** A control a person can use: not off itself, and not inside a disabled fieldset. */
const usable = (element) => !element.disabled && !element.closest("fieldset[disabled]");

afterEach(() => {
  configureTransport();
});

describe("Sign-in", () => {
  it("is editable when nothing is managed", async () => {
    mount("/settings/auth");
    const save = await screen.findByRole("button", { name: "Save" });
    expect(usable(save)).toBe(true);
    expect(usable(screen.getByLabelText("Issuer URL"))).toBe(true);
    expect(screen.queryByText(/Managed by/)).toBeNull();
  });

  it("is shown as it is, with the reason and nothing to change it with, when the platform manages it", async () => {
    const { writes, container } = mount("/settings/auth", MANAGED);
    await screen.findByText("Managed by rungeass");
    expect(screen.getByText("Sign-in settings are managed by rungeass and cannot be changed here.")).toBeTruthy();
    expect(container.querySelector('[data-managed-area="signIn"]')).not.toBeNull();
    // The settings are there to read.
    expect(screen.getByText("Signing in now")).toBeTruthy();
    expect(screen.getByLabelText("Issuer URL")).toBeTruthy();
    const page = container.querySelector("fieldset.geass-managed-fieldset");
    const controls = [...page.querySelectorAll("button, input, textarea, select")];
    expect(controls.length).toBeGreaterThan(5);
    for (const control of controls) expect(usable(control), control.outerHTML.slice(0, 80)).toBe(false);
    // A click on what is off does nothing, in a browser as in jsdom.
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(writes).toEqual([]);
  });

  it("is editable when the platform manages other areas and not this one", async () => {
    mount("/settings/auth", { ...MANAGED, locked: ["dashboardDomain"] });
    const save = await screen.findByRole("button", { name: "Save" });
    expect(usable(save)).toBe(true);
    expect(screen.queryByText(/Managed by/)).toBeNull();
  });
});

describe("Domain", () => {
  it("is editable when nothing is managed", async () => {
    mount("/settings/domain");
    const save = await screen.findByRole("button", { name: "Save dashboard domain" });
    expect(usable(save)).toBe(true);
    expect(screen.queryByText(/Managed by/)).toBeNull();
  });

  it("is shown as it is, with the reason and nothing to change it with, when the platform manages it", async () => {
    const { writes, container } = mount("/settings/domain", MANAGED);
    await screen.findByText("Managed by rungeass");
    expect(screen.getByText("The dashboard's domain and DNS are managed by rungeass and cannot be changed here.")).toBeTruthy();
    expect(screen.getByDisplayValue("example.com")).toBeTruthy();
    const controls = [...container.querySelectorAll("fieldset.geass-managed-fieldset button, fieldset.geass-managed-fieldset input")];
    expect(controls.length).toBeGreaterThan(2);
    for (const control of controls) expect(usable(control), control.outerHTML.slice(0, 80)).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "Save dashboard domain" }));
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(writes).toEqual([]);
  });

  it("does not ask the person to set a domain that the platform sets", async () => {
    const withoutDomain = (managed) => {
      const { fetchStub } = geassApi(managed);
      return vi.fn(async (url, init) => {
        const response = await fetchStub(url, init);
        if (!String(url).endsWith("/api/bootstrap")) return response;
        const body = await response.json();
        return json({ ...body, platform: { ...body.platform, hasDashboardURL: false } });
      });
    };
    const show = (managed) =>
      render(
        <MantineProvider theme={geassTheme}>
          <div style={{ height: 600 }}>
            <GeassDashboard
              basePath="/acme/clusters/c1"
              history={createMemoryHistory({ initialEntries: ["/acme/clusters/c1/projects"] })}
              apiBaseUrl="/proxy/c1"
              getToken={() => "gsw_test"}
              fetch={withoutDomain(managed)}
            />
          </div>
        </MantineProvider>,
      );
    const asked = show(undefined);
    await screen.findByRole("link", { name: /Payments/ });
    await screen.findByText("Dashboard domain");
    asked.unmount();

    const managed = show(MANAGED);
    await screen.findByRole("link", { name: /Payments/ });
    expect(managed.container.querySelector(".rw-strip-banner")).toBeNull();
    expect(screen.queryByText("Dashboard domain")).toBeNull();
  });
});

describe("Usage: Version and cluster backup", () => {
  it("tells the person to run the installer, and gives them the backup's commands, when nothing is managed", async () => {
    mount("/cluster");
    await screen.findByText("Update by running a newer installer on the control plane.");
    const setUp = await screen.findByRole("button", { name: "Set up" });
    expect(usable(setUp)).toBe(true);
    expect(screen.getByText("What this needs")).toBeTruthy();
    expect(screen.queryByText(/Managed by/)).toBeNull();
  });

  it("says who updates Geass and K3s, and keeps the installer's commands and the backup's settings away, when the platform manages them", async () => {
    const { container, writes } = mount("/cluster", MANAGED);
    await screen.findByText("Updating Geass and the K3s version is managed by rungeass and cannot be changed here.");
    // What runs is still shown.
    expect(screen.getByText("K3s (control plane)")).toBeTruthy();
    expect(screen.getByText("v1.31.0+k3s1")).toBeTruthy();
    expect(screen.queryByText("Update by running a newer installer on the control plane.")).toBeNull();

    await screen.findByText("The backup of the cluster's state, which the installer sets up, is managed by rungeass and cannot be changed here.");
    expect(container.querySelector('[data-managed-area="installer"]')).not.toBeNull();
    expect(screen.queryByRole("button", { name: "Set up" })).toBeNull();
    expect(screen.queryByText("What this needs")).toBeNull();
    expect(container.textContent).not.toContain("etcd-snapshots enable");
    expect(container.textContent).not.toContain("install.sh");
    expect(screen.queryByText(/Off\. K3s snapshots stay on the cluster's own disk/)).toBeNull();
    expect(writes).toEqual([]);
  });
});

describe("Connectors", () => {
  it("opens Cloudflare, which writes the dashboard's DNS records, with nothing to change when the platform manages the domain", async () => {
    const open = async (managed) => {
      const view = mount("/settings/connectors", managed);
      fireEvent.click(await screen.findByRole("button", { name: /Cloudflare/ }));
      return view;
    };
    const free = await open(undefined);
    await screen.findByText("API token");
    expect(screen.queryByText(/Managed by/)).toBeNull();
    free.unmount();

    await open(MANAGED);
    const notice = await screen.findByText("Managed by rungeass");
    const sheet = notice.closest("[role=dialog]") || document.body;
    expect(within(sheet).getByText("The dashboard's domain and DNS are managed by rungeass and cannot be changed here.")).toBeTruthy();
    expect(screen.getByText("API token")).toBeTruthy();
    const controls = [...sheet.querySelectorAll("fieldset.geass-managed-fieldset input, fieldset.geass-managed-fieldset button")];
    expect(controls.length).toBeGreaterThan(1);
    for (const control of controls) expect(usable(control), control.outerHTML.slice(0, 80)).toBe(false);
  });
});

describe("Organizations", () => {
  const openAcme = async (managed) => {
    const view = mount("/settings/organizations", managed);
    fireEvent.click(await screen.findByRole("button", { name: "Edit Acme" }));
    await screen.findByText("Sign-in provider of its own");
    return view;
  };

  it("lets an organization's own sign-in provider be set up when nothing is managed", async () => {
    await openAcme();
    expect(usable(screen.getByRole("button", { name: "Set up a provider" }))).toBe(true);
    expect(screen.queryByText(/Managed by/)).toBeNull();
  });

  it("shows an organization's own sign-in provider with nothing to change it with when the platform manages sign-in, and still lets organizations be edited", async () => {
    await openAcme(MANAGED);
    await screen.findByText("Managed by rungeass");
    expect(screen.getByText("Sign-in settings are managed by rungeass and cannot be changed here.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Set up a provider" })).toBeNull();
    // What the platform does through the API is not kept back: the organization's own name and workspaces.
    expect(usable(screen.getByLabelText("Organization name"))).toBe(true);
  });
});
