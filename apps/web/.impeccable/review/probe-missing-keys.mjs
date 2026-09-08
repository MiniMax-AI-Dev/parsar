// A key i18next cannot resolve renders as the key itself. Walk every view in
// both locales and look for text shaped like one.
import { chromium } from "@playwright/test"
const EXE = process.env.CHROME_EXE ?? `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const BASE = process.env.BASE ?? "http://127.0.0.1:5174"
const VIEWS = ["conversations","approvals","runs","scheduled","agents","capabilities","models","connections","members","settings","secrets","runtime","usage","audit"]
const KEYish = /\b[a-z][a-zA-Z0-9]*(\.[a-z][a-zA-Z0-9_]*){2,}\b/
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox","--disable-gpu"] })
let found = 0
for (const lang of ["zh-CN", "en-US"]) {
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 950 }, locale: lang,
    extraHTTPHeaders: process.env.DEV_USER ? { "X-Parsar-Dev-User-ID": process.env.DEV_USER } : {} })
  await ctx.addInitScript(([l, ws]) => {
    localStorage.setItem("parsar.lang", l); localStorage.setItem("parsar.theme", "light")
    if (ws) localStorage.setItem("parsar.ws", ws)
  }, [lang, process.env.WS ?? "0f4d2c6e-9b1a-4e7c-8f3d-2a1b5c6d7e8f"])
  const p = await ctx.newPage()
  for (const v of VIEWS) {
    await p.goto(`${BASE}/?admin=${v}`, { waitUntil: "networkidle" }).catch(() => {})
    await p.waitForTimeout(700)
    // open the first row's rail and every tab, so rail and tab copy is rendered too
    await p.locator('[role="list"] li, li[role="option"]').first().click({ timeout: 2500 }).catch(() => {})
    await p.waitForTimeout(700)
    for (const tab of await p.getByRole("tab").all()) {
      await tab.click({ timeout: 1500 }).catch(() => {})
      await p.waitForTimeout(350)
    }
    const hits = await p.evaluate((src) => {
      const re = new RegExp(src)
      const out = []
      const walk = (n) => {
        if (n.nodeType === 3) {
          const t = (n.textContent || "").trim()
          if (t && re.test(t) && !/[一-鿿]/.test(t) && !t.includes(" ") && !/\.(com|io|json|md|ts|tsx|py|go|sh)$/.test(t)) out.push(t.slice(0, 70))
        } else if (n.nodeType === 1 && !["SCRIPT","STYLE","PRE","CODE"].includes(n.tagName)) {
          for (const c of n.childNodes) walk(c)
        }
      }
      walk(document.body)
      return [...new Set(out)]
    }, KEYish.source)
    if (hits.length) { found += hits.length; console.log(`  ${lang} ${v}:`, hits) }
  }
  await ctx.close()
}
console.log(found === 0 ? "\n没有未解析的 key" : `\n发现 ${found} 处可疑文本`)
await browser.close()
