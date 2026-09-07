// A cold load straight into the expanded panel: does it arrive expanded?
import { chromium } from "@playwright/test"
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN", extraHTTPHeaders: { "X-Parsar-Dev-User-ID": process.env.DEV_USER } })
await ctx.addInitScript((ws) => { localStorage.setItem("parsar.theme","light"); localStorage.setItem("parsar.lang","zh-CN"); localStorage.setItem("parsar.ws", ws) }, process.env.WS)
const page = await ctx.newPage()
page.on("pageerror", e => console.log("  PAGEERROR:", e.message.slice(0,160)))
for (const url of [
  "/?admin=agents&id=9b8a5299-a8ac-427d-bede-fc60f5d7336b&tab=config&view=full",
  "/?admin=agents&view=full",                      // expanded with nothing selected
  "/?admin=capabilities&id=28e779de-6275-46c8-9d46-a77c83b664c3&view=full",
]) {
  await page.goto("http://127.0.0.1:5173" + url, { waitUntil: "load" })
  await page.waitForTimeout(2600)
  const s = await page.evaluate(() => ({
    panel: document.querySelector('[role="dialog"][data-state="open"]') !== null,
    rail: document.querySelector("aside.border-l") !== null,
    tab: document.querySelector('[role="tab"][data-state="active"]')?.textContent?.trim() ?? null,
  }))
  console.log(url.replace("/?admin=", "").padEnd(70), JSON.stringify(s))
}
await browser.close()
