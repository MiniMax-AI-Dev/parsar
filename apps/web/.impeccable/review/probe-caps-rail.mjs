// Walk the three capability details now that they open in the rail:
// selection, toggle-close, switch, the expand→URL round trip, and back.
import { chromium } from "@playwright/test"
import path from "node:path"
const OUT = path.resolve(".impeccable/review")
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const WS = process.env.WS, DEV = process.env.DEV_USER
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })

// The rail is the aside with the left hairline; the sidebar is an aside too.
const railWidth = (page) => page.evaluate(() => {
  const el = document.querySelector("aside.border-l")
  return el ? Math.round(el.parentElement.getBoundingClientRect().width) : 0
})
const railBox = (page) => page.evaluate(() => {
  const el = document.querySelector("aside")
  if (!el) return null
  const p = el.parentElement.getBoundingClientRect()
  return Math.round(p.width)
})

async function open(url, theme = "light") {
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN", extraHTTPHeaders: { "X-Parsar-Dev-User-ID": DEV } })
  await ctx.addInitScript(([ws, t]) => { localStorage.setItem("parsar.theme", t); localStorage.setItem("parsar.lang", "zh-CN"); localStorage.setItem("parsar.ws", ws) }, [WS, theme])
  const page = await ctx.newPage()
  const bad = []
  page.on("response", r => { const u = r.url(); if (u.includes("/api/") && r.status() >= 400) bad.push(`${r.status()} ${u.split("/api/v1")[1] || u}`) })
  page.on("pageerror", e => console.log("  PAGEERROR:", e.message.slice(0, 180)))
  await page.goto(`http://127.0.0.1:5173${url}`, { waitUntil: "load" })
  await page.waitForTimeout(2200)
  return { ctx, page, bad }
}

for (const [label, url, shot] of [
  ["workspace", "/?admin=capabilities", "rail-caps-ws"],
  ["marketplace", "/?admin=capabilities&tab=marketplace", "rail-caps-market"],
  ["mcp directory", "/?admin=capabilities&tab=connectors", "rail-caps-mcp"],
]) {
  console.log(`\n=== ${label}`)
  const { ctx, page, bad } = await open(url)
  const rows = page.locator('li[role="option"], li[tabindex="0"]')
  const n = await rows.count()
  console.log("  rows:", n)
  if (n === 0) { console.log("  (empty list — nothing to open)"); await ctx.close(); continue }

  await rows.nth(0).click(); await page.waitForTimeout(700)
  console.log("  open row0 → railWidth =", await railWidth(page), "| url:", (await page.url()).split("?")[1])
  const headerGone = await page.locator('input[type="search"]').count()
  console.log("  list header still present:", headerGone > 0)
  await page.screenshot({ path: path.join(OUT, `${shot}-rail.png`) })

  if (n > 1) {
    const widths = []
    await rows.nth(1).click()
    for (let i = 0; i < 4; i++) { widths.push(await railWidth(page)); await page.waitForTimeout(60) }
    console.log("  switch row1 → widths during switch:", widths.join(","))
  }

  // expand → the URL must carry it, and back must collapse it
  const expand = page.getByRole("button", { name: /展开|Expand/ }).first()
  if (await expand.count()) {
    await expand.click(); await page.waitForTimeout(600)
    console.log("  expand → url:", (await page.url()).split("?")[1])
    console.log("  modal present:", await page.locator('[role="dialog"]').count() > 0)
    await page.screenshot({ path: path.join(OUT, `${shot}-expanded.png`) })
    await page.goBack(); await page.waitForTimeout(600)
      await page.waitForTimeout(900)
    const visible = await page.evaluate(() => {
      const d = document.querySelector('[role="dialog"][data-state]')
      return d ? d.getAttribute("data-state") : "none"
    })
    console.log("  back  → url:", (await page.url()).split("?")[1], "| modal data-state:", visible)
  }

  // clicking the open row closes it, with the same width animation
  const openIndex = n > 1 ? 1 : 0
  const closing = []
  await rows.nth(openIndex).click()
  for (let i = 0; i < 4; i++) { closing.push(await railWidth(page)); await page.waitForTimeout(80) }
  await page.waitForTimeout(500)
  console.log("  toggle close → widths:", closing.join(","), "→ final", await railWidth(page))
  console.log("  api errors:", bad.length ? bad.join(" | ") : "none")
  await ctx.close()
}
await browser.close()
