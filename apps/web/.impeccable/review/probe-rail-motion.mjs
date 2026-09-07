import { chromium } from "@playwright/test"
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const b = await chromium.launch({ executablePath: EXE, args:["--no-sandbox","--disable-gpu"] })
const c = await b.newContext({ viewport:{width:1440,height:900}, locale:"zh-CN", reducedMotion:"no-preference",
  extraHTTPHeaders:{ "X-Parsar-Dev-User-ID": process.env.DEV_USER } })
await c.addInitScript((ws)=>{localStorage.setItem("parsar.theme","light");localStorage.setItem("parsar.lang","zh-CN");localStorage.setItem("parsar.ws",ws)},process.env.WS)
const p = await c.newPage()
const mainRight = () => p.evaluate(() => {
  const m = document.querySelector("main")
  const r = document.querySelector("aside[aria-label]")?.parentElement
  const list = m.querySelector("div.flex.min-w-0.flex-1")
  return {
    list: list ? Math.round(list.getBoundingClientRect().width) : 0,
    rail: r ? Math.round(r.getBoundingClientRect().width) : 0,
  }
})
await p.goto("http://127.0.0.1:5173/?admin=runs",{waitUntil:"load"}); await p.waitForTimeout(2500)
console.log("closed:", JSON.stringify(await mainRight()))
await p.locator('[role="option"]').first().click()
for (const ms of [60,130,220,600]) { await p.waitForTimeout(ms===60?60:ms-60); console.log(`open +${ms}ms:`, JSON.stringify(await mainRight())) }
await p.getByRole("button", { name: "关闭" }).click()
for (const ms of [60,130,220,600]) { await p.waitForTimeout(ms===60?60:ms-60); console.log(`close +${ms}ms:`, JSON.stringify(await mainRight())) }
await b.close()
