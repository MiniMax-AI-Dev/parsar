import { chromium } from "@playwright/test"
import path from "node:path"
const OUT = path.resolve(".impeccable/review")
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN", reducedMotion: "no-preference" })
await ctx.addInitScript(() => { localStorage.setItem("parsar.theme", "light"); localStorage.setItem("parsar.lang", "zh-CN"); localStorage.setItem("parsar.ws", "0f4d2c6e-9b1a-4e7c-8f3d-2a1b5c6d7e8f") })
const page = await ctx.newPage()
await page.goto("http://127.0.0.1:5173/?admin=runs&id=run_01J8Z03KX2P9Q03", { waitUntil: "networkidle" }); await page.waitForTimeout(700)
await page.getByRole("button", { name: "关闭" }).click()
await page.waitForTimeout(90); await page.screenshot({ path: path.join(OUT, "close-90.png") })
await page.waitForTimeout(500); await page.screenshot({ path: path.join(OUT, "close-final.png") })
console.log("url after close:", page.url())
await page.goto("http://127.0.0.1:5173/?admin=members", { waitUntil: "networkidle" }); await page.waitForTimeout(700)
await page.getByRole("button", { name: /邀请/ }).first().click(); await page.waitForTimeout(500)
await page.screenshot({ path: path.join(OUT, "members-invite-now.png") })
await browser.close()
