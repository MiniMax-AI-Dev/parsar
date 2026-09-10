import { expect, test, type Page } from "@playwright/test";
import { json, WORKSPACE_ID, OTHER_WORKSPACE_ID } from "./helpers/conversation-app";

type Failure = "all capabilities" | "one workspace" | "workspace directory" | null;
const noImpact = "No capability currently uses this credential type.";
const fullImpact = "2 capabilities across 2 workspaces use this credential type.";
const unavailable = "Unable to check capability references";

for (const failure of ["all capabilities", "one workspace", "workspace directory"] as const) {
  test(`credential deletion impact recovers from failed ${failure}`, async ({ page }) => {
    const state = await mockImpact(page, failure === "workspace directory" ? null : failure);
    await page.goto(`/?ws=${WORKSPACE_ID}&admin=secrets`);
    const open = page.getByRole("button", { name: "Delete", exact: true });
    await expect(open).toBeVisible();
    state.failure = failure;
    await open.click();
    const dialog = page.getByRole("alertdialog");
    await expect(dialog.getByText(unavailable, { exact: true })).toBeVisible();
    await expect(dialog.getByText(noImpact, { exact: true })).toHaveCount(0);
    await expect(dialog.getByText(/\d+ capabilities across/)).toHaveCount(0);
    const before = state.reads;
    state.failure = null;
    await dialog.getByRole("button", { name: "Retry", exact: true }).click();
    await expect(dialog.getByText(fullImpact, { exact: true })).toBeVisible();
    await expect(dialog.getByText(unavailable, { exact: true })).toHaveCount(0);
    expect(state.reads).toBeGreaterThan(before);
    await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
    await expect(dialog).toHaveCount(0);
    expect(state.writes).toEqual([]);
  });
}

test("failed refresh does not present cached references as a complete scan", async ({ page }) => {
  const state = await mockImpact(page, null);
  await page.goto(`/?ws=${WORKSPACE_ID}&admin=secrets`);
  const open = page.getByRole("button", { name: "Delete", exact: true });
  await open.click();
  const dialog = page.getByRole("alertdialog");
  await expect(dialog.getByText(fullImpact, { exact: true })).toBeVisible();
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
  state.failure = "one workspace";
  await open.click();
  await expect(dialog.getByText(unavailable, { exact: true })).toBeVisible();
  await expect(dialog.getByText(fullImpact, { exact: true })).toHaveCount(0);
  state.failure = null;
  await dialog.getByRole("button", { name: "Retry", exact: true }).click();
  await expect(dialog.getByText(fullImpact, { exact: true })).toBeVisible();
  expect(state.writes).toEqual([]);
});

test("successful empty credential impact stays distinct from a read failure", async ({ page }) => {
  const state = await mockImpact(page, null);
  state.empty = true;
  await page.goto(`/?ws=${WORKSPACE_ID}&admin=secrets`);
  await page.getByRole("button", { name: "Delete", exact: true }).click();
  const dialog = page.getByRole("alertdialog");
  await expect(dialog.getByText(noImpact, { exact: true })).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Retry", exact: true })).toHaveCount(0);
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
  expect(state.writes).toEqual([]);
});

test("advisory read failure preserves the existing explicit deletion action", async ({ page }) => {
  const state = await mockImpact(page, "all capabilities");
  await page.goto(`/?ws=${WORKSPACE_ID}&admin=secrets`);
  await page.getByRole("button", { name: "Delete", exact: true }).click();
  const dialog = page.getByRole("alertdialog");
  await expect(dialog.getByText(unavailable, { exact: true })).toBeVisible();
  await dialog.getByRole("button", { name: "Permanently delete", exact: true }).click();
  await expect.poll(() => state.writes).toEqual(["DELETE /api/v1/me/credentials/credential-test"]);
});

async function mockImpact(page: Page, failure: Failure) {
  const state = { failure, empty: false, reads: 0, writes: [] as string[] };
  const workspaces = [WORKSPACE_ID, OTHER_WORKSPACE_ID].map((id, index) => ({ id, name: `QA Workspace ${index + 1}`, slug: `qa-${index}`, role: "owner" }));
  const credential = { id: "credential-test", kind: "github_pat", status: "active", created_at: "2026-09-11T00:00:00Z" };
  let deleted = false;
  await page.addInitScript(() => localStorage.setItem("parsar.lang", "en-US"));
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    const method = route.request().method();
    const fail = () => json(route, { error: "temporarily_unavailable", message: "Synthetic impact read failure" }, 503);
    if (method !== "GET") {
      state.writes.push(`${method} ${path}`);
      if (method === "DELETE" && path === "/api/v1/me/credentials/credential-test") { deleted = true; return json(route, credential); }
      return json(route, { error: "unexpected_write" }, 400);
    }
    if (path === "/api/v1/me") return json(route, { user_id: "user-1", name: "QA", email: "qa@example.com" });
    if (path === "/api/v1/me/workspaces") {
      state.reads++;
      return state.failure === "workspace directory" ? fail() : json(route, { workspaces });
    }
    if (path === "/api/v1/me/discoverable-workspaces") return json(route, { workspaces: [], total: 0 });
    if (path === "/api/v1/me/credentials") return json(route, { credentials: deleted ? [] : [credential] });
    const workspace = workspaces.find((w) => path === `/api/v1/workspaces/${w.id}/capabilities`);
    if (workspace) {
      state.reads++;
      if (state.failure === "all capabilities" || (state.failure === "one workspace" && workspace.id === OTHER_WORKSPACE_ID)) return fail();
      return json(route, { capabilities: state.empty ? [] : [{ id: `capability-${workspace.id}`, name: "QA GitHub capability", status: "active", type: "mcp", required_credentials: [{ kind: "github_pat", required: true }] }] });
    }
    if (path.endsWith("/models")) return json(route, { models: [] });
    return json(route, {});
  });
  return state;
}
