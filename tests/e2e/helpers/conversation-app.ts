import { expect, type Page, type Route } from "@playwright/test";

export const WORKSPACE_ID = "00000000-0000-0000-0000-000000000011";
export const CONVERSATION_ID = "00000000-0000-0000-0000-000000000012";
export const AGENT_ID = "00000000-0000-0000-0000-000000000013";
export const OTHER_CONVERSATION_ID = "00000000-0000-0000-0000-000000000014";
export const OTHER_AGENT_ID = "00000000-0000-0000-0000-000000000015";
export const OTHER_WORKSPACE_ID = "00000000-0000-0000-0000-000000000016";
export type Surface = "existing" | "empty" | "new";
export type Failure = "create" | "message" | "network";
export async function mockApp(page: Page, surface: Surface, failure: Failure | null) {
  const state = {
    failure: failure as Failure | null, sent: [] as string[], messageRequests: 0,
    createRequests: 0, messageTargets: [] as string[],
    waitForSend: Promise.resolve(), waitForCreate: Promise.resolve(),
    role: "owner" as string | undefined, otherRole: "owner" as string | undefined,
    conversationWorkspaceId: WORKSPACE_ID, waitForRole: Promise.resolve(), writes: [] as string[],
  };
  const createdAt = new Date().toISOString();
  let creates = 0;
  let deleted = false;
  let conversation = {
    id: CONVERSATION_ID, workspace_id: WORKSPACE_ID, title: "Test Conversation", surface: "web",
    form: "thread", status: "active", metadata: {}, primary_agent_id: AGENT_ID,
    created_at: createdAt, updated_at: createdAt, message_count: surface === "existing" ? 1 : 0,
  };
  await page.addInitScript(() => localStorage.setItem("i18nextLng", "en-US"));
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    const method = route.request().method();
    if (method !== "GET") state.writes.push(`${method} ${path}`);
    if (path === "/api/v1/me") return json(route, { user_id: "user-1", email: "user@example.com", name: "User" });
    if (path === "/api/v1/me/workspaces") {
      await state.waitForRole;
      return json(route, { user_id: "user-1", workspaces: [
        { id: WORKSPACE_ID, name: "Send Test", slug: "send-test", role: state.role },
        { id: OTHER_WORKSPACE_ID, name: "Other Workspace", slug: "other-workspace", role: state.otherRole },
      ] });
    }
    if (path === "/api/v1/me/discoverable-workspaces") return json(route, { workspaces: [], total: 0 });
    if (path === `/api/v1/workspaces/${WORKSPACE_ID}/agents`) return json(route, { agents: [
      { id: AGENT_ID, workspace_id: WORKSPACE_ID, name: "Test Agent", status: "active", connector_type: "http_agent", config: {} },
      { id: OTHER_AGENT_ID, workspace_id: WORKSPACE_ID, name: "Other Agent", status: "active", connector_type: "http_agent", config: {} },
    ] });
    const otherConversation = { ...conversation, id: OTHER_CONVERSATION_ID, title: "Other Conversation", message_count: 1 };
    if (path === `/api/v1/workspaces/${WORKSPACE_ID}/conversations`) {
      if (method !== "POST") return json(route, { conversations: surface === "new" && !creates ? [] : [...(deleted ? [] : [conversation]), otherConversation] });
      state.createRequests++;
      await state.waitForCreate;
      if (state.failure === "create") return json(route, { error: "server_unreachable", message: "create rejected" }, 503);
      creates++;
      deleted = false;
      conversation = { ...conversation, id: `00000000-0000-0000-0000-${String(100 + creates).padStart(12, "0")}` };
      return json(route, conversation, 201);
    }
    if (path === `/api/v1/conversations/${OTHER_CONVERSATION_ID}`) return json(route, otherConversation);
    if (path === `/api/v1/conversations/${OTHER_CONVERSATION_ID}/timeline`) return json(route, {
      conversation_id: OTHER_CONVERSATION_ID, messages: [], agent_runs: [],
    });
    if (path === `/api/v1/conversations/${conversation.id}`) {
      if (method === "DELETE") {
        deleted = true;
        return route.fulfill({ status: 204 });
      }
      if (deleted) return json(route, { error: "not_found" }, 404);
      if (method === "PATCH") conversation.title = route.request().postDataJSON().title;
      return json(route, { ...conversation, workspace_id: state.conversationWorkspaceId });
    }
    if (path === `/api/v1/conversations/${conversation.id}/messages`) {
      state.messageRequests++;
      state.messageTargets.push(conversation.id);
      expect(method).toBe("POST");
      if (state.failure === "network") return route.abort("failed");
      if (state.failure === "message") return json(route, { error: "invalid_content", message: "message rejected" }, 422);
      await state.waitForSend;
      state.sent.push(route.request().postDataJSON().content);
      conversation.message_count++;
      return json(route, { message_id: "message-new" }, 201);
    }
    if (path === `/api/v1/conversations/${conversation.id}/timeline`) return json(route, {
      conversation_id: conversation.id, agent_runs: [], messages: [
        ...(surface === "existing" ? ["Earlier message"] : []), ...state.sent,
      ].map((content, index) => ({ id: `message-${index}`, conversation_id: conversation.id, sender_type: "user", content, created_at: createdAt })),
    });
    if (path === `/api/v1/workspaces/${WORKSPACE_ID}/interactions`) return json(route, { interactions: [] });
    return json(route, {});
  });
  return state;
}

export async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
}
