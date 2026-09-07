import { chromium } from "@playwright/test"
const EXE = `${process.env.HOME}/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`
const b = await chromium.launch({ executablePath: EXE, args:["--no-sandbox","--disable-gpu"] })
const c = await b.newContext({ viewport:{width:1440,height:900}, locale:"zh-CN" })
await c.addInitScript(()=>{localStorage.setItem("parsar.theme","light");localStorage.setItem("parsar.lang","zh-CN")})
const p = await c.newPage()
p.on("pageerror", e => console.log("PAGEERROR", e.message.slice(0,140)))
await p.goto("http://127.0.0.1:5173/",{waitUntil:"load"}); await p.waitForTimeout(2500)
// invite (register) view
await p.getByRole("button", { name: "注册" }).click(); await p.waitForTimeout(600)
await p.screenshot({ path: ".impeccable/review/login-invite.png" })
await p.locator("input").first().fill("not a link"); await p.getByRole("button", { name: "继续" }).click(); await p.waitForTimeout(500)
await p.screenshot({ path: ".impeccable/review/login-invite-invalid.png" })
console.log("invalid message shown:", await p.getByText("这不像是一个 Parsar 邀请链接。").count())
await p.getByRole("button", { name: "返回登录" }).click(); await p.waitForTimeout(600)
// filled state (primary at full colour) + real login
await p.locator('input[type="email"]').fill("fanjingluo@minimax.io")
await p.locator('input[type="password"]').fill("parsar-demo-2026")
await p.waitForTimeout(300)
await p.screenshot({ path: ".impeccable/review/login-filled.png" })
await p.getByRole("button", { name: "登录", exact: true }).click(); await p.waitForTimeout(4000)
console.log("landed:", p.url(), "| console:", (await p.locator("aside").count()) > 0)
await b.close()
