import { chromium } from "@playwright/test"
import { execSync } from "node:child_process"
import path from "node:path"
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const OUT = path.resolve(".impeccable/review")
const b = await chromium.launch({ executablePath: EXE, args:["--no-sandbox","--disable-gpu"] })
for (const theme of ["light","dark"]) {
  const c = await b.newContext({ viewport:{width:1440,height:900}, locale:"zh-CN", reducedMotion:"reduce",
    extraHTTPHeaders:{ "X-Parsar-Dev-User-ID": process.env.DEV_USER } })
  await c.addInitScript(([ws,t])=>{localStorage.setItem("parsar.theme",t);localStorage.setItem("parsar.lang","zh-CN");localStorage.setItem("parsar.ws",ws)},[process.env.WS,theme])
  const p = await c.newPage()
  await p.goto("http://127.0.0.1:5173/?admin=runs",{waitUntil:"load"}); await p.waitForTimeout(2500)
  await p.screenshot({ path: path.join(OUT, `sidebar-${theme}.png`) })
  await p.screenshot({ path: path.join(OUT, `sidebar-${theme}-corner.png`), clip:{x:0,y:0,width:260,height:52} })
  await c.close()
}
await b.close(); console.log("ok")
