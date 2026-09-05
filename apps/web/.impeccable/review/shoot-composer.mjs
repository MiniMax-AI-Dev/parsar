import { chromium } from "@playwright/test"
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN", reducedMotion: "reduce" })
await ctx.addInitScript(() => { localStorage.setItem("parsar.theme", "light"); localStorage.setItem("parsar.lang", "zh-CN"); localStorage.setItem("parsar.ws", "0f4d2c6e-9b1a-4e7c-8f3d-2a1b5c6d7e8f") })
const page = await ctx.newPage()
await page.goto("http://127.0.0.1:5173/?admin=conversations", { waitUntil: "load" }); await page.waitForTimeout(2000); await page.getByRole("option", { name: /Why does/ }).click(); await page.waitForTimeout(2000)
await page.screenshot({ path: ".impeccable/review/composer-now.png" })
console.log("ok")
await browser.close()
