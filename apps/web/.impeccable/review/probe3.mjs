import { chromium } from "@playwright/test"
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args:["--no-sandbox","--disable-gpu"] })
const ctx = await browser.newContext({ viewport:{width:1440,height:900}, locale:"zh-CN", extraHTTPHeaders:{ "X-Parsar-Dev-User-ID": process.env.DEV_USER } })
await ctx.addInitScript((ws)=>{localStorage.setItem("parsar.theme","light");localStorage.setItem("parsar.lang","zh-CN");localStorage.setItem("parsar.ws",ws)},process.env.WS)
const page = await ctx.newPage()
for (const v of ["scheduled","conversations"]) {
  await page.goto(`http://127.0.0.1:5173/?admin=${v}`,{waitUntil:"load"}); await page.waitForTimeout(2500)
  const info = await page.evaluate(() => ({
    options: document.querySelectorAll('[role="option"]').length,
    listboxes: document.querySelectorAll('[role="listbox"]').length,
    lis: document.querySelectorAll('main li').length,
    rows: document.querySelectorAll('tbody tr').length,
    sample: [...document.querySelectorAll('main li')].slice(0,2).map(e=>e.getAttribute("role")+"|"+(e.textContent||"").trim().slice(0,40)),
  }))
  console.log(v, JSON.stringify(info))
}
await browser.close()
