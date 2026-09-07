import { chromium } from "@playwright/test"
import path from "node:path"
const OUT = path.resolve(".impeccable/review")
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
const BASE = process.env.BASE ?? "http://127.0.0.1:5174"
for (const [name, url, theme] of [
  ["shared-thread", "/c/5e0a1b2c-3d4e-4f60-9a7b-8c9d0e1f2a01", "light"],
  ["shared-thread-dark", "/c/5e0a1b2c-3d4e-4f60-9a7b-8c9d0e1f2a01", "dark"],
  ["shared-noaccess", "/c/does-not-exist", "light"],
]) {
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN", reducedMotion: "reduce" })
  await ctx.addInitScript((t) => { localStorage.setItem("parsar.theme", t); localStorage.setItem("parsar.lang","zh-CN"); localStorage.setItem("parsar.ws","0f4d2c6e-9b1a-4e7c-8f3d-2a1b5c6d7e8f") }, theme)
  const page = await ctx.newPage()
  page.on("pageerror", e => console.log(`[${name}] PAGEERROR:`, e.message.slice(0,160)))
  await page.goto(BASE + url, { waitUntil: "load" })
  await page.waitForTimeout(2000)
  await page.screenshot({ path: path.join(OUT, `${name}.png`) })
  console.log("shot", name, "| sidebar present:", await page.locator("aside").count() > 0)
  await ctx.close()
}
await browser.close()
