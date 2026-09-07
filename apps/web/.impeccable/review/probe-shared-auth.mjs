// A colleague clicks a shared link without a session: do they come back to it?
import { chromium } from "@playwright/test"
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
const BASE = process.env.BASE ?? "http://127.0.0.1:5174"
const CID = "5e0a1b2c-3d4e-4f60-9a7b-8c9d0e1f2a01"
// no dev-auth header, no cookie: an anonymous visitor
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN" })
await ctx.addInitScript(() => { localStorage.setItem("parsar.lang","zh-CN"); localStorage.setItem("parsar.theme","light") })
await ctx.route("**/api/v1/me", (r) => r.fulfill({ status: 401, body: '{"error":"unauthorized"}' }))
const page = await ctx.newPage()
page.on("pageerror", e => console.log("  PAGEERROR:", e.message.slice(0,140)))
await page.goto(`${BASE}/c/${CID}`, { waitUntil: "load" })
await page.waitForTimeout(2000)
console.log("anonymous → path:", new URL(page.url()).pathname, "| shows login:", await page.getByRole("button", { name: /^登录$/ }).count() > 0)
console.log("            url still carries the conversation:", page.url().includes(CID))
await browser.close()
