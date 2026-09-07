import { chromium } from "@playwright/test"
import path from "node:path"
const OUT = path.resolve(".impeccable/review/smoke")
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const WS = process.env.WS, DEV = process.env.DEV_USER
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
async function open(view) {
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN", reducedMotion: "reduce", extraHTTPHeaders: { "X-Parsar-Dev-User-ID": DEV } })
  await ctx.addInitScript((ws) => { localStorage.setItem("parsar.theme","light"); localStorage.setItem("parsar.lang","zh-CN"); localStorage.setItem("parsar.ws", ws) }, WS)
  const page = await ctx.newPage()
  const bad = []
  page.on("response", r => { const u=r.url(); if ((u.includes("/api/")||u.includes("/dev/")) && r.status()>=400) bad.push(`${r.status()} ${r.request().method()} ${u.split("/api/v1")[1]||u}`) })
  page.on("pageerror", e => console.log("PAGEERROR:", e.message.slice(0,160)))
  await page.goto(`http://127.0.0.1:5173/?admin=${view}`, { waitUntil: "load" })
  await page.waitForTimeout(2200)
  return { ctx, page, bad }
}
// 1. runtime → cloud sandbox tab
{
  const { ctx, page, bad } = await open("runtime")
  await page.getByRole("tab", { name: "云端沙盒" }).click().catch(() => page.getByText("云端沙盒").click())
  await page.waitForTimeout(1500)
  await page.screenshot({ path: path.join(OUT, "runtime-sandbox-tab.png") })
  console.log("sandbox tab bad responses:", bad.join(" | ") || "none")
  await ctx.close()
}
// 2. agents → open create dialog, dump controls
{
  const { ctx, page } = await open("agents")
  await page.getByRole("button", { name: /新建 Agent/ }).click()
  await page.waitForTimeout(1200)
  await page.screenshot({ path: path.join(OUT, "agents-create-dialog.png") })
  const controls = await page.evaluate(() => {
    const root = document.querySelector('[role="dialog"]') || document.body
    return [...root.querySelectorAll("input,select,textarea,button")].slice(0, 30).map(el => {
      const label = el.getAttribute("aria-label") || el.getAttribute("placeholder") || (el.textContent||"").trim().slice(0,26)
      return `${el.tagName.toLowerCase()}${el.type?"["+el.type+"]":""} "${label}"`
    })
  })
  console.log("dialog controls:\n  " + controls.join("\n  "))
  await ctx.close()
}
await browser.close()
