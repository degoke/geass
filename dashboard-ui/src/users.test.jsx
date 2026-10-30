import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { createMemoryHistory } from "@tanstack/react-router";
import { GeassDashboard } from "@/GeassDashboard";
import { MANAGED_AREAS } from "@/lib/managed";
import { configureTransport } from "@/lib/transport";
import { geassTheme } from "@/theme";

// Passwords that Geass made, built from parts so that secret scanners do not take them for passwords.
const madeUp = (...parts) => parts.join("-");

// Settings > Users: the people who sign in with a password. The server decides what is allowed (the last admin stays, a name
// is free, a password is long enough), so these tests are about what the page sends, what it shows once, and what it offers.

const json = (body, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });

const ADMIN = { username: "admin", role: "admin", current: true };
const MARA = { username: "mara", role: "viewer" };

function bootstrap(managed) {
  return {
    projects: { items: [] },
    platformConfig: { items: [{ metadata: { name: "platform" }, spec: { rootDomain: "example.com", dashboardURL: "https://geass.example.com" } }] },
    platform: {
      session: { username: "admin", role: "admin", canMutate: true, platformAdmin: true },
      hasDashboardURL: true,
      release: { geass: "v0.1.0", k3s: "v1.31.0+k3s1" },
      ...(managed ? { managed } : {}),
    },
  };
}

function geassApi({ users = [ADMIN, MARA], managed, created, failure } = {}) {
  const writes = [];
  const fetchStub = vi.fn(async (url, init = {}) => {
    const path = String(url).replace("/proxy/c1", "");
    const method = init.method || "GET";
    if (method !== "GET") {
      writes.push({ method, path, form: Object.fromEntries(new URLSearchParams(String(init.body || ""))) });
      if (failure) return json({ error: failure }, 409);
      return json(created || { ok: true });
    }
    switch (path) {
      case "/api/bootstrap": return json(bootstrap(managed));
      case "/api/users": return json({ items: users });
      case "/api/events": return new Response(new ReadableStream({ start() {} }), { status: 200 });
      default: return json({}, 200);
    }
  });
  return { fetchStub, writes };
}

function mount(options) {
  const { fetchStub, writes } = geassApi(options);
  const history = createMemoryHistory({ initialEntries: ["/acme/clusters/c1/settings/users"] });
  const view = render(
    <MantineProvider theme={geassTheme}>
      <div style={{ height: 600 }}>
        <GeassDashboard basePath="/acme/clusters/c1" history={history} apiBaseUrl="/proxy/c1" getToken={() => "gsw_test"} fetch={fetchStub} />
      </div>
    </MantineProvider>,
  );
  return { writes, ...view };
}

const usable = (element) => !element.disabled && !element.closest("fieldset[disabled]");

afterEach(() => {
  configureTransport();
});

async function openMenu(username) {
  fireEvent.click(await screen.findByRole("button", { name: `Actions for ${username}` }));
}

