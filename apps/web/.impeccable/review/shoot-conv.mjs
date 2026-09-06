import { chromium } from "@playwright/test"
import path from "node:path"
const OUT = path.resolve(".impeccable/review")
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
async function open(theme) {
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN", reducedMotion: "no-preference" })
  await ctx.addInitScript((t) => { localStorage.setItem("parsar.theme", t); localStorage.setItem("parsar.lang", "zh-CN"); localStorage.setItem("parsar.ws", "0f4d2c6e-9b1a-4e7c-8f3d-2a1b5c6d7e8f") }, theme)
  const page = await ctx.newPage()
  await page.goto("http://127.0.0.1:5173/?admin=conversations", { waitUntil: "load" }); await page.waitForTimeout(1500)
  await page.getByRole("option", { name: /Review PR/ }).click(); await page.waitForTimeout(2500)
  return { ctx, page }
}
let { ctx, page } = await open("light")
await page.screenshot({ path: path.join(OUT, "conversations-trace-light.png") })
// expand the first tool row of the running trace
const rowToggles = page.locator('section[aria-label] [aria-expanded]')
const n = await rowToggles.count(); console.log("aria-expanded toggles:", n)
for (let i = 0; i < n; i++) { const el = rowToggles.nth(i); const tag = await el.evaluate((e) => e.tagName + "|" + (e.getAttribute("aria-expanded")) + "|" + (e.textContent || "").trim().slice(0, 30)); console.log("  ", i, tag) }
if (n > 2) { await rowToggles.nth(2).click(); await page.waitForTimeout(500) }
await page.screenshot({ path: path.join(OUT, "conversations-trace-open-light.png") })
// rail hover: use the 5-turn conversation so the rail has a real ladder
await page.getByRole("option", { name: /Why does/ }).click(); await page.waitForTimeout(2000)
const markers = page.locator('nav[aria-label="楼层导航"] button')
console.log("rail markers:", await markers.count())
if (await markers.count() > 1) {
  const m = markers.nth(1)
  const box = await m.boundingBox(); console.log("marker box:", JSON.stringify(box))
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)
  await page.waitForTimeout(450)
  const preview = page.locator('nav[aria-label="楼层导航"] [role="presentation"]')
  console.log("preview visible:", await preview.count(), await preview.first().isVisible().catch(() => false))
  await page.screenshot({ path: path.join(OUT, "conversations-rail-light.png") })
}
await page.getByRole("option", { name: /Review PR/ }).click(); await page.waitForTimeout(2000)
// decide → composer with stop button
await page.getByRole("button", { name: "拒绝" }).click(); await page.waitForTimeout(1500)
await page.screenshot({ path: path.join(OUT, "composer-running-light.png") })
console.log("stop button:", await page.getByRole("button", { name: /停止|Stop/ }).count())
await ctx.close()
;({ ctx, page } = await open("dark"))
await page.screenshot({ path: path.join(OUT, "conversations-trace-dark.png") })
await ctx.close()
await browser.close()
