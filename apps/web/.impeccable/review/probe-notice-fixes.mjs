import { chromium } from "@playwright/test"
import path from "node:path"
const OUT = path.resolve(".impeccable/review")
const EXE = process.env.CHROME_EXE ?? `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })

async function open(theme = "light", lang = "zh-CN") {
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: lang })
  await ctx.addInitScript(([t, l]) => {
    localStorage.setItem("parsar.theme", t)
    localStorage.setItem("parsar.lang", l)
    localStorage.setItem("parsar.ws", "0f4d2c6e-9b1a-4e7c-8f3d-2a1b5c6d7e8f")
  }, [theme, lang])
  const page = await ctx.newPage()
  page.on("pageerror", (e) => console.log("  PAGEERROR:", e.message.slice(0, 160)))
  return { ctx, page }
}

// 1. success: the result of "run now" floats and the ledger does not move
{
  const { ctx, page } = await open()
  await page.route("**/scheduled-tasks/*/run**", (route) =>
    route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ run_id: "r-1" }) }))
  await page.goto("http://127.0.0.1:5174/?admin=scheduled", { waitUntil: "networkidle" })
  await page.waitForTimeout(600)
  const rowBefore = await page.locator('[role="list"] >> nth=0').boundingBox()
  const row = page.locator('[data-testid="scheduled-run-now"]').first()
  await row.scrollIntoViewIfNeeded()
  await row.hover()
  await row.click({ force: true })
  await page.waitForTimeout(500)
  const rowAfter = await page.locator('[role="list"] >> nth=0').boundingBox()
  const strip = page.getByText(/已触发一次运行|Triggered a run/).first()
  const box = await strip.boundingBox().catch(() => null)
  console.log("成功提示浮起:", await strip.count() > 0, "| 位置 y:", Math.round(box?.y ?? -1), "| 账本位移:", Math.round((rowAfter?.y ?? 0) - (rowBefore?.y ?? 0)), "px")
  await page.screenshot({ path: path.join(OUT, "notice-run-success.png") })
  await ctx.close()
}

// 2. failure: the server string arrives in its own block
{
  const { ctx, page } = await open()
  await page.route("**/scheduled-tasks/*/run**", (route) =>
    route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "internal", message: 'migration 0042: relation "agent_runs" already exists' }) }))
  await page.goto("http://127.0.0.1:5174/?admin=scheduled", { waitUntil: "networkidle" })
  await page.waitForTimeout(600)
  await page.locator('[data-testid="scheduled-run-now"]').first().click({ force: true })
  await page.waitForTimeout(600)
  const pre = page.locator("body > div pre").first()
  console.log("失败提示带机器块:", await pre.count() > 0, "|", (await pre.textContent().catch(() => ""))?.slice(0, 60))
  await page.screenshot({ path: path.join(OUT, "notice-run-error.png") })
  await ctx.close()
}

// 3. a load failure: ErrorState's detail block
{
  const { ctx, page } = await open()
  await page.route("**/scheduled-tasks?**", (route) =>
    route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "internal", message: 'migration 0042: relation "agent_runs" already exists' }) }))
  await page.goto("http://127.0.0.1:5174/?admin=scheduled", { waitUntil: "networkidle" })
  await page.waitForTimeout(700)
  const pre = page.locator("main pre, [role=main] pre").first()
  console.log("加载失败带机器块:", await pre.count() > 0, "|", (await pre.textContent().catch(() => ""))?.slice(0, 60))
  await page.screenshot({ path: path.join(OUT, "notice-load-error.png") })
  await ctx.close()
}
await browser.close()
