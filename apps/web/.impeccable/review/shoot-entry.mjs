// Capture for the entry surfaces. Same flags as shoot-page.mjs plus
// per-context route interception, so the shared mock stays authenticated:
//   --unauth   /api/v1/me -> 401 and no parsar.ws in storage (login page)
//   --setup    /api/v1/bootstrap/status -> needed:true (first-owner setup)
//   --nows     /api/v1/me/workspaces -> [] (onboarding)
import { chromium } from "@playwright/test"
import { mkdirSync } from "node:fs"
import path from "node:path"

const args = Object.fromEntries(process.argv.slice(2).reduce((acc, a, i, arr) => {
  if (a.startsWith("--")) acc.push([a.slice(2), arr[i + 1]?.startsWith("--") || arr[i + 1] === undefined ? "true" : arr[i + 1]])
  return acc
}, []))
const name = args.name ?? "entry"
const url = args.url ?? "/"
const theme = args.theme ?? "light"
const width = Number(args.width ?? 1440)
const OUT = path.resolve(".impeccable/review")
mkdirSync(OUT, { recursive: true })
const BASE = process.env.BASE ?? "http://127.0.0.1:5173"
const EXE = process.env.CHROME ?? `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`

const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
const ctx = await browser.newContext({ viewport: { width, height: 900 }, deviceScaleFactor: 1, locale: "zh-CN", reducedMotion: "reduce" })
await ctx.addInitScript(([t, withWs]) => {
  localStorage.setItem("parsar.theme", t)
  localStorage.setItem("parsar.lang", "zh-CN")
  if (withWs) localStorage.setItem("parsar.ws", "0f4d2c6e-9b1a-4e7c-8f3d-2a1b5c6d7e8f")
}, [theme, args.unauth !== "true"])
const json = (route, status, body) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
if (args.unauth === "true") await ctx.route("**/api/v1/me", (r) => json(r, 401, { error: "unauthorized", message: "unauthorized" }))
if (args.setup === "true") await ctx.route("**/api/v1/bootstrap/status", (r) => json(r, 200, { needed: true, has_owners: false, owner_count: 0, dev_auth_enabled: false, public_url: "" }))
if (args.nows === "true") await ctx.route("**/api/v1/me/workspaces", (r) => json(r, 200, { user_id: "usr_fj", workspaces: [] }))
const page = await ctx.newPage()
if (args.trace === "true") page.on("response", (r) => { if (r.url().includes("/api/")) console.error(`[${name}] ${r.status()} ${r.request().method()} ${r.url().replace(BASE, "")}`) })
page.on("pageerror", (e) => console.error(`[${name}] pageerror:`, e.message))
page.on("console", (m) => { if (m.type() === "error") console.error(`[${name}] console.error:`, m.text().slice(0, 200)) })
await page.goto(BASE + url, { waitUntil: "networkidle" })
await page.waitForTimeout(800)
if (args.click) {
  for (const spec of String(args.click).split(";")) {
    const parts = spec.split(":")
    const hover = parts[0] === "hover"
    const [role, label] = hover ? parts.slice(1) : parts
    const target = page.getByRole(role, { name: label }).first()
    if (hover) await target.hover()
    else await target.click()
    await page.waitForTimeout(400)
  }
}
if (args.fill) {
  for (const spec of String(args.fill).split(";")) {
    const [label, value] = spec.split("=")
    await page.getByLabel(label).first().fill(value)
  }
  await page.waitForTimeout(200)
}
await page.screenshot({ path: path.join(OUT, `${name}.png`), fullPage: args.full === "true" })
console.log("shot", path.join(OUT, `${name}.png`))
await browser.close()
