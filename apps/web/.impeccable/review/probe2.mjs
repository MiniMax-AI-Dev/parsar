import { chromium } from "@playwright/test"
import path from "node:path"
const OUT = path.resolve(".impeccable/review/smoke")
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const WS = process.env.WS, DEV = process.env.DEV_USER
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox","--disable-gpu"] })
const ctx = await browser.newContext({ viewport:{width:1440,height:900}, locale:"zh-CN", reducedMotion:"reduce", extraHTTPHeaders:{ "X-Parsar-Dev-User-ID": DEV } })
await ctx.addInitScript((ws)=>{localStorage.setItem("parsar.theme","light");localStorage.setItem("parsar.lang","zh-CN");localStorage.setItem("parsar.ws",ws)},WS)
const page = await ctx.newPage()
await page.goto("http://127.0.0.1:5173/?admin=models",{waitUntil:"load"}); await page.waitForTimeout(2000)
await page.getByRole("button",{name:/新建模型/}).click(); await page.waitForTimeout(1200)
await page.screenshot({path:path.join(OUT,"models-create-dialog.png")})
const ctrls = await page.evaluate(()=>{
  const root=document.querySelector('[role="dialog"]')||document.body
  return [...root.querySelectorAll("input,select,textarea,button")].map((el,i)=>{
    const lbl=el.getAttribute("aria-label")||el.getAttribute("placeholder")||(el.tagName==="SELECT"?[...el.options].slice(0,4).map(o=>o.text).join("/"):(el.textContent||"").trim().slice(0,28))
    return `${i} ${el.tagName.toLowerCase()}${el.type?"["+el.type+"]":""} ${el.disabled?"DISABLED":"ok"} val="${(el.value||"").slice(0,28)}" "${lbl}"`
  })
})
console.log(ctrls.join("\n"))
await browser.close()
