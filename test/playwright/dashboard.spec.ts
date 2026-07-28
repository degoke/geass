import { expect, test } from "@playwright/test";

test("creates a project and exposes its isolated environments", async ({ page }) => {
  await page.goto("/projects/new");
  await page.getByLabel("Project name").fill("playwright-demo");
  await page.getByLabel("Display name").fill("Playwright Demo");
  await page.getByRole("button", { name: "Create project" }).click();
  await expect(page).toHaveURL(/\/projects\/playwright-demo$/);
  await expect(page.getByText("dev")).toBeVisible();
  await expect(page.getByText("staging")).toBeVisible();
  await expect(page.getByText("production")).toBeVisible();
  await expect(page.getByRole("link", { name: "Deploy image" })).toBeVisible();
});

test("shows unsupported cloud providers as unavailable", async ({ page }) => {
  await page.goto("/cloud-connections");
  await expect(page.getByText("Unavailable in this release")).toBeVisible();
  await expect(page.getByText(/AWS credentials and adapters/)).toBeVisible();
});
