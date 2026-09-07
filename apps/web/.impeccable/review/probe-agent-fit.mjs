// Anything in the agent rail that clips instead of ellipsizing?
import { chromium } from "@playwright/test"
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN", extraHTTPHeaders: { "X-Parsar-Dev-User-ID": process.env.DEV_USER } })
await ctx.addInitScript((ws) => { localStorage.setItem("parsar.theme","light"); localStorage.setItem("parsar.lang","zh-CN"); localStorage.setItem("parsar.ws", ws) }, process.env.WS)
const page = await ctx.newPage()
for (const tab of ["dynamics", "config", "audit"]) {
  await page.goto(`http://127.0.0.1:5173/?admin=agents&id=9b8a5299-a8ac-427d-bede-fc60f5d7336b&tab=${tab}`, { waitUntil: "load" })
  await page.waitForTimeout(2600)
  const over = await page.evaluate(() => {
    const rail = document.querySelector("aside.border-l")
    if (!rail) return ["NO RAIL"]
    const out = []
    for (const el of rail.querySelectorAll("*")) {
      if (el.scrollWidth > el.clientWidth + 1 && el.clientWidth > 0) {
        const s = getComputedStyle(el)
        if (s.overflowX === "auto" || s.overflowX === "scroll") continue
        if (s.textOverflow === "ellipsis") continue
        out.push(`${el.tagName.toLowerCase()}.${(el.className||"").toString().slice(0,40)} ${el.scrollWidth}>${el.clientWidth} "${(el.textContent||"").trim().slice(0,30)}"`)
      }
    }
    return [...new Set(out)].slice(0, 6)
  })
  console.log(`${tab}:`, over.length ? over.join("\n     ") : "no clipping")
}
await browser.close()
