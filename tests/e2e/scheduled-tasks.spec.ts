import { test, expect } from "@playwright/test"

// Opt-in: needs a running `make dev` stack (web + server + Postgres) reachable
// at PARSAR_E2E_WEB_URL, an authenticated session, and PARSAR_E2E_AGENT_URL
// pointing at an agent detail page. Run with:
//   PARSAR_E2E_SCHEDULED=1 PARSAR_E2E_AGENT_URL='/admin?admin=agents&id=<paID>' \
//     pnpm --dir tests/e2e exec playwright test scheduled-tasks.spec.ts
const ENABLED = process.env.PARSAR_E2E_SCHEDULED === "1"
const AGENT_URL = process.env.PARSAR_E2E_AGENT_URL ?? ""

test.skip(!ENABLED || !AGENT_URL, "set PARSAR_E2E_SCHEDULED=1 and PARSAR_E2E_AGENT_URL to run")

test("create a scheduled task, run it now, see it in the list", async ({ page }) => {
  const name = `e2e-sched-${Date.now()}`

  await page.goto(AGENT_URL)

  // Open the Scheduled tab (label is i18n; the tab value is stable).
  await page.getByRole("tab", { name: /Scheduled|定时任务/ }).click()

  await page.getByTestId("scheduled-new").click()
  await page.getByTestId("scheduled-name").fill(name)
  await page.getByTestId("scheduled-prompt").fill("e2e smoke prompt")
  await page.getByTestId("scheduled-freq").selectOption("custom")
  await page.getByTestId("scheduled-cron").fill("0 9 * * *")
  await page.getByTestId("scheduled-save").click()

  const row = page.locator(`[data-testid="scheduled-row"][data-task-name="${name}"]`)
  await expect(row).toBeVisible()

  // Run-now returns 202; the click should not surface an error banner.
  await row.getByTestId("scheduled-run-now").click()
  await expect(page.getByText(/Triggered a run|已触发一次运行/)).toBeVisible()
})
