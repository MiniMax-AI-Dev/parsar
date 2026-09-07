// Entry surfaces against the real backend: login (unauthenticated),
// onboarding (a user with no workspace), invite accept (a real token,
// signed in and signed out) and the join-workspace landing.
import { chromium } from "@playwright/test"
import { mkdirSync } from "node:fs"
import path from "node:path"

const OUT = path.resolve(".impeccable/review/crawl")
mkdirSync(OUT, { recursive: true })
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const WS = process.env.WS
const DEV = process.env.DEV_USER
const NOWS_USER = process.env.NOWS_USER
const INVITE = process.env.INVITE_TOKEN

const browser = await chromium.launch({ executablePath: EXE, args: ["--no-sandbox", "--disable-gpu"] })
const problems = []
const check = (label, ok, detail = "") => {
  console.log(`${ok ? "ok  " : "FAIL"} ${label}${detail ? "  " + detail : ""}`)
  if (!ok) problems.push(label)
}

async function visit(url, { user, theme = "light", ws } = {}) {
  const ctx = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    locale: "zh-CN",
    reducedMotion: "reduce",
    ...(user ? { extraHTTPHeaders: { "X-Parsar-Dev-User-ID": user } } : {}),
  })
  await ctx.addInitScript(([t, w]) => {
    localStorage.setItem("parsar.theme", t)
    localStorage.setItem("parsar.lang", "zh-CN")
    if (w) localStorage.setItem("parsar.ws", w)
    else localStorage.removeItem("parsar.ws")
  }, [theme, ws ?? ""])
  const page = await ctx.newPage()
  const errs = []
  page.on("pageerror", (e) => errs.push(e.message.slice(0, 160)))
  await page.goto(`http://127.0.0.1:5173${url}`, { waitUntil: "load" })
  await page.waitForTimeout(2500)
  return { ctx, page, errs }
}

/* login — no dev header, so /api/v1/me is 401 */
for (const theme of ["light", "dark"]) {
  const { ctx, page, errs } = await visit("/", { theme })
  const body = await page.locator("body").innerText()
  await page.screenshot({ path: path.join(OUT, `entry-login-${theme}.png`) })
  check(`login renders (${theme})`, /登录|Sign in|Parsar/.test(body) && errs.length === 0, errs[0] ?? "")
  await ctx.close()
}

/* onboarding — authenticated user with zero workspaces */
{
  const { ctx, page, errs } = await visit("/", { user: NOWS_USER })
  const body = await page.locator("body").innerText()
  await page.screenshot({ path: path.join(OUT, "entry-onboarding.png") })
  check("onboarding renders for a user with no workspace", /创建|工作区|workspace/i.test(body) && errs.length === 0, errs[0] ?? "")
  await ctx.close()
}

/* invite accept — real token, signed in and signed out */
{
  const { ctx, page, errs } = await visit(`/invite/${INVITE}`, { user: DEV, ws: WS })
  const body = await page.locator("body").innerText()
  await page.screenshot({ path: path.join(OUT, "entry-invite-authed.png") })
  check("invite accept renders for a signed-in user", body.length > 40 && errs.length === 0, errs[0] ?? "")
  await ctx.close()
}
{
  const { ctx, page, errs } = await visit(`/invite/${INVITE}`)
  await page.screenshot({ path: path.join(OUT, "entry-invite-anon.png") })
  check("invite accept renders signed out", errs.length === 0, errs[0] ?? "")
  await ctx.close()
}

/* join-workspace landing */
{
  const { ctx, page, errs } = await visit(`/join-workspace?id=${WS}`, { user: DEV })
  const body = await page.locator("body").innerText()
  await page.screenshot({ path: path.join(OUT, "entry-join.png") })
  check("join-workspace landing renders", body.length > 30 && errs.length === 0, errs[0] ?? "")
  await ctx.close()
}

await browser.close()
console.log(`\n${problems.length === 0 ? "all entry paths clean" : `${problems.length} problems`}`)
