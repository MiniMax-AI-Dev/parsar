import { chromium } from "@playwright/test"
import path from "node:path"
const OUT = path.resolve(".impeccable/review")
const EXE = process.env.CHROME_EXE ?? `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN" })
await ctx.addInitScript(() => {
  localStorage.setItem("parsar.theme", "dark")
  localStorage.setItem("parsar.lang", "zh-CN")
  localStorage.setItem("parsar.ws", "0f4d2c6e-9b1a-4e7c-8f3d-2a1b5c6d7e8f")
})
const page = await ctx.newPage()
await page.route("**/scheduled-tasks/*/run**", (route) =>
  route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "internal", message: 'migration 0042: relation "agent_runs" already exists' }) }))
await page.goto("http://127.0.0.1:5174/?admin=scheduled", { waitUntil: "networkidle" })
await page.waitForTimeout(600)
await page.locator('[data-testid="scheduled-run-now"]').first().click({ force: true })
await page.waitForTimeout(600)
const strip = page.locator("body > div").last()
const box = await strip.boundingBox()
console.log("暗色提示条:", box && `${Math.round(box.width)}×${Math.round(box.height)} @ ${Math.round(box.x)},${Math.round(box.y)}`)
await page.screenshot({ path: path.join(OUT, "notice-dark.png"), clip: { x: 380, y: 0, width: 700, height: 240 } })
await browser.close()
