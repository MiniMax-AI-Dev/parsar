import { chromium } from "@playwright/test"
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN", extraHTTPHeaders: { "X-Parsar-Dev-User-ID": process.env.DEV_USER } })
await ctx.addInitScript((ws) => { localStorage.setItem("parsar.theme","light"); localStorage.setItem("parsar.lang","zh-CN"); localStorage.setItem("parsar.ws", ws) }, process.env.WS)
const page = await ctx.newPage()
await page.goto("http://127.0.0.1:5173/?admin=capabilities", { waitUntil: "load" })
await page.waitForTimeout(2200)
const rows = page.locator('li[role="option"], li[tabindex="0"]')
console.log("row labels:", await rows.allInnerTexts().then(a => a.map(s => s.replace(/\s+/g," ").slice(0,60))))
await rows.nth(0).click(); await page.waitForTimeout(900)
await page.evaluate(() => {
  for (const [k, sel] of [["rail","aside.border-l"],["search",'input[type=search]'],["list",'ul'],["layout",'main']]) {
    const el = document.querySelector(sel); if (el) el.dataset["probe" + k] = "first"
  }
})
console.log("after row0, url:", (await page.url()).split("?")[1])
await rows.nth(1).click(); await page.waitForTimeout(900)
const stamp = await page.evaluate(() => {
  const out = {}
  for (const [k, sel] of [["rail","aside.border-l"],["search",'input[type=search]'],["list",'ul'],["layout",'main']]) {
    const el = document.querySelector(sel)
    out[k] = !el ? "gone" : (el.dataset["probe" + k] ?? "REMOUNTED")
  }
  return JSON.stringify(out)
})
console.log("after row1, url:", (await page.url()).split("?")[1])
console.log("rail node identity:", stamp)
const kind = async () => page.evaluate(() => {
  const a = document.querySelector("aside.border-l")
  return a ? [...a.querySelectorAll("button")].map(b => b.textContent.trim()).filter(Boolean).join("|") : "none"
})
console.log("row1 rail buttons:", await kind())
await rows.nth(0).click(); await page.waitForTimeout(900)
console.log("row0 rail buttons:", await kind())
await browser.close()
