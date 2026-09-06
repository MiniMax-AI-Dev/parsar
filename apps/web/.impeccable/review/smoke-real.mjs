// Smoke-test the console against the REAL backend (Go server + Postgres).
//
//   LD_LIBRARY_PATH=/tmp/chromedeps/root/usr/lib/x86_64-linux-gnu \
//   WS=<workspace-uuid> DEV_USER=<user-uuid> node .impeccable/review/smoke-real.mjs
//
// Dev auth: the server accepts X-Parsar-Dev-User-ID when PARSAR_DEV_AUTH=true,
// so the browser context injects it instead of logging in. Records page
// errors, console errors and non-2xx API responses per view.
import { chromium } from "@playwright/test"
import { mkdirSync, writeFileSync } from "node:fs"
import path from "node:path"

const OUT = path.resolve(".impeccable/review/smoke")
mkdirSync(OUT, { recursive: true })
const BASE = process.env.BASE ?? "http://127.0.0.1:5173"
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const WS = process.env.WS
const DEV_USER = process.env.DEV_USER
if (!WS || !DEV_USER) throw new Error("WS and DEV_USER are required")

const VIEWS = [
  "conversations", "approvals", "runs", "scheduled",
  "agents", "capabilities", "models", "connections",
  "members", "settings", "secrets", "runtime", "connectors", "usage", "audit",
]

const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
const report = []

for (const view of VIEWS) {
  const ctx = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    locale: "zh-CN",
    reducedMotion: "reduce",
    extraHTTPHeaders: { "X-Parsar-Dev-User-ID": DEV_USER },
  })
  await ctx.addInitScript((ws) => {
    localStorage.setItem("parsar.theme", "light")
    localStorage.setItem("parsar.lang", "zh-CN")
    localStorage.setItem("parsar.ws", ws)
  }, WS)

  const page = await ctx.newPage()
  const pageErrors = []
  const consoleErrors = []
  const badResponses = []
  page.on("pageerror", (e) => pageErrors.push(e.message))
  page.on("console", (m) => {
    if (m.type() === "error") consoleErrors.push(m.text().slice(0, 300))
  })
  page.on("response", (r) => {
    const u = r.url()
    if (!u.includes("/api/") && !u.includes("/dev/")) return
    if (r.status() >= 400) badResponses.push(`${r.status()} ${r.request().method()} ${u.replace(BASE, "")}`)
  })

  await page.goto(`${BASE}/?admin=${view}`, { waitUntil: "load" })
  await page.waitForTimeout(2500)
  await page.screenshot({ path: path.join(OUT, `${view}.png`) })

  const entry = { view, pageErrors, consoleErrors, badResponses: [...new Set(badResponses)] }
  report.push(entry)
  const flag = pageErrors.length || entry.badResponses.length ? "FAIL" : consoleErrors.length ? "warn" : "ok"
  console.log(
    `${flag.padEnd(4)} ${view.padEnd(15)} pageerr=${pageErrors.length} consoleerr=${consoleErrors.length} bad=${entry.badResponses.length}`,
  )
  for (const b of entry.badResponses.slice(0, 4)) console.log(`       ${b}`)
  for (const e of pageErrors.slice(0, 2)) console.log(`       PAGEERROR ${e.slice(0, 200)}`)
  await ctx.close()
}

writeFileSync(path.join(OUT, "report.json"), JSON.stringify(report, null, 2))
await browser.close()
const failed = report.filter((r) => r.pageErrors.length || r.badResponses.length)
console.log(`\n${report.length - failed.length}/${report.length} views clean`)
