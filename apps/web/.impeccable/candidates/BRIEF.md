# Parsar Runs page — candidate prototypes (shared brief)

You are building ONE self-contained HTML prototype of the Parsar "运行 (Runs)" page in a specific visual direction. The prototype is a static mock for a design decision, not production code. Output: a single `.html` file with inline `<style>` and minimal inline `<script>`. No frameworks, no build step. Fonts may be loaded from Google Fonts via `<link>`.

## Product (real facts, keep them)

Parsar is an open-source, self-hosted control plane for dispatching, managing, and auditing AI coding agents as a team. Users are developers and admins of an engineering team, on a desktop browser, daily. The UI is bilingual (zh/en); this mock is rendered in Chinese UI with English identifiers, exactly like the real app.

Register pinned by the user: the restrained, premium feel of Notion, OpenAI (openai.com / ChatGPT) and Multica (multica.ai). Hierarchy through spacing, weight, and a small number of tones. Never loud. Never "dashboard-y". Light AND dark must both be excellent.

## Page structure (all candidates carry all of this)

**App shell**
- Workspace: `MiniMax · Infra`. Brand: the word `Parsar` (set in the UI face, no logo image available; do not draw a fake logo mark).
- Navigation groups and items (Chinese labels, in this order):
  - 协作: 对话, 收件箱 (badge: 3), 运行 (ACTIVE), 定时任务
  - Agent: Agent, 能力, 模型, 连接
  - 团队: 成员, 设置
- Top-right: theme toggle (light/dark) and a user avatar with initials `FJ`.

**Page header**
- Title: `运行` with a quiet `Runs` beside/under it (the real app titles it "运行 (Runs)").
- Description: `Agent 每一次执行都对应一条 Run。这里能看到正在跑、卡住、失败、待审批的 run。`
- Tabs / filters: `全部` (active) · `进行中` · `失败`. Search placeholder: `搜索 run id / agent / 对话…`

**Run list** — columns: Run · 状态 · 对话 · 模型 · 耗时 · 成本. Statuses come from the real enum: queued (等待调度), running (运行中), completed (已完成), failed (失败), cancelled (已取消), interrupted (已中断). Ten rows (synthetic data, realistic):

| id | agent | status | conversation | model | duration | cost | age |
|---|---|---|---|---|---|---|---|
| run_01J8ZM3Q7K | reviewer-bot | running | #infra-alerts · 修复 nightly 构建失败 | claude-opus-5 | 4m 12s | ¥0.38 | 4 分钟前 |
| run_01J8ZKX2P9 | release-notes | completed | #release · v0.9.2 发布说明 | MiniMax-M2 | 2m 51s | ¥0.12 | 26 分钟前 |
| run_01J8ZKR8T4 | reviewer-bot | completed | PR #271 · store: split routes.go | claude-opus-5 | 6m 03s | ¥0.71 | 1 小时前 |
| run_01J8ZK9A1M | migrate-helper | failed | #backend · 迁移 0042 回滚 | gpt-5-codex | 0m 48s | ¥0.05 | 2 小时前 |
| run_01J8ZJH6W2 | reviewer-bot | queued | #frontend · 对话页 assistant-ui 接入 | claude-opus-5 | — | — | 2 小时前 |
| run_01J8ZJ3C5N | docs-writer | completed | #docs · CONTRIBUTING 补充 e2b 章节 | MiniMax-M2 | 1m 37s | ¥0.06 | 3 小时前 |
| run_01J8ZHT0X8 | migrate-helper | cancelled | #backend · sqlc 重新生成 | gpt-5-codex | 0m 12s | ¥0.01 | 5 小时前 |
| run_01J8ZH2R9Q | reviewer-bot | completed | PR #268 · web: marketplace tab filter | claude-opus-5 | 3m 44s | ¥0.42 | 昨天 |
| run_01J8ZGF4K7 | release-notes | interrupted | #release · v0.9.1 发布说明 | MiniMax-M2 | 5m 20s | ¥0.19 | 昨天 |
| run_01J8ZFQ1B3 | docs-writer | completed | #docs · README 快速开始 | MiniMax-M2 | 0m 58s | ¥0.03 | 昨天 |

Pagination line: `第 1-10 条,共 128 条` with `上一页` / `下一页`.

**Selected run detail** (the third row `run_01J8ZKR8T4` is selected; its detail is visible on the page in whatever way the direction dictates: a side pane, a reading column, a drawer, a lower panel):
- Header: run id, status 已完成, agent `reviewer-bot`, trigger `来自飞书线程 · PR #271`, timestamps 创建 09:41:02 · 开始 09:41:05 · 完成 09:47:08, 耗时 6m 03s, 成本 ¥0.71.
- Tabs: Overview · Events · Steps · Artifacts · Permissions · Audit (Overview active).
- 运行环境: 运行节点 `mbp-fanjingluo` · Agent 引擎 `Claude Code` · 运行模式 `沙盒 (docker)` · 托管模型 `claude-opus-5` · 工作目录 `~/dev/parsar` · 最近心跳 `12 秒前`.
- Steps (numbered, 6): 1 读取 PR #271 的 diff 与评论 · 2 搜索 store.go 中的相关调用 · 3 修改 3 个文件（+142 −37）· 4 运行 `make check-web` · 5 推送修订并更新 PR 描述 · 6 回帖到飞书线程.
- One quiet action row: `重试` (secondary) and `取消运行` (disabled because completed), plus a link `打开对话 ↗`.

**States to show somewhere**: hover on a row, the selected row, a disabled button, a badge count, a running indicator (subtle, animated only if reduced-motion is off).

## Hard rules (craft floor)

- Both themes via `html[data-theme="light"|"dark"]` and CSS custom properties; a toggle button in the shell switches `data-theme` and persists nothing. Default to light.
- Every color goes through a CSS variable named semantically (`--fg`, `--fg-muted`, `--surface`, `--line`, `--accent`, `--danger`… ). No raw hex in rules except in the variable definitions.
- Chinese text must render well: include `"Noto Sans SC"` in the font stack after the Latin face, and load it from Google Fonts (weights 400/500/700).
- Tabular numerals for ids, durations, costs (`font-variant-numeric: tabular-nums`). Identifiers and code in a real monospace face.
- Theme the browser surfaces: `::selection`, scrollbar (thin, tinted from the palette), `:focus-visible` rings, caret color.
- Contrast: body text ≥ 4.5:1 in both themes; secondary text never below 4.5:1.
- Icons: inline SVG with a consistent 1.5px stroke (lucide-style paths). No emoji, no unicode glyph icons.
- No kicker/eyebrow labels above headings. No stat-tile row of big numbers. No nested cards. No gradient text. No colored thick left borders. No decorative blur/glass. No zero-blur offset shadows.
- Spacing: tight within groups, generous between groups, more space above a heading than below.
- One authored motion moment at most (e.g. the detail pane entering, or the running dot). Respect `prefers-reduced-motion`.
- Layout for a 1440×900 desktop viewport; must still hold at 1280. The page is not responsive below 1100 (the real app sets min-width 960).
- Mark the data as synthetic in an HTML comment at the top of the file, not in the UI.
- Keep it to one file, under ~900 lines. Real, complete markup: every row, every nav item, the detail pane fully populated. No lorem ipsum, no placeholders.

Build the direction fully committed. Refinement of the category default is the failure mode; a page someone could guess from the category alone means you did not decide.
