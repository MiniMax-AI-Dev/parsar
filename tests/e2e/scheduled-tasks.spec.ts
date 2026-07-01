import { test, expect } from "@playwright/test"

// Opt-in: needs a running `make dev` stack (web + server + Postgres), an
// authenticated session, and PARSAR_E2E_SCHEDULED_URL pointing at the
// standalone 定时任务 page (协作现场 → 定时任务) for a workspace that already has
// at least one enabled agent. Run with:
//   PARSAR_E2E_SCHEDULED=1 PARSAR_E2E_SCHEDULED_URL='/admin?admin=scheduled' \
//     pnpm --dir tests/e2e exec playwright test scheduled-tasks.spec.ts
const ENABLED = process.env.PARSAR_E2E_SCHEDULED === "1"
const SCHEDULED_URL = process.env.PARSAR_E2E_SCHEDULED_URL ?? "/admin?admin=scheduled"

test.skip(!ENABLED, "set PARSAR_E2E_SCHEDULED=1 to run")

test("create a scheduled task with an agent, run it now, see it in the list", async ({ page }) => {
  const name = `e2e-sched-${Date.now()}`

  await page.goto(SCHEDULED_URL)

  await page.getByTestId("scheduled-new").click()
  await page.getByTestId("scheduled-name").fill(name)
  // Standalone page picks the executing agent in the dialog; default is the
  // first active agent, select it explicitly to exercise the dropdown.
  await page.getByTestId("scheduled-agent").selectOption({ index: 0 })
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
