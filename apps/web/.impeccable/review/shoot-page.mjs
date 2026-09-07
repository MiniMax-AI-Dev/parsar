// Generic page capture for the impeccable finish review.
// Usage:
//   LD_LIBRARY_PATH=/tmp/chromedeps/root/usr/lib/x86_64-linux-gnu node .impeccable/review/shoot-page.mjs \
//     --name agents --url "/?admin=agents" [--theme dark] [--width 1280] [--click "tab:Steps"] [--full]
// Writes .impeccable/review/<name>.png. Requires the dev server (5173) and the mock (18080).
import { chromium } from "@playwright/test"
import { mkdirSync } from "node:fs"
import path from "node:path"

const args = Object.fromEntries(process.argv.slice(2).reduce((acc, a, i, arr) => {
  if (a.startsWith("--")) acc.push([a.slice(2), arr[i + 1]?.startsWith("--") || arr[i + 1] === undefined ? "true" : arr[i + 1]])
  return acc
}, []))
const name = args.name ?? "page"
const url = args.url ?? "/?admin=agents"
const theme = args.theme ?? "light"
const width = Number(args.width ?? 1440)
const OUT = path.resolve(".impeccable/review")
mkdirSync(OUT, { recursive: true })
const BASE = process.env.BASE ?? "http://127.0.0.1:5173"
const EXE = process.env.CHROME ?? `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`

const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
const ctx = await browser.newContext({ viewport: { width, height: 900 }, deviceScaleFactor: 1, locale: "zh-CN", reducedMotion: "reduce" })
await ctx.addInitScript((t) => {
  localStorage.setItem("parsar.theme", t)
  localStorage.setItem("parsar.lang", "zh-CN")
  localStorage.setItem("parsar.ws", "0f4d2c6e-9b1a-4e7c-8f3d-2a1b5c6d7e8f")
}, theme)
const page = await ctx.newPage()
page.on("pageerror", (e) => console.error(`[${name}] pageerror:`, e.message))
page.on("console", (m) => { if (m.type() === "error") console.error(`[${name}] console.error:`, m.text().slice(0, 200)) })
await page.goto(BASE + url, { waitUntil: "networkidle" })
await page.waitForTimeout(800)
if (args.click) {
  for (const spec of String(args.click).split(";")) {
    const [role, label] = spec.split(":")
    await page.getByRole(role, { name: label }).first().click()
    await page.waitForTimeout(400)
  }
}
await page.screenshot({ path: path.join(OUT, `${name}.png`), fullPage: args.full === "true" })
console.log("shot", path.join(OUT, `${name}.png`))
await browser.close()
