// Systematic path crawler for the console, run against the REAL backend.
//
//   LD_LIBRARY_PATH=... WS=<uuid> DEV_USER=<uuid> node .impeccable/review/crawl.mjs [views...]
//
// For every view it: loads the page, clicks each topbar control, each tab,
// each row action of the first row, and opens the first row's detail —
// recording page errors, console errors, failed API calls and dialogs that
// refuse to close. Destructive actions are only opened, never confirmed
// (pass --destructive to confirm them too).
import { chromium } from "@playwright/test"
import { mkdirSync, writeFileSync } from "node:fs"
import path from "node:path"

const OUT = path.resolve(".impeccable/review/crawl")
mkdirSync(OUT, { recursive: true })
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const WS = process.env.WS
const DEV = process.env.DEV_USER
const CONFIRM_DESTRUCTIVE = process.argv.includes("--destructive")
const THEME = process.env.THEME ?? "light"
const ALL = [
  "conversations", "approvals", "runs", "scheduled", "agents", "capabilities",
  "models", "connections", "members", "settings", "secrets", "runtime",
  "connectors", "usage", "audit",
]
const views = process.argv.slice(2).filter((a) => !a.startsWith("--"))
const VIEWS = views.length ? views : ALL

// Never click these: they end the session or leave the app.
const SKIP = /退出|登出|Sign out|Log out|切换工作区|Skip to main/
const DESTRUCTIVE = /删除|移除|注销|撤销|吊销|归档|卸载|停用|Delete|Remove|Revoke/

const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
const findings = []

function watch(page, ctxLabel) {
  const bag = { pageErrors: [], consoleErrors: [], bad: [] }
  page.on("pageerror", (e) => bag.pageErrors.push(`${ctxLabel()}: ${e.message.slice(0, 200)}`))
  page.on("console", (m) => {
    if (m.type() !== "error") return
    const t = m.text()
    if (t.includes("Failed to load resource")) return // covered by the response hook
    bag.consoleErrors.push(`${ctxLabel()}: ${t.slice(0, 200)}`)
  })
  page.on("response", (r) => {
    if (!r.url().includes("/api/") && !r.url().includes("/dev/")) return
    if (r.status() < 400) return
    bag.bad.push(`${ctxLabel()}: ${r.status()} ${r.request().method()} ${r.url().split("/api/v1")[1] ?? r.url()}`)
  })
  return bag
}

