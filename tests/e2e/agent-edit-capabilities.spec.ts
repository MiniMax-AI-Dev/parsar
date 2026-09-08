import { expect, test, type Page } from "@playwright/test";
import { json, WORKSPACE_ID as workspace, AGENT_ID as agentID } from "./helpers/conversation-app";

const base = `/api/v1/workspaces/${workspace}`;
const agentURL = `${base}/agents/${agentID}`;
const capability = {
  id: "cap-1", name: "qa-existing-skill", type: "skill", workspace_id: workspace,
  status: "active", latest_version_id: "version-1", latest_version: "1.0.0", required_credentials: [],
};

for (const catalogUnavailable of [false, true]) {
  test(`profile editing preserves capability ownership${catalogUnavailable ? " with an unavailable catalog" : ""}`, async ({ page }) => {
    const state = await mockEditor(page, catalogUnavailable);
    await page.goto(`/?ws=${workspace}&admin=agents&id=${agentID}&tab=config`);
    await page.getByRole("button", { name: "Edit", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "Edit Agent" });
    await expect(dialog).toContainText("Manage skills, MCP tools, and their versions");
    await expect(dialog.getByRole("button", { name: "Next", exact: true })).toHaveCount(0);
    await dialog.getByPlaceholder("One-line description of what this Agent is good at").fill("Updated purpose");
    await dialog.getByRole("button", { name: "Save", exact: true }).click();
    await expect(dialog).not.toBeVisible();
    expect(state.updates).toHaveLength(1);
    expect(state.updates[0].description).toBe("Updated purpose");
    expect(state.updates[0]).not.toHaveProperty("capabilities");
    expect(state.updates[0].config).not.toHaveProperty("credential_bindings");
    expect(state.bindingWrites).toEqual([]);
    expect(state.profiles).toHaveLength(1);
    expect(state.profiles[0].config).not.toHaveProperty("profile.capabilities");
  });
}

test("creation still offers initial capabilities and submits selected versions", async ({ page }) => {
  const state = await mockEditor(page, false);
  await page.goto(`/?ws=${workspace}&admin=agents`);
  await page.getByRole("button", { name: "New Agent", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "New Agent" });
  await dialog.getByPlaceholder("e.g. Data analyst assistant").fill("Created agent");
  await dialog.getByRole("radio", { name: /Cloud isolation/ }).check();
  await dialog.getByPlaceholder("Search models in this Workspace").click();
  await dialog.getByRole("option", { name: /QA Model/ }).click();
  await dialog.getByRole("button", { name: "Next", exact: true }).click();
  await dialog.getByRole("checkbox", { name: /qa-existing-skill/ }).check();
  await dialog.getByRole("button", { name: "Create Agent", exact: true }).click();
  await expect.poll(() => state.creates.length).toBe(1);
  expect(state.creates[0].capabilities).toEqual([capability.name]);
  expect(state.creates[0].initial_capabilities).toEqual([{ capability_version_id: "version-1", pinning_mode: "latest" }]);
});

async function mockEditor(page: Page, unavailable: boolean) {
  const state = { updates: [] as Record<string, unknown>[], profiles: [] as Record<string, unknown>[], creates: [] as Record<string, unknown>[], bindingWrites: [] as string[] };
  const agent = {
    id: agentID, workspace_id: workspace, name: "Edit preservation", status: "active",
    default_model_id: "model-1", slug: "edit-preservation", description: "", visibility: "workspace", capabilities: ["stale-name"],
    created_at: "2026-09-09T00:00:00Z", updated_at: "2026-09-09T00:00:00Z",
    connector_type: "agent_daemon", config: {
      agent_kind: "opencode", daemon_mode: "sandbox",
      credential_bindings: { ticket_api: { source: "shared", secret_id: "existing-secret" } },
    },
  };
  await page.addInitScript(() => localStorage.setItem("parsar.lang", "en-US"));
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    const method = route.request().method();
    if (path === "/api/v1/me") return json(route, { user_id: "user-1", name: "User", email: "user@example.test" });
    if (path === "/api/v1/me/workspaces") return json(route, { workspaces: [{ id: workspace, name: "Edit test", slug: "edit-test", role: "owner" }] });
    if (path === "/api/v1/me/discoverable-workspaces") return json(route, { workspaces: [], total: 0 });
    if (path === `${base}/runtime/status`) return json(route, { available: true, profile: "managed" });
    if (path === `${base}/agents`) {
      if (method === "POST") { state.creates.push(route.request().postDataJSON()); return json(route, { agent }, 201); }
      return json(route, { agents: [agent] });
    }
    if (path === agentURL || path === `/api/v1/agents/${agentID}`) {
      if (method !== "GET") state.updates.push(route.request().postDataJSON());
      return json(route, agent);
    }
    if (path === `/api/v1/agents/${agentID}/profile`) {
      state.profiles.push(route.request().postDataJSON()); return json(route, {});
    }
    if (path === `${agentURL}/capabilities`) return json(route, { available: [capability], installed: [{
      id: "binding-1", agent_id: agentID, capability_id: capability.id, capability_version_id: "older-version",
      enabled: false, pinning_mode: "pinned", configuration: { marker: "preserve" },
    }] });
    if (path.startsWith(`${agentURL}/capabilities/`) && method !== "GET") state.bindingWrites.push(path);
    if (path === `${base}/capabilities`) return unavailable
      ? json(route, { error: "catalog unavailable" }, 503)
      : json(route, { capabilities: [capability], marketplace_installs: [], marketplace_available: [] });
    if (path.endsWith("/models")) return json(route, { models: [{
      id: "model-1", name: "QA Model", model_key: "qa-model", status: "active", provider_type: "anthropic", adapter: "anthropic", credential_mode: "inline_secret", config: {},
    }] });
    if (path.endsWith("/runtimes")) return json(route, { runtimes: [{ id: "device-1", name: "QA runtime", status: "online" }] });
    if (path.endsWith("/versions")) return json(route, { versions: [{ id: "version-1", version: "1.0.0" }] });
    return json(route, {});
  });
  return state;
}
