// Exercise the remaining write paths (members invite, scheduled task,
// org secret) against the real backend and report what each dialog posts.
import { chromium } from "@playwright/test"
import path from "node:path"

const OUT = path.resolve(".impeccable/review/smoke")
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const WS = process.env.WS
const DEV = process.env.DEV_USER
const stamp = Date.now().toString().slice(-5)

const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
const ctx = await browser.newContext({
  viewport: { width: 1440, height: 900 },
  locale: "zh-CN",
  extraHTTPHeaders: { "X-Parsar-Dev-User-ID": DEV },
})
await ctx.addInitScript((ws) => {
  localStorage.setItem("parsar.theme", "light")
  localStorage.setItem("parsar.lang", "zh-CN")
  localStorage.setItem("parsar.ws", ws)
}, WS)
const page = await ctx.newPage()
const calls = []
page.on("response", (r) => {
  const m = r.request().method()
  if (m !== "GET" && r.url().includes("/api/")) calls.push(`${r.status()} ${m} ${r.url().split("/api/v1")[1]}`)
})
page.on("pageerror", (e) => console.log("  PAGEERROR:", e.message.slice(0, 200)))

const open = async (view, button) => {
  await page.goto(`http://127.0.0.1:5173/?admin=${view}`, { waitUntil: "load" })
  await page.waitForTimeout(2200)
  await page.getByRole("button", { name: button }).first().click()
  const dlg = page.locator('[role="dialog"], [role="alertdialog"]').first()
  await dlg.waitFor({ state: "visible", timeout: 15000 })
  await page.waitForTimeout(700)
  return dlg
}
const dump = async (dlg) =>
  (await dlg.locator("input,select,textarea,button").evaluateAll((els) =>
    els.map((el, i) => {
      const l = el.getAttribute("aria-label") || el.getAttribute("placeholder") ||
        (el.tagName === "SELECT" ? [...el.options].slice(0, 3).map((o) => o.text).join("/") : (el.textContent || "").trim().slice(0, 22))
      return `${i}:${el.tagName.toLowerCase()}${el.disabled ? "(off)" : ""} "${l}"`
    }),
  )).join("  ")

for (const [view, button, file] of [
  ["members", /邀请/, "form-members"],
  ["scheduled", /新建|新增/, "form-scheduled"],
  ["secrets", /新建|添加|录入/, "form-secrets"],
]) {
  calls.length = 0
  try {
    const dlg = await open(view, button)
    console.log(`\n${view}: ${await dump(dlg)}`)
    await page.screenshot({ path: path.join(OUT, `${file}.png`) })
    if (view === "members") {
      await dlg.getByPlaceholder(/teammate@|@/).first().fill(`smoke${stamp}@minimax.io`)
      await dlg.getByRole("button", { name: /创建邀请|邀请/ }).last().click()
    } else if (view === "scheduled") {
      await dlg.locator("input").first().fill(`smoke-task-${stamp}`)
      await dlg.locator("textarea").first().fill("总结昨天的提交并列出今天待办。")
      const time = dlg.locator("input").nth(1)
      if (await time.count()) await time.fill("09:30").catch(() => {})
      await page.waitForTimeout(400)
      const save = dlg.getByRole("button", { name: /创建|保存/ }).last()
      console.log("  save enabled:", await save.isEnabled())
      await save.click()
    } else {
      const inputs = dlg.locator("input")
      await inputs.first().fill(`SMOKE_SECRET_${stamp}`)
      if (await inputs.count() > 1) await inputs.nth(1).fill("smoke-value")
      await dlg.getByRole("button", { name: /创建|保存/ }).last().click()
    }
    await page.waitForTimeout(2500)
    await page.screenshot({ path: path.join(OUT, `${file}-after.png`) })
    console.log(`  posted: ${calls.join(" | ") || "(no write call)"}`)
  } catch (e) {
    console.log(`  ${view} FAILED: ${String(e).split("\n")[0].slice(0, 140)}`)
    await page.screenshot({ path: path.join(OUT, `${file}-error.png`) })
  }
}
await browser.close()
