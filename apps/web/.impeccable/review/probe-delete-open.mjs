import { chromium } from "@playwright/test"
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args:["--no-sandbox","--disable-gpu"] })
const ctx = await browser.newContext({ viewport:{width:1440,height:900}, locale:"zh-CN", extraHTTPHeaders:{ "X-Parsar-Dev-User-ID": process.env.DEV_USER } })
await ctx.addInitScript((ws)=>{localStorage.setItem("parsar.theme","light");localStorage.setItem("parsar.lang","zh-CN");localStorage.setItem("parsar.ws",ws)},process.env.WS)
const page = await ctx.newPage()
const bad=[]
page.on("response", r => { if (r.url().includes("/api/") && r.status()>=400) bad.push(`${r.status()} ${r.request().method()} ${r.url().split("/api/v1")[1]}`) })
page.on("pageerror", e => console.log("  PAGEERROR", e.message.slice(0,160)))
await page.goto("http://127.0.0.1:5173/?admin=conversations",{waitUntil:"load"}); await page.waitForTimeout(2500)
const first = page.locator('[role="option"]').first()
await first.click(); await page.waitForTimeout(1800)
const openId = new URL(page.url()).searchParams.get("id")
console.log("opened conversation:", openId)
await first.hover(); await page.waitForTimeout(400)
const del = first.getByRole("button", { name: /删除/ }).first()
console.log("delete action on the row:", await del.count())
if (await del.count()) {
  await del.click(); await page.waitForTimeout(900)
  const dlg = page.locator('[role="dialog"], [role="alertdialog"]')
  console.log("confirm dialog:", await dlg.count())
  if (await dlg.count()) {
    const btns = await dlg.locator("button").evaluateAll(els=>els.map(e=>(e.textContent||"").trim().slice(0,12)))
    console.log("  buttons:", btns.join(" | "))
    await dlg.getByRole("button", { name: /删除|确认/ }).last().click()
    await page.waitForTimeout(2500)
  }
}
await page.screenshot({ path: ".impeccable/review/crawl/conv-after-delete.png" })
console.log("url after delete:", page.url())
console.log("rows now:", await page.locator('[role="option"]').count())
console.log("bad responses:", bad.join(" | ") || "none")
const txt = (await page.locator("main").innerText()).slice(0,160).replace(/\n/g," / ")
console.log("main:", txt)
await browser.close()
