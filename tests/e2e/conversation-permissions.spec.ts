import { expect, test, type Page } from "@playwright/test";
import {
  mockApp, json, WORKSPACE_ID, OTHER_WORKSPACE_ID, CONVERSATION_ID, OTHER_CONVERSATION_ID, OTHER_AGENT_ID,
} from "./helpers/conversation-app";

const READ_ONLY = "Read-only access. You cannot send messages.";
const RUN_ID = "00000000-0000-0000-0000-000000000021";
const cancelActions = ["Cancel all", "Cancel current task", "Stop generating"];

for (const role of ["owner", "admin", "member", "viewer", undefined]) {
  for (const surface of ["existing", "empty", "new"] as const) {
    test(`${surface} conversation permissions for ${role ?? "unknown role"}`, async ({ page }) => {
      const state = await mockApp(page, surface, null);
      state.role = role;
      if (surface === "existing") await mockActiveRun(page);
      await page.goto(`/?admin=conversations&ws=${WORKSPACE_ID}&${surface === "new" ? "focus=compose" : `id=${CONVERSATION_ID}`}`);
      await expect(page.getByRole("button", { name: "Switch workspace", exact: true })).toHaveText("Send Test");
      const input = page.locator("form").getByRole("textbox");
      const create = page.getByRole("button", { name: "New conversation", exact: true });
      const writable = role === "owner" || role === "admin" || role === "member";
      const deletable = role === "owner" || role === "admin";

      if (surface === "existing") {
        await expect(page.getByText("Earlier message", { exact: true })).toBeVisible();
        await expect(page.getByRole("button", { name: "Expand execution trace", exact: true })).toBeEnabled();
        for (const name of cancelActions) {
          const cancel = page.getByRole("button", { name, exact: true });
          if (writable) await expect(cancel).toBeEnabled();
          else await expect(cancel).toHaveCount(0);
        }
      }
      if (!writable) {
        await expect(input).toBeDisabled();
        await expect(create).toBeDisabled();
        await expect(page.locator("form")).toContainText(READ_ONLY);
        await expect(page.getByRole("button", { name: /^(Rename|Delete) conversation$/ })).toHaveCount(0);
        await page.locator("form").dispatchEvent("submit");
        if (surface !== "new") {
          await page.getByText("Other Conversation", { exact: true }).click();
          await expect(page).toHaveURL(new RegExp(`id=${OTHER_CONVERSATION_ID}`));
        }
        expect(state.writes).toEqual([]);
        return;
      }

      await expect(input).toBeEnabled();
      await expect(create).toBeEnabled();
      await input.fill("Allowed message");
      await page.getByRole("button", { name: "send", exact: true }).click();
      await expect.poll(() => state.sent).toEqual(["Allowed message"]);
      await expect(input).toHaveValue("");
      await page.getByRole("button", { name: "Rename conversation", exact: true }).first().click();
      const rename = page.getByRole("textbox", { name: "Rename conversation", exact: true });
      await rename.fill("Renamed conversation");
      await rename.press("Enter");
      await expect(page.getByText("Renamed conversation", { exact: true })).toBeVisible();
      const remove = page.getByRole("button", { name: "Delete conversation", exact: true });
      if (deletable) {
        await remove.first().click();
        await page.getByRole("dialog").getByRole("button", { name: "Delete", exact: true }).click();
        await expect(page.getByText("Renamed conversation", { exact: true })).toHaveCount(0);
        expect(state.writes.some((request) => request.startsWith("DELETE "))).toBe(true);
      } else {
        await expect(remove).toHaveCount(0);
        expect(state.writes.some((request) => request.startsWith("DELETE "))).toBe(false);
      }
    });
  }
}

for (const name of cancelActions) {
  test(`members can use ${name}`, async ({ page }) => {
    const state = await mockApp(page, "existing", null);
    state.role = "member";
    await mockActiveRun(page);
    await page.goto(`/?admin=conversations&ws=${WORKSPACE_ID}&id=${CONVERSATION_ID}`);
    await page.getByRole("button", { name, exact: true }).click();
    await expect.poll(() => state.writes).toEqual([name === "Cancel all"
      ? `POST /api/v1/conversations/${CONVERSATION_ID}/cancel-all`
      : `POST /api/v1/agent-runs/${RUN_ID}/cancel`]);
  });
}

test("conversation writes are unavailable while workspace roles load", async ({ page }) => {
  const state = await mockApp(page, "new", null);
  let releaseRole!: () => void;
  state.waitForRole = new Promise<void>((resolve) => { releaseRole = resolve; });
  const roles = page.waitForRequest("**/api/v1/me/workspaces");
  await page.goto(`/?admin=conversations&ws=${WORKSPACE_ID}&focus=compose`);
  await roles;
  await expect(page.getByRole("main")).toBeVisible();
  const input = page.locator("form").getByRole("textbox");
  await expect(input).toHaveCount(0);
  await expect(page.getByRole("button", { name: "New conversation", exact: true })).toHaveCount(0);
  expect(state.writes).toEqual([]);
  releaseRole();
  await expect(input).toBeEnabled();
});

test("conversation writes stay disabled when workspace roles cannot be fetched", async ({ page }) => {
  const state = await mockApp(page, "new", null);
  await page.route("**/api/v1/me/workspaces", (route) => json(route, { error: "unavailable" }, 503));
  await page.goto(`/?admin=conversations&ws=${WORKSPACE_ID}&focus=compose`);
  await expect(page.locator("form")).toContainText(READ_ONLY);
  await expect(page.locator("form").getByRole("textbox")).toBeDisabled();
  await expect(page.getByRole("button", { name: "New conversation", exact: true })).toBeDisabled();
  expect(state.writes).toEqual([]);
});

