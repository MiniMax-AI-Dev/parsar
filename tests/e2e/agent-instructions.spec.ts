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

for (const changePrompt of [false, true]) {
  test(`property-only edit preserves capability intent: ${changePrompt}`, async ({ page }) => {
    const writes = await mockAgents(page, true);
    await page.goto(`/?ws=${workspace}&admin=agents&id=${agentID}&tab=config`);
    await page.getByRole("button", { name: "Edit", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "Edit Agent" });
    if (changePrompt) await dialog.getByRole("textbox", { name: "Instructions", exact: true }).fill("Updated instructions");
    await dialog.getByRole("button", { name: "Next", exact: true }).click();
    await dialog.getByRole("button", { name: "Save", exact: true }).click();
    await expect(dialog).not.toBeVisible();
    expect(writes.updates).toHaveLength(1);
    expect(writes.updates[0]).not.toHaveProperty("capabilities");
    expect(writes.capabilityWrites).toHaveLength(0);
  });
}

test("explicit capability selection retains the existing replacement request", async ({ page }) => {
  const writes = await mockAgents(page, true);
  await page.goto(`/?ws=${workspace}&admin=agents&id=${agentID}&tab=config`);
  await page.getByRole("button", { name: "Edit", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Edit Agent" });
  await dialog.getByRole("button", { name: "Next", exact: true }).click();
  await dialog.getByRole("checkbox", { name: /Policy/ }).check();
  await dialog.getByRole("button", { name: "Save", exact: true }).click();
  await expect(dialog).not.toBeVisible();
  expect(writes.updates[0].capabilities).toEqual(["Policy"]);
});

for (const finish of ["create", "cancel"]) {
  test(`model prerequisite preserves the live form until ${finish}`, async ({ page }) => {
    const catalog = { available: false, failed: false, adapter: "openai" };
    const writes = await mockAgents(page, false, catalog);
    const originalURL = `/?ws=${workspace}&admin=agents`;
    await page.goto(originalURL);
    await page.getByRole("button", { name: "New Agent", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "New Agent" });
    await dialog.getByRole("textbox", { name: "Name", exact: true }).fill("Preserved model detour");
    const prompt = "Keep the first line.\n\nKeep the final line.";
    await dialog.getByRole("textbox", { name: "Instructions", exact: true }).fill(prompt);
    await dialog.getByRole("radio", { name: /Cloud isolation/ }).check();
    for (let detour = 0; detour < 2; detour++) {
      const popupPromise = page.waitForEvent("popup");
      await dialog.getByRole("link", { name: "Configure Model (new tab)" }).click();
      const popup = await popupPromise;
      await expect(popup).toHaveURL(new RegExp(`\\?admin=models&ws=${workspace}$`));
      expect(new URL(popup.url()).searchParams.size).toBe(2);
      await popup.close();
      await expect(page).toHaveURL(new RegExp(`\\?ws=${workspace}&admin=agents$`));
      await expect(dialog.getByRole("textbox", { name: "Instructions", exact: true })).toHaveValue(prompt);
      await expect(dialog.getByRole("radio", { name: /Cloud isolation/ })).toBeChecked();
    }
    if (finish === "create") {
      catalog.failed = true;
      await dialog.getByRole("button", { name: "Refresh models", exact: true }).click();
      await expect(dialog.getByRole("alert")).toContainText("Models could not be refreshed");
      await expect(dialog.getByRole("textbox", { name: "Name", exact: true })).toHaveValue("Preserved model detour");
      catalog.failed = false;
      catalog.available = true;
      await dialog.getByRole("button", { name: "Refresh models", exact: true }).click();
      await dialog.getByPlaceholder("Search models in this Workspace").click();
      await expect(dialog.getByRole("option", { name: /QA Model/ })).toBeDisabled();
      await expect(dialog.getByRole("link", { name: "Configure Model (new tab)" })).toBeVisible();
      catalog.adapter = "anthropic";
      await dialog.getByRole("button", { name: "Refresh models", exact: true }).click();
      await expect(dialog.getByRole("button", { name: "Refresh models", exact: true })).not.toBeVisible();
      await dialog.getByPlaceholder("Search models in this Workspace").click();
      await dialog.getByRole("option", { name: /QA Model/ }).click();
      await dialog.getByRole("button", { name: "Next", exact: true }).click();
      await dialog.getByRole("button", { name: "Create Agent", exact: true }).click();
      await expect.poll(() => writes.creates.length).toBe(1);
      expect(writes.creates[0]).toMatchObject({ name: "Preserved model detour", system_prompt: prompt, default_model_id: "model-1" });
      await expect(dialog).not.toBeVisible();
      await page.goto(originalURL);
    } else {
      await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
      expect(writes.creates).toHaveLength(0);
    }
    await page.getByRole("button", { name: "New Agent", exact: true }).click();
    await expect(dialog.getByRole("textbox", { name: "Name", exact: true })).not.toHaveValue("Preserved model detour");
    await expect(dialog.getByRole("textbox", { name: "Instructions", exact: true })).not.toHaveValue(prompt);
  });
}

async function mockAgents(page: Page, withBindings = false, catalog = { available: true, failed: false, adapter: "anthropic" }) {
  const writes = { updates: [] as Record<string, unknown>[], creates: [] as Record<string, unknown>[], capabilityWrites: [] as string[] };
  const installed = withBindings ? ["Policy", "Helpdesk"].map((name, index) => ({
    capability_id: `cap-${index}`, capability_version_id: `version-${index}`, name,
    type: index === 0 ? "skill" : "mcp", version: "1.0.0", pinning_mode: "pinned", configuration: { retained: true },
  })) : [];
  const base = `/api/v1/workspaces/${workspace}`;
  const agentURL = `${base}/agents/${agentID}`;
  const agent = {
    id: agentID, workspace_id: workspace, name: "Instruction test", slug: "instruction-test", status: "active",
    connector_type: "agent_daemon", visibility: "workspace", capabilities: [],
    config: { agent_kind: "opencode", daemon_mode: "sandbox", system_prompt: "Existing instructions" } as Record<string, unknown>,
  };
  await page.addInitScript(() => localStorage.setItem("i18nextLng", "en-US"));
  await page.context().route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    const method = route.request().method();
    if (method !== "GET" && path.includes("/capabilities")) writes.capabilityWrites.push(path);
    if (path === "/api/v1/me") return json(route, { user_id: "user-1", name: "User", email: "user@example.test" });
    if (path === "/api/v1/me/workspaces") return json(route, { workspaces: [{ id: workspace, name: "Instruction test", role: "owner" }] });
    if (path === `${base}/runtime/status`) return json(route, { available: true, profile: "managed" });
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
    if (path === `${agentURL}/capabilities`) return json(route, { available: [], installed });
    if (path === `${base}/capabilities`) return json(route, { capabilities: installed.map((binding) => ({
      ...binding, id: binding.capability_id, workspace_id: workspace, latest_version_id: binding.capability_version_id,
    })) });
    if (path.endsWith("/models") && catalog.failed) return json(route, { error: "server_unreachable", message: "Model service unavailable" }, 503);
    if (path.endsWith("/models")) return json(route, { models: catalog.available ? [{
      id: "model-1", name: "QA Model", model_key: "qa-model", status: "active", provider_type: catalog.adapter, adapter: catalog.adapter, credential_mode: "inline_secret", config: {},
    }] : [] });
    return json(route, {});
  });
  return writes;
}
