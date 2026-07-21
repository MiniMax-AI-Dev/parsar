import { expect, test } from "@playwright/test";

// Opt-in: requires the local stack and the deterministic interaction preview
// fixture. The test drives the real API and verifies both inline card types,
// answer state, refresh recovery, and the workspace-wide Inbox surface.
//
//   psql "$DATABASE_URL" -f /private/tmp/parsar_preview_interactions.sql
//   PARSAR_E2E_INTERACTIONS=1 pnpm exec playwright test \
//     --config tests/e2e/playwright.config.ts tests/e2e/interaction-cards.spec.ts
const ENABLED = process.env.PARSAR_E2E_INTERACTIONS === "1";
const INTERACTIONS_URL =
  process.env.PARSAR_E2E_INTERACTIONS_URL ??
  "/?admin=conversations&id=00000000-0000-0000-0000-000000000012&ws=00000000-0000-0000-0000-000000000002";

test.skip(!ENABLED, "set PARSAR_E2E_INTERACTIONS=1 to run");

test.use({
  extraHTTPHeaders: {
    "X-Parsar-Dev-User-ID": "00000000-0000-0000-0000-000000000001",
  },
});

test("inline approval and question cards survive refresh and remain available in Inbox", async ({
  page,
}) => {
  await page.goto(INTERACTIONS_URL);

  const cards = page.getByTestId("interaction-card");
  await expect(cards).toHaveCount(2);

  const permission = page.locator('[data-request-id="preview-permission-001"]');
  await expect(permission).toContainText("Write production configuration");
  await expect(
    permission.getByRole("button", { name: "Allow once" }),
  ).toBeEnabled();
  await expect(permission.getByRole("button", { name: "Deny" })).toBeEnabled();

  const question = page.locator('[data-request-id="preview-question-001"]');
  const submit = question.getByRole("button", { name: "Submit answers" });
  await expect(question).toContainText(
    "Which environment should the Agent deploy to?",
  );
  await expect(submit).toBeDisabled();
  await question.getByRole("radio", { name: /Staging/ }).check();
  await question.getByRole("checkbox", { name: /Smoke tests/ }).check();
  await expect(submit).toBeEnabled();

  await page.reload();
  await expect(page.getByTestId("interaction-card")).toHaveCount(2);

  await page.getByRole("button", { name: "View all in Inbox" }).click();
  await expect(
    page.getByRole("heading", { name: "Inbox / Approvals" }),
  ).toBeVisible();
  await expect(page.getByTestId("interaction-card")).toHaveCount(1);
  await expect(page.getByTestId("interaction-card")).toContainText(
    /Write production configuration|Which environment should the Agent deploy to\?/,
  );
});
