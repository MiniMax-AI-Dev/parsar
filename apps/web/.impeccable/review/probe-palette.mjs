// Every colour actually painted across the console, counted by pixel role.
// The rule: two warm greys + one accent; status colour only inside StatusIcon.
import { chromium } from "@playwright/test"
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
const VIEWS = ["conversations","approvals","runs","scheduled","agents","capabilities","models","connections","members","settings","secrets","runtime","connectors","usage","audit"]
const hue = (c) => {
  const m = c.match(/rgba?\((\d+),\s*(\d+),\s*(\d+)/); if (!m) return null
  const [r,g,b] = m.slice(1,4).map(Number)
  const mx = Math.max(r,g,b), mn = Math.min(r,g,b)
  return { r,g,b, chroma: mx - mn }
}
for (const theme of ["light","dark"]) {
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN", extraHTTPHeaders: { "X-Parsar-Dev-User-ID": process.env.DEV_USER } })
  await ctx.addInitScript(([ws,t]) => { localStorage.setItem("parsar.theme",t); localStorage.setItem("parsar.lang","zh-CN"); localStorage.setItem("parsar.ws", ws) }, [process.env.WS, theme])
  const page = await ctx.newPage()
  const tally = new Map()
  for (const v of VIEWS) {
    await page.goto(`http://127.0.0.1:5173/?admin=${v}`, { waitUntil: "load" })
    await page.waitForTimeout(1500)
    const found = await page.evaluate(() => {
      const out = []
      for (const el of document.querySelectorAll("body *")) {
        const r = el.getBoundingClientRect()
        if (r.width < 1 || r.height < 1) continue
        const s = getComputedStyle(el)
        const inIcon = !!el.closest("svg")
        for (const [prop, val] of [["color", s.color], ["bg", s.backgroundColor], ["border", s.borderTopColor], ["fill", s.fill], ["stroke", s.stroke]]) {
          if (!val || val === "none" || val.startsWith("rgba(0, 0, 0, 0")) continue
          out.push(`${val}|${inIcon ? "icon" : prop}`)
        }
      }
      return out
    })
    for (const f of found) tally.set(f, (tally.get(f) ?? 0) + 1)
  }
  // keep only chromatic colours — greys are the system's own two
  const chromatic = [...tally.entries()]
    .map(([k, n]) => { const [c, role] = k.split("|"); return { c, role, n, h: hue(c) } })
    .filter(x => x.h && x.h.chroma > 24)
  const byColour = new Map()
  for (const x of chromatic) {
    const e = byColour.get(x.c) ?? { n: 0, roles: new Set() }
    e.n += x.n; e.roles.add(x.role); byColour.set(x.c, e)
  }
  console.log(`\n=== ${theme}: chromatic colours painted (chroma > 24) ===`)
  for (const [c, e] of [...byColour.entries()].sort((a,b) => b[1].n - a[1].n)) {
    console.log(`  ${c.padEnd(26)} ${String(e.n).padStart(5)}×  roles: ${[...e.roles].join(",")}`)
  }
  await ctx.close()
}
await browser.close()
