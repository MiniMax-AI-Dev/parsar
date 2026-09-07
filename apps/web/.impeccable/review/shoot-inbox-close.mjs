import { chromium } from "@playwright/test"
import path from "node:path"
const OUT = path.resolve(".impeccable/review")
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN", reducedMotion: "no-preference" })
await ctx.addInitScript(() => { localStorage.setItem("parsar.theme", "light"); localStorage.setItem("parsar.lang", "zh-CN"); localStorage.setItem("parsar.ws", "0f4d2c6e-9b1a-4e7c-8f3d-2a1b5c6d7e8f") })
const page = await ctx.newPage()
await page.goto("http://127.0.0.1:5173/?admin=approvals", { waitUntil: "load" }); await page.waitForTimeout(1500)
console.log("rails at open:", await page.locator("aside[aria-label]").count())
await page.getByRole("button", { name: "关闭" }).click(); await page.waitForTimeout(600)
await page.screenshot({ path: path.join(OUT, "inbox-closed.png") })
console.log("rails after close:", await page.locator("aside").count(), "url:", page.url())
await page.getByRole("option").first().click(); await page.waitForTimeout(700)
await page.screenshot({ path: path.join(OUT, "inbox-reopened.png") })
console.log("rails after reopen:", await page.locator("aside[aria-label]").count())
await browser.close()
