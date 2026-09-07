import { chromium } from "@playwright/test"
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox"] })
async function measure(url, sel, init) {
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN" })
  if (init) await ctx.addInitScript(init)
  const page = await ctx.newPage(); await page.goto(url, { waitUntil: "networkidle" }); await page.waitForTimeout(600)
  const out = {}
  for (const [name, s] of Object.entries(sel)) {
    out[name] = await page.evaluate((s) => { const el = document.querySelector(s); if (!el) return "n/a"; const cs = getComputedStyle(el); return `${cs.fontSize} / ${cs.fontWeight} / lh ${cs.lineHeight} / ${cs.fontFamily.split(",")[0]}` }, s)
  }
  await ctx.close(); return out
}
const app = await measure("http://127.0.0.1:5173/?admin=runs&id=run_01J8Z03KX2P9Q03", {
  html: "html", body: "body", sidebarNav: "aside nav button span", groupLabel: "aside nav > div", wsRow: "aside > button span",
  title: "h1 > span", subtitle: "h1 span + span", search: "input[type=search]", filterBtn: "header button", colHeader: "[aria-hidden] > span:nth-child(2)",
  groupHdr: "section > button > span", rowId: "li[role=option] > span:nth-child(2)", rowAgent: "li[role=option] > span:nth-child(3) span:nth-child(2)",
  footer: "div.h-10 span", railHeader: "aside[aria-label] .text-sm", propLabel: "dt", propValue: "dd", tab: "[role=tab]",
}, () => { localStorage.setItem("parsar.ws","0f4d2c6e-9b1a-4e7c-8f3d-2a1b5c6d7e8f"); localStorage.setItem("parsar.lang","zh-CN") })
const proto = await measure("http://127.0.0.1:18478/e-ledger-compact.html", {
  html: "html", body: "body", sidebarNav: ".nav span", groupLabel: ".grp-label", wsRow: ".ws .name", title: ".topbar h1", subtitle: ".topbar h1 small",
  search: ".search input", filterBtn: ".topbar .btn", colHeader: ".hdr span:nth-child(2)", groupHdr: ".grp-h b", rowId: ".rid", rowAgent: ".ttl",
  footer: ".foot .count", railHeader: ".rail-h .status", propLabel: ".props dt", propValue: ".props dd", tab: ".seg button",
})
for (const k of Object.keys(app)) console.log(k.padEnd(12), "| app:", app[k], "\n".padEnd(14), "| proto:", proto[k])
await browser.close()
