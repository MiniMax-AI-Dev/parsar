// Drive a REAL run end to end: send a message to the local HTTP agent,
// watch the answer arrive, then inspect the run in the Runs ledger + rail.
import { chromium } from "@playwright/test"
import path from "node:path"

const OUT = path.resolve(".impeccable/review/smoke")
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const WS = process.env.WS
const DEV = process.env.DEV_USER

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
page.on("pageerror", (e) => console.log("PAGEERROR:", e.message.slice(0, 200)))

await page.goto("http://127.0.0.1:5173/?admin=conversations", { waitUntil: "load" })
await page.waitForTimeout(2500)

// Bind the conversation surface to the HTTP agent via the list-header switcher.
await page.locator("[aria-haspopup]").first().click().catch(() => {})
await page.waitForTimeout(700)
const opt = page.getByRole("menuitem", { name: /http-smoke/ })
if (await opt.count()) {
  await opt.first().click()
  await page.waitForTimeout(1800)
  console.log("agent switched to http-smoke")
} else {
  console.log("agent switcher: no http-smoke item")
  await page.keyboard.press("Escape")
}
await page.screenshot({ path: path.join(OUT, "run-0-before.png") })

const ta = page.locator("textarea").first()
const disabled = await ta.isDisabled().catch(() => "n/a")
console.log("composer disabled:", disabled)
if (disabled === false) {
  await ta.fill("用一句话解释什么是 Issue Ledger。")
  await page.keyboard.press("Enter")
  let i = 0
  for (const wait of [1500, 3000, 4000, 6000]) {
    await page.waitForTimeout(wait)
    await page.screenshot({ path: path.join(OUT, `run-1-${i++}.png`) })
  }
}
console.log("write calls:", calls.join(" | ") || "(none)")

await page.goto("http://127.0.0.1:5173/?admin=runs", { waitUntil: "load" })
await page.waitForTimeout(2500)
await page.screenshot({ path: path.join(OUT, "run-2-runs-list.png") })
const rows = page.getByRole("option")
console.log("run rows:", await rows.count())
if (await rows.count()) {
  await rows.first().click()
  await page.waitForTimeout(2500)
  await page.screenshot({ path: path.join(OUT, "run-3-run-rail.png") })
}
await browser.close()
