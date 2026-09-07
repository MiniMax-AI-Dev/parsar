// Does the rail body scroll sideways? A bled ledger must not create one.
import { chromium } from "@playwright/test"
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN", extraHTTPHeaders: { "X-Parsar-Dev-User-ID": process.env.DEV_USER } })
await ctx.addInitScript((ws) => { localStorage.setItem("parsar.theme","light"); localStorage.setItem("parsar.lang","zh-CN"); localStorage.setItem("parsar.ws", ws) }, process.env.WS)
const page = await ctx.newPage()
const check = async (label) => {
  const r = await page.evaluate(() => {
    const rail = document.querySelector("aside.border-l")
    if (!rail) return null
    const body = [...rail.querySelectorAll("div")].find(d => getComputedStyle(d).overflowY === "auto")
    if (!body) return { note: "no scroll body" }
    return { scrollW: body.scrollWidth, clientW: body.clientWidth, overflowX: getComputedStyle(body).overflowX }
  })
  console.log(label.padEnd(34), JSON.stringify(r))
}
for (const [label, url] of [
  ["agents / dynamics", "/?admin=agents&id=9b8a5299-a8ac-427d-bede-fc60f5d7336b&tab=dynamics"],
  ["agents / config",   "/?admin=agents&id=9b8a5299-a8ac-427d-bede-fc60f5d7336b&tab=config"],
  ["capabilities (nested ledgers)", "/?admin=capabilities&id=28e779de-6275-46c8-9d46-a77c83b664c3"],
  ["runs", "/?admin=runs"],
]) {
  await page.goto("http://127.0.0.1:5173" + url, { waitUntil: "load" })
  await page.waitForTimeout(2400)
  if (url === "/?admin=runs") { await page.locator('li[role="option"]').first().click(); await page.waitForTimeout(1200) }
  await check(label)
}
await browser.close()
