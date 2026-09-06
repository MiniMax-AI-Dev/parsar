import { chromium } from "@playwright/test"
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args:["--no-sandbox","--disable-gpu"] })
const ctx = await browser.newContext({ viewport:{width:1440,height:900}, locale:"zh-CN", extraHTTPHeaders:{ "X-Parsar-Dev-User-ID": process.env.DEV_USER } })
await ctx.addInitScript((ws)=>{localStorage.setItem("parsar.theme","light");localStorage.setItem("parsar.lang","zh-CN");localStorage.setItem("parsar.ws",ws)},process.env.WS)
const page = await ctx.newPage()
page.on("response", r => { if (r.url().includes("/api/") && (r.status()>=400 || r.request().method()!=="GET")) console.log("   ", r.status(), r.request().method(), r.url().split("/api/v1")[1]) })
await page.goto("http://127.0.0.1:5173/?admin=agents",{waitUntil:"load"}); await page.waitForTimeout(2500)
const row = page.locator('[role="option"]').first()
await row.hover(); await page.waitForTimeout(500)
await row.getByRole("button", { name: "更多操作" }).click()
await page.waitForTimeout(900)
const menus = await page.locator('[role="menu"]').count()
const items = await page.locator('[role="menuitem"], [role="menuitemradio"], [role="menuitemcheckbox"]').evaluateAll(els=>els.map(e=>`${e.getAttribute("role")}:"${(e.textContent||"").trim().slice(0,16)}"`))
console.log("menus:", menus, "items:", items.join(" | "))
await page.screenshot({ path: ".impeccable/review/crawl/agents-row-menu.png" })
const del = page.getByRole("menuitem", { name: /删除/ }).first()
if (await del.count()) {
  console.log("clicking delete item…")
  await del.click(); await page.waitForTimeout(1200)
  const dlg = page.locator('[role="dialog"], [role="alertdialog"]')
  console.log("dialog:", await dlg.count())
  if (await dlg.count()) {
    const btns = await dlg.locator("button").evaluateAll(els=>els.map(e=>`"${(e.textContent||"").trim().slice(0,12)}"${e.disabled?"(off)":""}`))
    console.log("  buttons:", btns.join(" "))
    const txt = (await dlg.first().innerText()).replace(/\n/g," / ").slice(0,160)
    console.log("  text:", txt)
    await page.screenshot({ path: ".impeccable/review/crawl/agents-delete-dialog.png" })
  }
}
await browser.close()
