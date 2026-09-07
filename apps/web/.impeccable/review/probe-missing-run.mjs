import { chromium } from "@playwright/test"
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args:["--no-sandbox","--disable-gpu"] })
const ctx = await browser.newContext({ viewport:{width:1440,height:900}, locale:"zh-CN", extraHTTPHeaders:{ "X-Parsar-Dev-User-ID": process.env.DEV_USER } })
await ctx.addInitScript((ws)=>{localStorage.setItem("parsar.theme","light");localStorage.setItem("parsar.lang","zh-CN");localStorage.setItem("parsar.ws",ws)},process.env.WS)
const page = await ctx.newPage()
await page.goto(`http://127.0.0.1:5173/?admin=runs&id=${process.env.MISSING_RUN}`,{waitUntil:"load"}); await page.waitForTimeout(3000)
await page.screenshot({ path: ".impeccable/review/crawl/runs-missing-detail.png" })
const rail = page.locator("aside[aria-label], aside").last()
console.log("rail text:", (await rail.innerText().catch(()=>"(no rail)")).replace(/\n/g," / ").slice(0,220))
await browser.close()
