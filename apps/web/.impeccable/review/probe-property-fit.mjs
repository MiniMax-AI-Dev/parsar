// Do the rail's property labels fit their 84px track, at every rail width?
import { chromium } from "@playwright/test"
import path from "node:path"
const OUT = path.resolve(".impeccable/review")
const EXE = process.env.CHROME_EXE ?? `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN", })
await ctx.addInitScript((ws) => { localStorage.setItem("parsar.theme","light"); localStorage.setItem("parsar.lang","zh-CN"); localStorage.setItem("parsar.ws", "0f4d2c6e-9b1a-4e7c-8f3d-2a1b5c6d7e8f") })
const page = await ctx.newPage()
for (const [lang, url] of [["zh-CN", "/?admin=runs"], ["en-US", "/?admin=runs"]]) {
  await page.addInitScript((l) => localStorage.setItem("parsar.lang", l), lang)
  await page.goto("http://127.0.0.1:5174" + url, { waitUntil: "load" })
  await page.waitForTimeout(2200)
  const rows = page.locator('li[role="option"]')
  if (await rows.count() === 0) { console.log(lang, "no rows"); continue }
  await rows.first().click(); await page.waitForTimeout(900)
  for (const w of [320, 384, 640]) {
    await page.evaluate((width) => {
      localStorage.setItem("parsar.layout.rail", String(width))
    }, w)
    await page.reload({ waitUntil: "load" }); await page.waitForTimeout(1800)
    const r = await page.evaluate(() => {
      const dts = [...document.querySelectorAll("aside.border-l dt")]
      const over = dts.filter(d => d.scrollWidth > d.clientWidth + 1)
      const tall = dts.filter(d => d.getBoundingClientRect().height > 30)
      const rail = document.querySelector("aside.border-l")
      return {
        rail: rail ? Math.round(rail.getBoundingClientRect().width) : 0,
        labels: dts.length,
        clipped: over.map(d => `${d.textContent.trim()} ${d.scrollWidth}>${d.clientWidth}`).slice(0,4),
        wrapped: tall.map(d => `${d.textContent.trim()} h=${Math.round(d.getBoundingClientRect().height)}`).slice(0,4),
      }
    })
    console.log(lang, "rail", r.rail, "| labels", r.labels, "| clipped:", r.clipped.join(", ") || "none", "| wrapped:", r.wrapped.join(", ") || "none")
  }
}
await browser.close()
