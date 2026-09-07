// Arriving at the inbox must not open a rail; clicking a row still must.
import { chromium } from "@playwright/test"
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
const BASE = process.env.BASE ?? "http://127.0.0.1:5174"
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN" })
await ctx.addInitScript(() => { localStorage.setItem("parsar.theme","light"); localStorage.setItem("parsar.lang","zh-CN"); localStorage.setItem("parsar.ws","0f4d2c6e-9b1a-4e7c-8f3d-2a1b5c6d7e8f") })
const page = await ctx.newPage()
page.on("pageerror", e => console.log("  PAGEERROR:", e.message.slice(0,140)))
const rail = () => page.locator("aside.border-l").count()
await page.goto(`${BASE}/?admin=approvals`, { waitUntil: "networkidle" })
await page.waitForTimeout(1200)
const rows = await page.locator('li[role="option"], li[tabindex="0"]').count()
console.log("到达收件箱 → 行:", rows, "| 侧边栏:", await rail())
if (rows) {
  await page.locator('li[role="option"], li[tabindex="0"]').first().click()
  await page.waitForTimeout(800)
  console.log("点第一行     → 侧边栏:", await rail(), "| url:", (await page.url()).split("?")[1])
  await page.locator('li[role="option"], li[tabindex="0"]').first().click()
  await page.waitForTimeout(900)
  console.log("再点同一行   → 侧边栏:", await rail())
}
await browser.close()
