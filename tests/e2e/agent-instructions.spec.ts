import { expect, test, type Page } from "@playwright/test";
import { json, WORKSPACE_ID as workspace, AGENT_ID as agentID } from "./helpers/conversation-app";

for (const instructions of ["Existing instructions", "Updated instructions\nAsk when context is missing.", ""]) {
  test(`edit instructions: ${instructions || "clear"}`, async ({ page }) => {
    const writes = await mockAgents(page);
    await page.goto(`/?ws=${workspace}&admin=agents&id=${agentID}&tab=config`);
    await page.getByRole("button", { name: "Edit", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "Edit Agent" });
    const field = dialog.getByRole("textbox", { name: "Instructions", exact: true });
    await expect(field).toHaveValue("Existing instructions");
    if (instructions !== "Existing instructions") await field.fill(instructions);
    await dialog.getByRole("button", { name: "Next", exact: true }).click();
    await dialog.getByRole("button", { name: "Save", exact: true }).click();
    await expect(dialog).not.toBeVisible();
    expect(writes.updates).toHaveLength(1);
    expect(writes.updates[0].system_prompt).toBe(instructions);
  });
}

for (const instructions of ["Answer from the approved policy.", ""]) {
  test(`creation saves instructions: ${instructions || "empty"}`, async ({ page }) => {
    const writes = await mockAgents(page);
    await page.goto(`/?ws=${workspace}&admin=agents`);
    await page.getByRole("button", { name: "New Agent", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "New Agent" });
    await dialog.getByRole("textbox", { name: "Instructions", exact: true }).fill(instructions);
    await dialog.getByRole("radio", { name: /Cloud isolation/ }).check();
    await dialog.getByPlaceholder("Search models in this Workspace").click();
    await dialog.getByRole("option", { name: /QA Model/ }).click();
    await dialog.getByRole("button", { name: "Next", exact: true }).click();
    await dialog.getByRole("button", { name: "Create Agent", exact: true }).click();
    await expect.poll(() => writes.creates.length).toBe(1);
    expect(writes.creates[0].system_prompt).toBe(instructions);
    await expect(dialog).not.toBeVisible();
    await page.goto(`/?ws=${workspace}&admin=agents&id=${agentID}&tab=config`);
    await page.getByRole("button", { name: "Edit", exact: true }).click();
    const edit = page.getByRole("dialog", { name: "Edit Agent" });
    await expect(edit.getByRole("textbox", { name: "Instructions", exact: true })).toHaveValue(instructions);
    await edit.getByRole("button", { name: "Next", exact: true }).click();
    await edit.getByRole("button", { name: "Save", exact: true }).click();
    await expect(edit).not.toBeVisible();
    expect(writes.updates[0].system_prompt).toBe(instructions);
  });
}

async function mockAgents(page: Page) {
  const writes = { updates: [] as Record<string, unknown>[], creates: [] as Record<string, unknown>[] };
  const base = `/api/v1/workspaces/${workspace}`;
  const agentURL = `${base}/agents/${agentID}`;
  const agent = {
    id: agentID, workspace_id: workspace, name: "Instruction test", slug: "instruction-test", status: "active",
    connector_type: "agent_daemon", visibility: "workspace", capabilities: [],
    config: { agent_kind: "opencode", daemon_mode: "sandbox", system_prompt: "Existing instructions" } as Record<string, unknown>,
  };
  await page.addInitScript(() => localStorage.setItem("i18nextLng", "en-US"));
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    const method = route.request().method();
    if (path === "/api/v1/me") return json(route, { user_id: "user-1", name: "User", email: "user@example.test" });
    if (path === "/api/v1/me/workspaces") return json(route, { workspaces: [{ id: workspace, name: "Instruction test", role: "owner" }] });
    if (path === `${base}/agents`) {
      if (method === "POST") {
        const input = route.request().postDataJSON();
        writes.creates.push(input);
        // Agent creation omits an empty prompt in the persisted config.
        if (input.system_prompt) agent.config.system_prompt = input.system_prompt;
        else delete agent.config.system_prompt;
        return json(route, { agent }, 201);
      }
      return json(route, { agents: [agent] });
    }
    if (path === agentURL || path === `/api/v1/agents/${agentID}`) {
      if (method !== "GET") writes.updates.push(route.request().postDataJSON());
      return json(route, agent);
    }
    if (path === `${agentURL}/capabilities`) return json(route, { available: [], installed: [] });
    if (path === `${base}/capabilities`) return json(route, { capabilities: [] });
    if (path.endsWith("/models")) return json(route, { models: [{
      id: "model-1", name: "QA Model", model_key: "qa-model", status: "active", provider_type: "anthropic", adapter: "anthropic", credential_mode: "inline_secret", config: {},
    }] });
    return json(route, {});
  });
  return writes;
}
