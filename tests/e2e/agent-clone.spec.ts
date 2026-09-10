import { expect, test, type Page } from "@playwright/test";
import { json, WORKSPACE_ID as workspace, AGENT_ID as agentID } from "./helpers/conversation-app";

test("clone retains enabled bindings, independent credentials, and pinned versions", async ({ page }) => {
  const state = await mockClone(page);
  const dialog = await openClone(page);
  await expect(dialog.getByRole("checkbox", { name: /Policy/ })).toBeChecked();
  await expect(dialog.getByRole("checkbox", { name: /Desk/ })).toBeChecked();
  await dialog.getByRole("button", { name: "Create Agent", exact: true }).click();
  await expect.poll(() => state.creates.length).toBe(1);
  const body = state.creates[0];
  expect(body.visibility).toBe("workspace");
  expect(body.initial_capabilities).toEqual([
    { capability_version_id: "policy-v1", pinning_mode: "pinned", configuration: { retained: "policy", credential_bindings: { mcp_oauth: { source: "shared", secret_id: "notion-key" } } } },
    { capability_version_id: "desk-v1", pinning_mode: "latest", configuration: { retained: "desk", credential_bindings: { mcp_oauth: { source: "shared", secret_id: "linear-key" } } } },
  ]);
  expect(body.config).not.toHaveProperty("credential_bindings");
  expect(state.otherWrites).toEqual([]);
});

for (const foreign of [false, true]) {
test(`latest display, credentials, and submission advance together (${foreign ? "marketplace" : "workspace"})`, async ({ page }) => {
  await page.clock.install();
  const state = await mockClone(page, { foreign });
  const dialog = await openClone(page);
  await expect(dialog.getByRole("combobox", { name: "Desk · Version" })).toContainText("v1.0.0");
  state.latest = 2;
  await page.clock.fastForward(31_000);
  await page.evaluate(() => { window.dispatchEvent(new Event("offline")); window.dispatchEvent(new Event("online")); });
  await expect(dialog.getByRole("combobox", { name: "Desk · Version" })).toContainText("v2.0.0");
  await expect(dialog.getByRole("button", { name: "Create Agent", exact: true })).toBeDisabled();
  const credential = dialog.getByRole("combobox", { name: /Desk · GitHub/ });
  await credential.click();
  await page.getByRole("option", { name: /GitHub key/ }).click();
  await dialog.getByRole("button", { name: "Create Agent", exact: true }).click();
  await expect.poll(() => state.creates.length).toBe(1);
  expect(state.creates[0].initial_capabilities[1]).toEqual({ capability_version_id: "desk-v2", pinning_mode: "latest", configuration: {
    retained: "desk", credential_bindings: { github_pat: { source: "shared", secret_id: "github-key" } },
  } });
  if (foreign) expect(state.versionReads).toEqual([]);
});
}

test("marketplace clones use installed metadata without private history reads", async ({ page }) => {
  const state = await mockClone(page, { foreign: true });
  const dialog = await openClone(page);
  await dialog.getByRole("button", { name: "Create Agent", exact: true }).click();
  await expect.poll(() => state.creates.length).toBe(1);
  expect(state.versionReads).toEqual([]);
  expect(state.creates[0].initial_capabilities.map((item: any) => item.capability_version_id)).toEqual(["policy-v1", "desk-v1"]);
});

test("source failure cannot create an empty clone and retry retains the draft", async ({ page }) => {
  const state = await mockClone(page, { failure: true });
  const dialog = await openClone(page);
  await expect(dialog.getByRole("button", { name: "Create Agent", exact: true })).toBeDisabled();
  await expect(dialog.getByRole("button", { name: "Retry", exact: true })).toBeVisible();
  state.failure = false;
  await dialog.getByRole("button", { name: "Retry", exact: true }).click();
  await expect(dialog.getByRole("checkbox", { name: /Policy/ })).toBeChecked();
  await dialog.getByRole("checkbox", { name: /Desk/ }).uncheck();
  await dialog.getByRole("button", { name: "Create Agent", exact: true }).click();
  await expect.poll(() => state.creates.length).toBe(1);
  expect(state.creates[0].name).toBe("Clone draft");
  expect(state.creates[0].initial_capabilities).toHaveLength(1);
});

