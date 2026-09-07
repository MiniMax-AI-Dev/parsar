import { chromium } from "@playwright/test"
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const b = await chromium.launch({ executablePath: EXE, args:["--no-sandbox","--disable-gpu"] })
const c = await b.newContext({ viewport:{width:1440,height:900}, locale:"zh-CN", reducedMotion:"no-preference",
  extraHTTPHeaders:{ "X-Parsar-Dev-User-ID": process.env.DEV_USER } })
await c.addInitScript((ws)=>{localStorage.setItem("parsar.theme","light");localStorage.setItem("parsar.lang","zh-CN");localStorage.setItem("parsar.ws",ws)},process.env.WS)
const p = await c.newPage()
p.on("pageerror", e => console.log("PAGEERROR", e.message.slice(0,140)))
const railW = () => p.evaluate(() => {
  const r = document.querySelector("aside[aria-label]")?.parentElement
  return r ? Math.round(r.getBoundingClientRect().width) : 0
})
const id = () => new URL(p.url()).searchParams.get("id")?.slice(0,8) ?? "none"
await p.goto("http://127.0.0.1:5173/?admin=runs",{waitUntil:"load"}); await p.waitForTimeout(2500)
const rows = p.locator('[role="option"]')
console.log("rows:", await rows.count())
await rows.nth(0).click(); await p.waitForTimeout(700)
console.log(`open row0    → id=${id()} railWidth=${await railW()}`)
// switching: the rail must stay at full width the whole time (no re-entrance)
await rows.nth(1).click()
const mid = []
for (let i=0;i<4;i++){ await p.waitForTimeout(50); mid.push(await railW()) }
await p.waitForTimeout(500)
console.log(`switch row1  → id=${id()} widthsDuringSwitch=${mid.join(",")} final=${await railW()}`)
// clicking the open row closes it
// closing by clicking the open row must play the same exit as the X
await rows.nth(1).click()
const outA = []
for (let i=0;i<4;i++){ await p.waitForTimeout(45); outA.push(await railW()) }
await p.waitForTimeout(500)
console.log(`toggle close → id=${id()} widths=${outA.join(",")} final=${await railW()}`)
// reopen, then close with the X and compare the curve
await rows.nth(0).click(); await p.waitForTimeout(700)
await p.getByRole("button", { name: "关闭" }).click()
const outB = []
for (let i=0;i<4;i++){ await p.waitForTimeout(45); outB.push(await railW()) }
await p.waitForTimeout(500)
console.log(`X close      → id=${id()} widths=${outB.join(",")} final=${await railW()}`)
await b.close()
