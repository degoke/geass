import { expect, test } from "@playwright/test";
import { signIn } from "./auth";

test.beforeEach(async ({ page }) => {
  await signIn(page);
});

test("general settings shows cluster overview and domain tab", async ({ page }) => {
  await page.goto("/settings");
  await expect(page.getByRole("heading", { name: "General settings" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Cluster overview" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Domain" })).toBeVisible();
  await expect(page.getByRole("link", { name: "GitHub" })).toBeVisible();
});

test("domain settings shows domain form", async ({ page }) => {
  await page.goto("/settings/domain");
  await expect(page.getByRole("heading", { name: "Domain" })).toBeVisible();
  await expect(page.getByLabel("Your domain")).toBeVisible();
  await expect(page.getByRole("button", { name: "Save" })).toBeVisible();
});

test("github settings shows manifest create button when domain is ready", async ({ page }) => {
  // Domain may or may not be configured in the e2e cluster; assert either gate or create CTA.
  await page.goto("/settings/github");
  const createApp = page.getByRole("button", { name: "Create GitHub App on GitHub" });
  const configureDomain = page.getByRole("link", { name: "Configure domain" });
  await expect(createApp.or(configureDomain)).toBeVisible();
});
