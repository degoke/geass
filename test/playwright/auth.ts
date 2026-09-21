import { expect, type Page } from "@playwright/test";

export async function signIn(page: Page) {
  const username = process.env.GEASS_DASHBOARD_USERNAME ?? "admin";
  const password = process.env.GEASS_DASHBOARD_PASSWORD ?? "";
  await page.goto("/projects");
  const heading = page.getByRole("heading", { name: "Sign in to Geass" });
  if (!(await heading.isVisible().catch(() => false))) {
    return;
  }
  if (!password) {
    throw new Error("GEASS_DASHBOARD_PASSWORD is required to sign in to the dashboard");
  }
  await page.getByLabel("Username").fill(username);
  await page.getByLabel("Password").fill(password);
  await page.getByRole("button", { name: "Continue" }).click();
  await expect(heading).toHaveCount(0);
}
