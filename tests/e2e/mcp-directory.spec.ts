import { expect, test, type Page, type Route } from "@playwright/test";

const WORKSPACE_ID = "00000000-0000-0000-0000-000000000011";
const CAPABILITY_ID = "00000000-0000-0000-0000-000000000033";
const WORKSPACE_SKILL_ID = "00000000-0000-0000-0000-000000000077";
const WORKSPACE_SKILL_VERSION_ID = "00000000-0000-0000-0000-000000000078";

const directoryItems = [
  connector("context7", "Context7", "Documentation", 1),
  connector("exa", "Exa", "Search", 2),
  connector("firecrawl", "Firecrawl", "Web", 3),
];

const skillDirectoryItems = [
  {
    ...skill("frontend-design", "Frontend Design", 1),
    installed: true,
    installed_capability_id: WORKSPACE_SKILL_ID,
  },
];

const workspaceSkill = {
  id: WORKSPACE_SKILL_ID,
  workspace_id: WORKSPACE_ID,
  type: "skill",
  name: "Frontend Design",
  description: "Workspace Frontend Design skill.",
  scope: "private",
  status: "active",
  required_credentials: [],
  creator_id: "user-1",
  created_at: "2026-07-23T00:00:00Z",
  updated_at: "2026-07-23T00:00:00Z",
};

const workspaceSkillVersion = {
  id: WORKSPACE_SKILL_VERSION_ID,
  capability_id: WORKSPACE_SKILL_ID,
  version: "1.0.0",
  git_repo_url: "https://github.com/anthropics/skills",
  git_ref: "main",
  path: "skills/frontend-design",
  creator_id: "user-1",
  created_at: "2026-07-23T00:00:00Z",
};

