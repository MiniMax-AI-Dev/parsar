import { chromium } from "@playwright/test"
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args:["--no-sandbox","--disable-gpu"] })
const ctx = await browser.newContext({ viewport:{width:1440,height:900}, locale:"zh-CN", extraHTTPHeaders:{ "X-Parsar-Dev-User-ID": process.env.DEV_USER } })
await ctx.addInitScript((ws)=>{localStorage.setItem("parsar.theme","light");localStorage.setItem("parsar.lang","zh-CN");localStorage.setItem("parsar.ws",ws)},process.env.WS)
const page = await ctx.newPage()
page.on("response", r => { if (r.status()>=400 && r.url().includes("/api/")) console.log("  BAD", r.status(), r.request().method(), r.url().split("/api/v1")[1]) })
for (const v of ["agents","models","scheduled"]) {
  await page.goto(`http://127.0.0.1:5173/?admin=${v}`,{waitUntil:"load"}); await page.waitForTimeout(2500)
  const row = page.locator('[role="option"], [role="listitem"]').first()
  if (!(await row.count())) { console.log(v, "no rows"); continue }
  await row.hover(); await page.waitForTimeout(500)
  const actions = await row.locator("button").evaluateAll(els => els.map(e => (e.getAttribute("aria-label")||e.textContent||"").trim().slice(0,20)).filter(Boolean))
  console.log(`${v} row actions: ${actions.join(" | ")}`)
  // open the delete confirm and dump its buttons
  const del = row.getByRole("button", { name: /删除/ }).first()
  if (await del.count()) {
    await del.click(); await page.waitForTimeout(1000)
    const dlg = page.locator('[role="dialog"], [role="alertdialog"]')
    if (await dlg.count()) {
      const btns = await dlg.locator("button").evaluateAll(els => els.map(e => `"${(e.textContent||"").trim().slice(0,14)}"${e.disabled?"(off)":""}`))
      console.log(`  ${v} confirm dialog buttons: ${btns.join(" ")}`)
      const title = (await dlg.first().innerText()).split("\n").slice(0,2).join(" / ")
      console.log(`  title: ${title}`)
    } else console.log(`  ${v}: delete opened no dialog`)
    await page.keyboard.press("Escape"); await page.waitForTimeout(400)
  }
}
await browser.close()