test("closing a clone does not seed ordinary creation", async ({ page }) => {
  const state = await mockClone(page);
  const dialog = await openClone(page);
  await expect(dialog.getByRole("checkbox", { name: /Policy/ })).toBeChecked();
  await dialog.getByRole("button", { name: "Close", exact: true }).click();
  await page.getByRole("button", { name: "New Agent", exact: true }).click();
  await dialog.getByRole("radio", { name: /Cloud isolation/ }).check();
  await dialog.getByPlaceholder("Search models in this Workspace").click();
  await dialog.getByRole("option", { name: /QA Model/ }).click();
  await dialog.getByRole("button", { name: "Next", exact: true }).click();
  await expect(dialog.getByRole("checkbox", { name: /Policy/ })).not.toBeChecked();
  await dialog.getByRole("button", { name: "Create Agent", exact: true }).click();
  await expect.poll(() => state.creates.length).toBe(1);
  expect(state.creates[0].initial_capabilities).toEqual([]);
});

for (const source of ["personal", "shared"]) {
  test(`clone model credentials: ${source}`, async ({ page }) => {
    const state = await mockClone(page, { modelSource: source });
    await page.goto(`/?ws=${workspace}&admin=agents`);
    await page.getByRole("button", { name: "More actions", exact: true }).click();
    await page.getByRole("menuitem", { name: "Clone", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "New Agent", exact: true });
    if (source === "personal") {
      await expect(dialog.getByRole("button", { name: "Next", exact: true })).toBeDisabled();
      await dialog.getByRole("radio", { name: /Each caller/ }).check();
    }
    await dialog.getByRole("button", { name: "Next", exact: true }).click();
    await dialog.getByRole("button", { name: "Create Agent", exact: true }).click();
    await expect.poll(() => state.creates.length).toBe(1);
    if (source === "shared") expect(state.creates[0].config.model_credential_binding).toEqual({ source: "shared", secret_id: "model-key" });
    else expect(state.creates[0].config).not.toHaveProperty("model_credential_binding");
  });
}

async function openClone(page: Page) {
  await page.goto(`/?ws=${workspace}&admin=agents`);
  await page.getByRole("button", { name: "More actions", exact: true }).click();
  await page.getByRole("menuitem", { name: "Clone", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "New Agent", exact: true });
  await dialog.getByRole("textbox", { name: "Name", exact: true }).fill("Clone draft");
  await dialog.getByRole("button", { name: "Next", exact: true }).click();
  return dialog;
}

