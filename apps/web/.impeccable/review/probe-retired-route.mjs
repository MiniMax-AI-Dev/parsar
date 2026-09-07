// A saved ?admin=connectors link must land on 执行层, not silently on Agents.
import { chromium } from "@playwright/test"
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN", extraHTTPHeaders: { "X-Parsar-Dev-User-ID": process.env.DEV_USER } })
await ctx.addInitScript((ws) => { localStorage.setItem("parsar.theme","light"); localStorage.setItem("parsar.lang","zh-CN"); localStorage.setItem("parsar.ws", ws) }, process.env.WS)
const page = await ctx.newPage()
page.on("pageerror", e => console.log("  PAGEERROR:", e.message.slice(0,140)))
for (const url of ["/?admin=connectors", "/?admin=connectors&id=http", "/?admin=runtime", "/?admin=nonsense"]) {
  await page.goto("http://127.0.0.1:5173" + url, { waitUntil: "load" })
  await page.waitForTimeout(2200)
  const s = await page.evaluate(() => ({
    title: document.querySelector("h1")?.textContent?.trim() ?? null,
    active: document.querySelector('[aria-current="page"]')?.textContent?.trim() ?? null,
  }))
  console.log(url.padEnd(34), JSON.stringify(s))
}
await browser.close()
