import { expect, test, type Page, type Route } from "@playwright/test";

const WORKSPACE_ID = "00000000-0000-0000-0000-000000000011";
const CAPABILITY_ID = "00000000-0000-0000-0000-000000000033";
const AGENT_ID = "00000000-0000-0000-0000-000000000066";

const directoryItems = [
  {
    id: "filesystem",
    name: "Filesystem",
    description: "Read and write files from configured directories.",
    publisher: {
      name: "Model Context Protocol",
      url: "https://example.com/mcp",
    },
    repository_url: "https://example.com/filesystem",
    verified: true,
    categories: ["Developer Tools", "Files"],
    featured_rank: 1,
    version: "1.0.0",
    transport: "stdio",
    authentication: "none",
    connection_supported: true,
    connected: false,
    installed: false,
    installed_capability_id: null,
  },
  {
    id: "memory",
    name: "Memory",
    description: "Store knowledge in a local graph.",
    publisher: {
      name: "Model Context Protocol",
      url: "https://example.com/mcp",
    },
    verified: true,
    categories: ["Data"],
    featured_rank: 2,
    version: "1.1.0",
    transport: "stdio",
    authentication: "none",
    connection_supported: true,
    connected: false,
    installed: false,
    installed_capability_id: null,
  },
  {
    id: "community-clock",
    name: "Community Clock",
    description: "An unverified test connector.",
    publisher: { name: "Community", url: "https://example.com/community" },
    verified: false,
    categories: ["Utilities"],
    featured_rank: 3,
    version: "0.2.0",
    transport: "stdio",
    authentication: "none",
    connection_supported: true,
    connected: false,
    installed: false,
    installed_capability_id: null,
  },
  {
    id: "deepwiki",
    name: "DeepWiki",
    description: "Read public repositories as generated documentation.",
    publisher: { name: "Cognition", url: "https://www.cognition.ai" },
    verified: true,
    categories: ["Documentation"],
    featured_rank: 4,
    version: "2.14.3",
    transport: "streamable-http",
    authentication: "none",
    connection_supported: true,
    connected: false,
    installed: false,
    installed_capability_id: null,
  },
  {
    id: "notion",
    name: "Notion",
    description: "Search, read, create, and update Notion content.",
    publisher: { name: "Notion", url: "https://www.notion.so" },
    verified: true,
    categories: ["Productivity"],
    featured_rank: 5,
    version: "1.0.0",
    transport: "streamable-http",
    authentication: "oauth2",
    credential_kind: "notion_mcp_oauth",
    connection_supported: true,
    connected: false,
    installed: false,
    installed_capability_id: null,
  },
];

