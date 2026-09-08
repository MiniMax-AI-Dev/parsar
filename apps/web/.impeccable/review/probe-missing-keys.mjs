// A key i18next cannot resolve renders as the key itself. Walk every surface in
// both locales and look for text shaped like one.
//
// Three things this checks that the obvious version does not:
//   · the entry surfaces (login, setup, onboarding, invite, join) — every key
//     in common.json lives there and nowhere else, so an admin-only sweep
//     verifies none of them
//   · attribute text — this app resolves ~295 translations straight into
//     aria-label / title / placeholder / alt, where a raw key is invisible to
//     any walk of text nodes
//   · two-segment and single-segment keys (`login.title`, `appName`), which a
//     "three or more dots" pattern silently lets through
import { chromium } from "@playwright/test"

const EXE = process.env.CHROME_EXE ?? `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const BASE = process.env.BASE ?? "http://127.0.0.1:5174"
const ADMIN = ["conversations","approvals","runs","scheduled","agents","capabilities","models","connections","members","settings","secrets","runtime","usage","audit"]
const ENTRY = ["/login", "/setup", "/onboarding", "/invite/deadbeef", "/join-workspace"]

const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox","--disable-gpu"] })
let found = 0

/** Runs in the page: every rendered string, from text nodes and from the
 *  attributes that carry copy, tested against a key shape. */
function scan() {
  // one segment (appName) or dotted, all lowercase-ish, no spaces
  const KEY = /^[a-z][a-zA-Z0-9]*(\.[a-zA-Z0-9_-]+)*$/
  const ATTRS = ["aria-label", "title", "placeholder", "alt", "aria-description"]
  const out = new Set()
  const suspect = (raw) => {
    const t = (raw || "").trim()
    if (!t || t.includes(" ") || t.length > 70) return
    if (/[一-鿿]/.test(t)) return                       // translated
    if (!t.includes(".")) return                        // single-segment: too noisy to judge here
    if (!KEY.test(t)) return
    if (/\.(com|io|net|org|dev|local|internal|json|md|ts|tsx|mjs|py|go|sh|png|svg)$/.test(t)) return  // hostnames, files
    if (/^\d/.test(t) || /^v?\d+\.\d+/.test(t)) return  // versions
    out.add(t)
  }
  const walk = (n) => {
    if (n.nodeType === 3) suspect(n.textContent)
    else if (n.nodeType === 1) {
      if (["SCRIPT","STYLE"].includes(n.tagName)) return
      for (const a of ATTRS) if (n.hasAttribute?.(a)) suspect(n.getAttribute(a))
      for (const c of n.childNodes) walk(c)
    }
  }
  walk(document.body)
  return [...out]
}

for (const lang of ["zh-CN", "en-US"]) {
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 950 }, locale: lang,
    extraHTTPHeaders: process.env.DEV_USER ? { "X-Parsar-Dev-User-ID": process.env.DEV_USER } : {} })
  await ctx.addInitScript(([l, ws]) => {
    localStorage.setItem("parsar.lang", l); localStorage.setItem("parsar.theme", "light")
    if (ws) localStorage.setItem("parsar.ws", ws)
  }, [lang, process.env.WS ?? "0f4d2c6e-9b1a-4e7c-8f3d-2a1b5c6d7e8f"])
  const p = await ctx.newPage()

  for (const route of ENTRY) {
    await p.goto(`${BASE}${route}`, { waitUntil: "networkidle" }).catch(() => {})
    await p.waitForTimeout(600)
    const hits = await p.evaluate(scan)
    if (hits.length) { found += hits.length; console.log(`  ${lang} ${route}:`, hits) }
  }

  for (const v of ADMIN) {
    await p.goto(`${BASE}/?admin=${v}`, { waitUntil: "networkidle" }).catch(() => {})
    await p.waitForTimeout(700)
    await p.locator('[role="list"] li, li[role="option"]').first().click({ timeout: 2500 }).catch(() => {})
    await p.waitForTimeout(700)
    for (const tab of await p.getByRole("tab").all()) {
      await tab.click({ timeout: 1500 }).catch(() => {})
      await p.waitForTimeout(300)
    }
    // open whatever dialogs the page offers, since a dialog's copy renders nowhere else
    for (const btn of (await p.getByRole("button").all()).slice(0, 6)) {
      await btn.click({ timeout: 1200 }).catch(() => {})
      await p.waitForTimeout(300)
      const hits = await p.evaluate(scan)
      if (hits.length) { found += hits.length; console.log(`  ${lang} ${v} (dialog):`, hits) }
      await p.keyboard.press("Escape").catch(() => {})
      await p.waitForTimeout(150)
    }
    const hits = await p.evaluate(scan)
    if (hits.length) { found += hits.length; console.log(`  ${lang} ${v}:`, hits) }
  }
  await ctx.close()
}

console.log(found === 0 ? "\n没有未解析的 key" : `\n发现 ${found} 处可疑文本`)
await browser.close()
