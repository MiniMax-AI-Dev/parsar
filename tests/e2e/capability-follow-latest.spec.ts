import { expect, test, type Page } from "@playwright/test";
import { json, WORKSPACE_ID as ws, AGENT_ID as aid } from "./helpers/conversation-app";
const base = `/api/v1/workspaces/${ws}`;
const configuration = { retained: { note: "Keep this configuration" } };

test("one-version Skill can follow future releases and return to a fixed version", async ({ page }) => {
  const state = await fixture(page);
  let dialog = await open(page);
  await dialog.getByRole("radio", { name: "Follow the latest version", exact: true }).check();
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
  expect(state.writes).toEqual([]);
  dialog = await open(page);
  await expect(dialog.getByRole("radio", { name: /^v1/ })).toBeChecked();
  await dialog.getByRole("radio", { name: "Follow the latest version", exact: true }).check();
  await dialog.getByRole("button", { name: "Follow the latest version", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  expect(state.writes).toEqual([{ version: "v1", body: { pinning_mode: "latest", configuration } }]);
  state.publish();
  dialog = await open(page);
  await expect(dialog.getByRole("radio", { name: "Follow the latest version", exact: true })).toBeChecked();
  await expect(dialog.getByRole("button", { name: "Follow the latest version", exact: true })).toBeDisabled();
  await dialog.getByRole("radio", { name: /^v2/ }).check();
  await dialog.getByRole("button", { name: "Switch to 2.0.0", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  expect(state.writes[1]).toEqual({ version: "v2", body: { configuration } });
});

for (const kind of ["plugin", "bundle", "knowledge"]) {
  test(`${kind} exposes the existing automatic-follow policy`, async ({ page }) => {
    const state = await fixture(page, { kind });
    const dialog = await open(page);
    await dialog.getByRole("radio", { name: "Follow the latest version", exact: true }).check();
    await dialog.getByRole("button", { name: "Follow the latest version", exact: true }).click();
    await expect(dialog).toHaveCount(0);
    expect(state.writes[0]).toEqual({ version: "v1", body: { pinning_mode: "latest", configuration } });
  });
}

for (const kind of ["mcp", "system_prompt"]) {
  test(`${kind} keeps its fixed-version choices`, async ({ page }) => {
    const state = await fixture(page, { kind, twoVersions: true });
    const dialog = await open(page);
    await expect(dialog.getByRole("radio", { name: "Follow the latest version", exact: true })).toHaveCount(0);
    await dialog.getByRole("radio", { name: /^v2/ }).check();
    await dialog.getByRole("button", { name: "Switch to 2.0.0", exact: true }).click();
    await expect(dialog).toHaveCount(0);
    expect(state.writes[0]).toEqual({ version: "v2", body: { configuration } });
  });
}

test("marketplace policy uses published and bound metadata without reading private version history", async ({ page }) => {
  const state = await fixture(page, { foreign: true, twoVersions: true });
  const dialog = await open(page);
  await expect(dialog.getByRole("radio", { name: /^v1/ })).toBeChecked();
  await expect(dialog.getByRole("button", { name: "Switch to 1.0.0", exact: true })).toBeDisabled();
  await dialog.getByRole("radio", { name: "Follow the latest version", exact: true }).check();
  await dialog.getByRole("button", { name: "Follow the latest version", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  expect(state.writes[0]).toEqual({ version: "v2", body: { pinning_mode: "latest", configuration } });
  expect(state.versionReads).toBe(0);
});

test("failed policy change retains the choice for retry", async ({ page }) => {
  const state = await fixture(page, { saveFailure: true });
  const dialog = await open(page);
  await dialog.getByRole("radio", { name: "Follow the latest version", exact: true }).check();
  await dialog.getByRole("button", { name: "Follow the latest version", exact: true }).click();
  await expect(dialog.getByText("Synthetic binding failure", { exact: true })).toBeVisible();
  await expect(dialog.getByRole("radio", { name: "Follow the latest version", exact: true })).toBeChecked();
  state.saveFailure = false;
  await dialog.getByRole("button", { name: "Follow the latest version", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  expect(state.writes.every(w => w.version === "v1" && w.body.pinning_mode === "latest")).toBe(true);
});

test("viewer cannot use the policy editing action", async ({ page }) => {
  const state = await fixture(page, { viewer: true });
  await page.goto(`/?ws=${ws}&admin=agents&id=${aid}&tab=config`);
  await expect(page.getByRole("button", { name: "Switch version", exact: true })).toBeDisabled();
  expect(state.writes).toEqual([]);
});

async function open(page: Page) {
  await page.goto(`/?ws=${ws}&admin=agents&id=${aid}&tab=config`);
  await page.getByRole("button", { name: "Switch version", exact: true }).click();
  return page.getByRole("dialog", { name: 'Switch "QA Version Capability" version', exact: true });
}

async function fixture(page: Page, options: { kind?: string; twoVersions?: boolean; foreign?: boolean; saveFailure?: boolean; viewer?: boolean } = {}) {
  const cap = { id: "cap", workspace_id: options.foreign ? "publisher" : ws, type: options.kind ?? "skill", name: "QA Version Capability", status: "active", visibility: "public",
    from_marketplace: options.foreign ?? false, latest_version_id: options.twoVersions ? "v2" : "v1", latest_version: options.twoVersions ? "2.0.0" : "1.0.0", pinned_version: "1.0.0", required_credentials: [] };
  const binding = { id: "binding", capability_id: "cap", capability_version_id: "v1", version: "1.0.0", enabled: true, pinning_mode: "pinned", configuration, capability: cap };
  const agent = { id: aid, name: "Version Agent", workspace_id: ws, status: "active", visibility: "workspace", connector_type: "agent_daemon", config: { agent_kind: "claude_code", daemon_mode: "local" } };
  const state = { saveFailure: options.saveFailure ?? false, versionReads: 0, writes: [] as Array<{ version: string; body: { pinning_mode?: string; configuration: typeof configuration } }>,
    publish: () => { cap.latest_version_id = "v2"; cap.latest_version = "2.0.0"; } };
  await page.addInitScript(() => { localStorage.setItem("parsar.lang", "en-US"); localStorage.setItem("parsar.theme", "light"); });
  await page.route("**/api/v1/**", async route => {
    const path = new URL(route.request().url()).pathname;
    if (route.request().method() !== "GET") {
      const version = path.split("/").at(-2)!;
      const body = route.request().postDataJSON(); state.writes.push({ version, body });
      if (!path.startsWith(`${base}/agents/${aid}/capabilities/`) || !path.endsWith("/enable")) return json(route, { error: "unexpected_write" }, 400);
      if (state.saveFailure) return json(route, { error: "invalid_binding", message: "Synthetic binding failure" }, 422);
      Object.assign(binding, body, { pinning_mode: body.pinning_mode ?? "pinned", capability_version_id: version, version: version === "v2" ? "2.0.0" : "1.0.0" });cap.pinned_version = binding.version;
      return json(route, binding);
    }
    if (path === "/api/v1/me") return json(route, { user_id: "qa", name: "QA", email: "qa@example.test" });
    if (path === "/api/v1/me/workspaces") return json(route, { workspaces: [{ id: ws, name: "Version QA", role: options.viewer ? "viewer" : "owner" }] });
    if (path === `${base}/agents`) return json(route, { agents: [agent] });
    if (path === `${base}/agents/${aid}` || path === `/api/v1/agents/${aid}`) return json(route, agent);
    if (path === `${base}/agents/${aid}/capabilities`) return json(route, { installed: [binding], available: [] });
    if (path === `${base}/capabilities`) return json(route, { capabilities: options.foreign ? [] : [cap] });
    if (path === `${base}/capabilities/cap/versions`) {state.versionReads++; return json(route, { versions: (cap.latest_version_id === "v2" ? [2, 1] : [1]).map(n => ({ id: `v${n}`, capability_id: "cap", version: `${n}.0.0`, required_credentials: [] })) });}
    if (path.endsWith("/models")) return json(route, { models: [] });
    if (path.endsWith("/secrets")) return json(route, { secrets: [] });
    if (path.endsWith("/credentials")) return json(route, { credentials: [] });
    return json(route, {});
  });
  return state;
}
