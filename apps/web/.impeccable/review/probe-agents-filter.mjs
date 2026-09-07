// The connector filter: counts, filtering, and the way out of an empty result.
import { chromium } from "@playwright/test"
import path from "node:path"
const OUT = path.resolve(".impeccable/review")
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN" })
await ctx.addInitScript(() => { localStorage.setItem("parsar.theme","light"); localStorage.setItem("parsar.lang","zh-CN"); localStorage.setItem("parsar.ws","0f4d2c6e-9b1a-4e7c-8f3d-2a1b5c6d7e8f") })
const page = await ctx.newPage()
page.on("pageerror", e => console.log("  PAGEERROR:", e.message.slice(0,150)))
const rows = () => page.locator('li[role="option"], li[tabindex="0"]').count()
await page.goto("http://127.0.0.1:5174/?admin=agents", { waitUntil: "networkidle" })
await page.waitForTimeout(700)
console.log("unfiltered rows:", await rows())
await page.getByRole("button", { name: /筛选/ }).click(); await page.waitForTimeout(400)
console.log("menu:", (await page.locator('[role="menuitemradio"]').allInnerTexts()).map(s => s.replace(/\s+/g," ").trim()).join(" | "))
await page.getByRole("menuitemradio", { name: /HTTP Agent/ }).click(); await page.waitForTimeout(600)
console.log("after HTTP filter → rows:", await rows(), "| trigger:", (await page.getByRole("button", { name: /筛选/ }).innerText()).replace(/\s+/g," "))
await page.screenshot({ path: path.join(OUT, "ia-agents-filtered.png") })
// a value with nothing on it must not be a dead end
await page.getByRole("button", { name: /筛选/ }).click(); await page.waitForTimeout(400)
await page.getByRole("menuitemradio", { name: /A2A/ }).click(); await page.waitForTimeout(600)
console.log("after A2A (0) → rows:", await rows(), "| escape hatch:", await page.getByRole("button", { name: /清除筛选/ }).count() > 0)
await page.screenshot({ path: path.join(OUT, "ia-agents-empty.png") })
await page.getByRole("button", { name: /清除筛选/ }).click(); await page.waitForTimeout(600)
console.log("after clear → rows:", await rows())
await browser.close()
