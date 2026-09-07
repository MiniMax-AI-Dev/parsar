import { chromium } from "@playwright/test"
import path from "node:path"
const OUT = path.resolve(".impeccable/review")
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN" })
await ctx.addInitScript(() => { localStorage.setItem("parsar.theme","light"); localStorage.setItem("parsar.lang","zh-CN"); localStorage.setItem("parsar.ws","0f4d2c6e-9b1a-4e7c-8f3d-2a1b5c6d7e8f") })
const page = await ctx.newPage()
page.on("pageerror", e => console.log("  PAGEERROR:", e.message.slice(0,140)))
await page.goto("http://127.0.0.1:5174/?admin=models", { waitUntil: "networkidle" })
await page.waitForTimeout(800)
const boxes = () => page.evaluate(() => [...document.querySelectorAll('li input[type=checkbox]')]
  .filter(el => getComputedStyle(el).opacity !== "0").length)
console.log("静止时可见的复选框:", await boxes())
await page.screenshot({ path: path.join(OUT, "sel-rest.png") })
await page.locator('li[role="listitem"]').first().hover(); await page.waitForTimeout(350)
console.log("hover 一行后可见:", await boxes())
await page.screenshot({ path: path.join(OUT, "sel-hover.png") })
await page.locator('li[role="listitem"]').first().click(); await page.waitForTimeout(350)
await page.mouse.move(700, 700); await page.waitForTimeout(350)
console.log("选中一行、鼠标移开后可见:", await boxes())
await page.screenshot({ path: path.join(OUT, "sel-active.png") })
await browser.close()
