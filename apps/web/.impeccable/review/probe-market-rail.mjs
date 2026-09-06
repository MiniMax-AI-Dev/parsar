// Marketplace + skills tabs against the fixture mock: open, switch, expand, close.
import { chromium } from "@playwright/test"
import path from "node:path"
const OUT = path.resolve(".impeccable/review")
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
const railWidth = (page) => page.evaluate(() => {
  const el = document.querySelector("aside.border-l")
  return el ? Math.round(el.parentElement.getBoundingClientRect().width) : 0
})
for (const [label, url, shot, theme] of [
  ["marketplace", "/?admin=capabilities&tab=marketplace", "railx-market", "light"],
  ["marketplace dark", "/?admin=capabilities&tab=marketplace", "railx-market-dark", "dark"],
  ["mcp", "/?admin=capabilities&tab=connectors", "railx-mcp", "light"],
  ["workspace", "/?admin=capabilities", "railx-ws", "light"],
]) {
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN" })
  await ctx.addInitScript((t) => { localStorage.setItem("parsar.theme", t); localStorage.setItem("parsar.lang","zh-CN"); localStorage.setItem("parsar.ws","0f4d2c6e-9b1a-4e7c-8f3d-2a1b5c6d7e8f") }, theme)
  const page = await ctx.newPage()
  const bad = []
  page.on("response", r => { const u=r.url(); if (u.includes("/api/") && r.status()>=400) bad.push(`${r.status()} ${u.split("/api/v1")[1]||u}`) })
  page.on("pageerror", e => console.log("  PAGEERROR:", e.message.slice(0,180)))
  await page.goto("http://127.0.0.1:5174" + url, { waitUntil: "networkidle" })
  await page.waitForTimeout(900)
  const rows = page.locator('li[role="option"], li[tabindex="0"]')
  const n = await rows.count()
  console.log(`\n=== ${label} — rows: ${n}`)
  if (!n) { await ctx.close(); continue }
  await rows.nth(0).click(); await page.waitForTimeout(800)
  console.log("  open → width", await railWidth(page), "| header kept:", await page.locator('input[type="search"]').count() > 0)
  await page.screenshot({ path: path.join(OUT, `${shot}-rail.png`) })
  if (n > 1) {
    const w = []
    await rows.nth(1).click()
    for (let i=0;i<4;i++){ w.push(await railWidth(page)); await page.waitForTimeout(60) }
    console.log("  switch → widths", w.join(","))
  }
  const exp = page.getByRole("button", { name: /展开|Expand/ }).first()
  if (await exp.count()) {
    await exp.click(); await page.waitForTimeout(700)
    console.log("  expand → url", (await page.url()).split("?")[1])
    await page.screenshot({ path: path.join(OUT, `${shot}-expanded.png`) })
    await page.keyboard.press("Escape"); await page.waitForTimeout(600)
    console.log("  esc   → url", (await page.url()).split("?")[1])
  }
  const w2 = []
  await rows.nth(n>1?1:0).click()
  for (let i=0;i<4;i++){ w2.push(await railWidth(page)); await page.waitForTimeout(80) }
  await page.waitForTimeout(400)
  console.log("  toggle close → widths", w2.join(","), "→", await railWidth(page))
  console.log("  api errors:", bad.length ? bad.join(" | ") : "none")
  await ctx.close()
}
await browser.close()