for (const action of ["Rename", "Delete"]) {
  test(`losing permission closes the pending ${action.toLowerCase()}`, async ({ page }) => {
    const state = await mockApp(page, "existing", null);
    await page.goto(`/?admin=conversations&ws=${WORKSPACE_ID}&id=${CONVERSATION_ID}`);
    await page.getByRole("button", { name: `${action} conversation`, exact: true }).first().click();
    const pending = action === "Delete" ? page.getByRole("dialog") : page.getByRole("textbox", { name: "Rename conversation" });
    await expect(pending).toBeVisible();
    state.role = "viewer";
    const roles = page.waitForResponse("**/api/v1/me/workspaces");
    await page.evaluate(() => window.dispatchEvent(new Event("visibilitychange")));
    await roles;
    await expect(pending).toHaveCount(0);
    await expect(page.locator("form").getByRole("textbox")).toBeDisabled();
    expect(state.writes).toEqual([]);
  });
}

test("workspace navigation discards a pending delete target", async ({ page }) => {
  const state = await mockApp(page, "new", null);
  const conversation = {
    id: OTHER_CONVERSATION_ID, workspace_id: OTHER_WORKSPACE_ID, title: "Other Conversation",
    primary_agent_id: OTHER_AGENT_ID, surface: "web", form: "thread", status: "active",
    metadata: {}, message_count: 1, created_at: new Date().toISOString(), updated_at: new Date().toISOString(),
  };
  await page.route((url) => url.pathname.startsWith(`/api/v1/workspaces/${OTHER_WORKSPACE_ID}/`), (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/conversations")) return json(route, { conversations: [conversation] });
    if (path.endsWith("/agents")) return json(route, { agents: [{
      id: OTHER_AGENT_ID, workspace_id: OTHER_WORKSPACE_ID, name: "Other Agent", status: "active", connector_type: "http_agent", config: {},
    }] });
    return route.fallback();
  });
  await page.route(`**/api/v1/conversations/${OTHER_CONVERSATION_ID}`, (route) => {
    if (route.request().method() === "DELETE") state.writes.push(`DELETE /api/v1/conversations/${OTHER_CONVERSATION_ID}`);
    return json(route, conversation);
  });
  await page.goto(`/?admin=conversations&ws=${WORKSPACE_ID}&focus=compose`);
  await expect(page.locator("form").getByRole("textbox")).toBeEnabled();
  await page.evaluate((url) => {
    window.history.pushState({}, "", url);
    window.dispatchEvent(new PopStateEvent("popstate"));
  }, `/?admin=conversations&ws=${OTHER_WORKSPACE_ID}&id=${OTHER_CONVERSATION_ID}`);
  await page.getByRole("button", { name: "Delete conversation", exact: true }).click();
  await expect(page.getByRole("dialog")).toBeVisible();
  await page.goBack();
  await expect(page).toHaveURL(new RegExp(`ws=${WORKSPACE_ID}`));
  state.otherRole = "viewer";
  const roles = page.waitForResponse("**/api/v1/me/workspaces");
  await page.evaluate(() => window.dispatchEvent(new Event("visibilitychange")));
  await roles;
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.locator("form").getByRole("textbox")).toBeEnabled();
  expect(state.writes).toEqual([]);
});

for (const [role, conversationRole] of [["owner", "viewer"], ["viewer", "owner"], ["owner", undefined]]) {
  test(`conversation actions use its own workspace role ${conversationRole ?? "unknown"}, not URL role ${role}`, async ({ page }) => {
    const state = await mockApp(page, "existing", null);
    state.role = role;
    state.otherRole = conversationRole;
    state.conversationWorkspaceId = OTHER_WORKSPACE_ID;
    await mockActiveRun(page);
    await page.goto(`/?admin=conversations&ws=${WORKSPACE_ID}&id=${CONVERSATION_ID}`);
    const input = page.locator("form").getByRole("textbox");
    if (conversationRole === "owner") {
      await expect(input).toBeEnabled();
      await input.fill("Allowed in this conversation");
      await page.getByRole("button", { name: "send", exact: true }).click();
      await expect.poll(() => state.sent).toEqual(["Allowed in this conversation"]);
    } else {
      await expect(page.getByRole("button", { name: "Expand execution trace", exact: true })).toBeEnabled();
      await expect(input).toBeDisabled();
      for (const name of cancelActions) await expect(page.getByRole("button", { name, exact: true })).toHaveCount(0);
      expect(state.writes).toEqual([]);
    }
  });
}

async function mockActiveRun(page: Page) {
  await page.route(`**/api/v1/conversations/${CONVERSATION_ID}/timeline?*`, (route) => json(route, {
    conversation_id: CONVERSATION_ID,
    messages: [{ id: "message-1", conversation_id: CONVERSATION_ID, sender_type: "user", content: "Earlier message", created_at: new Date().toISOString() }],
    agent_runs: [{ id: RUN_ID, status: "running", created_at: new Date().toISOString(), steps: [] }],
  }));
  await page.addInitScript(() => {
    class MockEventSource extends EventTarget {
      private timer: ReturnType<typeof setTimeout>;
      constructor() {
        super();
        this.timer = setTimeout(() => {
          this.dispatchEvent(new Event("open"));
          this.dispatchEvent(new MessageEvent("tool", { data: JSON.stringify({
            tool: { id: "tool-1", name: "read", stage: "before", args: { path: "README.md" } },
          }) }));
        }, 0);
      }
      close() { clearTimeout(this.timer); }
    }
    window.EventSource = MockEventSource as unknown as typeof EventSource;
  });
}
