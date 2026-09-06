import { chromium } from "@playwright/test"
import path from "node:path"
const OUT = path.resolve(".impeccable/review")
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const b = await chromium.launch({ executablePath: EXE, args:["--no-sandbox","--disable-gpu"] })
const c = await b.newContext({ viewport:{width:1440,height:900}, locale:"zh-CN", reducedMotion:"reduce",
  extraHTTPHeaders:{ "X-Parsar-Dev-User-ID": process.env.DEV_USER } })
await c.addInitScript((ws)=>{localStorage.setItem("parsar.theme","light");localStorage.setItem("parsar.lang","zh-CN");localStorage.setItem("parsar.ws",ws)},process.env.WS)
const p = await c.newPage()
p.on("pageerror", e => console.log("PAGEERROR", e.message.slice(0,140)))
async function detail(view, name, tab) {
  await p.goto(`http://127.0.0.1:5173/?admin=${view}`,{waitUntil:"load"}); await p.waitForTimeout(2200)
  if (tab) { await p.getByRole("tab", { name: tab }).click().catch(()=>{}); await p.waitForTimeout(1500) }
  const row = p.locator('[role="option"], [role="listitem"]').first()
  if (!(await row.count())) { console.log(name, "no rows"); return }
  await row.click(); await p.waitForTimeout(2200)
  await p.screenshot({ path: path.join(OUT, `head-${name}.png`), clip:{x:232,y:0,width:900,height:150} })
  console.log(name, "captured")
}
await detail("capabilities","capability")
await detail("capabilities","mcp", /连接器/)
await detail("capabilities","skill", /Skills/)
await detail("agents","agent")
await detail("connectors","connector")
await b.close()
