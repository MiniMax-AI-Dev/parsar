import { expect, test, type Page, type Route } from "@playwright/test";

const WORKSPACE_ID = "00000000-0000-0000-0000-000000000011";
const CONVERSATION_ID = "00000000-0000-0000-0000-000000000012";
const AGENT_ID = "00000000-0000-0000-0000-000000000013";
const DRAFT = "Keep this draft after a failed send";
type Surface = "existing" | "empty" | "new";
type Failure = "create" | "message" | "network";

const cases: Array<{ surface: Surface; failure: Failure }> = [
  { surface: "existing", failure: "message" },
  { surface: "existing", failure: "network" },
  { surface: "empty", failure: "message" },
  { surface: "new", failure: "create" },
  { surface: "new", failure: "message" },
];

for (const { surface, failure } of cases) {
  test(`${surface} conversation shows ${failure} failure and allows another send`, async ({ page }) => {
    const state = await mockApp(page, surface, failure);
    const pageErrors: string[] = [];
    page.on("pageerror", (error) => pageErrors.push(error.message));
    await page.goto(`/?admin=conversations&ws=${WORKSPACE_ID}&${surface === "new" ? "focus=compose" : `id=${CONVERSATION_ID}`}`);
    const composer = page.locator("form").filter({ has: page.getByRole("button", { name: "send", exact: true }) });
    const input = composer.getByRole("textbox");
    const send = composer.getByRole("button", { name: "send", exact: true });
    await input.fill(DRAFT);
    await send.click();

    const error = composer.getByText(failure === "network" ? "Failed to fetch" : `${failure} rejected`, { exact: true });
    await expect(error).toBeVisible();
    await expect(input).toHaveValue(DRAFT);
    await expect(send).toBeEnabled();
    expect(state.sent).toEqual([]);
    if (failure === "create") expect(state.messageRequests).toBe(0);
    expect(pageErrors).toEqual([]);

    state.failure = null;
    let releaseSend!: () => void;
    state.waitForSend = new Promise<void>((resolve) => { releaseSend = resolve; });
    await send.click();
    await expect(error).toHaveCount(0);
    await expect(send).toBeDisabled();
    await expect(input).toHaveValue(DRAFT);
    releaseSend();
    await expect.poll(() => state.sent).toEqual([DRAFT]);
    await expect(input).toHaveValue("");
    await expect(page.getByText(DRAFT, { exact: true })).toBeVisible();
    expect(pageErrors).toEqual([]);
  });
}

test("send errors can be dismissed without losing the draft", async ({ page }) => {
  await mockApp(page, "existing", "message");
  await page.goto(`/?admin=conversations&ws=${WORKSPACE_ID}&id=${CONVERSATION_ID}`);
  const composer = page.locator("form");
  await composer.getByRole("textbox").fill(DRAFT);
  await composer.getByRole("button", { name: "send", exact: true }).click();
  await expect(composer.getByText("message rejected", { exact: true })).toBeVisible();
  await composer.getByRole("button", { name: "×", exact: true }).click();
  await expect(composer.getByText("message rejected", { exact: true })).toHaveCount(0);
  await expect(composer.getByRole("textbox")).toHaveValue(DRAFT);
});

test("long send errors stay within the composer", async ({ page }) => {
  await mockApp(page, "existing", "message");
  const message = "unbroken-error-".repeat(100);
  await page.route(`**/api/v1/conversations/${CONVERSATION_ID}/messages`, (route) =>
    json(route, { error: "rejected", message }, 422));
  await page.goto(`/?admin=conversations&ws=${WORKSPACE_ID}&id=${CONVERSATION_ID}`);
  const composer = page.locator("form");
  await composer.getByRole("textbox").fill(DRAFT);
  await composer.getByRole("button", { name: "send", exact: true }).click();
  const alert = composer.getByRole("alert");
  await expect(alert).toHaveText(`${message}×`);
  expect(await alert.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
});

for (const response of [{ agent_run_id: "run-1" }, { run_ids: ["run-1"] }]) {
  test(`run start failures remain separate from sends with ${Object.keys(response)[0]}`, async ({ page }) => {
    const state = await mockApp(page, "existing", "message");
    await page.route(`**/api/v1/conversations/${CONVERSATION_ID}/messages`, (route) => {
      state.sent.push(route.request().postDataJSON().content);
      return json(route, { message_id: "message-new", ...response }, 201);
    });
    await page.route("**/runs/run-1/start", (route) => json(route, { error: "unavailable", message: "run start rejected" }, 503));
    await page.route("**/runs/run-1/stream", (route) => route.fulfill({
      contentType: "text/event-stream", body: 'event: done\ndata: {"final":{"content":""}}\n\n',
    }));
    await page.goto(`/?admin=conversations&ws=${WORKSPACE_ID}&id=${CONVERSATION_ID}`);
    const composer = page.locator("form");
    await composer.getByRole("textbox").fill(DRAFT);
    await composer.getByRole("button", { name: "send", exact: true }).click();
    await expect(page.getByRole("alert")).toHaveText("run start rejected×");
    await expect(composer.getByRole("alert")).toHaveCount(0);
    await expect(composer.getByRole("textbox")).toHaveValue("");
    await expect(page.getByText(DRAFT, { exact: true })).toBeVisible();
    expect(state.sent).toEqual([DRAFT]);
  });
}

async function mockApp(page: Page, surface: Surface, failure: Failure) {
  const state = { failure: failure as Failure | null, sent: [] as string[], messageRequests: 0, waitForSend: Promise.resolve() };
  const createdAt = new Date().toISOString();
  let creates = 0;
  let conversation = {
    id: CONVERSATION_ID, workspace_id: WORKSPACE_ID, title: "Test Conversation", surface: "web",
    form: "thread", status: "active", metadata: {}, primary_agent_id: AGENT_ID,
    created_at: createdAt, updated_at: createdAt, message_count: surface === "existing" ? 1 : 0,
  };
  await page.addInitScript(() => localStorage.setItem("i18nextLng", "en-US"));
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    const method = route.request().method();
    if (path === "/api/v1/me") return json(route, { user_id: "user-1", email: "user@example.com", name: "User" });
    if (path === "/api/v1/me/workspaces") return json(route, { user_id: "user-1", workspaces: [
      { id: WORKSPACE_ID, name: "Send Test", slug: "send-test", role: "owner" },
    ] });
    if (path === "/api/v1/me/discoverable-workspaces") return json(route, { workspaces: [], total: 0 });
    if (path === `/api/v1/workspaces/${WORKSPACE_ID}/agents`) return json(route, { agents: [
      { id: AGENT_ID, workspace_id: WORKSPACE_ID, name: "Test Agent", status: "active", connector_type: "http_agent", config: {} },
    ] });
    if (path === `/api/v1/workspaces/${WORKSPACE_ID}/conversations`) {
      if (method !== "POST") return json(route, { conversations: surface === "new" && !creates ? [] : [conversation] });
      if (state.failure === "create") return json(route, { error: "server_unreachable", message: "create rejected" }, 503);
      creates++;
      conversation = { ...conversation, id: `00000000-0000-0000-0000-${String(100 + creates).padStart(12, "0")}` };
      return json(route, conversation, 201);
    }
    if (path === `/api/v1/conversations/${conversation.id}`) return json(route, conversation);
    if (path === `/api/v1/conversations/${conversation.id}/messages`) {
      state.messageRequests++;
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

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
}
