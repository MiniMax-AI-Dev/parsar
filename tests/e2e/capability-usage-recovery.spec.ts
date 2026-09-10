import { expect, test, type Page } from "@playwright/test";
import { json, WORKSPACE_ID as workspace } from "./helpers/conversation-app";

const capabilityID = "00000000-0000-0000-0000-000000000033";
type Failure = "all bindings" | "one binding" | "agent directory" | "marketplace";

for (const failure of ["all bindings", "one binding", "agent directory", "marketplace"] as const) {
  test(`capability usage recovers from failed ${failure}`, async ({ page }) => {
    const state = await mockUsage(page, failure);
    await page.goto(`/?ws=${workspace}&admin=capabilities&id=${capabilityID}${failure === "marketplace" ? "&from=marketplace" : ""}`);
    const rail = page.getByRole("complementary", { name: "QA capability loading", exact: true });
    const section = rail.locator("section").filter({ has: page.getByRole("heading", { name: /^Agents($| using)/ }) });
    await expect(section.locator(".animate-pulse")).toHaveCount(1);
    if (failure !== "marketplace") await expect(rail.getByRole("listbox", { name: "Version history" })).toBeVisible();
    state.release();
    await expect(section.getByText("Failed to load", { exact: true })).toBeVisible();
    await expect(section.locator(".animate-pulse")).toHaveCount(0);
    await expect(section.getByRole("heading")).toHaveText("Agents");
    if (failure !== "marketplace") {
      await expect(rail.getByRole("listbox", { name: "Version history" }).getByRole("option")).toHaveText(/—$/);
      const row = page.getByRole("listbox", { name: "Capabilities", exact: true }).getByRole("option");
      await expect(row.locator("dl").getByText("Agents", { exact: true }).locator("..").locator("dd")).toHaveText("—");
    }
    state.failing = false;
    await section.getByRole("button", { name: "Retry", exact: true }).click();
    await expect(section.getByRole("heading")).toHaveText("Agents using this capability (2)");
    await expect(section.getByRole("option")).toHaveCount(2);
    await expect(section).toContainText("QA Agent 1");
    await expect(section).toContainText("QA Agent 2");
    if (failure !== "marketplace") await expect(rail.getByRole("listbox", { name: "Version history" }).getByRole("option")).toHaveText(/2$/);
    expect(state.writes).toEqual([]);
  });
}

for (const marketplace of [false, true]) {
  test(`successful empty usage remains empty (${marketplace ? "marketplace" : "workspace"})`, async ({ page }) => {
    const state = await mockUsage(page, marketplace ? "marketplace" : "all bindings");
    state.failing = false;
    state.empty = true;
    state.release();
    await page.goto(`/?ws=${workspace}&admin=capabilities&id=${capabilityID}${marketplace ? "&from=marketplace" : ""}`);
    const rail = page.getByRole("complementary", { name: "QA capability loading", exact: true });
    const section = rail.locator("section").filter({ has: page.getByRole("heading", { name: /^Agents using/ }) });
    await expect(section.getByRole("heading").first()).toHaveText("Agents using this capability (0)");
    await expect(section.locator(".animate-pulse")).toHaveCount(0);
    await expect(section.getByRole("button", { name: "Retry", exact: true })).toHaveCount(0);
    await expect(section.getByRole("listbox")).toHaveCount(0);
    expect(state.writes).toEqual([]);
  });
}

async function mockUsage(page: Page, failure: Failure) {
  let release!: () => void;
  const wait = new Promise<void>((resolve) => { release = resolve; });
  const state = { failing: true, empty: false, writes: [] as string[], release };
  const base = `/api/v1/workspaces/${workspace}`;
  const capability = { id: capabilityID, workspace_id: workspace, name: "QA capability loading", type: "skill", status: "active", visibility: "workspace", latest_version_id: "version-1", latest_version: "1.0.0" };
  const agents = [1, 2].map((id) => ({ id: `agent-${id}`, workspace_id: workspace, name: `QA Agent ${id}`, status: "active", connector_type: "agent_daemon", config: {} }));
  await page.addInitScript(() => localStorage.setItem("parsar.lang", "en-US"));
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (route.request().method() !== "GET") state.writes.push(path);
    const fail = () => json(route, { error: "temporarily_unavailable", message: "Synthetic read failure" }, 503);
    if (path === "/api/v1/me") return json(route, { user_id: "user-1", email: "qa@example.com", name: "QA" });
    if (path === "/api/v1/me/workspaces") return json(route, { user_id: "user-1", workspaces: [{ id: workspace, name: "QA Lab", slug: "qa-lab", role: "owner" }] });
    if (path === "/api/v1/me/discoverable-workspaces") return json(route, { workspaces: [], total: 0 });
    if (path === `${base}/agents`) {
      if (failure === "agent directory") { await wait; if (state.failing) return fail(); }
      return json(route, { agents });
    }
    const agent = agents.find((a) => path === `${base}/agents/${a.id}/capabilities`);
    if (agent) {
      if (failure === "all bindings" || (failure === "one binding" && agent.id === "agent-2")) { await wait; if (state.failing) return fail(); }
      return json(route, { installed: state.empty ? [] : [{ id: `binding-${agent.id}`, capability_id: capabilityID, capability_version_id: "version-1", enabled: true, pinning_mode: "pinned", capability }], available: [] });
    }
    if (path === `${base}/capabilities`) return json(route, { capabilities: failure === "marketplace" ? [] : [capability], marketplace_installs: [], total: 1 });
    if (path === `${base}/capabilities/${capabilityID}`) return json(route, capability);
    if (path.endsWith("/versions")) return json(route, { versions: [{ id: "version-1", capability_id: capabilityID, version: "1.0.0", spec: {}, created_at: "2026-09-11T00:00:00Z" }] });
    if (path.endsWith("/marketplace-installs")) return json(route, { installs: failure === "marketplace" ? [{ ...capability, from_marketplace: true, source_workspace_name: "QA source", pinned_version: "1.0.0", enabled_agent_count: 0 }] : [] });
    if (path.endsWith("/enabled-agents")) { await wait; return state.failing ? fail() : json(route, { agents: state.empty ? [] : agents.map((a) => ({ agent_id: a.id, name: a.name, version: "1.0.0" })) }); }
    if (path.includes("marketplace")) return json(route, { capabilities: [] });
    return json(route, {});
  });
  return state;
}
