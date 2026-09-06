// Screenshot the live dev server for the impeccable finish review.
// Usage: LD_LIBRARY_PATH=/tmp/chromedeps/root/usr/lib/x86_64-linux-gnu node .impeccable/review/shoot.mjs
import { chromium } from "@playwright/test"
import { mkdirSync } from "node:fs"
import path from "node:path"

const OUT = path.resolve(".impeccable/review")
mkdirSync(OUT, { recursive: true })
const BASE = process.env.BASE ?? "http://127.0.0.1:5173"
const EXE = process.env.CHROME ?? `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const RUN_ID = "run_01J8Z03KX2P9Q03"

const shots = [
  { name: "desktop", width: 1440, theme: "light", url: `/?admin=runs&id=${RUN_ID}` },
  { name: "desktop-dark", width: 1440, theme: "dark", url: `/?admin=runs&id=run_01J8Z04KX2P9Q04` },
  { name: "desktop-unselected", width: 1440, theme: "light", url: `/?admin=runs` },
  { name: "user-1280", width: 1280, theme: "light", url: `/?admin=runs&id=${RUN_ID}` },
  { name: "agents-page", width: 1440, theme: "light", url: `/?admin=agents` },
  { name: "rail-overview", width: 1440, theme: "light", url: `/?admin=runs&id=${RUN_ID}`, tab: "Overview" },
]

const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
for (const s of shots) {
  const ctx = await browser.newContext({ viewport: { width: s.width, height: 900 }, deviceScaleFactor: 1, locale: "zh-CN", reducedMotion: "reduce" })
  await ctx.addInitScript((theme) => {
    localStorage.setItem("parsar.theme", theme)
    localStorage.setItem("parsar.lang", "zh-CN")
    localStorage.setItem("parsar.ws", "0f4d2c6e-9b1a-4e7c-8f3d-2a1b5c6d7e8f")
  }, s.theme)
  const page = await ctx.newPage()
  page.on("pageerror", (e) => console.error(`[${s.name}] pageerror:`, e.message))
  await page.goto(BASE + s.url, { waitUntil: "networkidle" })
  await page.waitForTimeout(800)
  if (s.tab) {
    await page.getByRole("tab", { name: s.tab }).click()
    await page.waitForTimeout(300)
  }
  await page.screenshot({ path: path.join(OUT, `${s.name}.png`), fullPage: false })
  console.log("shot", s.name)
  await ctx.close()
}
await browser.close()
