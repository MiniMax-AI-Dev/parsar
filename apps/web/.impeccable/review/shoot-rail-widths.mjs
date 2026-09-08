import { chromium } from "@playwright/test"
import path from "node:path"
const OUT = path.resolve(".impeccable/review")
const EXE = process.env.CHROME_EXE ?? `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
for (const [w, lang] of [[320,"en-US"],[384,"zh-CN"],[640,"en-US"]]) {
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN", reducedMotion: "reduce" })
  await ctx.addInitScript(([width, l]) => {
    localStorage.setItem("parsar.theme","light"); localStorage.setItem("parsar.lang", l)
    localStorage.setItem("parsar.ws","0f4d2c6e-9b1a-4e7c-8f3d-2a1b5c6d7e8f")
    localStorage.setItem("parsar.layout.rail", String(width))
  }, [w, lang])
  const page = await ctx.newPage()
  await page.goto("http://127.0.0.1:5174/?admin=runs", { waitUntil: "networkidle" })
  await page.waitForTimeout(700)
  await page.locator('li[role="option"]').first().click()
  await page.waitForTimeout(900)
  const box = await page.locator("aside.border-l").boundingBox()
  await page.screenshot({ path: path.join(OUT, `rail-${w}-${lang}.png`), clip: { x: box.x, y: 0, width: box.width, height: 900 } })
  console.log("shot", w, lang, "rail width", Math.round(box.width))
  await ctx.close()
}
await browser.close()
