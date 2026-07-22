import { execFileSync } from "node:child_process";
import { resolve } from "node:path";

import { expect, test } from "@playwright/test";

// Requires the local stack. The committed deterministic fixture lets the test
// drive the real API and verify inline cards, isolation
// between repeated question ids, secret/custom input semantics, resolve error
// recovery, refresh recovery, and the workspace-wide Inbox surface.
//
//   pnpm exec playwright test \
//     --config tests/e2e/playwright.config.ts tests/e2e/interaction-cards.spec.ts
const INTERACTIONS_URL =
  process.env.PARSAR_E2E_INTERACTIONS_URL ??
  "/?admin=conversations&id=00000000-0000-0000-0000-000000000012&ws=00000000-0000-0000-0000-000000000002";

const REPO_ROOT = resolve(__dirname, "..", "..");

test.beforeAll(() => {
  execFileSync(
    resolve(REPO_ROOT, "scripts", "with-dev-env.sh"),
    [
      "bash",
      "-c",
      'psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f "$1"',
      "parsar-interaction-fixture",
      resolve(REPO_ROOT, "tests", "e2e", "fixtures", "interaction-cards.sql"),
    ],
    { cwd: REPO_ROOT, stdio: "inherit" },
  );
});

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
  await expect(cards).toHaveCount(3);

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
  await expect(question.getByPlaceholder("Custom answer")).toHaveCount(0);

  const secret = page.locator('[data-request-id="preview-secret-001"]');
  await secret.getByRole("radio", { name: /Use stored token/ }).check();
  await expect(question.getByRole("radio", { name: /Staging/ })).toBeChecked();
  const secretInput = secret.getByPlaceholder("Custom answer");
  await expect(secretInput).toHaveAttribute("type", "password");
  await secretInput.fill("not-persisted-test-secret");
  await expect(
    secret.getByRole("button", { name: "Submit answers" }),
  ).toBeEnabled();

  // The fixture intentionally has no live daemon. A real resolve POST must
  // surface a retryable runtime error and leave the durable card pending.
  await permission.getByRole("button", { name: "Deny" }).click();
  await expect(permission).toContainText(/runtime|unavailable|pending/i);

  await page.reload();
  await expect(page.getByTestId("interaction-card")).toHaveCount(3);

  await page.getByRole("button", { name: "View all in Inbox" }).click();
  await expect(
    page.getByRole("heading", { name: "Inbox / Approvals" }),
  ).toBeVisible();
  await expect(page.getByTestId("interaction-card")).toHaveCount(1);
  await expect(page.getByTestId("interaction-card")).toContainText(
    /Write production configuration|Which environment should the Agent deploy to\?/,
  );
});
