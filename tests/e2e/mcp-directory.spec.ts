import { expect, test, type Page, type Route } from "@playwright/test";

const WORKSPACE_ID = "00000000-0000-0000-0000-000000000011";
const CAPABILITY_ID = "00000000-0000-0000-0000-000000000033";

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
    popularity_rank: 1,
    version: "1.0.0",
    transport: "stdio",
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
    popularity_rank: 2,
    version: "1.1.0",
    transport: "stdio",
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
    popularity_rank: 3,
    version: "0.2.0",
    transport: "stdio",
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
    popularity_rank: 4,
    version: "2.14.3",
    transport: "streamable-http",
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
  await expect(page.getByTestId("mcp-directory-card")).toHaveCount(4);

  const search = page.getByPlaceholder("Search capability name / description");
  await search.fill("memory");
  await expect(page.getByTestId("mcp-directory-card")).toHaveCount(1);
  await expect(page.getByRole("heading", { name: "Memory" })).toBeVisible();
  await search.clear();

  await page.getByRole("button", { name: "Files", exact: true }).click();
  await expect(page.getByTestId("mcp-directory-card")).toHaveCount(1);
  await page.getByRole("button", { name: "All categories" }).click();

  await page.getByRole("checkbox", { name: "Verified only" }).check();
  await expect(page.getByTestId("mcp-directory-card")).toHaveCount(3);
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
  await expect(page.getByTestId("mcp-directory-card")).toHaveCount(4);
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
  await expect(page.getByTestId("mcp-directory-card")).toHaveCount(4);
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

  await expect(page).toHaveURL(
    new RegExp(`admin=agents.*pendingCapability=${CAPABILITY_ID}`),
  );
  await expect(
    page.getByText('You are preparing to add "Filesystem"', { exact: false }),
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
) {
  let imported = initiallyImported;
  await page.route("**/api/v1/**", async (route) => {
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
      return json(route, { agents: [] });
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
        name: "Git",
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
        items: directoryItems,
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
