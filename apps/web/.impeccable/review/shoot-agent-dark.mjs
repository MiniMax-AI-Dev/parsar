import { chromium } from "@playwright/test"
import path from "node:path"
const OUT = path.resolve(".impeccable/review")
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
for (const [name, theme, expand] of [["eff-agent-dark", "dark", false], ["eff-agent-expanded-cfg", "light", true]]) {
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN", reducedMotion: "reduce" })
  await ctx.addInitScript((t) => { localStorage.setItem("parsar.theme", t); localStorage.setItem("parsar.lang","zh-CN"); localStorage.setItem("parsar.ws","0f4d2c6e-9b1a-4e7c-8f3d-2a1b5c6d7e8f") }, theme)
  const page = await ctx.newPage()
  page.on("pageerror", e => console.log("PAGEERROR", e.message.slice(0,140)))
  await page.goto("http://127.0.0.1:5174/?admin=agents", { waitUntil: "networkidle" })
  await page.waitForTimeout(700)
  await page.locator('li[role="option"], li[tabindex="0"]').first().click()
  await page.waitForTimeout(900)
  if (expand) {
    await page.getByRole("tab", { name: "配置" }).click(); await page.waitForTimeout(900)
    await page.getByRole("button", { name: /展开/ }).first().click(); await page.waitForTimeout(900)
  }
  await page.screenshot({ path: path.join(OUT, `${name}.png`) })
  console.log("shot", name)
  await ctx.close()
}
await browser.close()
