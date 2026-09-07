import { chromium } from "@playwright/test"
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const b = await chromium.launch({ executablePath: EXE, args:["--no-sandbox","--disable-gpu"] })
const c = await b.newContext({ viewport:{width:1440,height:900}, locale:"zh-CN", reducedMotion:"reduce",
  extraHTTPHeaders:{ "X-Parsar-Dev-User-ID": process.env.DEV_USER } })
await c.addInitScript((ws)=>{localStorage.setItem("parsar.theme","light");localStorage.setItem("parsar.lang","zh-CN");localStorage.setItem("parsar.ws",ws)},process.env.WS)
const p = await c.newPage()
await p.goto("http://127.0.0.1:5173/?admin=members",{waitUntil:"load"}); await p.waitForTimeout(2500)
const rows = p.locator('[role="option"], [role="listitem"]')
// pick the first row that actually owns an action cluster
const n = await rows.count()
let row = rows.first()
for (let i = 0; i < n; i++) {
  if (await rows.nth(i).locator("button").count()) { row = rows.nth(i); console.log("row index with actions:", i); break }
}
const cluster = row.locator("div").filter({ has: p.locator("button") }).last()
const op = async (label) => {
  const v = await row.evaluate(el => {
    const c = [...el.querySelectorAll("div")].find(d => d.querySelector("button"))
    if (!c) return "no-cluster(row=" + el.getAttribute("role") + ", buttons=" + el.querySelectorAll("button").length + ")"
    return getComputedStyle(c).opacity
  })
  console.log(`${label.padEnd(28)} opacity=${v}`)
}
await op("resting")
await row.hover(); await p.waitForTimeout(400); await op("hovered")
await row.click(); await p.waitForTimeout(400)
await p.mouse.move(1200, 700); await p.waitForTimeout(700)
await op("after click, pointer away")
await p.keyboard.press("Tab"); await p.keyboard.press("Tab"); await p.waitForTimeout(400)
await op("after keyboard focus")
await b.close()
