import { expect, test } from "@playwright/test";

test("creates a project with a generated name and production environment", async ({ page }) => {
  await page.goto("/projects");
  await page.getByRole("button", { name: "New" }).click();
  await expect(page).toHaveURL(/\/projects\/proj-[a-f0-9]{8}$/);
  await expect(page.getByText("production")).toBeVisible();
  await expect(page.getByRole("link", { name: "Add the first resource" })).toBeVisible();
});

test("shows unsupported cloud providers as unavailable", async ({ page }) => {
  await page.goto("/cloud-connections");
  await expect(page.getByText("Unavailable in this release")).toBeVisible();
  await expect(page.getByText(/AWS credentials and adapters/)).toBeVisible();
});
