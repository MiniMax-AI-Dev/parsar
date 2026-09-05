import { expect, test, type Page, type Route } from "@playwright/test";

const WORKSPACE_ID = "00000000-0000-0000-0000-000000000011";
const CAPABILITY_ID = "00000000-0000-0000-0000-000000000033";
const MARKETPLACE_URL = `/?admin=capabilities&tab=marketplace&ws=${WORKSPACE_ID}`;
const skill = { id: "example/skills/triage", source: "example/skills", slug: "triage", name: "Triage" };

for (const role of ["owner", "admin", "member", "viewer", undefined]) {
  test(`Skill installation permission for ${role ?? "unknown role"}`, async ({ page }) => {
    const installs = await mockApp(page, role);
    await page.goto(MARKETPLACE_URL);
    await page.getByRole("tab", { name: "Skill", exact: true }).click();
    const card = page.getByTestId("skills-directory-card");
    await expect(card.getByRole("heading", { name: skill.name })).toBeVisible();

    if (role === "owner" || role === "admin") {
      await card.getByRole("button", { name: "Install", exact: true }).click();
      await expect(page.getByRole("status")).toContainText(skill.name);
      expect(installs).toEqual([{
        source: skill.source,
        slug: skill.slug,
        registry_id: skill.id,
        registry: "skills.sh",
      }]);
      await page.getByRole("status").getByRole("button", { name: "View Capability" }).click();
      await expect(page).toHaveURL(new RegExp(`id=${CAPABILITY_ID}`));
      await expect(page.getByRole("heading", { name: skill.name })).toBeVisible();
    } else {
      const button = card.getByRole("button", { name: "Owner / admin only", exact: true });
      await expect(button).toBeDisabled();
      await button.dispatchEvent("click");
      await page.getByPlaceholder("Search capability name / description").fill("missing");
      await expect(card).toHaveCount(0);
      expect(installs).toEqual([]);
    }
  });
}

test("Skill installation stays unavailable while the workspace role loads", async ({ page }) => {
  let releaseRole!: (role: string) => void;
  const role = new Promise<string>((resolve) => { releaseRole = resolve; });
  const installs = await mockApp(page, role);
  const workspaceRequest = page.waitForRequest("**/api/v1/me/workspaces");
  await page.goto(MARKETPLACE_URL);
  await workspaceRequest;
  await expect(page.getByRole("main")).toBeVisible();
  await expect(page.getByTestId("skills-directory-card")).toHaveCount(0);
  expect(installs).toEqual([]);

  releaseRole("owner");
  await page.getByRole("tab", { name: "Skill", exact: true }).click();
  await expect(page.getByTestId("skills-directory-card").getByRole("button", { name: "Install", exact: true })).toBeEnabled();
});

async function mockApp(page: Page, role: string | undefined | Promise<string>) {
  const installs: unknown[] = [];
  await page.addInitScript(() => localStorage.setItem("i18nextLng", "en-US"));
  await page.route("https://agent-skill-index.vercel.app/data/latest/skills.json", (route) => json(route, { items: [skill] }));
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/api/v1/me")
      return json(route, { user_id: "user-1", email: "user@example.com", name: "User" });
    if (path === "/api/v1/me/workspaces")
      return json(route, {
        user_id: "user-1",
        workspaces: [{ id: WORKSPACE_ID, name: "Skill Test", slug: "skill-test", role: await role }],
      });
    if (path === "/api/v1/me/discoverable-workspaces")
      return json(route, { workspaces: [], total: 0 });
    if (path === `/api/v1/workspaces/${WORKSPACE_ID}/agents`)
      return json(route, { agents: [] });
    if (path === `/api/v1/workspaces/${WORKSPACE_ID}/capabilities`)
      return json(route, { capabilities: [], marketplace_installs: [], total: 0 });
    if (path === "/api/v1/capabilities/marketplace" || path.endsWith("/capabilities/marketplace-installs"))
      return json(route, { capabilities: [] });
    if (path === `/api/v1/workspaces/${WORKSPACE_ID}/skills/install`) {
      expect(route.request().method()).toBe("POST");
      installs.push(route.request().postDataJSON());
      return json(route, { capability: { id: CAPABILITY_ID, name: skill.name, type: "skill" }, capability_version: {}, created_secret_ids: [] }, 201);
    }
    if (path === `/api/v1/workspaces/${WORKSPACE_ID}/capabilities/${CAPABILITY_ID}`)
      return json(route, { id: CAPABILITY_ID, name: skill.name, type: "skill" });
    if (path.endsWith("/versions")) return json(route, { versions: [] });
    return json(route, {});
  });
  return installs;
}

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
}
