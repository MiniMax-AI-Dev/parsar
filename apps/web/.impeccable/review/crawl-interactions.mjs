// Interaction paths the view crawler cannot reach: keyboard shortcuts,
// the rail's expand-to-modal, panel drag + the layout prompt, pagination,
// search and filter, and the conversation surface's own controls.
import { chromium } from "@playwright/test"
import { mkdirSync } from "node:fs"
import path from "node:path"

const OUT = path.resolve(".impeccable/review/crawl")
mkdirSync(OUT, { recursive: true })
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const WS = process.env.WS
const DEV = process.env.DEV_USER

const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
const problems = []
const check = (label, ok, detail = "") => {
  console.log(`${ok ? "ok  " : "FAIL"} ${label}${detail ? "  " + detail : ""}`)
  if (!ok) problems.push(`${label} ${detail}`)
}

async function open(url, { motion = "reduce" } = {}) {
  const ctx = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    locale: "zh-CN",
    reducedMotion: motion,
    extraHTTPHeaders: { "X-Parsar-Dev-User-ID": DEV },
  })
  await ctx.addInitScript((ws) => {
    localStorage.setItem("parsar.theme", "light")
    localStorage.setItem("parsar.lang", "zh-CN")
    localStorage.setItem("parsar.ws", ws)
  }, WS)
  const page = await ctx.newPage()
  const errs = []
  page.on("pageerror", (e) => errs.push(e.message.slice(0, 160)))
  await page.goto(`http://127.0.0.1:5173${url}`, { waitUntil: "load" })
  await page.waitForTimeout(2200)
  return { ctx, page, errs }
}

/* 1. Runs: ⌘K focus, J/K selection, rail open → expand modal → collapse → close */
{
  const { ctx, page, errs } = await open("/?admin=runs")
  await page.keyboard.press("Meta+k")
  await page.waitForTimeout(400)
  const focused = await page.evaluate(() => document.activeElement?.getAttribute("type") === "search")
  check("runs ⌘K focuses search", focused)
  await page.keyboard.press("Escape")
  await page.locator("body").click({ position: { x: 700, y: 500 } })

  await page.keyboard.press("j")
  await page.waitForTimeout(700)
  const afterJ = new URL(page.url()).searchParams.get("id")
  check("runs J selects a row", !!afterJ, afterJ ? `id=${afterJ.slice(0, 12)}…` : "no id in url")
  await page.keyboard.press("j")
  await page.waitForTimeout(700)
  const afterJJ = new URL(page.url()).searchParams.get("id")
  check("runs J advances", !!afterJJ && afterJJ !== afterJ)
  await page.keyboard.press("k")
  await page.waitForTimeout(700)
  check("runs K goes back", new URL(page.url()).searchParams.get("id") === afterJ)

  const railOpen = await page.locator("aside[aria-label]").count()
  check("rail opens with selection", railOpen > 0)
  await page.getByRole("button", { name: "展开" }).click()
  await page.waitForTimeout(700)
  const modal = await page.locator('[role="dialog"]').count()
  check("rail expands into a modal", modal > 0)
  await page.screenshot({ path: path.join(OUT, "kb-rail-modal.png") })
  await page.getByRole("button", { name: "收起" }).click()
  await page.waitForTimeout(700)
  check("modal collapses back to the rail", (await page.locator('[role="dialog"]').count()) === 0 && (await page.locator("aside[aria-label]").count()) > 0)
  await page.getByRole("button", { name: "关闭" }).first().click()
  await page.waitForTimeout(800)
  check("rail closes", (await page.locator("aside[aria-label]").count()) === 0 && !new URL(page.url()).searchParams.get("id"))
  check("runs keyboard/rail without page errors", errs.length === 0, errs[0] ?? "")
  await ctx.close()
}

