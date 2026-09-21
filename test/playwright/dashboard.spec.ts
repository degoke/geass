import { expect, test } from "@playwright/test";

test("creates a project with a generated name and production environment", async ({ page }) => {
  await page.goto("/projects");
  await page.getByRole("button", { name: "New project" }).click();
  await expect(page).toHaveURL(/\/projects\/proj-[a-f0-9]{8}$/);
  await expect(page.getByText("production")).toBeVisible();
  await expect(page.getByRole("button", { name: "Add the first resource" })).toBeVisible();
});

test("resource create dialog lets users assign CPU memory and autoscaling", async ({ page }) => {
  await page.goto("/projects");
  await page.getByRole("button", { name: "New project" }).click();
  await page.getByRole("button", { name: "Add the first resource" }).click();
  await page.getByRole("button", { name: /Service/ }).click();
  await page.getByRole("button", { name: "Continue" }).click();
  await page.getByRole("button", { name: /Docker image/ }).click();
  await page.getByRole("button", { name: "Continue" }).click();
  await expect(page.getByLabel("CPU")).toBeVisible();
  await expect(page.getByLabel("Memory")).toBeVisible();
  await expect(page.getByLabel("Replicas")).toBeVisible();
  await expect(page.getByText("Autoscaling")).toBeVisible();
});

test("cloud connections allow AWS and PlanetScale", async ({ page }) => {
  await page.goto("/cloud-connections");
  await expect(page.getByRole("heading", { name: "Cloud connections" })).toBeVisible();
  await expect(page.getByLabel("Provider")).toBeVisible();
  await expect(page.getByRole("button", { name: "Save connection" })).toBeVisible();
  await expect(page.getByText("Unavailable in this release")).toHaveCount(0);
});

test("cluster capacity page shows CPU and memory", async ({ page }) => {
  await page.goto("/cluster");
  await expect(page.getByRole("heading", { name: "Cluster" })).toBeVisible();
  await expect(page.getByText(/allocatable CPU and memory|Node capacity is unavailable/i)).toBeVisible();
});

test("cluster object storage settings can set up MinIO", async ({ page }) => {
  await page.goto("/object-storage");
  await expect(page.getByRole("heading", { name: "Object storage" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Set up MinIO server" })).toBeVisible();
});

test("in-cluster buckets stay disabled until MinIO is set up", async ({ page }) => {
  await page.goto("/projects");
  await page.getByRole("button", { name: "New project" }).click();
  await page.getByRole("button", { name: "Add the first resource" }).click();
  await page.getByRole("button", { name: /Bucket/ }).click();
  await page.getByRole("button", { name: "Continue" }).click();
  await expect(page.getByRole("button", { name: /In-cluster bucket/ })).toBeDisabled();
  await expect(page.getByText(/cluster settings first/)).toBeVisible();
});
