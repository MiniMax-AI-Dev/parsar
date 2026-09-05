import { expect, test, type Page, type Route } from "@playwright/test";
import type { AgentInteraction, ResolveAgentInteractionRequest } from "../../apps/web/src/lib/api-types";

const WORKSPACE_ID = "00000000-0000-0000-0000-000000000011";
const CONVERSATION_ID = "00000000-0000-0000-0000-000000000012";
const PERMISSION_ID = "00000000-0000-0000-0000-000000000013";
const QUESTION_ID = "00000000-0000-0000-0000-000000000014";
const READ_ONLY = "Read-only access. Workspace owners, admins, and members can respond.";
const surfaces = {
  inbox: `/?admin=approvals&ws=${WORKSPACE_ID}`,
  conversation: `/?admin=conversations&id=${CONVERSATION_ID}&ws=${WORKSPACE_ID}`,
};

for (const role of ["owner", "admin", "member", "viewer", undefined]) {
  for (const [surface, url] of Object.entries(surfaces)) {
    test(`${surface} interaction permissions for ${role ?? "unknown role"}`, async ({ page }) => {
      const decisions = await mockApp(page, role);
      await page.goto(url);
      const permission = page.locator(`[data-request-id="${PERMISSION_ID}"]`);
      await expect(permission.getByRole("heading", { name: "Write configuration" })).toBeVisible();
      const writable = role === "owner" || role === "admin" || role === "member";

      if (writable) {
        await permission.getByRole("button", { name: "Allow once" }).click();
        await expect(permission).toHaveCount(0);
      } else {
        await expect(permission).toContainText(READ_ONLY);
        await expect(permission.getByRole("button", { name: /Allow once|Deny/ })).toHaveCount(0);
        await expect(permission.getByRole("button", { name: "Open Run Detail" })).toBeEnabled();
        if (surface === "inbox") await page.getByRole("button", { name: /Choose environment/ }).click();
      }

      const question = page.locator(`[data-request-id="${QUESTION_ID}"]`);
      await expect(question.getByRole("heading", { name: "Choose environment" })).toBeVisible();
      const option = question.getByRole("radio", { name: "Staging" });
      const custom = question.getByPlaceholder("Optional custom answer");
      if (writable) {
        await expect(custom).toBeEnabled();
        await option.check();
        await question.getByRole("button", { name: "Submit answers" }).click();
        await expect(question).toHaveCount(0);
        expect(decisions).toEqual([
          { id: PERMISSION_ID, body: { approved: true } },
          { id: QUESTION_ID, body: { answers: { environment: ["Staging"] } } },
        ]);
      } else {
        await expect(question).toContainText(READ_ONLY);
        await expect(option).toBeDisabled();
        await expect(custom).toBeDisabled();
        await expect(question.getByRole("button", { name: /Submit answers|Cancel request/ })).toHaveCount(0);
        await expect(question.getByRole("button", { name: "Open Conversation", exact: true })).toBeEnabled();
        expect(decisions).toEqual([]);
      }
    });
  }
}

for (const action of ["Deny", "Cancel request"]) {
  test(`members can ${action.toLowerCase()}`, async ({ page }) => {
    const decisions = await mockApp(page, "member");
    await page.goto(surfaces.inbox);
    if (action === "Cancel request") await page.getByRole("button", { name: /Choose environment/ }).click();
    await page.getByTestId("interaction-card").getByRole("button", { name: action, exact: true }).click();
    await expect.poll(() => decisions).toEqual([action === "Deny"
      ? { id: PERMISSION_ID, body: { approved: false } }
      : { id: QUESTION_ID, body: { cancelled: true, note: "cancelled by user" } }]);
  });
}

test("viewers retain the existing decided state", async ({ page }) => {
  const decisions = await mockApp(page, "viewer", "approved");
  await page.goto(surfaces.inbox);
  await page.getByRole("tab", { name: "Decided", exact: true }).click();
  const card = page.getByTestId("interaction-card");
  await expect(card).toContainText("This approval has been handled");
  await expect(card).not.toContainText(READ_ONLY);
  expect(decisions).toEqual([]);
});