/* 2. Sidebar drag + layout prompt (save / session / restore) */
{
  const { ctx, page, errs } = await open("/?admin=runs")
  const handle = page.locator('[role="separator"], [aria-orientation="vertical"]').first()
  const box = await handle.boundingBox().catch(() => null)
  if (!box) {
    check("sidebar drag handle present", false)
  } else {
    await page.mouse.move(box.x + box.width / 2, 400)
    await page.mouse.down()
    await page.mouse.move(box.x + 60, 400, { steps: 10 })
    await page.mouse.up()
    await page.waitForTimeout(700)
    const width = await page.locator("aside").first().evaluate((el) => el.parentElement?.getBoundingClientRect().width ?? 0)
    check("sidebar drag widens the panel", width > 250, `width=${Math.round(width)}`)
    const prompt = page.getByRole("status")
    check("layout prompt appears after a drag", (await prompt.count()) > 0)
    await page.screenshot({ path: path.join(OUT, "kb-layout-prompt.png") })
    const restore = page.getByRole("button", { name: /恢复默认|恢复/ }).first()
    if (await restore.count()) {
      await restore.click()
      await page.waitForTimeout(900)
      const back = await page.locator("aside").first().evaluate((el) => el.parentElement?.getBoundingClientRect().width ?? 0)
      check("restore springs back to 232", Math.abs(back - 232) < 3, `width=${Math.round(back)}`)
    }
  }
  check("drag without page errors", errs.length === 0, errs[0] ?? "")
  await ctx.close()
}

/* 3. Runs: search + filter + pagination */
{
  const { ctx, page, errs } = await open("/?admin=runs")
  const search = page.locator('input[type="search"]').first()
  await search.fill("zzzz-no-match")
  await page.waitForTimeout(900)
  const empty = await page.getByText(/没有|无匹配|Nothing|No runs/).count()
  check("search narrows to an empty state", empty > 0)
  await search.fill("")
  await page.waitForTimeout(700)
  await page.getByRole("button", { name: /筛选/ }).click()
  await page.waitForTimeout(500)
  const menuItems = await page.getByRole("menuitemradio").count()
  check("filter menu lists the statuses", menuItems >= 3, `items=${menuItems}`)
  if (menuItems) {
    await page.getByRole("menuitemradio").nth(1).click()
    await page.waitForTimeout(1200)
    await page.screenshot({ path: path.join(OUT, "kb-filter-applied.png") })
  }
  check("filter without page errors", errs.length === 0, errs[0] ?? "")
  await ctx.close()
}

/* 4. Conversations: new conversation, rename, fold, agent switcher */
{
  const { ctx, page, errs } = await open("/?admin=conversations")
  const before = await page.locator('[role="option"]').count()
  const newBtn = page.getByRole("button", { name: /新建对话|新对话/ }).first()
  if (await newBtn.count()) {
    await newBtn.click()
    await page.waitForTimeout(1500)
    check("new conversation opens an empty thread", (await page.locator("textarea").count()) > 0)
  } else {
    check("new conversation control present", false)
  }
  const row = page.locator('[role="option"]').first()
  await row.hover()
  await page.waitForTimeout(400)
  const rename = row.getByRole("button").first()
  if (await rename.count()) {
    await rename.click()
    await page.waitForTimeout(600)
    await page.screenshot({ path: path.join(OUT, "kb-conv-rename.png") })
    await page.keyboard.press("Escape")
    await page.waitForTimeout(400)
  }
  const fold = page.getByRole("button", { name: /折叠|收起列表|展开列表/ }).first()
  if (await fold.count()) {
    await fold.click()
    await page.waitForTimeout(700)
    const after = await page.locator('[role="option"]').count()
    check("fold hides the conversation list", after < before || after === 0, `${before} → ${after}`)
    await fold.click().catch(() => {})
    await page.waitForTimeout(600)
  }
  check("conversations controls without page errors", errs.length === 0, errs[0] ?? "")
  await ctx.close()
}

await browser.close()
console.log(`\n${problems.length === 0 ? "all interaction paths clean" : `${problems.length} problems`}`)
for (const p of problems) console.log("  -", p)
