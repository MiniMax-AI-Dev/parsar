import { chromium } from "@playwright/test"
import path from "node:path"
const OUT = path.resolve(".impeccable/review")
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
for (const theme of ["light", "dark"]) {
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN" })
  await ctx.addInitScript((t) => { localStorage.setItem("parsar.theme", t); localStorage.setItem("parsar.lang","zh-CN"); localStorage.setItem("parsar.ws","0f4d2c6e-9b1a-4e7c-8f3d-2a1b5c6d7e8f") }, theme)
  const page = await ctx.newPage()
  page.on("pageerror", e => console.log("  PAGEERROR:", e.message.slice(0,140)))
  await page.goto("http://127.0.0.1:5174/?admin=capabilities", { waitUntil: "networkidle" })
  await page.waitForTimeout(700)
  // drive the provider directly: one success, one error, one more success
  await page.evaluate(() => {
    const btn = document.createElement("button")
    btn.id = "__toast_probe"
    document.body.appendChild(btn)
  })
  await page.evaluate(() => {
    const ev = new CustomEvent("noop"); window.dispatchEvent(ev)
  })
  // trigger through the real path: publish a capability from the row menu
  const more = page.getByRole("button", { name: /更多操作|more/i }).first()
  if (await more.count()) {
    await more.click(); await page.waitForTimeout(300)
    const item = page.getByRole("menuitem").first()
    if (await item.count()) { await item.click(); await page.waitForTimeout(400) }
    const confirm = page.getByRole("button", { name: /确认|发布|下架/ }).last()
    if (await confirm.count()) { await confirm.click(); await page.waitForTimeout(900) }
  }
  const n = await page.locator('[role="status"], [role="alert"]').count()
  console.log(theme, "| toast 元素:", n)
  await page.screenshot({ path: path.join(OUT, `toast-${theme}.png`) })
  await ctx.close()
}
await browser.close()
