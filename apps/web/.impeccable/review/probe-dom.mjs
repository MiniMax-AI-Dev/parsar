import { chromium } from "@playwright/test"
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args:["--no-sandbox","--disable-gpu"] })
const ctx = await browser.newContext({ viewport:{width:1440,height:900}, locale:"zh-CN", extraHTTPHeaders:{ "X-Parsar-Dev-User-ID": process.env.DEV_USER } })
await ctx.addInitScript((ws)=>{localStorage.setItem("parsar.theme","light");localStorage.setItem("parsar.lang","zh-CN");localStorage.setItem("parsar.ws",ws)},process.env.WS)
const page = await ctx.newPage()
page.on("response", r => { if (r.url().includes("/api/") && r.status()>=400) console.log("   BAD", r.status(), r.url().split("/api/v1")[1]) })
for (const v of process.argv.slice(2)) {
  await page.goto(`http://127.0.0.1:5173/?admin=${v}`,{waitUntil:"load"}); await page.waitForTimeout(3000)
  const info = await page.evaluate(() => {
    const roles = {}
    document.querySelectorAll("main *[role]").forEach(e => { roles[e.getAttribute("role")] = (roles[e.getAttribute("role")]||0)+1 })
    return { roles, lis: document.querySelectorAll("main li").length, text: (document.querySelector("main")?.innerText||"").slice(0,150).replace(/\n/g," / ") }
  })
  console.log(v, JSON.stringify(info.roles), "li=", info.lis)
  console.log("   ", info.text)
}
await browser.close()