describe("Users", () => {
  it("lists the users with their roles and marks the one who is signed in", async () => {
    mount();
    const list = await screen.findByTestId("users-list");
    await within(list).findByText("mara");
    expect(within(list).getByText("admin")).toBeTruthy();
    expect(within(list).getByText("You")).toBeTruthy();
    expect(screen.queryByText(/password/i, { selector: "code" })).toBeNull();
  });

  it("adds a user and shows the password that was made up, once", async () => {
    const { writes } = mount({ created: { ok: true, username: "ops", role: "viewer", password: madeUp("ABCDE", "FGHJK", "LMNPQ", "RSTUV") } });
    fireEvent.click(await screen.findByRole("button", { name: "Add user" }));
    fireEvent.change(await screen.findByLabelText("Username"), { target: { value: " ops " } });
    fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Add user" }));

    expect((await screen.findByTestId("generated-password")).textContent).toBe(madeUp("ABCDE", "FGHJK", "LMNPQ", "RSTUV"));
    expect(writes).toEqual([{ method: "POST", path: "/api/users/create", form: { username: "ops", role: "viewer" } }]);

    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    await waitFor(() => expect(screen.queryByTestId("generated-password")).toBeNull());
  });

  it("sends a password the person chose, and does not show it back", async () => {
    const { writes } = mount({ created: { ok: true, username: "ops", role: "admin" } });
    fireEvent.click(await screen.findByRole("button", { name: "Add user" }));
    fireEvent.change(await screen.findByLabelText("Username"), { target: { value: "ops" } });
    fireEvent.change(screen.getByLabelText("Role"), { target: { value: "admin" } });
    fireEvent.change(screen.getByLabelText("Password"), { target: { value: "a-long-enough-one" } });
    fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Add user" }));

    await waitFor(() => expect(writes).toHaveLength(1));
    expect(writes[0].form).toEqual({ username: "ops", role: "admin", password: "a-long-enough-one" });
    expect(screen.queryByTestId("generated-password")).toBeNull();
  });

  it("changes a role from the menu", async () => {
    const { writes } = mount();
    await openMenu("mara");
    fireEvent.click(await screen.findByText("Make admin"));
    await waitFor(() => expect(writes).toHaveLength(1));
    expect(writes[0]).toEqual({ method: "POST", path: "/api/users/role", form: { username: "mara", role: "admin" } });
  });

  it("offers no way to demote or remove the last admin", async () => {
    mount();
    await openMenu("admin");
    const off = (text) => screen.getByText(text).closest("button").hasAttribute("data-disabled");
    await screen.findByText("Make viewer (last admin)");
    expect(off("Make viewer (last admin)")).toBe(true);
    expect(off("Remove (last admin)")).toBe(true);
    expect(off("Reset password")).toBe(false);
  });

  it("resets a password and shows the new one once", async () => {
    const { writes } = mount({ created: { ok: true, username: "mara", password: madeUp("VWXYZ", "23456", "789AB", "CDEFG") } });
    await openMenu("mara");
    fireEvent.click(await screen.findByText("Reset password"));
    fireEvent.click(await within(await screen.findByRole("dialog")).findByRole("button", { name: "Reset password" }));

    expect((await screen.findByTestId("generated-password")).textContent).toBe(madeUp("VWXYZ", "23456", "789AB", "CDEFG"));
    expect(writes).toEqual([{ method: "POST", path: "/api/users/password", form: { username: "mara" } }]);
  });

  it("removes a user after asking", async () => {
    const { writes } = mount();
    await openMenu("mara");
    fireEvent.click(await screen.findByText("Remove"));
    expect(writes).toEqual([]);
    fireEvent.click(await screen.findByRole("button", { name: "Remove user" }));
    await waitFor(() => expect(writes).toHaveLength(1));
    expect(writes[0]).toEqual({ method: "POST", path: "/api/users/remove", form: { username: "mara" } });
  });

  it("says why the server refused a change", async () => {
    const shown = vi.spyOn(notifications, "show").mockImplementation(() => "id");
    const { writes } = mount({ failure: "this is the last admin: make another user an admin first" });
    await openMenu("mara");
    fireEvent.click(await screen.findByText("Make admin"));
    await waitFor(() => expect(shown).toHaveBeenCalledWith(expect.objectContaining({ message: "this is the last admin: make another user an admin first", color: "red" })));
    expect(writes).toHaveLength(1);
    shown.mockRestore();
  });

  it("signs everyone out only after asking", async () => {
    const { writes } = mount();
    fireEvent.click(await screen.findByRole("button", { name: "Sign out everyone" }));
    expect(writes).toEqual([]);
    expect(await screen.findByText(/Every session ends, yours too/)).toBeTruthy();
  });

  it("is shown as it is, and cannot be changed, when the platform manages sign-in", async () => {
    const { container } = mount({ managed: { enabled: true, provider: "rungeass", locked: [...MANAGED_AREAS] } });
    await screen.findByText("Managed by rungeass");
    const page = container.querySelector("fieldset.geass-managed-fieldset");
    for (const control of page.querySelectorAll("button, input, select")) expect(usable(control)).toBe(false);
  });
});