async function mockClone(page: Page, options: { foreign?: boolean; failure?: boolean; modelSource?: string } = {}) {
  const state = { latest: 1, failure: options.failure ?? false, creates: [] as any[], otherWrites: [] as string[], versionReads: [] as string[] };
  const base = `/api/v1/workspaces/${workspace}`;
  const agent = { id: agentID, workspace_id: workspace, name: "Clone source", status: "active", connector_type: "agent_daemon", visibility: "workspace",
    config: { agent_kind: "opencode", daemon_mode: "sandbox", model_id: "model-1", credential_bindings: { removed_personal: { source: "personal" } },
      ...(options.modelSource ? { model_credential_binding: options.modelSource === "shared" ? { source: "shared", secret_id: "model-key" } : { source: "personal" } } : {}),
    } };
  const required = (kind: string) => [{ kind, required: true }];
  const caps = () => ["policy", "desk"].map((id) => ({ id, workspace_id: options.foreign ? "foreign" : workspace, name: id === "policy" ? "Policy" : "Desk", type: "mcp", status: "active",
    visibility: "public", from_marketplace: options.foreign ?? false, latest_version_id: `${id}-v${id === "policy" ? 2 : state.latest}`, latest_version: `${id === "policy" ? 2 : state.latest}.0.0`,
    required_credentials: required(id === "desk" && state.latest === 2 ? "github_pat" : "mcp_oauth"),
  }));
  const installed = () => caps().map((cap) => ({ id: `binding-${cap.id}`, capability_id: cap.id, capability_version_id: `${cap.id}-v1`, enabled: true,
    pinning_mode: cap.id === "policy" ? "pinned" : "latest", version: "1.0.0",
    capability: { ...cap, pinned_version: "1.0.0", required_credentials: required("mcp_oauth") },
    configuration: { retained: cap.id, credential_bindings: { mcp_oauth: { source: "shared", secret_id: cap.id === "policy" ? "notion-key" : "linear-key" } } },
  }));
  await page.addInitScript(() => { localStorage.setItem("parsar.lang", "en-US"); localStorage.setItem("parsar.theme", "light"); });
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    const method = route.request().method();
    if (method !== "GET" && path !== `${base}/agents`) state.otherWrites.push(`${method} ${path}`);
    if (path === "/api/v1/me") return json(route, { user_id: "user-1", name: "Owner", email: "owner@example.test" });
    if (path === "/api/v1/me/workspaces") return json(route, { workspaces: [{ id: workspace, name: "Clone test", role: "owner" }] });
    if (path === `${base}/runtime/status`) return json(route, { available: true, profile: "managed" });
    if (path === `${base}/agents`) {
      if (method === "POST") { state.creates.push(route.request().postDataJSON()); return json(route, { agent }, 201); }
      return json(route, { agents: [agent] });
    }
    if (path === `${base}/agents/${agentID}` || path === `/api/v1/agents/${agentID}`) return json(route, agent);
    if (path === `${base}/agents/${agentID}/capabilities`) return state.failure
      ? json(route, { error: "forbidden", message: "Synthetic source failure" }, 403)
      : json(route, { installed: [...installed(), { id: "builtin", capability_id: "builtin", built_in: true, enabled: true }, { id: "disabled", capability_id: "disabled", enabled: false }], available: [] });
    if (path === `${base}/capabilities`) return json(route, options.foreign ? { capabilities: [], marketplace_installs: caps().map((cap) => ({
      capability_id: cap.id, source_workspace_id: "foreign", source_workspace_name: "Publisher", name: cap.name, type: cap.type, visibility: "public",
      pinned_version_id: `${cap.id}-v1`, pinned_version: "1.0.0", latest_version_id: cap.latest_version_id, latest_published_version: cap.latest_version,
      required_credentials: required("mcp_oauth"),
    })) } : { capabilities: caps() });
    if (path === "/api/v1/capabilities/marketplace") return json(route, { capabilities: caps().map(({ id, ...cap }) => ({ ...cap, capability_id: id })) });
    const match = path.match(/\/capabilities\/(policy|desk)\/versions$/);
    if (match) {
      state.versionReads.push(match[1]);
      if (options.foreign) return json(route, { error: "not_found" }, 404);
      const id = match[1];
      return json(route, { versions: [1, 2].map((n) => ({ id: `${id}-v${n}`, capability_id: id, version: `${n}.0.0`,
        required_credentials: required(id === "desk" && n === 2 ? "github_pat" : "mcp_oauth"), source_payload: { catalog_id: id === "policy" ? "notion" : "linear" },
      })) });
    }
    if (path === `${base}/secrets`) return json(route, { secrets: ["notion", "linear", "github", "model"].map((name) => ({ id: `${name}-key`, name: name === "github" ? "GitHub key" : `${name} key`, kind: "capability_inline", status: "active", auth_type: "oauth2", provider: name,
      metadata: { credential_kind_code: name === "github" ? "github_pat" : name === "model" ? "model_api_key" : "mcp_oauth" },
    })) });
    if (path.endsWith("/models")) return json(route, { models: [{ id: "model-1", name: "QA Model", model_key: "qa-model", status: "active", provider_type: "anthropic", adapter: "anthropic", credential_mode: options.modelSource ? "credential_ref" : "inline_secret", credential_kind_code: "model_api_key", config: {} }] });
    return json(route, {});
  });
  return state;
}
