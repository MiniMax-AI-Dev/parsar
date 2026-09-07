import { chromium } from "@playwright/test"
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const b = await chromium.launch({ executablePath: EXE, args:["--no-sandbox","--disable-gpu"] })
const c = await b.newContext({ viewport:{width:1440,height:900}, locale:"zh-CN", reducedMotion:"reduce",
  extraHTTPHeaders:{ "X-Parsar-Dev-User-ID": process.env.DEV_USER } })
await c.addInitScript((ws)=>{localStorage.setItem("parsar.theme","light");localStorage.setItem("parsar.lang","zh-CN");localStorage.setItem("parsar.ws",ws)},process.env.WS)
const p = await c.newPage()
p.on("pageerror", e => console.log("PAGEERROR", e.message.slice(0,140)))
await p.goto("http://127.0.0.1:5173/?admin=capabilities",{waitUntil:"load"}); await p.waitForTimeout(2500)
await p.getByRole("tab", { name: /连接器/ }).click().catch(()=>{})
await p.waitForTimeout(1800)
const row = p.locator('[role="option"]').first()
console.log("rows:", await p.locator('[role="option"]').count())
if (await row.count()) { await row.click(); await p.waitForTimeout(2000) }
await p.screenshot({ path: ".impeccable/review/mcp-detail.png" })
await p.screenshot({ path: ".impeccable/review/mcp-detail-top.png", clip:{x:232,y:0,width:1000,height:220} })
await b.close(); console.log("ok")