test("no decisions are available while workspace roles load", async ({ page }) => {
  let releaseRole!: (role: string) => void;
  const role = new Promise<string>((resolve) => { releaseRole = resolve; });
  const decisions = await mockApp(page, role);
  const workspaceRequest = page.waitForRequest("**/api/v1/me/workspaces");
  await page.goto(surfaces.inbox);
  await workspaceRequest;
  await expect(page.getByRole("main")).toBeVisible();
  await expect(page.getByTestId("interaction-card")).toHaveCount(0);
  expect(decisions).toEqual([]);
  releaseRole("member");
  await expect(page.getByRole("button", { name: "Allow once" })).toBeEnabled();
});

async function mockApp(page: Page, role: string | undefined | Promise<string>, status: AgentInteraction["status"] = "pending") {
  const decisions: Array<{ id: string; body: ResolveAgentInteractionRequest }> = [];
  const createdAt = new Date().toISOString();
  const permission: AgentInteraction = {
    id: PERMISSION_ID, request_id: PERMISSION_ID, workspace_id: WORKSPACE_ID,
    conversation_id: CONVERSATION_ID, agent_run_id: "00000000-0000-0000-0000-000000000015",
    kind: "permission", status, request: { resource: "Write configuration", payload: { path: "/config" } },
    response: {}, agent_name: "Test Agent", conversation_title: "Test Conversation",
    created_at: createdAt, updated_at: createdAt, expires_at: new Date(Date.now() + 3_600_000).toISOString(),
  };
  const question: AgentInteraction = {
    ...permission, id: QUESTION_ID, request_id: QUESTION_ID, kind: "user_choice",
    request: { questions: [{ id: "environment", question: "Choose environment", options: [{ label: "Staging" }] }] },
  };
  const interactions = status === "pending" ? [permission, question] : [permission];
  const conversation = {
    id: CONVERSATION_ID, workspace_id: WORKSPACE_ID, title: "Test Conversation", surface: "web",
    form: "thread", status: "active", metadata: {}, created_at: createdAt, updated_at: createdAt, message_count: 1,
  };
  await page.addInitScript(() => localStorage.setItem("i18nextLng", "en-US"));
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    if (path === "/api/v1/me") return json(route, { user_id: "user-1", email: "user@example.com", name: "User" });
    if (path === "/api/v1/me/workspaces") return json(route, { user_id: "user-1", workspaces: [
      { id: "00000000-0000-0000-0000-000000000099", name: "Other Workspace", slug: "other", role: "owner" },
      { id: WORKSPACE_ID, name: "Interaction Test", slug: "interaction-test", role: await role },
    ] });
    if (path === "/api/v1/me/discoverable-workspaces") return json(route, { workspaces: [], total: 0 });
    if (path === `/api/v1/workspaces/${WORKSPACE_ID}/agents`) return json(route, { agents: [] });
    if (path === `/api/v1/workspaces/${WORKSPACE_ID}/conversations`) return json(route, { conversations: [conversation] });
    if (path === `/api/v1/conversations/${CONVERSATION_ID}`) return json(route, conversation);
    if (path === `/api/v1/conversations/${CONVERSATION_ID}/timeline`) return json(route, {
      conversation_id: CONVERSATION_ID, agent_runs: [], messages: [{
        id: "message-1", conversation_id: CONVERSATION_ID, sender_type: "user", content: "Review configuration", created_at: createdAt,
      }],
    });
    if (path === `/api/v1/workspaces/${WORKSPACE_ID}/interactions`) return json(route, {
      interactions: interactions.filter((item) => url.searchParams.get("status") === "pending" ? item.status === "pending" : item.status !== "pending"),
    });
    const target = interactions.find((item) => path === `/api/v1/workspaces/${WORKSPACE_ID}/interactions/${item.id}/resolve`);
    if (target) {
      expect(route.request().method()).toBe("POST");
      const body = route.request().postDataJSON() as ResolveAgentInteractionRequest;
      decisions.push({ id: target.id, body });
      target.status = body.cancelled ? "cancelled" : body.answers ? "answered" : body.approved ? "approved" : "denied";
      return json(route, { interaction: target, applied: true, already_resolved: false });
    }
    return json(route, {});
  });
  return decisions;
}

async function json(route: Route, body: unknown) {
  await route.fulfill({ contentType: "application/json", body: JSON.stringify(body) });
}
