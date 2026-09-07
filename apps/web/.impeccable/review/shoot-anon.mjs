import { chromium } from "@playwright/test"
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const b = await chromium.launch({ executablePath: EXE, args:["--no-sandbox","--disable-gpu"] })
for (const theme of ["light","dark"]) {
  const c = await b.newContext({ viewport:{width:1440,height:900}, locale:"zh-CN" })
  await c.addInitScript((t)=>{localStorage.setItem("parsar.theme",t);localStorage.setItem("parsar.lang","zh-CN")}, theme)
  const p = await c.newPage()
  await p.goto("http://127.0.0.1:5173/",{waitUntil:"load"}); await p.waitForTimeout(2500)
  await p.screenshot({ path: `.impeccable/review/login-${theme}.png` })
  await c.close()
}
await b.close(); console.log("ok")
