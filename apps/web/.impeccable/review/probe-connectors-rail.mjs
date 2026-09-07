import { chromium } from "@playwright/test"
import path from "node:path"
const OUT = path.resolve(".impeccable/review")
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN", extraHTTPHeaders: { "X-Parsar-Dev-User-ID": process.env.DEV_USER } })
await ctx.addInitScript((ws) => { localStorage.setItem("parsar.theme","light"); localStorage.setItem("parsar.lang","zh-CN"); localStorage.setItem("parsar.ws", ws) }, process.env.WS)
const page = await ctx.newPage()
const bad = []
page.on("response", r => { const u=r.url(); if (u.includes("/api/") && r.status()>=400) bad.push(`${r.status()} ${u.split("/api/v1")[1]||u}`) })
page.on("pageerror", e => console.log("  PAGEERROR:", e.message.slice(0,160)))
const railWidth = () => page.evaluate(() => { const el = document.querySelector("aside.border-l"); return el ? Math.round(el.parentElement.getBoundingClientRect().width) : 0 })
await page.goto("http://127.0.0.1:5173/?admin=settings", { waitUntil: "load" })
await page.waitForTimeout(2400)
console.log("settings tab strip:", (await page.locator('[role="tab"]').allInnerTexts()).join(" | "))
await page.getByRole("tab", { name: "Agent 连接器" }).click()
await page.waitForTimeout(1800)
console.log("clicked Agent 连接器 → url:", (await page.url()).split("?")[1])
console.log("active tab there:", await page.evaluate(() => document.querySelector('[role="tab"][data-state="active"]')?.textContent?.trim()))
const rows = page.locator('li[role="option"], li[tabindex="0"]')
const n = await rows.count()
console.log("rows:", n)
await rows.nth(0).click(); await page.waitForTimeout(900)
console.log("open → width", await railWidth(), "| header kept:", await page.locator('input[type="search"]').count() > 0)
console.log("       active tab still:", await page.evaluate(() => document.querySelector('[role="tab"][data-state="active"]')?.textContent?.trim()))
await page.screenshot({ path: path.join(OUT, "eff-connectors-rail.png") })
if (n > 1) {
  const w = []
  await rows.nth(1).click()
  for (let i=0;i<4;i++){ w.push(await railWidth()); await page.waitForTimeout(60) }
  console.log("switch → widths", w.join(","))
}
await page.getByRole("button", { name: /展开/ }).first().click(); await page.waitForTimeout(800)
console.log("expand →", (await page.url()).split("?")[1])
await page.keyboard.press("Escape"); await page.waitForTimeout(700)
const w2 = []
await rows.nth(n>1?1:0).click()
for (let i=0;i<4;i++){ w2.push(await railWidth()); await page.waitForTimeout(80) }
await page.waitForTimeout(400)
console.log("toggle close → widths", w2.join(","), "→", await railWidth())
console.log("api errors:", bad.length ? [...new Set(bad)].join(" | ") : "none")
await browser.close()
