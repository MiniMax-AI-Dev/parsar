// The findings from both reviews, checked in a real browser.
import { chromium } from "@playwright/test"
import path from "node:path"
const OUT = path.resolve(".impeccable/review")
const EXE = process.env.CHROME_EXE ?? `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
async function open(view) {
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN" })
  await ctx.addInitScript(() => {
    localStorage.setItem("parsar.theme", "light"); localStorage.setItem("parsar.lang", "zh-CN")
    localStorage.setItem("parsar.ws", "0f4d2c6e-9b1a-4e7c-8f3d-2a1b5c6d7e8f")
  })
  const page = await ctx.newPage()
  page.on("pageerror", (e) => console.log("  PAGEERROR:", e.message.slice(0, 140)))
  await page.goto(`http://127.0.0.1:5174/?admin=${view}`, { waitUntil: "networkidle" })
  await page.waitForTimeout(600)
  return { ctx, page }
}

// A. one question per panel: the sidebar's survives a rail opening
{
  const { ctx, page } = await open("capabilities")
  const side = await page.locator('[role="separator"]').first().boundingBox()
  await page.mouse.move(side.x + 3, side.y + 300); await page.mouse.down()
  await page.mouse.move(side.x + 60, side.y + 300, { steps: 8 }); await page.mouse.up()
  await page.waitForTimeout(500)
  const before = await page.getByRole("button", { name: /保存/ }).count()
  await page.locator('li[role="option"]').first().click()
  await page.waitForTimeout(700)
  const after = await page.getByRole("button", { name: /保存/ }).count()
  console.log("A 侧边栏问题 开栏前:", before, "| 开栏后:", after)
  await ctx.close()
}

// B. the rail's question leaves with the rail
{
  const { ctx, page } = await open("capabilities")
  await page.locator('li[role="option"]').first().click(); await page.waitForTimeout(700)
  const h = await page.locator('[role="separator"]').last().boundingBox()
  await page.mouse.move(h.x + 3, h.y + 200); await page.mouse.down()
  await page.mouse.move(h.x - 60, h.y + 200, { steps: 8 }); await page.mouse.up()
  await page.waitForTimeout(500)
  const up = await page.getByRole("button", { name: /保存/ }).count()
  await page.locator('li[role="option"]').first().click()  // the open row closes the rail
  await page.waitForTimeout(1200)
  const railGone = (await page.locator('[role="separator"]').count()) === 1
  const left = await page.getByRole("button", { name: /保存/ }).count()
  console.log("B 侧栏问题 拖动后:", up, "| 侧栏已关:", railGone, "| 关栏后:", left)
  await ctx.close()
}

// C. a toast raised while a dialog is open is on top of it
{
  const { ctx, page } = await open("scheduled")
  await page.route("**/scheduled-tasks/*/run**", (r) =>
    r.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "internal", message: 'migration 0042: relation "agent_runs" already exists' }) }))
  await page.getByTestId("scheduled-new").click(); await page.waitForTimeout(500)
  const z = await page.evaluate(() => {
    const stack = document.querySelector("body > div:not(#root)")
    const overlay = [...document.querySelectorAll("body > div")].map((n) => n.querySelector("[data-state=open]"))
      .find(Boolean)
    const dialog = document.querySelector('[role="dialog"]')
    const zOf = (n) => (n ? getComputedStyle(n).zIndex : null)
    return { toast: zOf(stack), overlay: zOf(overlay), dialog: zOf(dialog) }
  })
  console.log("C z-index — 提示条:", z.toast, "| 遮罩:", z.overlay, "| 对话框:", z.dialog)
  await page.screenshot({ path: path.join(OUT, "notice-over-dialog.png") })
  await ctx.close()
}

// D. a lone message sits at the 12px mark, not 20px
{
  const { ctx, page } = await open("scheduled")
  await page.route("**/scheduled-tasks/*/run**", (r) => r.fulfill({ status: 200, contentType: "application/json", body: "{}" }))
  await page.locator('[data-testid="scheduled-run-now"]').first().click({ force: true })
  await page.waitForTimeout(500)
  const y = await page.evaluate(() => {
    const strip = document.querySelector("body > div:not(#root) .app-shadow-floating")
    return strip ? Math.round(strip.getBoundingClientRect().top) : -1
  })
  console.log("D 单条提示的顶距:", y, "px")
  await ctx.close()
}
await browser.close()
