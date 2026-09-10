import { expect, test, type Page } from "@playwright/test";
import { json, WORKSPACE_ID as workspace, AGENT_ID as agentID } from "./helpers/conversation-app";

const base = `/api/v1/workspaces/${workspace}`;
const configuration = { retained: { path: "/qa/context" }, credential_bindings: {
  github_pat: { source: "shared", secret_id: "key-a" },
  unrelated: { source: "shared", secret_id: "keep-me" },
} };

test("changes only the chosen binding credential and preserves its pinned version", async ({ page }) => {
  const state = await mockCredentials(page);
  const dialog = await openCredentials(page);
  await expect(dialog.getByRole("combobox").first()).toContainText("Key A");
  await expect(dialog.getByRole("button", { name: "Save", exact: true })).toBeDisabled();
  await choose(page, "Key B");
  await dialog.getByRole("button", { name: "Save", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  expect(state.writes).toEqual([{ path: `${base}/agents/${agentID}/capabilities/desk-v1/enable`, method: "POST", body: {
    pinning_mode: "pinned", configuration: { ...configuration, credential_bindings: {
      ...configuration.credential_bindings, github_pat: { source: "shared", secret_id: "key-b" },
    } },
  } }]);
});

test("personal selection overrides Agent-wide shared defaults without changing latest mode", async ({ page }) => {
  const state = await mockCredentials(page, { inherited: true, latest: true });
  const dialog = await openCredentials(page);
  await expect(dialog.getByRole("combobox").first()).toContainText("Key A");
  await dialog.getByRole("combobox").first().click();
  await page.getByRole("option", { name: "Each caller uses their own token", exact: true }).click();
  await dialog.getByRole("button", { name: "Save", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  expect(state.writes).toHaveLength(1);
  expect(state.writes[0].body).toEqual({ pinning_mode: "latest", configuration: {
    retained: configuration.retained, credential_bindings: {
      unrelated: configuration.credential_bindings.unrelated, github_pat: { source: "personal" },
    },
  } });
  expect(state.writes[0].path).toBe(`${base}/agents/${agentID}/capabilities/desk-v1/enable`);
});

test("rotates a shared key while another kind uses callers' personal credentials", async ({ page }) => {
  const state = await mockCredentials(page, { personalRemainder: true });
  const dialog = await openCredentials(page);
  await expect(dialog.getByRole("combobox")).toHaveCount(2);
  await choose(page, "Key B");
  await dialog.getByRole("button", { name: "Save", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  expect(state.writes).toHaveLength(1);
  expect(state.writes[0].body.configuration.credential_bindings).toEqual({
    github_pat: { source: "shared", secret_id: "key-b" }, unrelated: { source: "personal" },
  });
});

test("cancel discards choices and reopening shows the saved credential", async ({ page }) => {
  const state = await mockCredentials(page);
  const dialog = await openCredentials(page);
  await choose(page, "Key B");
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
  await page.getByRole("button", { name: "Change credentials", exact: true }).click();
  await expect(dialog.getByRole("combobox").first()).toContainText("Key A");
  await expect(dialog.getByRole("button", { name: "Save", exact: true })).toBeDisabled();
  expect(state.writes).toEqual([]);
});

test("missing shared credential is explicit and never silently falls back to personal", async ({ page }) => {
  const state = await mockCredentials(page, { missing: true });
  const dialog = await openCredentials(page);
  await expect(dialog.getByRole("combobox").first()).toContainText("Previously selected credential is unavailable");
  await expect(dialog.getByRole("button", { name: "Save", exact: true })).toBeDisabled();
  await choose(page, "Key B");
  await dialog.getByRole("button", { name: "Save", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  expect(state.writes[0].body.configuration.credential_bindings.github_pat.secret_id).toBe("key-b");
});

test("read and write failures recover without losing the chosen credential", async ({ page }) => {
  const state = await mockCredentials(page, { readFailure: true });
  const dialog = await openCredentials(page);
  await expect(dialog.getByText("Unable to load credential options", { exact: true })).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Save", exact: true })).toBeDisabled();
  state.readFailure = false;
  await dialog.getByRole("button", { name: "Retry", exact: true }).click();
  await choose(page, "Key B");
  state.saveFailure = true;
  await dialog.getByRole("button", { name: "Save", exact: true }).click();
  await expect(dialog.getByText("Could not save credential changes", { exact: true })).toBeVisible();
  await expect(dialog.getByRole("combobox").first()).toContainText("Key B");
  const failedAttempts = state.writes.length;
  state.saveFailure = false;
  await dialog.getByRole("button", { name: "Save", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  expect(state.writes).toHaveLength(failedAttempts + 1);
  for (const write of state.writes) expect(write).toEqual(state.writes[0]);
});

test("marketplace binding uses authorized metadata and keeps its stored version", async ({ page }) => {
  const state = await mockCredentials(page, { foreign: true });
  const dialog = await openCredentials(page);
  await choose(page, "Key B");
  await dialog.getByRole("button", { name: "Save", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  expect(state.versionReads).toBe(0);
  expect(state.writes[0].path).toBe(`${base}/agents/${agentID}/capabilities/desk-v1/enable`);
});

test("viewer sees no credential editing action", async ({ page }) => {
  const state = await mockCredentials(page, { viewer: true });
  await page.goto(`/?ws=${workspace}&admin=agents&id=${agentID}&tab=config`);
  await expect(page.getByText("QA Desk", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Change credentials", exact: true })).toHaveCount(0);
  expect(state.writes).toEqual([]);
});

async function openCredentials(page: Page) {
  await page.goto(`/?ws=${workspace}&admin=agents&id=${agentID}&tab=config`);
  await page.getByRole("button", { name: "Change credentials", exact: true }).click();
  return page.getByRole("dialog", { name: "Credentials for QA Desk", exact: true });
}

async function choose(page: Page, name: string) {
  await page.getByRole("dialog").getByRole("combobox").first().click();
  await page.getByRole("option", { name: new RegExp(name) }).click();
}

async function mockCredentials(page: Page, options: { inherited?: boolean; latest?: boolean; missing?: boolean; readFailure?: boolean; foreign?: boolean; viewer?: boolean; personalRemainder?: boolean } = {}) {
  const state = { readFailure: options.readFailure ?? false, saveFailure: false, versionReads: 0,
    writes: [] as Array<{ path: string; method: string; body: { pinning_mode: string; configuration: typeof configuration } }> };
  const required = [{ kind: "github_pat", required: true }, { kind: "unrelated", required: true }, { kind: "optional", required: false }];
  const capability = { id: "desk", workspace_id: options.foreign ? "publisher" : workspace, name: "QA Desk", type: "mcp", status: "active",
    from_marketplace: options.foreign ?? false, latest_version_id: "desk-v2", latest_version: "2.0.0", pinned_version: "1.0.0", required_credentials: required };
  const binding = { id: "binding-desk", capability_id: "desk", capability_version_id: "desk-v1", enabled: true, version: "1.0.0",
    pinning_mode: options.latest ? "latest" : "pinned", capability, configuration: structuredClone(configuration) };
  if (options.personalRemainder) (binding.configuration.credential_bindings as Record<string, unknown>).unrelated = { source: "personal" };
  if (options.inherited) delete (binding.configuration.credential_bindings as Record<string, unknown>).github_pat;
  const agent = { id: agentID, workspace_id: workspace, name: "Credential Agent", status: "active", connector_type: "agent_daemon", visibility: "workspace",
    config: { agent_kind: "claude_code", daemon_mode: "local", credential_bindings: { github_pat: { source: "shared", secret_id: "key-a" } } } };
  await page.addInitScript(() => { localStorage.setItem("parsar.lang", "en-US"); localStorage.setItem("parsar.theme", "light"); });
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    const method = route.request().method();
    if (method !== "GET") {
      const body = route.request().postDataJSON();
      state.writes.push({ path, method, body });
      if (path !== `${base}/agents/${agentID}/capabilities/desk-v1/enable` || method !== "POST") return json(route, { error: "unexpected_write" }, 400);
      if (state.saveFailure) return json(route, { error: "credential_unavailable", message: "Synthetic save failure" }, 422);
      Object.assign(binding, body); return json(route, binding);
    }
    if (path === "/api/v1/me") return json(route, { user_id: "qa-user", name: "QA", email: "qa@example.test" });
    if (path === "/api/v1/me/workspaces") return json(route, { workspaces: [{ id: workspace, name: "QA Credentials", role: options.viewer ? "viewer" : "owner" }] });
    if (path === "/api/v1/me/credentials") return json(route, { credentials: [] });
    if (path === `${base}/agents`) return json(route, { agents: [agent] });
    if (path === `${base}/agents/${agentID}` || path === `/api/v1/agents/${agentID}`) return json(route, agent);
    if (path === `${base}/agents/${agentID}/capabilities`) return json(route, { installed: [binding], available: [] });
    if (path === `${base}/capabilities`) return json(route, { capabilities: options.foreign ? [] : [capability] });
    if (path === `${base}/capabilities/desk/versions`) {
      state.versionReads++;
      if (options.foreign) return json(route, { error: "not_found" }, 404);
      return json(route, { versions: [2, 1].map((n) => ({ id: `desk-v${n}`, capability_id: "desk", version: `${n}.0.0`, required_credentials: required })) });
    }
    if (path === `${base}/secrets`) return state.readFailure ? json(route, { error: "unavailable" }, 503)
      : json(route, { secrets: (options.missing ? ["b"] : ["a", "b"]).map((key) => ({ id: `key-${key}`, name: `Key ${key.toUpperCase()}`, kind: "capability_inline", status: "active", metadata: { credential_kind_code: "github_pat" } })).concat([{ id: "keep-me", name: "Other credential", kind: "capability_inline", status: "active", metadata: { credential_kind_code: "unrelated" } }]) });
    if (path.endsWith("/models")) return json(route, { models: [] });
    return json(route, {});
  });
  return state;
}