for (const view of VIEWS) {
  const ctx = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    locale: "zh-CN",
    reducedMotion: "reduce",
    extraHTTPHeaders: { "X-Parsar-Dev-User-ID": DEV },
  })
  await ctx.addInitScript(([ws, theme]) => {
    localStorage.setItem("parsar.theme", theme)
    localStorage.setItem("parsar.lang", "zh-CN")
    localStorage.setItem("parsar.ws", ws)
  }, [WS, THEME])
  const page = await ctx.newPage()
  let step = "load"
  const bag = watch(page, () => `${view}/${step}`)
  page.on("popup", (p) => p.close().catch(() => {}))

  await page.goto(`http://127.0.0.1:5173/?admin=${view}`, { waitUntil: "load" })
  await page.waitForTimeout(2200)

  // Ledgers are listbox/option when rows select something and list/listitem
  // otherwise, so both shapes count as rows.
  const rowSel = '[role="option"], [role="listitem"], tbody tr'
  const rowsBefore = await page.locator(rowSel).count()

  const dialogSel = '[role="dialog"], [role="alertdialog"]'
  const closeAnyDialog = async () => {
    for (let i = 0; i < 3; i++) {
      if (!(await page.locator(dialogSel).count())) return true
      await page.keyboard.press("Escape")
      await page.waitForTimeout(450)
    }
    if (await page.locator(dialogSel).count()) {
      const cancel = page.locator(dialogSel).getByRole("button", { name: /取消|关闭|Cancel|Close/ }).last()
      if (await cancel.count()) {
        await cancel.click().catch(() => {})
        await page.waitForTimeout(500)
      }
    }
    return !(await page.locator(dialogSel).count())
  }


  // Some destructive dialogs require typing the object's name; the confirm
  // button stays disabled until it matches. Pull the token out of 「…」 (or
  // the input's placeholder) and type it.
  const confirmDestructive = async () => {
    const dlg = page.locator(dialogSel).first()
    const confirm = dlg.getByRole("button", { name: /删除|移除|吊销|撤销|归档|卸载|停用|确认|Delete|Remove|Confirm/ }).last()
    if (!(await confirm.count())) return false
    if (!(await confirm.isEnabled())) {
      const text = await dlg.innerText().catch(() => "")
      const quoted = text.match(/[「『"]([^」』"]{2,60})[」』"]/)
      const input = dlg.locator("input").first()
      const token = quoted?.[1] ?? (await input.getAttribute("placeholder")) ?? ""
      if ((await input.count()) && token) {
        await input.fill(token)
        await page.waitForTimeout(500)
      }
    }
    if (!(await confirm.isEnabled())) return false
    await confirm.click().catch(() => {})
    await page.waitForTimeout(1800)
    return true
  }

  const clickables = async (scope) =>
    scope.locator('button:visible, [role="tab"]:visible').evaluateAll((els) =>
      els.map((el) => (el.getAttribute("aria-label") || el.textContent || "").trim().slice(0, 24)).filter(Boolean),
    )

  // 1. topbar controls + tabs
  const header = page.locator("header").first()
  const names = [...new Set(await clickables(page.locator("main")))]
  const opened = []
  for (const name of names) {
    if (!name || SKIP.test(name)) continue
    step = `click:${name}`
    const target = page.locator("main").getByRole("button", { name, exact: true })
      .or(page.locator("main").getByRole("tab", { name, exact: true })).first()
    if (!(await target.count())) continue
    await target.click({ timeout: 4000 }).catch(() => {})
    await page.waitForTimeout(900)
    const hadDialog = (await page.locator(dialogSel).count()) > 0
    if (hadDialog) {
      opened.push(name)
      await page.screenshot({ path: path.join(OUT, `${view}-dlg-${name.replace(/[^\w一-龥]/g, "_").slice(0, 14)}.png`) })
      if (CONFIRM_DESTRUCTIVE && DESTRUCTIVE.test(name)) await confirmDestructive()
      const closed = await closeAnyDialog()
      if (!closed) bag.pageErrors.push(`${view}/${step}: dialog would not close`)
    }
    await page.keyboard.press("Escape").catch(() => {})
    await page.waitForTimeout(200)
  }

  // 2. first row: hover for actions, then open the detail
  step = "row"
  const rows = page.locator(rowSel)
  const rowCount = rowsBefore
  if (rowCount > 0) {
    const row = rows.first()
    await row.hover().catch(() => {})
    await page.waitForTimeout(400)
    const actions = await row.locator("button:visible").evaluateAll((els) =>
      els.map((e) => (e.getAttribute("aria-label") || e.textContent || "").trim().slice(0, 20)).filter(Boolean),
    )
    const startUrl = page.url()
    for (const a of actions) {
      if (SKIP.test(a)) continue
      // A row action may navigate (e.g. "chat with this agent"); come back
      // so the next action is still read from the row we are crawling.
      if (page.url() !== startUrl) {
        await page.goto(startUrl, { waitUntil: "load" })
        await page.waitForTimeout(1600)
        await rows.first().hover().catch(() => {})
        await page.waitForTimeout(300)
      }
      step = `rowaction:${a}`
      const btn = row.getByRole("button", { name: a, exact: true }).first()
      if (!(await btn.count())) continue
      await btn.click({ timeout: 4000 }).catch(() => {})
      await page.waitForTimeout(900)
      // Row actions often live behind a "more" menu: walk it too.
      const menu = page.locator('[role="menu"]:visible')
      if (await menu.count()) {
        const items = await menu.locator('[role="menuitem"], [role="menuitemradio"]').evaluateAll((els) =>
          els.map((e) => (e.textContent || "").trim().slice(0, 20)).filter(Boolean),
        )
        for (const item of items) {
          if (SKIP.test(item)) continue
          if (DESTRUCTIVE.test(item) && !CONFIRM_DESTRUCTIVE) continue
          step = `menuitem:${item}`
          const mi = page.locator('[role="menu"]:visible').getByRole("menuitem", { name: item, exact: true }).first()
          if (!(await mi.count())) {
            await btn.click({ timeout: 3000 }).catch(() => {})
            await page.waitForTimeout(600)
            continue
          }
          await mi.click({ timeout: 3000 }).catch(() => {})
          await page.waitForTimeout(1000)
          if (await page.locator(dialogSel).count()) {
            await page.screenshot({ path: path.join(OUT, `${view}-menu-${item.replace(/[^\w一-龥]/g, "_").slice(0, 12)}.png`) })
            if (CONFIRM_DESTRUCTIVE && DESTRUCTIVE.test(item)) await confirmDestructive()
            if (!(await closeAnyDialog())) bag.pageErrors.push(`${view}/${step}: dialog would not close`)
          }
          await page.keyboard.press("Escape").catch(() => {})
          await page.waitForTimeout(300)
          if (page.url() !== startUrl) break
          await rows.first().hover().catch(() => {})
          await btn.click({ timeout: 3000 }).catch(() => {})
          await page.waitForTimeout(600)
        }
        await page.keyboard.press("Escape").catch(() => {})
      }
      if (await page.locator(dialogSel).count()) {
        await page.screenshot({ path: path.join(OUT, `${view}-row-${a.replace(/[^\w一-龥]/g, "_").slice(0, 12)}.png`) })
        if (CONFIRM_DESTRUCTIVE && DESTRUCTIVE.test(a)) await confirmDestructive()
        if (!(await closeAnyDialog())) bag.pageErrors.push(`${view}/${step}: dialog would not close`)
      }
      await page.keyboard.press("Escape").catch(() => {})
      await page.waitForTimeout(200)
    }
    step = "open-detail"
    await rows.first().click({ timeout: 4000 }).catch(() => {})
    await page.waitForTimeout(1800)
    await page.screenshot({ path: path.join(OUT, `${view}-detail.png`) })
    // tabs inside the detail (rail or page)
    const detailTabs = [...new Set(await clickables(page))]
    for (const t of detailTabs.slice(0, 12)) {
      if (!t || SKIP.test(t)) continue
      step = `detail-tab:${t}`
      const tab = page.getByRole("tab", { name: t, exact: true }).first()
      if (!(await tab.count())) continue
      await tab.click({ timeout: 3000 }).catch(() => {})
      await page.waitForTimeout(800)
    }
    await page.screenshot({ path: path.join(OUT, `${view}-detail-tabs.png`) })
  }

  const entry = {
    view,
    rows: rowCount,
    rowsAfterClicks: await page.locator(rowSel).count(),
    dialogsOpened: opened,
    pageErrors: bag.pageErrors,
    consoleErrors: [...new Set(bag.consoleErrors)],
    bad: [...new Set(bag.bad)],
  }
  findings.push(entry)
  const flag = entry.pageErrors.length ? "FAIL" : entry.bad.length ? "BAD " : entry.consoleErrors.length ? "warn" : "ok  "
  console.log(`${flag} ${view.padEnd(14)} rows=${String(rowCount).padStart(3)} dialogs=${opened.length} pageerr=${entry.pageErrors.length} bad=${entry.bad.length}`)
  for (const e of entry.pageErrors.slice(0, 3)) console.log(`      ${e}`)
  for (const b of entry.bad.slice(0, 5)) console.log(`      ${b}`)
  for (const c of entry.consoleErrors.slice(0, 3)) console.log(`      console: ${c}`)
  await ctx.close()
}

writeFileSync(path.join(OUT, `report-${THEME}.json`), JSON.stringify(findings, null, 2))
await browser.close()
const broken = findings.filter((f) => f.pageErrors.length || f.bad.length)
console.log(`\n${findings.length - broken.length}/${findings.length} views walked clean`)
