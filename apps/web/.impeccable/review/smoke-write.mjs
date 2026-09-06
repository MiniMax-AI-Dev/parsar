// Exercise the console's write paths against the REAL backend:
// create a model, create an agent, start a conversation and send a message.
// Reports every API call the UI makes and its status.
import { chromium } from "@playwright/test"
import { mkdirSync } from "node:fs"
import path from "node:path"

const OUT = path.resolve(".impeccable/review/smoke")
mkdirSync(OUT, { recursive: true })
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const WS = process.env.WS
const DEV = process.env.DEV_USER
const stamp = Date.now().toString().slice(-5)

const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
const ctx = await browser.newContext({
  viewport: { width: 1440, height: 900 },
  locale: "zh-CN",
  reducedMotion: "reduce",
  extraHTTPHeaders: { "X-Parsar-Dev-User-ID": DEV },
})
await ctx.addInitScript((ws) => {
  localStorage.setItem("parsar.theme", "light")
  localStorage.setItem("parsar.lang", "zh-CN")
  localStorage.setItem("parsar.ws", ws)
}, WS)

const page = await ctx.newPage()
const writes = []
page.on("response", (r) => {
  const m = r.request().method()
  if (m === "GET" || !r.url().includes("/api/")) return
  writes.push(`${r.status()} ${m} ${r.url().split("/api/v1")[1]}`)
})
page.on("pageerror", (e) => console.log("  PAGEERROR:", e.message.slice(0, 160)))

const go = async (view) => {
  await page.goto(`http://127.0.0.1:5173/?admin=${view}`, { waitUntil: "load" })
  await page.waitForTimeout(2000)
}
const flush = (label) => {
  console.log(`${label}: ${writes.length ? writes.join(" | ") : "(no write calls)"}`)
  writes.length = 0
}

// ── 1. create a model ────────────────────────────────────────────────
await go("models")
await page.getByRole("button", { name: /新建模型/ }).click()
const dlg = page.locator('[role="dialog"]')
await dlg.waitFor({ state: "visible", timeout: 20000 }).catch(async () => {
  await page.screenshot({ path: path.join(OUT, "write-0-no-dialog.png") })
  console.log("  model dialog did not open; buttons on page:",
    (await page.locator("button").evaluateAll((els) => els.map((e) => (e.textContent || "").trim().slice(0, 16)).filter(Boolean))).join(" / "))
  throw new Error("model dialog missing")
})
await page.waitForTimeout(600)
// React inputs carry no explicit type attribute, so match by placeholder.
await dlg.getByPlaceholder("例：Claude Opus 4.5").fill(`Smoke Model ${stamp}`)
await dlg.getByPlaceholder("https://api.example.com/v1").fill("https://api.minimax.chat/v1")
await dlg.getByPlaceholder("sk-...").fill("sk-smoke-not-a-real-key")
await page.screenshot({ path: path.join(OUT, "write-1-model-filled.png") })
await dlg.getByRole("button", { name: "创建模型" }).click()
await page.waitForTimeout(2500)
await page.screenshot({ path: path.join(OUT, "write-2-model-created.png") })
flush("model create")
console.log("  rows in ledger:", await page.getByRole("option").count())

// ── 2. create an agent ───────────────────────────────────────────────
await go("agents")
await page.getByRole("button", { name: /新建 Agent/ }).click()
const wiz = page.locator('[role="dialog"]')
await wiz.waitFor({ state: "visible", timeout: 20000 })
await page.waitForTimeout(600)
await wiz.getByPlaceholder("e.g. 数据分析助手").fill(`smoke-agent-${stamp}`)
// Cloud isolation needs no paired device.
await wiz.getByText("云端隔离", { exact: false }).first().click().catch(() => {})
await page.waitForTimeout(500)
await page.screenshot({ path: path.join(OUT, "write-3-agent-step1.png") })
const next = wiz.getByRole("button", { name: "下一步" })
if (await next.isEnabled()) {
  await next.click()
  await page.waitForTimeout(1200)
  await page.screenshot({ path: path.join(OUT, "write-4-agent-step2.png") })
  const labels = await wiz.locator("button").evaluateAll((els) =>
    els.map((e) => (e.textContent || "").trim().slice(0, 20)).filter(Boolean),
  )
  console.log("  step2 buttons:", labels.join(" / "))
  const create = wiz.getByRole("button", { name: /创建|完成/ }).last()
  if (await create.count()) {
    await create.click()
    await page.waitForTimeout(3000)
  }
} else {
  console.log("  next disabled — step 1 incomplete (model or device missing)")
}
await page.screenshot({ path: path.join(OUT, "write-5-agent-done.png") })
flush("agent create")
await go("agents")
console.log("  agent rows:", await page.getByRole("option").count())

// ── 3. conversation + message ────────────────────────────────────────
await go("conversations")
await page.screenshot({ path: path.join(OUT, "write-6-conversations.png") })
const composer = page.locator("textarea").first()
if (await composer.count()) {
  await composer.fill("Say hello and stop.")
  await page.waitForTimeout(300)
  await page.keyboard.press("Enter")
  await page.waitForTimeout(6000)
  await page.screenshot({ path: path.join(OUT, "write-7-message-sent.png") })
} else {
  console.log("  no composer (no agent bound?)")
}
flush("conversation send")

await browser.close()
