import { chromium } from "@playwright/test"
import path from "node:path"
const OUT = path.resolve(".impeccable/review/smoke")
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const WS = process.env.WS, DEV = process.env.DEV_USER
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox","--disable-gpu"] })
const ctx = await browser.newContext({ viewport:{width:1440,height:900}, locale:"zh-CN", extraHTTPHeaders:{ "X-Parsar-Dev-User-ID": DEV } })
await ctx.addInitScript((ws)=>{localStorage.setItem("parsar.theme","light");localStorage.setItem("parsar.lang","zh-CN");localStorage.setItem("parsar.ws",ws)},WS)
const page = await ctx.newPage()
const calls = []
page.on("response", r => { const m=r.request().method(); if (m!=="GET" && r.url().includes("/api/")) calls.push(`${r.status()} ${m} ${r.url().split("/api/v1")[1]}`) })
page.on("pageerror", e => console.log("PAGEERROR:", e.message.slice(0,200)))

for (const v of ["models","agents"]) {
  await page.goto(`http://127.0.0.1:5173/?admin=${v}`,{waitUntil:"load"}); await page.waitForTimeout(2500)
  await page.screenshot({path:path.join(OUT,`real-${v}.png`)})
  console.log(`${v}: ${await page.getByRole("option").count()} rows`)
}
await page.goto("http://127.0.0.1:5173/?admin=conversations",{waitUntil:"load"}); await page.waitForTimeout(3000)
const rows = page.getByRole("option")
console.log("conversations:", await rows.count(), "rows")
if (await rows.count()) { await rows.first().click(); await page.waitForTimeout(2000) }
const ta = page.locator("textarea").first()
console.log("composer present:", await ta.count(), "disabled:", await ta.isDisabled().catch(()=>"n/a"))
if (await ta.count() && !(await ta.isDisabled())) {
  await ta.fill("Say hello and stop.")
  await page.keyboard.press("Enter")
  await page.waitForTimeout(12000)
  await page.screenshot({path:path.join(OUT,"real-send.png")})
} else {
  await page.screenshot({path:path.join(OUT,"real-send-blocked.png")})
}
console.log("write calls:", calls.join(" | ") || "(none)")
await browser.close()
