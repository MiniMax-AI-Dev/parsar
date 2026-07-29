import { expect, test, type Page, type Route } from "@playwright/test";

const WORKSPACE_ID = "00000000-0000-0000-0000-000000000011";
const CAPABILITY_ID = "00000000-0000-0000-0000-000000000033";

const directoryItems = [
  connector("context7", "Context7", "Documentation", 1),
  connector("exa", "Exa", "Search", 2),
  connector("firecrawl", "Firecrawl", "Web", 3),
];

test("browses and imports a hosted MCP connector", async ({ page }) => {
  await mockApp(page);
  await page.goto(`/?admin=capabilities&tab=marketplace&ws=${WORKSPACE_ID}`);

  await expect(page.getByRole("heading", { name: "Connectors" })).toBeVisible();
  await expect(page.getByTestId("mcp-directory-card")).toHaveCount(3);

  const search = page.getByPlaceholder("Search capability name / description");
  await search.fill("exa");
  await expect(page.getByTestId("mcp-directory-card")).toHaveCount(1);
  await search.clear();

  await page.getByRole("button", { name: "Documentation", exact: true }).click();
  await expect(page.getByTestId("mcp-directory-card")).toHaveCount(1);
  await page.getByRole("heading", { name: "Context7" }).click();

  const detail = page.getByTestId("mcp-directory-detail");
  await expect(detail).toContainText("https://mcp.context7.com/mcp");
  await expect(detail).toContainText("Not required");

  await page.getByRole("button", { name: "Import", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("https://mcp.context7.com/mcp");
  await expect(dialog.getByRole("textbox")).toHaveCount(0);
  await dialog.getByRole("button", { name: "Import", exact: true }).click();

  const success = page.getByRole("status");
  await expect(success).toContainText("imported as a workspace MCP Capability");
  await expect(success.getByRole("button", { name: "View Capability" })).toBeVisible();
  await expect(success.getByRole("button", { name: "Add to Agent" })).toHaveCount(0);

  await page.getByRole("button", { name: "Back to connectors" }).click();
  await page.getByRole("tab", { name: "Skill" }).click();
  await expect(page.getByRole("heading", { name: "Diagram Maker" })).toBeVisible();
});

test("retries a failed connector directory request", async ({ page }) => {
  let directoryCalls = 0;
  await mockApp(page, async (route) => {
    directoryCalls += 1;
    if (directoryCalls !== 1) return false;
    await json(route, { error: "mcp_catalog_unavailable" }, 503);
    return true;
  });
  await page.goto(`/?admin=capabilities&tab=marketplace&ws=${WORKSPACE_ID}`);

  await expect(page.getByText("Couldn't load the connectors directory", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Retry" }).click();
  await expect(page.getByTestId("mcp-directory-card")).toHaveCount(3);
});

function connector(id: string, name: string, category: string, featuredRank: number) {
  return {
    id,
    name,
    description: `${name} hosted MCP connector.`,
    publisher: { name, url: `https://${id}.example.com` },
    repository_url: `https://github.com/example/${id}`,
    verified: true,
    categories: ["Developer Tools", category],
    featured_rank: featuredRank,
    version: "1.0.0",
    transport: "streamable-http",
    installed: false,
    installed_capability_id: null,
  };
}

async function mockApp(
  page: Page,
  directoryOverride?: (route: Route) => Promise<boolean>,
) {
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;

    if (path === "/api/v1/me")
      return json(route, {
        user_id: "user-1",
        email: "admin@example.com",
        name: "Admin",
        avatar_url: "",
      });
    if (path === "/api/v1/me/workspaces")
      return json(route, {
        user_id: "user-1",
        workspaces: [{
          id: WORKSPACE_ID,
          name: "Directory Test",
          slug: "directory-test",
          visibility: "private",
          role: "owner",
          created_at: "2026-07-23T00:00:00Z",
          updated_at: "2026-07-23T00:00:00Z",
        }],
      });
    if (path === "/api/v1/me/discoverable-workspaces")
      return json(route, { user_id: "user-1", workspaces: [], total: 0, limit: 5, offset: 0 });
    if (path === `/api/v1/workspaces/${WORKSPACE_ID}/agents`)
      return json(route, { agents: [] });
    if (path === `/api/v1/workspaces/${WORKSPACE_ID}/capabilities`)
      return json(route, { capabilities: [], marketplace_installs: [], total: 0 });
    if (path === `/api/v1/workspaces/${WORKSPACE_ID}/capabilities/marketplace-installs`)
      return json(route, { capabilities: [] });
    if (path === "/api/v1/capabilities/marketplace")
      return json(route, {
        capabilities: [{
          id: "00000000-0000-0000-0000-000000000044",
          type: "skill",
          name: "Diagram Maker",
          description: "Create diagrams.",
          visibility: "public",
          status: "active",
          required_credentials: [],
          latest_version: "1.0.0",
          source_workspace_name: "Public Catalog",
          installed: false,
          self_published: false,
        }],
      });
    if (path === `/api/v1/workspaces/${WORKSPACE_ID}/mcp-directory`) {
      if (directoryOverride && (await directoryOverride(route))) return;
      return json(route, {
        items: directoryItems,
        updated_at: "2026-07-23T00:00:00Z",
        source: "builtin",
      });
    }
    if (path === `/api/v1/workspaces/${WORKSPACE_ID}/mcp-directory/context7`)
      return json(route, { ...directoryItems[0], url: "https://mcp.context7.com/mcp" });
    if (path === `/api/v1/workspaces/${WORKSPACE_ID}/mcp-directory/context7/import`)
      return json(route, { installed: true, capability_id: CAPABILITY_ID, created: true }, 201);
    return json(route, {});
  });
}

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  });
}
