// How much surface does each chromatic colour actually cover?
import { chromium } from "@playwright/test"
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN", extraHTTPHeaders: { "X-Parsar-Dev-User-ID": process.env.DEV_USER } })
await ctx.addInitScript((ws) => { localStorage.setItem("parsar.theme","light"); localStorage.setItem("parsar.lang","zh-CN"); localStorage.setItem("parsar.ws", ws) }, process.env.WS)
const page = await ctx.newPage()
const seen = new Map()
for (const v of ["agents","runs","capabilities","connectors","models","members","usage"]) {
  await page.goto(`http://127.0.0.1:5173/?admin=${v}`, { waitUntil: "load" })
  await page.waitForTimeout(1500)
  const rows = await page.evaluate(() => {
    const out = []
    const chroma = (c) => { const m = c.match(/rgba?\((\d+),\s*(\d+),\s*(\d+)/); if (!m) return 0; const [r,g,b]=m.slice(1,4).map(Number); return Math.max(r,g,b)-Math.min(r,g,b) }
    for (const el of document.querySelectorAll("body *")) {
      const s = getComputedStyle(el)
      const bg = s.backgroundColor
      if (!bg || bg.startsWith("rgba(0, 0, 0, 0")) continue
      if (chroma(bg) <= 24) continue
      const r = el.getBoundingClientRect()
      if (r.width < 1 || r.height < 1) continue
      out.push({ bg, w: Math.round(r.width), h: Math.round(r.height), tag: el.tagName.toLowerCase(), cls: (el.className||"").toString().slice(0,44) })
    }
    return out
  })
  for (const r of rows) {
    const k = `${r.bg} ${r.w}x${r.h} <${r.tag}>`
    seen.set(k, (seen.get(k) ?? 0) + 1)
  }
}
console.log("chromatic BACKGROUNDS by size (light):")
for (const [k, n] of [...seen.entries()].sort()) console.log(`  ${n}×  ${k}`)
await browser.close()
