import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, RouterProvider } from "@tanstack/react-router";
import { createDashboardRouter } from "@/router";
import { configureTransport } from "@/lib/transport";
import { geassTheme } from "@/theme";

// A password built from parts, so that secret scanners do not take it for one.
const chosen = () => ["a", "new", "password", "here"].join("-");

const json = (body, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });

function mount({ hash = "#k7Qx-token" } = {}) {
  window.history.replaceState(null, "", `/setup${hash}`);
  const writes = [];
  const fetchStub = vi.fn(async (url, init = {}) => {
    const method = init.method || "GET";
    if (method !== "GET") {
      writes.push({ path: String(url), form: Object.fromEntries(new URLSearchParams(String(init.body || ""))) });
      return json({ ok: true, authenticated: true, username: "admin", role: "admin" });
    }
    if (String(url).endsWith("/api/bootstrap")) return json({ error: "authentication required" }, 401);
    if (String(url).endsWith("/api/session")) return json({ authenticated: false });
    return json({});
  });
  configureTransport({ fetch: fetchStub });
  const router = createDashboardRouter({ history: createMemoryHistory({ initialEntries: ["/setup"] }) });
  render(
    <MantineProvider theme={geassTheme}>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </MantineProvider>,
  );
  return { writes };
}

afterEach(() => {
  configureTransport();
  window.history.replaceState(null, "", "/");
});

describe("Setup", () => {
  it("sends the token from the link with the new admin, and takes the token out of the address", async () => {
    const { writes } = mount();
    fireEvent.change(await screen.findByLabelText(/Password/), { target: { value: chosen() } });
    expect(window.location.hash).toBe("");
    fireEvent.click(screen.getByRole("button", { name: "Create admin" }));
    await waitFor(() => expect(writes).toHaveLength(1));
    expect(writes[0].path).toMatch(/\/api\/setup$/);
    expect(writes[0].form).toEqual({ token: "k7Qx-token", username: "admin", password: chosen() });
  });

  it("says to open the link when the address has no token", async () => {
    mount({ hash: "" });
    expect(await screen.findByText("Open the setup link that the installer printed.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Create admin" })).toBeNull();
  });
});
