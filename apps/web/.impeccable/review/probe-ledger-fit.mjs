// Does the ledger reflow when the rail opens, or does it overflow under it?
import { chromium } from "@playwright/test"
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN", extraHTTPHeaders: { "X-Parsar-Dev-User-ID": process.env.DEV_USER } })
await ctx.addInitScript((ws) => { localStorage.setItem("parsar.theme","light"); localStorage.setItem("parsar.lang","zh-CN"); localStorage.setItem("parsar.ws", ws) }, process.env.WS)
const page = await ctx.newPage()
const measure = () => page.evaluate(() => {
  const rail = document.querySelector("aside.border-l")
  const col = rail ? rail.closest("div.flex.min-h-0").firstElementChild : document.querySelector("main > div > div")
  const header = [...document.querySelectorAll("div")].find(d => d.className.includes("sticky") || d.getAttribute("role") === "row")
  const rows = [...document.querySelectorAll('li[role="option"]')]
  const grid = rows[0]
  return {
    column: col ? Math.round(col.getBoundingClientRect().width) : null,
    rowWidth: grid ? Math.round(grid.getBoundingClientRect().width) : null,
    rowRight: grid ? Math.round(grid.getBoundingClientRect().right) : null,
    railLeft: rail ? Math.round(rail.getBoundingClientRect().left) : null,
    scrollW: grid ? grid.scrollWidth : null,
  }
})
for (const url of ["/?admin=capabilities", "/?admin=runs"]) {
  await page.goto("http://127.0.0.1:5173" + url, { waitUntil: "load" })
  await page.waitForTimeout(2400)
  console.log(url, "closed:", JSON.stringify(await measure()))
  await page.locator('li[role="option"]').first().click()
  await page.waitForTimeout(1500)
  console.log(url, "  open:", JSON.stringify(await measure()))
}
await browser.close()
