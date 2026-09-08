// The four things the review said were broken.
import { chromium } from "@playwright/test"
import path from "node:path"
const OUT = path.resolve(".impeccable/review")
const EXE = process.env.CHROME_EXE ?? `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const b = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox","--disable-gpu"] })
const ctx = await b.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN" })
await ctx.addInitScript(() => { localStorage.setItem("parsar.theme","light"); localStorage.setItem("parsar.lang","zh-CN"); localStorage.setItem("parsar.ws","0f4d2c6e-9b1a-4e7c-8f3d-2a1b5c6d7e8f") })
const p = await ctx.newPage()
p.on("pageerror", e => console.log("  PAGEERROR:", e.message.slice(0,150)))
await p.goto("http://127.0.0.1:5174/?admin=capabilities", { waitUntil: "networkidle" })
await p.waitForTimeout(700)

// 1. the stack lives outside #root, so a dialog cannot aria-hide it
const outside = await p.evaluate(() => {
  const root = document.getElementById("root")
  const regions = [...document.querySelectorAll("[aria-live]")]
  return regions.length > 0 && regions.every(r => !root.contains(r))
})
console.log("live region 在 #root 之外:", outside)

// 2. drag the rail edge → the prompt now arrives as a toast
await p.locator('li[role="option"]').first().click(); await p.waitForTimeout(700)
const handle = await p.locator('[role="separator"]').last().boundingBox()
if (handle) {
  await p.mouse.move(handle.x + 3, handle.y + 200)
  await p.mouse.down(); await p.mouse.move(handle.x - 60, handle.y + 200, { steps: 8 }); await p.mouse.up()
  await p.waitForTimeout(700)
}
const prompt = await p.getByRole("button", { name: /保存/ }).count()
console.log("拖动后出现布局提示:", prompt > 0, "| 顶部条数:", await p.locator('[aria-live] > div').count())
await p.screenshot({ path: path.join(OUT, "toast-prompt.png") })

// 3. a confirmation stacks with it instead of covering it
const more = p.getByRole("button", { name: /更多操作/ }).first()
if (await more.count()) {
  await more.click(); await p.waitForTimeout(300)
  const item = p.getByRole("menuitem").first()
  if (await item.count()) { await item.click(); await p.waitForTimeout(400) }
  const ok = p.getByRole("button", { name: /确认|发布|下架/ }).last()
  if (await ok.count()) { await ok.click(); await p.waitForTimeout(900) }
}
console.log("发布后顶部条数:", await p.locator('[aria-live] > div').count(), "| 保存按钮仍可点:", await p.getByRole("button", { name: /保存/ }).isVisible().catch(()=>false))
await p.screenshot({ path: path.join(OUT, "toast-stacked.png") })
await b.close()