test("browses and imports a hosted MCP connector", async ({ page }) => {
  await mockApp(page);
  await page.goto(`/?admin=capabilities&tab=marketplace&ws=${WORKSPACE_ID}`);

  await expect(page.getByRole("heading", { name: "Connectors" })).toBeVisible();
  await expect(page.getByTestId("mcp-directory-card")).toHaveCount(3);
  const marketplaceGrid = page.getByTestId("mcp-marketplace-grid");
  await expect(marketplaceGrid.getByRole("heading", { name: "Context7" })).toBeVisible();
  await expect(marketplaceGrid.getByRole("heading", { name: "My MCP" })).toBeVisible();
  await expect(page.getByTestId("marketplace-mcp-card")).toHaveCount(1);
  await marketplaceGrid.getByRole("button", { name: "Delete", exact: true }).click();
  const deleteDialog = page.getByRole("alertdialog", { name: 'Delete capability "My MCP"' });
  await expect(deleteDialog).toBeVisible();
  await deleteDialog.getByRole("button", { name: "Cancel", exact: true }).click();

  const search = page.getByPlaceholder("Search capability name / description");
  await search.fill("exa");
  await expect(page.getByTestId("mcp-directory-card")).toHaveCount(1);
  await expect(page.getByRole("heading", { name: "My MCP" })).toHaveCount(0);
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

test("keeps the skill marketplace selected and hides a duplicate self-published skill", async ({ page }) => {
  await mockApp(page);
  await page.goto(`/?admin=capabilities&tab=marketplace&marketplace=skill&ws=${WORKSPACE_ID}`);

  await expect(page.getByTestId("skill-directory")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Frontend Design", exact: true })).toHaveCount(1);
  await expect(page.getByTestId("marketplace-skill-card")).toHaveCount(1);

  await page.getByRole("heading", { name: "Frontend Design", exact: true }).click();
  await expect(page.getByTestId("skill-directory-detail")).toBeVisible();
  await expect(page).toHaveURL(/marketplace=skill/);

  await page.getByRole("button", { name: "Back to Skills", exact: true }).click();
  await expect(page.getByTestId("skill-directory")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Connectors", exact: true })).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Frontend Design", exact: true })).toHaveCount(1);

  await page.getByRole("button", { name: "Installed", exact: true }).click();
  await expect(page.getByRole("heading", { name: /Frontend Design/ })).toBeVisible();
  await expect.poll(() => new URL(page.url()).searchParams.get("tab")).toBe("marketplace");
  await page.locator("#main-content").getByRole("button", { name: "Capabilities", exact: true }).click();
  await expect(page.getByTestId("skill-directory")).toBeVisible();

  await page.getByRole("tab", { name: "Workspace", exact: true }).click();
  await expect(page.getByRole("button", { name: /Frontend Design Workspace/ })).toBeVisible();
  await page.getByRole("button", { name: /Frontend Design Workspace/ }).click();
  await expect.poll(() => new URL(page.url()).searchParams.get("tab")).toBe("workspace");
  await page.locator("#main-content").getByRole("button", { name: "Capabilities", exact: true }).click();
  await expect(page.getByTestId("skill-directory")).toHaveCount(0);
  await expect(page.getByRole("button", { name: /Frontend Design Workspace/ })).toBeVisible();

  await page.getByRole("tab", { name: "MCP", exact: true }).click();
  await expect.poll(() => new URL(page.url()).searchParams.get("marketplace")).toBe("mcp");
  await page.getByRole("tab", { name: "Marketplace", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Connectors", exact: true })).toBeVisible();
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

function skill(id: string, name: string, featuredRank: number) {
  return {
    id,
    name,
    description: `${name} catalog skill.`,
    publisher: { name: "Anthropic", url: "https://www.anthropic.com" },
    repository_url: "https://github.com/anthropics/skills",
    verified: true,
    categories: ["Developer Tools"],
    featured_rank: featuredRank,
    version: "1.0.0",
    license: "Apache-2.0",
    slug: id,
    title: name,
    instruction: "Use this skill when the user asks for a design task.",
    files: [{ path: "SKILL.md", content: "# Frontend Design", kind: "markdown" }],
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
    const requestURL = new URL(request.url());
    const path = requestURL.pathname;

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
      return requestURL.searchParams.get("type") === "mcp"
        ? json(route, { capabilities: [], marketplace_installs: [], total: 0 })
        : json(route, { capabilities: [workspaceSkill], marketplace_installs: [], total: 1 });
    if (path === `/api/v1/workspaces/${WORKSPACE_ID}/capabilities/${WORKSPACE_SKILL_ID}`)
      return json(route, workspaceSkill);
    if (path === `/api/v1/workspaces/${WORKSPACE_ID}/capabilities/${WORKSPACE_SKILL_ID}/versions`)
      return json(route, { capability_id: WORKSPACE_SKILL_ID, versions: [workspaceSkillVersion] });
    if (path === `/api/v1/workspaces/${WORKSPACE_ID}/capabilities/marketplace-installs`)
      return json(route, { capabilities: [] });
    if (path === "/api/v1/capabilities/marketplace")
      return json(route, {
        capabilities: [
          {
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
          },
          {
            id: "00000000-0000-0000-0000-000000000066",
            type: "skill",
            name: "Frontend Design",
            description: "A legacy workspace-published copy.",
            visibility: "public",
            status: "active",
            required_credentials: [],
            latest_version: "1.0.0",
            source_workspace_id: WORKSPACE_ID,
            source_workspace_name: "Directory Test",
            installed: false,
            self_published: true,
          },
          {
            id: "00000000-0000-0000-0000-000000000055",
            type: "mcp",
            name: "My MCP",
            description: "A workspace-published MCP.",
            visibility: "public",
            status: "active",
            required_credentials: [],
            latest_version: "1.0.0",
            source_workspace_name: "Directory Test",
            installed: false,
            self_published: true,
          },
        ],
      });
    if (path === `/api/v1/workspaces/${WORKSPACE_ID}/skill-directory`)
      return json(route, { items: skillDirectoryItems });
    if (path === `/api/v1/workspaces/${WORKSPACE_ID}/skill-directory/frontend-design`)
      return json(route, skillDirectoryItems[0]);
    if (path === `/api/v1/workspaces/${WORKSPACE_ID}/mcp-directory`) {
      if (directoryOverride && (await directoryOverride(route))) return;
      return json(route, { items: directoryItems });
    }
    if (path === `/api/v1/workspaces/${WORKSPACE_ID}/mcp-directory/context7`)
      return json(route, { ...directoryItems[0], url: "https://mcp.context7.com/mcp" });
    if (path === `/api/v1/workspaces/${WORKSPACE_ID}/mcp-directory/context7/import`)
      return json(route, { installed: true, capability_id: CAPABILITY_ID }, 201);
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
