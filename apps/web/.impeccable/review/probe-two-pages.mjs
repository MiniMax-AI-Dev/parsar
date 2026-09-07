import { chromium } from "@playwright/test"
import path from "node:path"
const OUT = path.resolve(".impeccable/review")
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN", extraHTTPHeaders: { "X-Parsar-Dev-User-ID": process.env.DEV_USER } })
await ctx.addInitScript((ws) => { localStorage.setItem("parsar.theme","light"); localStorage.setItem("parsar.lang","zh-CN"); localStorage.setItem("parsar.ws", ws) }, process.env.WS)
const page = await ctx.newPage()
page.on("pageerror", e => console.log("  PAGEERROR:", e.message.slice(0,140)))
await page.goto("http://127.0.0.1:5173/?admin=settings", { waitUntil: "load" })
await page.waitForTimeout(2400)
console.log("settings tab strip:", (await page.locator('[role="tab"]').allInnerTexts()).join(" | "))
await page.goto("http://127.0.0.1:5173/?admin=connectors", { waitUntil: "load" })
await page.waitForTimeout(2400)
const crows = await page.locator('li[role="option"], li[tabindex="0"]').count()
console.log("connectors rows:", crows)
await page.screenshot({ path: path.join(OUT, "two-connectors-list.png") })
if (crows) {
  await page.locator('li[role="option"], li[tabindex="0"]').first().click()
  await page.waitForTimeout(1600)
  console.log("  after click → url:", (await page.url()).split("?")[1])
  await page.screenshot({ path: path.join(OUT, "two-connectors-detail.png") })
}
await page.goto("http://127.0.0.1:5173/?admin=runtime", { waitUntil: "load" })
await page.waitForTimeout(2600)
const rrows = await page.locator('li[role="option"], li[tabindex="0"]').count()
console.log("runtime rows:", rrows)
if (rrows) {
  await page.locator('li[role="option"], li[tabindex="0"]').first().click()
  await page.waitForTimeout(1400)
  console.log("  after click → url:", (await page.url()).split("?")[1])
}
await page.goto("http://127.0.0.1:5173/?admin=runtime&id=demo-sandbox-1", { waitUntil: "load" })
await page.waitForTimeout(2400)
await page.screenshot({ path: path.join(OUT, "two-runtime-detail.png") })
console.log("runtime detail body:", (await page.locator('[data-testid="runtime-detail-placeholder"]').innerText().catch(() => "(none)")).replace(/\s+/g, " ").slice(0, 100))
await browser.close()
