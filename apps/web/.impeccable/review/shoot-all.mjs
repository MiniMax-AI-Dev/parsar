// Capture every console view for the whole-app finish review.
// LD_LIBRARY_PATH=/tmp/chromedeps/root/usr/lib/x86_64-linux-gnu node .impeccable/review/shoot-all.mjs
import { chromium } from "@playwright/test"
import { mkdirSync } from "node:fs"
import path from "node:path"

const OUT = path.resolve(".impeccable/review/all")
mkdirSync(OUT, { recursive: true })
const BASE = process.env.BASE ?? "http://127.0.0.1:5173"
const EXE = process.env.CHROME ?? `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`

const views = [
  "conversations", "approvals", "runs", "scheduled",
  "agents", "capabilities", "models", "connections",
  "members", "settings", "secrets", "runtime", "connectors", "usage", "audit",
]
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
for (const theme of ["light", "dark"]) {
  for (const v of views) {
    const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 1, locale: "zh-CN", reducedMotion: "reduce" })
    await ctx.addInitScript((t) => {
      localStorage.setItem("parsar.theme", t)
      localStorage.setItem("parsar.lang", "zh-CN")
      localStorage.setItem("parsar.ws", "0f4d2c6e-9b1a-4e7c-8f3d-2a1b5c6d7e8f")
    }, theme)
    const page = await ctx.newPage()
    const errors = []
    page.on("pageerror", (e) => errors.push(e.message))
    await page.goto(`${BASE}/?admin=${v}`, { waitUntil: "networkidle" })
    await page.waitForTimeout(700)
    await page.screenshot({ path: path.join(OUT, `${v}-${theme}.png`) })
    console.log(`shot ${v}-${theme}${errors.length ? "  PAGEERROR: " + errors.join(" | ").slice(0, 200) : ""}`)
    await ctx.close()
  }
}
await browser.close()
