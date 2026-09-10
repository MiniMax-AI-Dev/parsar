import { expect, test, type Page } from "@playwright/test";
import { CONVERSATION_ID, json, mockApp, WORKSPACE_ID } from "./helpers/conversation-app";

const base = {
  id: "saved-question", request_id: "saved-request", workspace_id: WORKSPACE_ID,
  conversation_id: CONVERSATION_ID, agent_run_id: "run-1", kind: "user_choice",
  agent_name: "Test Agent", conversation_title: "Test Conversation",
  created_at: "2026-09-10T00:00:00Z", expires_at: "2099-01-01T00:00:00Z",
};
const language = {
  id: "language", question: "Choose language", is_other: false,
  options: [{ label: "English" }, { label: "Chinese" }],
};

for (const surface of ["inbox", "conversation"] as const) {
  const url = surface === "inbox"
    ? `/?ws=${WORKSPACE_ID}&admin=approvals&id=${base.id}`
    : `/?ws=${WORKSPACE_ID}&admin=conversations&id=${CONVERSATION_ID}`;

  async function setup(page: Page, row: Record<string, unknown>) {
    await mockApp(page, "existing", null);
    await page.addInitScript(() => localStorage.setItem("parsar.lang", "en-US"));
    const state = { row, writes: [] as unknown[] };
    await page.route("**/api/v1/workspaces/*/interactions**", (route) => {
      if (route.request().method() === "POST") {
        state.writes.push(route.request().postDataJSON());
        state.row = { ...state.row, status: "answered", response: { answers: { language: ["Chinese"] } } };
        return json(route, { interaction: state.row, applied: false, already_resolved: true });
      }
      const bucket = new URL(route.request().url()).searchParams.get("status");
      // The conversation normally removes completed cards. Keep this fixture
      // visible to exercise the shared card's answered-state rendering only.
      const visible = surface === "conversation" ? bucket === "pending"
        : bucket === (state.row.status === "pending" ? "pending" : "decided");
      return json(route, { interactions: visible ? [state.row] : [] });
    });
    await page.goto(url);
    return state;
  }

  test(`${surface}: persisted choices, custom answers and secret masking survive reload`, async ({ page }) => {
    await setup(page, { ...base, status: "answered", request: { questions: [language,
      { id: "checks", question: "Select checks", multi_select: true, options: [{ label: "Smoke" }, { label: "Audit" }] },
      { question: "Additional note", options: [] },
      { id: "secret", question: "Access token", is_secret: true, options: [] },
    ] }, response: { answers: { language: ["English"], checks: ["Smoke", "Audit", "Check logs", "Email team"], q2: ["Bring a laptop"], secret: ["synthetic-only"] } } });
    for (let attempt = 0; attempt < 2; attempt++) {
      const card = page.getByTestId("interaction-card");
      await expect(card.getByRole("radio", { name: "English", exact: true })).toBeChecked();
      await expect(card.getByRole("radio", { name: "Chinese", exact: true })).not.toBeChecked();
      for (const name of ["Smoke", "Audit"]) await expect(card.getByRole("checkbox", { name, exact: true })).toBeChecked();
      await expect(card.locator('input[type="text"]').nth(0)).toHaveValue("Check logs, Email team");
      await expect(card.locator('input[type="text"]').nth(1)).toHaveValue("Bring a laptop");
      const secret = card.locator('input[type="password"]');
      await expect(secret).toHaveValue("synthetic-only");
      await expect(secret).toBeDisabled();
      await expect(card.getByRole("radio", { name: "English", exact: true })).toBeDisabled();
      await expect(card.getByRole("button", { name: "Submit answers" })).toHaveCount(0);
      await page.reload();
    }
  });

  test(`${surface}: completed server answer replaces the local draft without changing submission`, async ({ page }) => {
    const state = await setup(page, { ...base, status: "pending", request: { questions: [language] }, response: {} });
    const card = page.getByTestId("interaction-card");
    await card.getByRole("radio", { name: "English", exact: true }).check();
    await card.getByRole("button", { name: "Submit answers" }).click();
    await expect(card.getByRole("radio", { name: "Chinese", exact: true })).toBeChecked();
    await expect(card.getByRole("radio", { name: "English", exact: true })).not.toBeChecked();
    await expect(card.getByRole("radio", { name: "Chinese", exact: true })).toBeDisabled();
    expect(state.writes).toEqual([{ answers: { language: ["English"] } }]);
  });
}
