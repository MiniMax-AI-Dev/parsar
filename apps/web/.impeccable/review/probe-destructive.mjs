// Walk each destructive row path end to end: open the row's menu (or its
// direct action), pick the destructive item, satisfy a type-to-confirm
// dialog, and report the API call plus the row delta.
import { chromium } from "@playwright/test"
import path from "node:path"

const OUT = path.resolve(".impeccable/review/crawl")
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const WS = process.env.WS
const DEV = process.env.DEV_USER
const ROW = '[role="option"], [role="listitem"]'
const DLG = '[role="dialog"], [role="alertdialog"]'

const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })

for (const view of process.argv.slice(2)) {
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
  const errs = []
  page.on("response", (r) => {
    const m = r.request().method()
    if (r.url().includes("/api/") && (m !== "GET" || r.status() >= 400)) {
      calls.push(`${r.status()} ${m} ${r.url().split("/api/v1")[1]}`)
    }
  })
  page.on("pageerror", (e) => errs.push(e.message.slice(0, 160)))

  await page.goto(`http://127.0.0.1:5173/?admin=${view}`, { waitUntil: "load" })
  await page.waitForTimeout(2500)
  const before = await page.locator(ROW).count()
  if (!before) {
    console.log(`${view.padEnd(11)} no rows to act on`)
    await ctx.close()
    continue
  }

  const row = page.locator(ROW).first()
  await row.hover().catch(() => {})
  await page.waitForTimeout(400)

  // Prefer a direct destructive button; otherwise open the row's menu.
  let opened = false
  const direct = row.getByRole("button", { name: /删除|移除|吊销|撤销/ }).first()
  if (await direct.count()) {
    await direct.click().catch(() => {})
    opened = true
  } else {
    const more = row.getByRole("button", { name: /更多操作|More/ }).first()
    if (await more.count()) {
      await more.click().catch(() => {})
      await page.waitForTimeout(800)
      const item = page.getByRole("menuitem", { name: /删除|移除|吊销|撤销/ }).first()
      if (await item.count()) {
        await item.click().catch(() => {})
        opened = true
      } else {
        const items = await page.locator('[role="menuitem"]').evaluateAll((els) =>
          els.map((e) => (e.textContent || "").trim().slice(0, 14)),
        )
        console.log(`${view.padEnd(11)} menu has no destructive item: ${items.join(" | ")}`)
        await page.keyboard.press("Escape")
      }
    }
  }
  if (!opened) {
    console.log(`${view.padEnd(11)} no destructive action on the row`)
    await ctx.close()
    continue
  }

  await page.waitForTimeout(1000)
  const dlg = page.locator(DLG).first()
  if (!(await dlg.count())) {
    console.log(`${view.padEnd(11)} destructive action opened no confirm dialog`)
    await ctx.close()
    continue
  }
  const text = await dlg.innerText()
  const confirm = dlg.getByRole("button", { name: /删除|移除|吊销|撤销|确认|Delete|Remove/ }).last()
  const guarded = !(await confirm.isEnabled())
  if (guarded) {
    const token = text.match(/[「『]([^」』]{2,60})[」』]/)?.[1] ?? ""
    const input = dlg.locator("input").first()
    if ((await input.count()) && token) {
      await input.fill(token)
      await page.waitForTimeout(500)
    }
  }
  const enabled = await confirm.isEnabled()
  if (enabled) {
    await confirm.click().catch(() => {})
    await page.waitForTimeout(2500)
  }
  const after = await page.locator(ROW).count()
  const stuck = await page.locator(DLG).count()
  console.log(
    `${view.padEnd(11)} rows ${before}→${after}  typeToConfirm=${guarded}  confirmed=${enabled}  stuckDialog=${stuck}  ${calls.join(" | ") || "(no call)"}${errs.length ? "  PAGEERROR " + errs[0] : ""}`,
  )
  await page.screenshot({ path: path.join(OUT, `destructive-${view}.png`) })
  await ctx.close()
}
await browser.close()
