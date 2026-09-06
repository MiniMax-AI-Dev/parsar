// Capture frames of the rail → modal absorb animation (motion enabled).
import { chromium } from "@playwright/test"
import path from "node:path"
const OUT = path.resolve(".impeccable/review")
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN", reducedMotion: "no-preference" })
await ctx.addInitScript(() => { localStorage.setItem("parsar.theme", "light"); localStorage.setItem("parsar.lang", "zh-CN"); localStorage.setItem("parsar.ws", "0f4d2c6e-9b1a-4e7c-8f3d-2a1b5c6d7e8f") })
const page = await ctx.newPage()
await page.goto("http://127.0.0.1:5173/?admin=runs&id=run_01J8Z03KX2P9Q03", { waitUntil: "networkidle" })
await page.waitForTimeout(800)
await page.getByRole("button", { name: "展开" }).click()
for (const t of [60, 160, 300]) { await page.waitForTimeout(t === 60 ? 60 : t === 160 ? 100 : 140); await page.screenshot({ path: path.join(OUT, `anim-in-${t}.png`) }) }
await page.waitForTimeout(600)
await page.screenshot({ path: path.join(OUT, `anim-in-final.png`) })
await page.getByRole("button", { name: "收起" }).click()
await page.waitForTimeout(90)
await page.screenshot({ path: path.join(OUT, `anim-out-90.png`) })
await page.waitForTimeout(600)
await page.screenshot({ path: path.join(OUT, `anim-out-final.png`) })
console.log("done")
await browser.close()