test("browse, filter, inspect, and import an MCP connector without affecting Skills", async ({
  page,
}) => {
  await mockApp(page);
  await page.goto(`/?admin=capabilities&tab=marketplace&ws=${WORKSPACE_ID}`);

  await expect(page.getByRole("heading", { name: "Connectors" })).toBeVisible();
  await expect(page.getByTestId("mcp-directory-card")).toHaveCount(5);

  const search = page.getByPlaceholder("Search capability name / description");
  await search.fill("memory");
  await expect(page.getByTestId("mcp-directory-card")).toHaveCount(1);
  await expect(page.getByRole("heading", { name: "Memory" })).toBeVisible();
  await search.clear();

  await page.getByRole("button", { name: "Files", exact: true }).click();
  await expect(page.getByTestId("mcp-directory-card")).toHaveCount(1);
  await page.getByRole("button", { name: "All categories" }).click();

  await page.getByRole("checkbox", { name: "Verified only" }).check();
  await expect(page.getByTestId("mcp-directory-card")).toHaveCount(4);
  await page.getByRole("checkbox", { name: "Verified only" }).uncheck();

  await page
    .getByRole("combobox", { name: "Sort connectors" })
    .selectOption("name");
  await expect(page.getByTestId("mcp-directory-card").first()).toHaveAttribute(
    "data-catalog-id",
    "community-clock",
  );

  await page.getByRole("heading", { name: "Filesystem" }).click();
  await expect(page.getByTestId("mcp-directory-detail")).toContainText(
    "npx -y @modelcontextprotocol/server-filesystem@1.0.0",
  );
  await expect(page.getByTestId("mcp-directory-detail")).toContainText(
    "FILESYSTEM_ROOT",
  );

  await page.getByRole("button", { name: "Import", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("No token is required during import");
  await expect(dialog.getByRole("textbox")).toHaveCount(0);
  await dialog.getByRole("button", { name: "Import", exact: true }).click();

  const success = page.getByRole("status");
  await expect(success).toContainText("imported as a workspace MCP Capability");
  await expect(
    success.getByRole("button", { name: "View Capability" }),
  ).toBeVisible();
  await expect(
    success.getByRole("button", { name: "Add to Agent" }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Import", exact: true }),
  ).toHaveCount(0);

  await page.getByRole("button", { name: "Back to connectors" }).click();
  await expect(
    page
      .getByTestId("mcp-directory-card")
      .filter({ has: page.getByRole("heading", { name: "Filesystem" }) })
      .getByRole("button", { name: "View Capability" }),
  ).toBeVisible();
  await page.getByRole("tab", { name: "Skill" }).click();
  await expect(
    page.getByRole("heading", { name: "Diagram Maker" }),
  ).toBeVisible();
});

test("shows a retryable connector directory error", async ({ page }) => {
  let directoryCalls = 0;
  await mockApp(page, async (route) => {
    directoryCalls += 1;
    if (directoryCalls === 1) {
      await json(route, { error: "mcp_catalog_unavailable" }, 503);
      return true;
    }
    return false;
  });
  await page.goto(`/?admin=capabilities&tab=marketplace&ws=${WORKSPACE_ID}`);

  await expect(
    page.getByText(
      "Couldn't load the connectors directory. Some connector details may be missing.",
      { exact: false },
    ),
  ).toBeVisible();
  await page.getByRole("button", { name: "Retry" }).click();
  await expect(page.getByTestId("mcp-directory-card")).toHaveCount(5);
});

test("returns to the directory when a bookmarked connector no longer exists", async ({
  page,
}) => {
  await mockApp(page);
  await page.goto(
    `/?admin=capabilities&tab=marketplace&item=mcp%3Agit&ws=${WORKSPACE_ID}`,
  );

  await expect(page.getByTestId("mcp-directory-card")).toHaveCount(5);
  await expect(page).not.toHaveURL(/(?:\?|&)item=/);
  await expect(page.getByText("connector_not_found")).toHaveCount(0);
});

test("shows a loading state while the connector catalog is pending", async ({
  page,
}) => {
  let releaseDirectory: (() => void) | undefined;
  const directoryReady = new Promise<void>((resolve) => {
    releaseDirectory = resolve;
  });
  await mockApp(page, async () => {
    await directoryReady;
    return false;
  });
  await page.goto(`/?admin=capabilities&tab=marketplace&ws=${WORKSPACE_ID}`);

  await expect(page.getByTestId("mcp-directory-loading")).toBeVisible();
  releaseDirectory?.();
  await expect(page.getByTestId("mcp-directory-card")).toHaveCount(5);
});

test("shows a no-auth streamable HTTP connector endpoint", async ({ page }) => {
  await mockApp(page);
  await page.goto(`/?admin=capabilities&tab=marketplace&ws=${WORKSPACE_ID}`);

  await page.getByRole("heading", { name: "DeepWiki" }).click();
  const detail = page.getByTestId("mcp-directory-detail");
  await expect(detail).toContainText("streamable-http");
  await expect(detail).toContainText("https://mcp.deepwiki.com/mcp");
  await expect(detail).toContainText("Not required");

  await page.getByRole("button", { name: "Import", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("https://mcp.deepwiki.com/mcp");
  await expect(dialog).toContainText("Not required");
  await expect(dialog.getByRole("textbox")).toHaveCount(0);
});

test("requires Notion OAuth before confirming the connector import", async ({
  page,
}) => {
  await mockApp(page);
  await page.goto(`/?admin=capabilities&tab=marketplace&ws=${WORKSPACE_ID}`);

  await page.getByRole("heading", { name: "Notion" }).click();
  const detail = page.getByTestId("mcp-directory-detail");
  await expect(detail).toContainText("OAuth 2.1 required");
  await expect(
    page.getByRole("button", { name: "Connect Notion" }),
  ).toBeVisible();
  await expect(page.getByTestId("mcp-oauth-status")).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Import", exact: true }),
  ).toBeVisible();

  await page.getByRole("button", { name: "Import", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText(
    "Authorize this workspace before importing. You'll return here to confirm the import.",
  );
  await expect(
    dialog.getByRole("button", { name: "Authorize & continue" }),
  ).toBeVisible();
  await expect(
    dialog.getByRole("button", { name: "Import", exact: true }),
  ).toHaveCount(0);
});

test("reopens the Notion import confirmation after OAuth", async ({ page }) => {
  await mockApp(page, undefined, false, "owner", 0, true);
  await page.goto(
    `/?admin=capabilities&tab=marketplace&ws=${WORKSPACE_ID}&item=mcp:notion&connected=notion&import=notion`,
  );

  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText("Authorized");
  await expect(
    dialog.getByRole("button", { name: "Import", exact: true }),
  ).toBeVisible();
  await expect(
    dialog.getByRole("button", { name: "Authorize & continue" }),
  ).toHaveCount(0);

  const returnURL = new URL(page.url());
  expect(returnURL.searchParams.get("import")).toBeNull();
  expect(returnURL.searchParams.get("connected")).toBe("notion");
  expect(returnURL.searchParams.get("item")).toBe("mcp:notion");
});

test("opens Notion OAuth in a popup and refreshes the original page on return", async ({
  page,
}) => {
  await mockApp(page);
  await page.goto(`/?admin=capabilities&tab=marketplace&ws=${WORKSPACE_ID}`);
  await page.getByRole("heading", { name: "Notion" }).click();
  const originalURL = page.url();

  const popupPromise = page.waitForEvent("popup");
  await page.getByRole("button", { name: "Connect Notion" }).click();
  const popup = await popupPromise;

  await expect.poll(() => popup.isClosed()).toBe(true);
  await expect(page).toHaveURL(originalURL);
  await expect(page.getByTestId("mcp-directory-detail")).toContainText(
    "Authorized, connection not verified",
  );
  await expect(
    page.getByRole("button", { name: "Test connection" }),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "Reconnect" })).toBeVisible();
});

test("members can authorize the shared workspace OAuth connection", async ({
  page,
}) => {
  await mockApp(page, undefined, false, "member");
  await page.goto(`/?admin=capabilities&tab=marketplace&ws=${WORKSPACE_ID}`);

  await page.getByRole("heading", { name: "Notion" }).click();
  await expect(
    page.getByRole("button", { name: "Connect Notion" }),
  ).toBeVisible();
  await expect(page.getByTestId("mcp-oauth-status")).toHaveCount(0);
});

test("shows an authorized connector with a green status", async ({ page }) => {
  await mockApp(page, undefined, false, "owner", 0, true);
  await page.goto(`/?admin=capabilities&tab=marketplace&ws=${WORKSPACE_ID}`);

  const card = page.getByTestId("mcp-directory-card").filter({
    has: page.getByRole("heading", { name: "Notion" }),
  });
  await expect(card.getByText("Authorized", { exact: true })).toHaveClass(
    /bg-success-subtle/,
  );

  await card.getByRole("heading", { name: "Notion" }).click();
  await expect(
    page
      .getByTestId("mcp-directory-detail")
      .getByText("Authorized", { exact: true }),
  ).toHaveClass(/bg-success-subtle/);
});

test("verifies an authorized Notion connection without verbose status copy", async ({
  page,
}) => {
  await mockApp(page, undefined, false, "owner", 0, true);
  await page.goto(`/?admin=capabilities&tab=marketplace&ws=${WORKSPACE_ID}`);

  await page.getByRole("heading", { name: "Notion" }).click();
  const detail = page.getByTestId("mcp-directory-detail");
  const response = page.waitForResponse(
    (candidate) =>
      candidate.url().includes("/mcp-directory/notion/oauth/test") &&
      candidate.request().method() === "POST",
  );
  await page.getByRole("button", { name: "Test connection" }).click();

  await expect((await response).ok()).toBe(true);
  await expect(
    page.getByRole("button", { name: "Connection works" }),
  ).toHaveClass(/bg-success-subtle/);
  await expect(detail).not.toContainText("Notion connection verified");
  await expect(detail).not.toContainText("available tools");
  await expect(detail).not.toContainText("NOTION · tool");
});

test("resolves an imported workspace connector on the Add to Agent path", async ({
  page,
}) => {
  await mockApp(page);
  await page.goto(`/?admin=capabilities&tab=marketplace&ws=${WORKSPACE_ID}`);

  await page.getByRole("heading", { name: "Filesystem" }).click();
  await page.getByRole("button", { name: "Import", exact: true }).click();
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Import", exact: true })
    .click();
  await page
    .getByRole("status")
    .getByRole("button", { name: "Add to Agent" })
    .click();

  const originalURL = page.url();
  const addDialog = page.getByRole("dialog");
  await expect(
    addDialog.getByRole("heading", { name: "Add Filesystem to an Agent" }),
  ).toBeVisible();
  await addDialog.getByRole("radio", { name: "Directory Agent" }).check();

  const enableRequest = page.waitForRequest(
    (request) =>
      request.method() === "POST" &&
      new URL(request.url()).pathname ===
        `/api/v1/workspaces/${WORKSPACE_ID}/agents/${AGENT_ID}/capabilities/version-1/enable`,
  );
  await addDialog.getByRole("button", { name: "Add to Agent" }).click();
  const request = await enableRequest;

  expect(request.postDataJSON()).toEqual({ pinning_mode: "latest" });
  await expect(addDialog).toHaveCount(0);
  await expect(page).toHaveURL(originalURL);
  await expect(
    page.getByText("Filesystem was added to Directory Agent.", { exact: true }),
  ).toBeVisible();
});

test("prefills an imported connector edit from its canonical spec", async ({
  page,
}) => {
  await mockApp(page, undefined, true, "owner", 500);
  await page.goto(
    `/?admin=capabilities&id=${CAPABILITY_ID}&ws=${WORKSPACE_ID}`,
  );

  await page.getByRole("button", { name: "Submit new version" }).click();
  const editor = page.getByRole("dialog").locator("textarea");
  await expect(editor).toHaveValue(/mcp-server-git==2026\.7\.10/);
  await expect(editor).toHaveValue(/"--repository"/);
  await expect(editor).toHaveValue(/"\."/);
  await expect(editor).toHaveValue(/"startup_timeout_sec": 30/);
});

test("does not offer publishing a Connector Directory import again", async ({
  page,
}) => {
  await mockApp(page, undefined, true);
  await page.goto(`/?admin=capabilities&ws=${WORKSPACE_ID}`);

  const row = page.getByRole("row").filter({ hasText: "Filesystem" });
  await row.getByRole("button", { name: "More actions" }).click();
  await expect(
    page.getByRole("menuitem", { name: "Publish to market" }),
  ).toHaveCount(0);
  await expect(page.getByRole("menuitem", { name: "Delete" })).toBeVisible();

  await page.goto(
    `/?admin=capabilities&id=${CAPABILITY_ID}&ws=${WORKSPACE_ID}`,
  );
  await expect(page.getByText("Connector Directory item")).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Publish to market" }),
  ).toHaveCount(0);
});

test("lets members open a Skill-only capability import", async ({ page }) => {
  await mockApp(page, undefined, false, "member");
  await page.goto(`/?admin=capabilities&ws=${WORKSPACE_ID}`);

  await page.getByRole("button", { name: "New capability" }).first().click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("Paste a SKILL.md or upload a zip");
  await expect(dialog.getByRole("tab", { name: "MCP" })).toHaveCount(0);
  await expect(dialog.locator("textarea")).toBeVisible();
});

async function mockApp(
  page: Page,
  directoryOverride?: (route: Route) => Promise<boolean>,
  initiallyImported = false,
  workspaceRole = "owner",
  versionDelayMs = 0,
  notionConnected = false,
) {
  let imported = initiallyImported;
  let notionAuthorized = notionConnected;
  let notionStatus = notionAuthorized ? "authorized" : "not_connected";
  await page.context().route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;

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
        workspaces: [
          {
            id: WORKSPACE_ID,
            name: "Directory Test",
            slug: "directory-test",
            visibility: "private",
            role: workspaceRole,
            created_at: "2026-07-22T00:00:00Z",
            updated_at: "2026-07-22T00:00:00Z",
          },
        ],
      });
    if (path === "/api/v1/me/discoverable-workspaces")
      return json(route, {
        user_id: "user-1",
        workspaces: [],
        total: 0,
        limit: 5,
        offset: 0,
      });
    if (path === `/api/v1/workspaces/${WORKSPACE_ID}/agents`)
      return json(route, {
        agents: [
          {
            id: AGENT_ID,
            workspace_id: WORKSPACE_ID,
            name: "Directory Agent",
            slug: "directory-agent",
            description: "Agent used by the connector directory test.",
            connector_type: "agent_daemon",
            status: "active",
            runtime: "local",
            visibility: "workspace",
            created_by_user_id: "user-1",
            config: { daemon_mode: "local", agent_kind: "claude_code" },
            created_at: "2026-07-22T00:00:00Z",
            updated_at: "2026-07-22T00:00:00Z",
          },
        ],
      });
    if (path === `/api/v1/workspaces/${WORKSPACE_ID}/agents/${AGENT_ID}`)
      return json(route, {
        id: AGENT_ID,
        workspace_id: WORKSPACE_ID,
        name: "Directory Agent",
        slug: "directory-agent",
        description: "Agent used by the connector directory test.",
        connector_type: "agent_daemon",
        status: "active",
        runtime: "local",
        visibility: "workspace",
        config: { daemon_mode: "local", agent_kind: "claude_code" },
        created_at: "2026-07-22T00:00:00Z",
        updated_at: "2026-07-22T00:00:00Z",
      });
    if (
      path ===
      `/api/v1/workspaces/${WORKSPACE_ID}/agents/${AGENT_ID}/capabilities`
    )
      return json(route, {
        workspace_id: WORKSPACE_ID,
        agent_id: AGENT_ID,
        installed: [],
        available: [
          {
            id: CAPABILITY_ID,
            workspace_id: WORKSPACE_ID,
            type: "mcp",
            name: "Filesystem",
            description: "Read and write files from configured directories.",
            visibility: "workspace",
            status: "active",
            required_credentials: [],
            latest_version_id: "version-1",
            latest_version: "1.0.0",
            creator_id: "user-1",
            created_at: "2026-07-22T00:00:00Z",
            updated_at: "2026-07-22T00:00:00Z",
          },
        ],
      });
    if (
      path ===
        `/api/v1/workspaces/${WORKSPACE_ID}/agents/${AGENT_ID}/capabilities/version-1/enable` &&
      request.method() === "POST"
    )
      return json(route, {});
    if (path === `/api/v1/workspaces/${WORKSPACE_ID}/models`)
      return json(route, { models: [] });
    if (path === `/api/v1/workspaces/${WORKSPACE_ID}/secrets`)
      return json(route, { secrets: [] });
    if (path === "/api/v1/me/credentials")
      return json(route, { credentials: [] });
    if (
      path ===
      `/api/v1/workspaces/${WORKSPACE_ID}/capabilities/marketplace-installs`
    )
      return json(route, { capabilities: [] });
    if (
      path ===
      `/api/v1/workspaces/${WORKSPACE_ID}/capabilities/${CAPABILITY_ID}`
    )
      return json(route, {
        id: CAPABILITY_ID,
        workspace_id: WORKSPACE_ID,
        type: "mcp",
        name: "Filesystem",
        description: "Read, search, and inspect a local Git repository.",
        visibility: "workspace",
        status: "active",
        creator_id: "user-1",
        created_at: "2026-07-22T00:00:00Z",
        updated_at: "2026-07-22T00:00:00Z",
      });
    if (
      path ===
      `/api/v1/workspaces/${WORKSPACE_ID}/capabilities/${CAPABILITY_ID}/versions`
    ) {
      if (versionDelayMs > 0) {
        await new Promise((resolve) => setTimeout(resolve, versionDelayMs));
      }
      return json(route, {
        versions: [
          {
            id: "version-1",
            capability_id: CAPABILITY_ID,
            version: "2026.7.10",
            source_payload: {
              source_format: "mcp_catalog",
              catalog_id: "git",
              catalog_version: "2026.7.10",
              catalog_source: "builtin",
            },
            canonical_spec: {
              schema_version: 1,
              kind: "mcp",
              mcp: {
                servers: [
                  {
                    name: "git",
                    command: "uvx",
                    args: [
                      "--from",
                      "mcp-server-git==2026.7.10",
                      "mcp-server-git",
                      "--repository",
                      ".",
                    ],
                    startup_timeout_sec: 30,
                  },
                ],
              },
            },
            creator_id: "user-1",
            created_at: "2026-07-22T00:00:00Z",
          },
        ],
      });
    }
    if (
      path === `/api/v1/workspaces/${WORKSPACE_ID}/capabilities/import/preview`
    )
      return json(route, {
        canonical_spec: {
          schema_version: 1,
          kind: "mcp",
          mcp: {
            servers: [
              {
                name: "git",
                command: "uvx",
                args: [
                  "--from",
                  "mcp-server-git==2026.7.10",
                  "mcp-server-git",
                  "--repository",
                  ".",
                ],
                startup_timeout_sec: 30,
              },
            ],
          },
        },
        warnings: [],
        suggested_name: "git",
      });
    if (path === `/api/v1/workspaces/${WORKSPACE_ID}/capabilities`)
      return json(route, {
        capabilities: imported
          ? [
              {
                id: CAPABILITY_ID,
                workspace_id: WORKSPACE_ID,
                type: "mcp",
                name: "Filesystem",
                description:
                  "Read and write files from configured directories.",
                visibility: "workspace",
                status: "active",
                creator_id: "user-1",
                created_at: "2026-07-22T00:00:00Z",
                updated_at: "2026-07-22T00:00:00Z",
              },
            ]
          : [],
        marketplace_installs: [],
        total: imported ? 1 : 0,
      });
    if (path === "/api/v1/capabilities/marketplace")
      return json(route, {
        capabilities: [
          {
            id: "00000000-0000-0000-0000-000000000044",
            capability_id: "00000000-0000-0000-0000-000000000044",
            workspace_id: "00000000-0000-0000-0000-000000000055",
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
        ],
      });
    if (path === `/api/v1/workspaces/${WORKSPACE_ID}/mcp-directory`) {
      if (directoryOverride && (await directoryOverride(route))) return;
      return json(route, {
        items: directoryItems.map((item) => {
          if (item.id === "notion")
            return {
              ...item,
              connected: notionAuthorized,
              connection_status: notionStatus,
            };
          if (item.id === "filesystem" && imported)
            return {
              ...item,
              installed: true,
              installed_capability_id: CAPABILITY_ID,
            };
          return item;
        }),
        updated_at: "2026-07-22T00:00:00Z",
        source: "builtin",
      });
    }
    if (
      path === `/api/v1/workspaces/${WORKSPACE_ID}/mcp-directory/filesystem` &&
      request.method() === "GET"
    ) {
      return json(route, {
        ...directoryItems[0],
        command: "npx",
        args: ["-y", "@modelcontextprotocol/server-filesystem@1.0.0"],
        env: ["FILESYSTEM_ROOT"],
        startup_timeout_sec: 30,
      });
    }
    if (
      path === `/api/v1/workspaces/${WORKSPACE_ID}/mcp-directory/deepwiki` &&
      request.method() === "GET"
    ) {
      return json(route, {
        ...directoryItems[3],
        url: "https://mcp.deepwiki.com/mcp",
      });
    }
    if (
      path === `/api/v1/workspaces/${WORKSPACE_ID}/mcp-directory/notion` &&
      request.method() === "GET"
    ) {
      return json(route, {
        ...directoryItems[4],
        connected: notionAuthorized,
        connection_status: notionStatus,
        url: "https://mcp.notion.com/mcp",
      });
    }
    if (
      path ===
        `/api/v1/workspaces/${WORKSPACE_ID}/mcp-directory/notion/oauth/test` &&
      request.method() === "POST"
    ) {
      notionStatus = "verified";
      return json(route, {
        authorized: true,
        verified: true,
        status: "verified",
        checked_at: "2026-07-22T10:30:00Z",
        protocol_version: "2025-06-18",
        server_name: "Notion",
        server_version: "1.0.0",
        tool_count: 2,
      });
    }
    if (
      path ===
        `/api/v1/workspaces/${WORKSPACE_ID}/mcp-directory/notion/oauth/start` &&
      request.method() === "GET"
    ) {
      notionAuthorized = true;
      notionStatus = "authorized";
      const intent = url.searchParams.get("intent");
      const callback = new URL("/", url.origin);
      callback.searchParams.set("admin", "capabilities");
      callback.searchParams.set("tab", "marketplace");
      callback.searchParams.set("ws", WORKSPACE_ID);
      callback.searchParams.set("item", "mcp:notion");
      callback.searchParams.set("connected", "notion");
      if (intent === "import") callback.searchParams.set("import", "notion");
      return route.fulfill({
        status: 302,
        headers: { location: callback.toString() },
      });
    }
    if (
      path ===
      `/api/v1/workspaces/${WORKSPACE_ID}/mcp-directory/filesystem/import`
    ) {
      imported = true;
      return json(
        route,
        { installed: true, capability_id: CAPABILITY_ID, created: true },
        201,
      );
    }
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
