// The Agent rail: open, switch, every tab, expand (tabs must survive it), close.
import { chromium } from "@playwright/test"
import path from "node:path"
const OUT = path.resolve(".impeccable/review")
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
const railWidth = (page) => page.evaluate(() => {
  const el = document.querySelector("aside.border-l")
  return el ? Math.round(el.parentElement.getBoundingClientRect().width) : 0
})
const fits = (page) => page.evaluate(() => {
  const out = []
  for (const g of document.querySelectorAll('li[role="option"], div[aria-hidden="true"]')) {
    if (g.scrollWidth > g.clientWidth + 1) out.push(`${g.scrollWidth}>${g.clientWidth}`)
  }
  return out.slice(0, 4)
})
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN", extraHTTPHeaders: { "X-Parsar-Dev-User-ID": process.env.DEV_USER } })
await ctx.addInitScript((ws) => { localStorage.setItem("parsar.theme","light"); localStorage.setItem("parsar.lang","zh-CN"); localStorage.setItem("parsar.ws", ws) }, process.env.WS)
const page = await ctx.newPage()
const bad = []
page.on("response", r => { const u = r.url(); if (u.includes("/api/") && r.status() >= 400) bad.push(`${r.status()} ${u.split("/api/v1")[1] || u}`) })
page.on("pageerror", e => console.log("  PAGEERROR:", e.message.slice(0, 200)))
await page.goto("http://127.0.0.1:5173/?admin=agents", { waitUntil: "load" })
await page.waitForTimeout(2400)
const rows = page.locator('li[role="option"], li[tabindex="0"]')
const n = await rows.count()
console.log("rows:", n, "| list overflow closed:", (await fits(page)).join(",") || "none")

await rows.nth(0).click(); await page.waitForTimeout(900)
console.log("open   → width", await railWidth(page), "| header kept:", await page.locator('input[type="search"]').count() > 0)
console.log("       → list overflow open:", (await fits(page)).join(",") || "none")
await page.screenshot({ path: path.join(OUT, "eff-agent-dynamics.png") })

for (const tab of ["配置", "审计"]) {
  await page.getByRole("tab", { name: tab }).click(); await page.waitForTimeout(1400)
  console.log(`tab ${tab} → url`, (await page.url()).split("?")[1], "| width", await railWidth(page))
  await page.screenshot({ path: path.join(OUT, `eff-agent-${tab === "配置" ? "config" : "audit"}.png`) })
}

// expand must survive a tab change
await page.getByRole("button", { name: /展开/ }).first().click(); await page.waitForTimeout(900)
console.log("expand →", (await page.url()).split("?")[1])
await page.screenshot({ path: path.join(OUT, "eff-agent-expanded.png") })
await page.getByRole("tab", { name: "动态" }).click(); await page.waitForTimeout(1200)
const stillOpen = await page.evaluate(() => document.querySelector('[role="dialog"][data-state="open"]') !== null)
console.log("tab inside panel → url", (await page.url()).split("?")[1], "| panel still open:", stillOpen)
await page.keyboard.press("Escape"); await page.waitForTimeout(700)
console.log("esc    →", (await page.url()).split("?")[1])

if (n > 1) {
  const w = []
  await rows.nth(1).click()
  for (let i = 0; i < 4; i++) { w.push(await railWidth(page)); await page.waitForTimeout(60) }
  console.log("switch → widths", w.join(","))
}
const w2 = []
await rows.nth(n > 1 ? 1 : 0).click()
for (let i = 0; i < 4; i++) { w2.push(await railWidth(page)); await page.waitForTimeout(80) }
await page.waitForTimeout(500)
console.log("toggle close → widths", w2.join(","), "→", await railWidth(page))
console.log("api errors:", bad.length ? [...new Set(bad)].join(" | ") : "none")
await browser.close()
