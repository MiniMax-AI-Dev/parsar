---
name: Parsar
description: A dense, keyboard-first issue ledger for teams running AI coding agents; restrained, premium, system-native.
colors:
  ink: "#37352f"
  ink-muted: "#787774"
  ink-sidebar: "#5f5e5a"
  ink-on-emphasis: "#ffffff"
  paper: "#ffffff"
  paper-panel: "#fafafa"
  paper-muted: "#f1f1f0"
  paper-inverse: "#191919"
  hairline: "#e9e9ec"
  hairline-muted: "#efeff1"
  hairline-strong: "#d6d7dc"
  indigo: "#4f46e5"
  indigo-emphasis: "#4338ca"
  indigo-fg: "#ffffff"
  danger: "#dc2626"
  danger-emphasis: "#b91c1c"
  status-queued: "#9a9ca4"
  status-running: "#d97706"
  status-completed: "#16a34a"
  status-failed: "#dc2626"
  status-cancelled: "#9a9ca4"
  status-interrupted: "#ea580c"
  status-track: "#d4d4d8"
  dark-ink: "#d4d4d4"
  dark-ink-emphasis: "#ededed"
  dark-ink-muted: "#9b9b9b"
  dark-ink-sidebar: "#bdbdbd"
  dark-ground: "#191919"
  dark-panel: "#202020"
  dark-surface-muted: "#2a2a2a"
  dark-paper-inverse: "#ededed"
  dark-hairline: "rgb(255 255 255 / 9%)"
  dark-hairline-muted: "rgb(255 255 255 / 6%)"
  dark-hairline-strong: "rgb(255 255 255 / 16%)"
  dark-indigo: "#8b90f6"
  dark-indigo-emphasis: "#a5a9f8"
  dark-indigo-fg: "#14142b"
  dark-status-queued: "#71737b"
  dark-status-running: "#f5a524"
  dark-status-completed: "#3fb950"
  dark-status-failed: "#f05252"
  dark-status-interrupted: "#fb8a3c"
  dark-status-track: "#3f3f3f"
typography:
  title:
    fontFamily: "-apple-system, BlinkMacSystemFont, 'PingFang SC', 'Hiragino Sans GB', 'Segoe UI', 'Microsoft YaHei', 'Noto Sans SC', 'Helvetica Neue', Helvetica, Arial, sans-serif"
    fontSize: "20px"
    fontWeight: 600
    lineHeight: 1
    letterSpacing: "-0.02em"
  entry-title:
    fontFamily: "-apple-system, BlinkMacSystemFont, 'PingFang SC', 'Hiragino Sans GB', 'Segoe UI', 'Microsoft YaHei', 'Noto Sans SC', 'Helvetica Neue', Helvetica, Arial, sans-serif"
    fontSize: "22px"
    fontWeight: 500
    lineHeight: "30px"
    letterSpacing: "-0.02em"
  panel-title:
    fontFamily: "-apple-system, BlinkMacSystemFont, 'PingFang SC', 'Hiragino Sans GB', 'Segoe UI', 'Microsoft YaHei', 'Noto Sans SC', 'Helvetica Neue', Helvetica, Arial, sans-serif"
    fontSize: "13px"
    fontWeight: 500
    lineHeight: "18px"
  body:
    fontFamily: "-apple-system, BlinkMacSystemFont, 'PingFang SC', 'Hiragino Sans GB', 'Segoe UI', 'Microsoft YaHei', 'Noto Sans SC', 'Helvetica Neue', Helvetica, Arial, sans-serif"
    fontSize: "14px"
    fontWeight: 400
    lineHeight: "20px"
  row:
    fontFamily: "-apple-system, BlinkMacSystemFont, 'PingFang SC', 'Hiragino Sans GB', 'Segoe UI', 'Microsoft YaHei', 'Noto Sans SC', 'Helvetica Neue', Helvetica, Arial, sans-serif"
    fontSize: "13px"
    fontWeight: 400
    lineHeight: "18px"
  label:
    fontFamily: "-apple-system, BlinkMacSystemFont, 'PingFang SC', 'Hiragino Sans GB', 'Segoe UI', 'Microsoft YaHei', 'Noto Sans SC', 'Helvetica Neue', Helvetica, Arial, sans-serif"
    fontSize: "12px"
    fontWeight: 400
    lineHeight: "16px"
  mono:
    fontFamily: "ui-monospace, 'SF Mono', Menlo, Consolas, 'Liberation Mono', 'Noto Sans Mono', monospace"
    fontSize: "12px"
    fontWeight: 400
    lineHeight: "16px"
    fontFeature: "'tnum', 'zero'"
rounded:
  sm: "4px"
  md: "6px"
  lg: "8px"
  pill: "999px"
spacing:
  xxs: "2px"
  xs: "4px"
  sm: "8px"
  gutter: "10px"
  md: "12px"
  lg: "16px"
  xl: "24px"
components:
  button-outline:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    typography: "{typography.row}"
    rounded: "{rounded.md}"
    height: "28px"
    padding: "0 10px"
  button-primary:
    backgroundColor: "{colors.indigo}"
    textColor: "{colors.indigo-fg}"
    typography: "{typography.row}"
    rounded: "{rounded.md}"
    height: "28px"
    padding: "0 10px"
  button-primary-hover:
    backgroundColor: "{colors.indigo-emphasis}"
  button-destructive:
    backgroundColor: "{colors.danger}"
    textColor: "{colors.ink-on-emphasis}"
    typography: "{typography.row}"
    rounded: "{rounded.md}"
    height: "28px"
    padding: "0 10px"
  button-secondary:
    backgroundColor: "{colors.paper-muted}"
    textColor: "{colors.ink}"
    typography: "{typography.row}"
    rounded: "{rounded.md}"
    height: "28px"
    padding: "0 10px"
  button-ghost:
    textColor: "{colors.ink-muted}"
    typography: "{typography.row}"
    rounded: "{rounded.md}"
    height: "28px"
    padding: "0 10px"
  button-sm:
    typography: "{typography.label}"
    rounded: "{rounded.md}"
    height: "24px"
    padding: "0 8px"
  button-lg:
    typography: "{typography.row}"
    rounded: "{rounded.md}"
    height: "32px"
    padding: "0 12px"
  button-icon:
    rounded: "{rounded.md}"
    size: "28px"
  action-icon-button:
    textColor: "{colors.ink-muted}"
    rounded: "{rounded.md}"
    size: "28px"
  input:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    typography: "{typography.row}"
    rounded: "{rounded.md}"
    height: "28px"
    padding: "0 8px"
  select:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    typography: "{typography.row}"
    rounded: "{rounded.md}"
    height: "28px"
    padding: "0 28px 0 8px"
  textarea:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    typography: "{typography.row}"
    rounded: "{rounded.md}"
    height: "72px"
    padding: "6px 8px"
  field-label:
    textColor: "{colors.ink-muted}"
    typography: "{typography.label}"
    padding: "0 0 4px"
  tabs-list:
    backgroundColor: "{colors.paper}"
    rounded: "{rounded.md}"
    height: "28px"
    padding: "2px"
  tabs-trigger:
    textColor: "{colors.ink-muted}"
    typography: "{typography.label}"
    rounded: "{rounded.sm}"
    height: "24px"
    padding: "0 8px"
  tabs-trigger-active:
    textColor: "{colors.ink}"
  view-tabs-row:
    height: "40px"
    padding: "0 16px"
  badge:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    typography: "{typography.label}"
    rounded: "{rounded.md}"
    height: "20px"
    padding: "0 6px"
  kbd:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink-muted}"
    typography: "{typography.label}"
    rounded: "{rounded.sm}"
    padding: "2px 4px"
  nav-item:
    textColor: "{colors.ink-sidebar}"
    typography: "{typography.body}"
    rounded: "{rounded.md}"
    height: "30px"
    padding: "0 8px"
  nav-item-active:
    textColor: "{colors.ink}"
  workspace-row:
    textColor: "{colors.ink}"
    typography: "{typography.row}"
    rounded: "{rounded.md}"
    height: "32px"
    padding: "0 8px"
  sidebar:
    backgroundColor: "{colors.paper-panel}"
    width: "232px"
    padding: "10px"
  topbar:
    backgroundColor: "{colors.paper}"
    typography: "{typography.title}"
    height: "64px"
    padding: "0 24px"
  topbar-subtitle:
    textColor: "{colors.ink-muted}"
    typography: "{typography.label}"
  page-section-head:
    textColor: "{colors.ink}"
    typography: "{typography.panel-title}"
    height: "28px"
    padding: "0 0 8px"
  ledger-header:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink-muted}"
    typography: "{typography.label}"
    height: "28px"
    padding: "0 24px"
  ledger-group:
    textColor: "{colors.ink-muted}"
    typography: "{typography.label}"
    height: "28px"
    padding: "0 24px 0 22px"
  ledger-row:
    textColor: "{colors.ink}"
    typography: "{typography.row}"
    height: "36px"
    padding: "0 24px"
  ledger-row-actions:
    backgroundColor: "{colors.paper}"
    rounded: "{rounded.md}"
    height: "28px"
    padding: "0 2px"
  ledger-footer:
    textColor: "{colors.ink-muted}"
    typography: "{typography.label}"
    height: "40px"
    padding: "0 16px"
  dialog:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    typography: "{typography.row}"
    rounded: "{rounded.lg}"
    width: "448px"
    padding: "16px"
  dialog-header:
    textColor: "{colors.ink}"
    typography: "{typography.panel-title}"
    height: "48px"
    padding: "10px 48px 10px 16px"
  dialog-footer:
    padding: "12px 16px"
  composer:
    backgroundColor: "{colors.paper-muted}"
    textColor: "{colors.ink}"
    typography: "{typography.body}"
    rounded: "16px"
    padding: "16px 16px 12px"
  composer-send:
    backgroundColor: "{colors.ink}"
    textColor: "{colors.ink-on-emphasis}"
    rounded: "{rounded.pill}"
    size: "32px"
  trace-head:
    textColor: "{colors.ink-muted}"
    typography: "{typography.row}"
    height: "32px"
  trace-step:
    textColor: "{colors.ink}"
    typography: "{typography.row}"
    height: "32px"
  approval-head:
    textColor: "{colors.ink-muted}"
    typography: "{typography.label}"
    height: "28px"
  turn-marker:
    backgroundColor: "{colors.hairline-strong}"
    rounded: "{rounded.pill}"
    height: "2px"
    width: "6px"
  turn-marker-active:
    backgroundColor: "{colors.ink}"
    width: "26px"
  turn-preview:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink-muted}"
    typography: "{typography.label}"
    rounded: "{rounded.lg}"
    width: "256px"
    padding: "8px"
  initial-tile:
    textColor: "{colors.ink}"
    rounded: "{rounded.sm}"
    size: "18px"
  detail-rail:
    backgroundColor: "{colors.paper-panel}"
    width: "384px"
    padding: "16px 16px 8px"
  rail-header:
    typography: "{typography.panel-title}"
    height: "64px"
    padding: "0 8px 0 16px"
  rail-section-head:
    textColor: "{colors.ink}"
    typography: "{typography.label}"
    padding: "0 0 2px"
  rail-modal:
    backgroundColor: "{colors.paper-panel}"
    rounded: "{rounded.lg}"
    width: "70vw"
    height: "70vh"
    padding: "16px 24px 8px"
  resize-handle:
    width: "6px"
  toast:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    typography: "{typography.row}"
    rounded: "{rounded.lg}"
    padding: "6px 6px 6px 12px"
  inline-notice:
    textColor: "{colors.ink}"
    typography: "{typography.row}"
  entry-panel:
    backgroundColor: "{colors.paper}"
    rounded: "{rounded.lg}"
    width: "400px"
    padding: "24px"
  property-row:
    textColor: "{colors.ink}"
    typography: "{typography.row}"
    height: "28px"
  property-label:
    textColor: "{colors.ink-muted}"
    typography: "{typography.label}"
    width: "84px"
  step-row:
    textColor: "{colors.ink}"
    typography: "{typography.row}"
    height: "32px"
  theme-segment:
    textColor: "{colors.ink-muted}"
    rounded: "{rounded.sm}"
    height: "22px"
    width: "26px"
  avatar-tile:
    textColor: "{colors.ink}"
    typography: "{typography.label}"
    rounded: "{rounded.sm}"
    size: "24px"
  tooltip:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    typography: "{typography.label}"
    rounded: "{rounded.md}"
    padding: "6px 10px"
---

# Design System: Parsar

## Overview

**Creative North Star: "The Issue Ledger"**

Parsar's console reads like the issue tracker an engineering team already lives in: one dense row per object, status drawn as a small icon, identifiers in mono, and a keyboard that can do everything. The register is the restrained, premium calm of Notion and OpenAI: hierarchy comes from spacing, weight and two tones of ink, never from colour or decoration. Chrome is thin and structural; content is the surface.

Density is a feature, not a compromise. Rows are 36px, sidebar rows 30px, the topbar 64px. Panels are separated by hairlines and one step of tone, not by cards. Anything that describes or qualifies content is small and grey; anything that is content is ink. The two side panels (sidebar, detail rail) are the user's: their edges drag, and the console asks once whether to keep the new width.

The system rejects, by the user's decision: the card dashboard (sidebar + stat tiles + card grid), the document-page arrangement (large centred title, database view), pure-black dark mode, webfont CJK, tinted message boxes, category chips, padding above ledger headers, a rail "drain" effect on expand, and any control that appears twice on one screen.

This file was written with the user as the spec, refreshed against the Runs build, and refreshed again against the whole shipped console (fifteen views in light and dark, `src/style.css`, `src/components/ui/*`, `src/components/layout/*`, `src/lib/layout-width.ts`). This pass adds what the refinement batch landed: the ledger **column model** with fixed 24px outer edges, the **level-aware entrance** (page-in for sidebar views, detail-in for drill-downs), an **exit for every entrance**, dialogs rebuilt on the rail's header/body/footer frame, and the conversation surface (composer, `WorkTrace`, `ApprovalBar`, `TurnNavRail`). Where the build settled a value the earlier record had approximate, the build's value is recorded here; the user rules themselves are unchanged.

**Key Characteristics:**
- Dense rows, hairline structure, two greys, one accent.
- System typefaces only: SF Pro / PingFang on macOS, Segoe UI / YaHei on Windows.
- Status colour lives only inside 14px status icons (and the 14px glyph of an inline message).
- One control per semantic per screen; no page description subtitles; the English page name is the only companion to a title.
- Springy, non-linear motion, short and rare: two page entrances (section switch, drill-down), one rail entrance, one modal flight — and every one of them has a reverse.
- Every ledger and the topbar share the same two vertical edges (24px), so page after page reads as one document.

## Colors

Two tones of ink on paper, one indigo for selection and focus, and six status hues that never leave their icons. Every colour in TSX goes through a semantic token in `src/style.css` (`fg-*`, `surface-*`, `line-*`, `accent`, `status-*`); the raw palette is forbidden by lint. Light and dark are one token set: the same class names resolve to the dark values under `html[data-theme="dark"]`.

### Primary
- **Indigo** (`indigo`, dark `dark-indigo`): the selected row's 2px left marker, focus rings (indigo at 40%), the focused border and 1px ring of inputs, selects and textareas, the caret, text selection (indigo at 18%, dark 28%), the resize handle's hairline while hovered or dragged, and the primary button. **Indigo Emphasis** (`indigo-emphasis`, dark `dark-indigo-emphasis`) is the primary button's hover only. Never used for text, headings, links in body copy, or backgrounds larger than a button.

### Neutral
- **Ink** (`ink`, dark `dark-ink`): warm near-black, never pure black. All content, values, names, active items, actions, section heads and the text of every inline message. In dark the "emphasis" alias (`dark-ink-emphasis`) brightens the active-row text and the selection foreground by one step; in light it is identical to Ink.
- **Ink Muted** (`ink-muted`, dark `dark-ink-muted`): labels, metadata (ids, ages, counts, column headers, group labels, placeholders, field labels and hints), categories (member role, capability type and source), the English page name, inactive tabs, inactive nav icons, resting action icons, disabled text. `fg-subtle` and `fg-faint` exist as aliases and resolve to the same value; there is no third grey for content.
- **Ink Sidebar** (`ink-sidebar`, dark `dark-ink-sidebar`): the resting colour of sidebar text only, set once on the `.app-sidebar` surface; the active nav row returns to full Ink. This is a surface rule, not a third content grey.
- **Ink On Emphasis** (`ink-on-emphasis`): text on an ink or danger background (the skip link, the destructive button).
- **Paper** (`paper`, dark `dark-ground`): the main list ground, the topbar, buttons, inputs, kbd, tooltips, menus, the toast strip and the entry panel. In dark these all sit on the ground (#191919); nothing lifts to a lighter grey except the muted tint.
- **Paper Panel** (`paper-panel`, dark `dark-panel`): the sidebar, the detail rail and the rail's expanded modal: one step off the ground. Group header rows inside the list stay on the list ground.
- **Paper Muted** (`paper-muted`, dark `dark-surface-muted`): the secondary button, a disabled field, the skeleton, every `VerbatimBlock`, and the pressed row in a menu.
- **Paper Inverse** (`paper-inverse`, dark `dark-paper-inverse`): the ground's opposite (`surface-inverse`), used at 30% as the overlay behind every dialog and behind the expanded rail modal. Never used solid.
- **Hairline** (`hairline`, dark `dark-hairline`): every structural line: row separators, panel edges, the topbar and rail header, the 40px view-tabs row, the tab and theme-toggle frames, menu edges, the entry panel's border, and the resting resize handle (which is the panel's own hairline). **Hairline Muted** (`hairline-muted`) exists in the token set and is not used by the console surfaces.
- **Hairline Strong** (`hairline-strong`, dark `dark-hairline-strong`): borders of interactive controls: outline buttons, inputs, selects, textareas, kbd, badges.
- **Tints** (`--app-*` in `style.css`, exposed as `app-hover` / `app-pressed` / `app-selected` / `app-tile` utilities): hover = ink at 3% (dark white at 5.5%); pressed / active nav / active tab / open menu trigger = ink at 6% (dark white at 10%); selected row = indigo at 3% (dark 4%); avatar and initial tile = ink at 7% (dark white at 10%).

### Status (icon-only)
- **Queued** (`status-queued`, dark `dark-status-queued`): dashed ring.
- **Running** (`status-running`, dark `dark-status-running`): three-quarter arc on a grey track (`status-track`, dark `dark-status-track`), slowly rotating. The same amber colours the 14px triangle of a warning `InlineNotice`.
- **Completed** (`status-completed`, dark `dark-status-completed`): filled disc with a check cut in paper. The same icon leads a success `InlineNotice`.
- **Failed** (`status-failed`, dark `dark-status-failed`): filled disc with an x cut in paper. The same red colours the 14px triangle of `ErrorState`, `InlineError` and an error `InlineNotice`, and the hover of a `danger`-tone action icon.
- **Cancelled** (`status-cancelled`, same value as queued): ring with a slash.
- **Interrupted** (`status-interrupted`, dark `dark-status-interrupted`): ring with a dash.
- The same six hues colour the 6px dot of a `Badge`. `danger` / `danger-emphasis` are the destructive button only; the `*-subtle` and `*-border` semantic fills (danger, warning, success, info) exist in the token set and are rendered nowhere in the console: every tinted message box was replaced by `InlineError` / `InlineNotice`.

### Named Rules
**The Icon-Only Status Rule.** Status colour appears only inside a 14px glyph: the status icon, the inline-message triangle or check, the badge dot. Status words, rows, chips and message text stay in ink and grey.

**The Two Greys Rule.** Content text is ink or ink-muted, nothing in between. Muted is for labels, metadata and categories; a value is never muted. The sidebar rests one shade lighter than ink as a whole surface, which is not a third content grey.

**The Grey Night Rule.** Dark mode is warm grey (#191919 ground / #202020 panel / #2a2a2a muted), never pure black. Hairlines in dark are white at 9% and 16%, so structure stays visible without a third grey.

**The No Tinted Box Rule.** A message is a 14px glyph plus ink text, inline, at the size of its surroundings. There is no coloured background, no coloured border, no coloured text behind a message; the `*-subtle` fills stay unused.

## Typography

**Display Font:** system UI stack (SF Pro, Segoe UI; PingFang SC / Microsoft YaHei for CJK; Noto Sans SC last fallback)
**Body Font:** the same system stack
**Label/Mono Font:** ui-monospace, SF Mono, Menlo, Consolas, Liberation Mono

**Character:** native and invisible. The type looks like the operating system, which is the point: a Mac user sees SF Pro and PingFang exactly as Notion renders them. No webfont is loaded for Latin or CJK. `text-rendering: optimizeLegibility`, headings `text-wrap: balance`, paragraphs `text-wrap: pretty`.

The stylesheet defines an eight-tick scale (`text-2xs` 11 … `text-3xl` 28) with a fixed line-height per tick; arbitrary sizes are forbidden by lint. The console uses five ticks; 16 is reserved for dialog headings, 22 for the entry-panel title, 28 exists for a setup hero, and 11 exists only for the letter inside an 18px initial tile.

### Hierarchy
- **Title** (600, 20px, line-height 1, -0.02em; `.font-display`): the page title in the 64px topbar, followed on the same baseline by the page's English name in 12px muted (`subtitleFor`, resolved from the en-US locale). The only 600 on a screen.
- **Entry Title** (500, 22px, 30px, -0.02em): the one heading inside `EntryPanel` on login / setup / invite / join. Not 600: the wordmark and the form are the point, not the heading.
- **Section Head** (500, 16px, 24px): the head of a `PageSection` — the one heading between the page title and its content. Also a dialog's heading.
- **Rail Section Head** (500, 14px, 20px): the head of a `RailSection`. The rail runs its own compressed ladder — 14 over 13px values over 12px labels — because a 384px panel cannot carry the page's.
- **Panel Title** (500, 13px, 18px): the status word in the rail header, the agent name at the top of the rail body, a sub-head *inside* a section; also every button label, the active nav item, the active tab, the workspace brand, the account name and the "Parsar" wordmark in the entry panel.
- **Body / UI** (400, 14px, 20px): sidebar nav items, form copy, dialogs, the entry panel's one muted sentence.
- **Row** (400, 13px, 18px): list rows, inputs, selects, textareas, property values, step titles, inline messages, the toast strip, footers.
- **Label** (400, 12px, 16px): column headers, group labels, property labels, field labels and hints, kbd, counts, ages, connector names, categories (role, type, source), tooltips, the topbar's English name.
- **Mono** (400, 12px, 16px, `tnum` + `zero`, tabular slashed-zero): identifiers, durations, timestamps, paths, e-mail addresses in rows, model ids, step indices.

### Named Rules
**The Three Weights Rule.** 600 is the page title only. 500 is the name of a thing (workspace, user, row title, run id in the rail), the active nav item or tab, button labels, section heads and the entry title. Everything else, including every label and every value, is 400. 700 does not exist.

**The Five Sizes Rule.** 20 / 14 / 13 / 12 and 12-mono. There is no 11px functional text and no sixth size in the console; 22 belongs to the entry panel alone.

**The Layered Base Rule.** Element-level type and reset rules live in `@layer base`, never unlayered. An unlayered `button, select, textarea { font: inherit }` outranks Tailwind's utility layer and silently drags every button, tab and nav row to the 16px root size; inside `@layer base` the `text-*` utilities win. Any new element rule added to `src/style.css` goes in that layer.

**The Plain Category Rule.** A category (member role, capability type, source, connector) is 12px muted text in its own column, never a chip. A `Badge` marks at most one secondary state per row (disabled, unmanaged, pending) with a neutral dot.

## Layout

Three columns on a 1440 desktop (the design width): a 232px sidebar (draggable 200–360), a fluid main column, and a 384px detail rail (draggable 320–640). The page is not responsive below 960px; `body` sets a 960px minimum width and the turn-nav rail hides there. At or below 1360px the ledger row's inline error summary hides; the rail no longer narrows. The shell is `h-screen` with each column scrolling on its own. The conversation thread is measured, not full-bleed: 48rem (`--thread-max-width`) centred, with the composer footer at the same measure.

Panel edges are the user's. A `ResizeHandle` straddles each inner hairline: a 6px invisible hit area (`role="separator"`, focusable) whose 1px line turns indigo while hovered, focused or dragged; arrow keys move it 8px, shift-arrow 32px. After a drag or key press the `LayoutPrompt` raises one persistent toast, keyed on the panel so the sidebar and the rail can each be mid-question: the sentence "布局已调整" and three small buttons — 保存 (primary; writes `localStorage`), 临时 (outline; writes `sessionStorage`), 恢复 (ghost; clears both and springs the panel back to its default over 420ms on the spring ease). It leaves with its panel. Storage keys are `parsar.layout.sidebar` and `parsar.layout.rail`; resolution on load is localStorage → sessionStorage → default.

Sidebar (paper-panel, 1px right hairline, 10px padding): a single 32px text row "Parsar / Workspace ⇕" at the top, then group labels (12px muted, 14px above, 4px below, 8px inset) and 30px nav rows 1px apart, then the account row pinned to the bottom (top hairline, 10px above; 24px avatar tile, name 13px/500, role 12px muted, and a two-segment theme toggle).

Main column: every page begins with the 64px `PageHeader` (title left with its English name; actions right, 8px apart; 24px side padding; bottom hairline). Two page shapes follow:
- **List pages** (Runs, Members, Agents, Capabilities, Models, Connections, Scheduled, Approvals, Audit, Usage): `AdminLayout fullBleed` with `PageHeader className="static mx-0 mb-0"`, so the sticky 28px column header sits directly under the topbar with no padding between them, then grouped 36px rows, then the 40px `OffsetPagination` footer. The topbar action slot holds a 240px search field, one filter or one primary button.
- **Settings page** (设置 alone): the ordinary topbar — its action slot free, because there is nothing to do here — then a 24px-padded scrolling body of `PageSection`s. 凭据, 用量 and 审计 are not settings and are not tabs of it; they are their own sidebar entries, each an ordinary ledger or report page.
- **Conversations**: a full-bleed two-pane page whose list-panel header is also 64px with a bottom hairline, matching the topbar.

One kind of tab, one place: *content / view* tabs in a 40px hairline-bottomed row directly under the topbar, 16px inset, never repeated on the page. There is no second, *navigation* kind — navigation is the sidebar's job, and a segmented control in the topbar's action slot was navigation wearing an action's clothes.

Row grid (padding 0 24px, column gap 10px; a trailing zero-width actions track shortens the right padding to 14px so the gap makes up the difference). Header, group header and rows all take the same template and the same gutters, so the first and last columns of every ledger land on the same two edges as the topbar. Runs example: `[col.icon(), col.id(132), col.title(), col.meta(104), col.meta(104), col.num(64), col.age(80), col.actions(2)]`. Every list leads with the status column when the object has a status (Runs, Agents, Connections, Scheduled, Runtimes, Models, sandbox instances) — and where that list also multi-selects, the status slot is what carries the checkbox or with the 18px initial tile when it is a person or a capability; numbers and ages are right-aligned and tabular; `RowActions` has no track of its own and floats over the row's right end. Group headers indent 22px so the chevron sits on the status-icon column.

Rail (paper-panel, 1px left hairline): 64px header (status icon + word, mono id, then a 28px ghost expand button and a 28px ghost close button; 16px left, 8px right), body padding 16px / 16px / 8px, a property grid whose label column is `minmax(5.25rem, max-content)` with 28px rows and a 12px column gap, a full-width segmented tab control (24px segments in a 28px frame) with 16px below it, rail sections 20px apart with a 14px/500 head, hairline-separated 32px step rows, and a 12px/16px action footer with a top hairline (outline buttons left, a link with a trailing arrow right). Expanded, the same header · body · footer frame fills a centred 70vw × 70vh modal (min 720px, max 1200px wide) with 24px side padding and a 52rem centred measure on body and footer, so expanding buys reading room rather than stretched rows; the collapse button is its only control.

Entry surfaces (login, setup, onboarding, invite, join): `EntryPage` centres a single `EntryPanel` (400px wide, 24px padding) on the paper ground; nothing else is on the page.

Spacing rhythm: 2 / 4 / 8 / 10 / 12 / 16 / 24. Tight inside a group, generous between groups, more space above a heading than below. 10px is the ledger's column gutter and the sidebar's inset; 2px is the inset of every segmented frame; 24px is the topbar's and every settings body's side padding.

### Named Rules
**The Fixed Outer Edges Rule.** The topbar and every ledger — header row, group header, data row, and the ledger on every page — share the same two vertical edges at 24px. Internal space is divided only after those edges are set: content columns are `minmax(floor, weight fr)`, never fixed pixels that leave a ragged right edge. A page that starts its rows at a different inset is wrong, not merely different.

**The Two Levels of Arrival Rule.** Opening a level-one view (anything reached from the sidebar) rises in with `page-in`. Opening a level-two view (a detail page carrying an entity id on a non-rail view) is pushed in from the right with `detail-in`. Views whose `?id=` selects a rail instead of a page (runs, approvals, conversations, connections, capabilities, agents, connectors) stay level one — a view listed in `RAIL_VIEWS` must never key its page transition on the entity id, or selecting a second row replays the entrance instead of swapping the rail's content. Two arrivals, no third.

**The One Way In Rule.** Clicking a row in any ledger opens the detail rail — never a second page. Clicking the row that is already open closes it, and clicking a different row swaps the rail's content in place rather than replaying its entrance. There is exactly one escalation from there, the rail's expand button, and exactly one control that means "bigger" anywhere in the console. Two forms of detail would make the user predict which list navigates and which peeks; there is nothing to predict.

**The Addressable Detail Rule.** Every step of the ladder lives in the URL: `?id=` (or `&item=`) selects the rail, `&tab=` picks its tab, `&view=full` lifts it into the expanded panel. The expanded panel is therefore linkable, bookmarkable, and dismissed by the browser's back button — never a dead end floating over the list. `view` belongs to the selected entity, not to the route: it survives a tab change on the same entity and is dropped the moment the selection changes or clears, because an expanded panel with nothing selected is not a reachable state.

**The Every Entrance Has An Exit Rule.** Nothing disappears without playing its reverse: `rail-out` before the rail unmounts, `pop-out` on every floating layer (menus, tooltips, dialogs, the toast strip, the turn preview), `overlay-out` under them, `modal-out` when the expanded rail collapses. A component that owns an exit stays mounted until the animation ends (the rail unmounts on `animationend`; a toast pushed past the cap is asked to leave and drops itself when its own `pop-out` ends, never mid-flight and never on a child's animation).

**The Action Slot Is For Actions Rule.** The topbar's right end holds what you can *do* on this page, and nothing else. A segmented control that navigates between pages is not an action, and putting one there — as 设置 did, one control away from 添加凭据 — asks the reader to tell navigation from action by reading rather than by position. Navigation is the sidebar's job.

**A Place Is Not An Object Rule.** 设置 named a place and hid three real objects inside it: a credential ledger, a usage report and an audit log, none of which is a setting, each reachable only by first going somewhere it does not belong. When a "section" turns out to hold things of different shapes, those things are pages, and the section was a folder someone put them in. Settings keeps what is genuinely the workspace's own — its name, its language, its auth providers, its runtime policy — and the ledgers stand with the other ledgers.

**The Passing Message Rule.** The result of pressing a button is a passing thing: it floats in at the top, holds long enough to be read, and leaves. It never opens a row in the layout — a strip under the header pushes the whole list down, stays after it has been read, and makes a confirmation look like a page state. A *standing condition* is the opposite and keeps its place in the page: "this workspace has no agent" is why the button is disabled, not news about something that just happened.

**The Two Voices Rule.** Two voices speak in this console and they never share a tone: ours (a sentence, in the reader's language, in ink) and the machine's (a server string, an exit code, a payload). The machine's goes in a `VerbatimBlock`, one tone step down. `ErrorState` and the toast both take them as separate props for this reason: passing a raw server message as `description` puts words in our mouth that we did not write, and interpolating one into a translated sentence (`"切换失败:{{message}}"`) does the same thing with extra steps. The rule governs those two props; the block itself is the wider idiom and also holds quoted content a person wrote.

**The Quiet Selection Rule.** A list gets multi-select only when it has a verb that is *uniform* across rows and *genuinely repeated* — the same action with the same parameters applied to each (delete, cancel, reclaim; never edit or configure, where every row differs), and an action people really do to many at once (stale sandboxes, models imported in a batch; not the agents you built by hand). A checkbox with no bulk verb to submit to is an affordance with no destination. Approvals are the deliberate exception: approve and deny are uniform, but they are *decisions*, and batching a decision is the thing not to build.

Where it applies, selection spends no width. `SelectableStatus` puts the checkbox in the 14px status slot: the glyph at rest, the checkbox on hover, and the checkbox on every row while any row is selected, so the set you have built stays visible. A permanent column of empty boxes is a lot of furniture for a rare verb, and the one small checkbox in the ledger header — select-all — is what says the list can do this at all.

**The Row Reading Rule.** A column header never wraps: one line, truncating, in a 28px header. Every cell in a row is vertically centred, and every numeric or age column is right-aligned and tabular, so the eye reads down a column of figures without drifting.

### Runs (first surface) note
- The run list has no **model** column: the list API (`AgentRunSummary`) carries no model field. The model appears only in the rail as a mono property from the run detail. Add the column (mono, 12px, muted-bordered, right of agent) when the API does.
- The run list has no **cost** column for the same reason; the row template reserves nothing for it. Add it as a right-aligned `LedgerNum` when the API carries cost.

### Product-truth notes
- The approval bar ships **deny + allow-once** only. The API has no session-scoped approval, so a third button would name a decision the product cannot make. This is a product fact recorded here so it is not read as a missing state.
- **The Agent is the first-class object; the sidebar says so.** It stands alone above the groups rather than sitting as a sibling of the things it is assembled from. Below it, **运行中 / Activity** is what agents are doing (conversations, inbox, runs, schedules) and **构建 / Build** is what an agent is made of — its model, its capabilities, where it runs (执行层), and the platforms it can be let out on (集成). A thing that only exists to configure an Agent belongs in Build, never beside the Agent.
- **Tabs are for a different shape of content; groups are for one shape in categories.** 执行层 showed three tabs — local device, cloud sandbox, external agent — where every row was the same shape, a runtime. They are one ledger with `LedgerGroup` headers now, which shows the whole picture at once and folds what the reader does not need. An Agent's 动态 / 配置 / 审计 stay tabs, because those are three different kinds of content.
- **执行层 lives in Build, not in Settings.** It was a settings tab, which made every settings-family page render a control for it — and, because the tab strip is rendered on each of them, made five unrelated pages fire a `/sandboxes` request they had no use for. One control, one home.
- **Parsar has two ends, and each gets its own door.** Upward it connects to the platforms people are on — Feishu, Slack, Discord, Teams — which receive events; that is **集成 / Integrations**, in the sidebar, backed by `workspace_im_connectors`. Downward it connects to the agents themselves — `agent_daemon`, `http`, `a2a` — which Parsar *calls* (`AgentConnector.Prompt`); that is an Agent's **connector**, an attribute on the Agent, surfaced as a column and a counted filter on the Agent list. The two ends are not two views of one thing and must never be merged. The UI once named them 连接 and 连接器, one character apart, because it dropped the qualifiers the server already carries (`workspace_im_` and `Agent`); the names above put them back. The bare word **连接器 / Connectors** belongs to the MCP directory, which is where the industry uses it bare. The downstream connector had a page of its own for a while; it does not deserve one. `agent_daemon`, `http` and `a2a` are not instanceable, hold no state and cannot be created — they are the *values of an attribute on Agent*, and a value set is not an object. Promoting one to a first-class place is the OOUX error of mistaking instances for objects, the same mistake as giving "CAC" and "ROAS" pages instead of giving one to Metric. The question the page answered — which connectors are in use, and how many agents on each — belongs on the object that owns the attribute, so the Agent list's filter carries a count per value, zeroes included: a value with nothing on it is usually what the reader opened the menu to check. **集成** rather than **渠道** because the products this console is modelled on — Multica and Linear — both call this Integrations; 渠道 / Channels is the help-desk convention (Intercom, Zendesk), and Parsar is an agent console, not a support desk.
- The turn-nav rail shipped with its full motion vocabulary from the first commit; an earlier review that scored it as static was reading a module a stray NUL byte made ungreppable. The byte is gone; the behaviour above is the shipped behaviour.

### Compatibility note
- `adaptLegacyTemplate` in `ledger.tsx` accepts a raw grid string and adapts it to the flexible model. Every page is on `col.*`; the adapter exists so an outside caller does not break. Do not write a new string template.
- Some i18n strings survive for compatibility and are never rendered: every page `description`, `audit.footer.shownCount`, and login `noAccountHint`. `PageHeader` accepts `description` and drops it. Do not wire them back into the UI.

## Elevation & Depth

Flat by default. Depth is tonal: panel tone one step off the ground, hairlines for structure. Shadows exist in exactly two sizes and appear only on raised controls (outline and primary buttons, inputs, selects, textareas, the active theme segment) and on floating layers (menus, tooltips, dialogs, the rail's expanded modal, the toast strip, the entry panel, the skip link). The one dimming layer is the ground's inverse at 30%, under dialogs and the expanded rail.

### Shadow Vocabulary
- **control** (`0 1px 2px rgb(0 0 0 / 6%)`, dark `40%`): outline / primary / destructive buttons, inputs, selects, textareas, the pressed theme segment. Removed on a disabled outline button.
- **floating** (`0 1px 2px rgb(24 24 27 / 4%), 0 8px 24px -12px rgb(24 24 27 / 18%)`, dark `30%` / `55%` black): dropdown menus, tooltips, dialogs, the rail modal, the toast strip, the `.app-panel` entry card.

### Named Rules
**The Flat-By-Default Rule.** Rows, panels, group headers, sections and the rail never carry a shadow or a tint of their own. If something floats, it is a menu, a tooltip, a dialog, the rail's modal or the toast strip.

## Shapes

Small radii, straight structure. Controls are 6px (buttons, inputs, selects, textareas, badges, tooltips, nav rows, the workspace row, tab and theme frames); kbd, initial tiles, avatar tiles, tab segments and theme segments are 4px; menus, dialogs, the rail modal, the toast strip and the entry panel are 8px; the nav count badge is a 999px pill; a `VerbatimBlock` is 6px. Button `shape` also offers `pill`, `circle` and `square` for chips-as-buttons, round icon buttons and flush accents. Rows, panels, group headers, sections and the rail are square-cornered and hairline-bounded. Icons are 16px (14px inside rows, buttons, action buttons, messages and the rail), 1.5px stroke, round caps, lucide geometry; the status icon is a hand-drawn 14px SVG. Scrollbars are 8px, thin, transparent-tracked, with a 999px thumb. The resize handle has no visible shape of its own: it is the panel's hairline, recoloured.

## Components

### Buttons
The one button (`Button`, cva): 28px tall, 6px radius, 13px/500 label, 10px horizontal padding, 6px gap, 14px leading icon. Every variant shares the press and focus treatment.
- **Outline** (the ledger's default look): paper background, hairline-strong border, control shadow; hover = ink 3% tint. Disabled: hairline border, transparent background, no shadow, 50% opacity.
- **Primary** (`default`): indigo background, white text, control shadow; hover = indigo-emphasis. Reserved for the one primary action of a screen, if any (邀请新成员, 创建能力, 配对新设备; the Runs page has none) and for 保存 in the layout prompt.
- **Destructive:** danger background, white text, control shadow; hover = danger-emphasis. Dialog confirmations only.
- **Secondary:** paper-muted background, ink text, no border, no shadow; hover = pressed tint.
- **Ghost:** muted text, no background; hover = ink 3% tint and text to ink. Used for the rail's expand, collapse and close, the step "view raw" toggles and 恢复 in the layout prompt.
- **Link:** ink text, underline on hover, 4px underline offset, trailing 14px arrow. The rail's "open conversation".
- **Sizes:** `sm` 24px / 8px padding / 12px label (pagination, retry, the layout prompt); `default` 28px; `lg` 32px / 12px padding; `icon` 28px square.
- **Press / Focus:** `scale(0.97)` on active with the spring ease (off under reduced motion); focus = 2px ring of indigo at 40%, offset 1px from a paper ring-offset.

### Action icon buttons (`ActionIconButton`, `RowActions`)
- **Button:** 28px square, 6px radius, a 14px muted lucide icon; hover = ink 3% tint and the icon to ink; `danger` tone turns the icon failed-red on hover only; the resting icon never carries colour. A 12px tooltip (paper, hairline, 6px radius, floating shadow, `pop-in`, 4px offset) names the action. `busy` swaps the icon for a spinning loader.
- **Cluster (`RowActions`):** floats over the row's right end — absolutely placed 16px from the right edge, vertically centred, 6px radius, 2px inner padding — on `app-hover-solid`, the opaque twin of the hover tint, so it hides the cells it covers while matching the hovered row. 2px gap, 28px minimum height; hidden (opacity 0) until the row is hovered or holds focus, revealing over 150ms on the settle ease. Pass `always` for rows whose actions must be discoverable at rest, and `inline` — the escape hatch — for an action pair that belongs to a form control inside the row (rename, confirm), which stays in flow and visible.

### Chips
- **Badge:** 20px, 12px ink text, hairline-strong border, paper background, 6px radius, 6px padding, 6px gap. The `variant` colours only the optional 6px dot (success → completed green, warning → running amber, destructive → failed red, neutral → queued grey with muted text, primary → indigo); `pulse` adds a ping on the dot. At most one per row, for a secondary state (已禁用, 待处理, unmanaged via `ManagedBadge`); capability flags in the rail.
- **Count pill:** 12px muted tabular number on the ink-7% tile, 999px pill, 6px padding (sidebar nav badge).
- **Kbd:** 12px muted sans on paper, hairline-strong border with a 1.5px bottom edge, 4px radius, 2px/4px padding, 20px minimum width, leading-none.

### Cards / Containers
- There are no cards. Containers are the sidebar, the main column, the rail, `PageSection`s and ledger groups, separated by hairlines, spacing and one tone step. Content never sits in a bordered box inside another bordered box. The single floating card is `EntryPanel` (`.app-panel`: hairline, 8px radius, floating shadow, 400px, 24px padding) on login / setup / onboarding / invite / join: a 13px/500 "Parsar" wordmark, an optional 22px/500 title and one 14px muted sentence, the form, and an `EntryFooter` (top hairline, 16px above, message left, buttons right).
- **PageSection / SectionHead:** the section of a settings or full-page view: a 28px head row (16px/500 ink title, optional 12px muted tabular count, one right-aligned action), 8px below it, content beneath; sections 24px apart; no border, no background. `SectionHead` is the head on its own, for a section whose content must span the page. Sixteen and not thirteen because a heading the size of the rows beneath it is a caption: the page reads 20 · 16 · 13, three sizes the eye separates at a glance.
- **RailSection:** the rail's section, and the only one it has (`DetailSection` is this component). A 14px/500 head with an optional muted count and one right-aligned action, 4px above its content, sections 20px apart. Fourteen and not the page's sixteen: in a 384px panel the head introduces a property list, not a page, so it sits one tick over the 13px values and two over the 12px labels. Twelve was the same size as every label under it and separated by weight alone.

### Inputs / Fields
- **Input:** 28px, paper background, hairline-strong border, control shadow, 6px radius, 8px padding, 13px ink text; search variant adds a 14px muted leading icon (28px left padding) and a trailing kbd (44px right padding).
- **Select:** the native `select` styled as the input: 28px, 8px left / 28px right padding, appearance none, a 14px muted chevron 8px from the right edge.
- **Textarea:** the input's stroke and shadow at 72px minimum height, 6px/8px padding, relaxed line-height.
- **Label / Field:** `Label` is 12px muted, 4px above its control; `Field` stacks label · control · optional 12px muted hint (4px below). An `InlineError` may stand in for the hint.
- **Focus:** border and a 1px ring turn indigo, 150ms settle on border and shadow.
- **Placeholder:** muted. **Disabled:** paper-muted background, 60% opacity, not-allowed cursor.
- **⌘K exception:** the search field on a ledger page may carry a `⌘K` kbd inside its right edge. This is the one keyboard hint the UI shows, approved by the user; no other shortcut is advertised.

### Messages (`ErrorState`, `InlineError`, `InlineNotice`, `VerbatimBlock`, `Toast`)
- **InlineError:** a 14px failed-red triangle (top-aligned) and 13px ink text, 6px apart; `role="alert"`. Under a field, in an `EntryFooter`, or as a 36px hairline row above a ledger's rows.
- **InlineNotice:** the same shape with the tone in the glyph: success = the completed status icon, error = the failed-red triangle, warning = the running-amber triangle, info = a muted info circle; text in ink; optional trailing action. Inside a full-bleed page it sits in a hairline-bottomed 8px/16px row under the tabs row.
- **ErrorState:** the block form: 14px triangle, 13px/500 title, `description` (12px ink — our sentence), `detail` (the server's string, in a `VerbatimBlock` that shrinks to its content, wraps at 52rem and scrolls past 160px), the hint 12px muted, one `sm` outline retry button indented 24px; 16px vertical padding; no red box.
- **VerbatimBlock:** content shown exactly as it was written — a server message, a command, a payload, a log, a skill's instruction, an agent's system prompt — in a paper-muted block: 6px radius, 8px padding, 12px mono ink, relaxed leading, `break-words` so a line breaks only when it cannot fit on its own. The fill is the whole point: one tone step down from our own copy is how a reader tells which half is quoted. No border, no colour, no label. It states no height — a five-line server message and a 400-line skill file want different answers — so each caller passes the `max-h-*` its content deserves.
- **Toast (`ToastProvider` / `useToast`):** the console's one transient message, portalled to `body` at z-60 — one step above the 50 dialogs use, because a message an overlay dims is a message nobody reads — fixed 12px from the top and centred; paper, hairline, 8px radius, floating shadow, `pop-in` on arrival and `pop-out` on exit; 6px vertical padding, 12px sides (12px / 6px when it carries an action); a 14px tone glyph with 13px ink text, an optional `detail` in a `VerbatimBlock` beneath it, and optional buttons to its right. Four seconds, seven for an error; hovering or focusing *pauses* the countdown and leaving resumes it with the time that was left, so a resting cursor cannot pin a message on screen. At most three stand at once — a fourth asks the oldest to leave rather than yanking it. Failures live in an `aria-live="assertive"` region and everything else in a polite one; the strips carry no role of their own, so nothing is announced twice, and both regions keep speaking under a modal because `aria-hidden` exempts `[aria-live]` and its ancestors. `key` re-words the strip that is already standing — same node, no second entrance — instead of stacking a second one, and `persist` keeps it until dismissed and exempts it from the cap, because a question is not something to crowd out.
- **Empty:** 20px muted icon, 13px/500 title, muted description, one action, 64px vertical padding, centred.

### Navigation
- **PageHeader (topbar):** 64px, sticky, full column width, bottom hairline, 24px padding, 12px gap; title 20px/600 on the scale's own 24px line — never `leading-none`, because the title truncates and a 20px line box around a 20px face clips the tail off every descender — with the English name (`subtitleFor`) in 12px muted, 8px after it on the same baseline; an optional 12px muted back link before the title; actions right-aligned 8px apart. On full-bleed pages it is `static mx-0 mb-0`. `description` is accepted for type-compatibility and never rendered.
- **View tabs row:** content tabs (`TabsList`) in a 40px row, bottom hairline, 16px inset, directly under the topbar; content begins beneath with no top margin.
- **Sidebar groups:** Agent alone at the top, then 运行中 (对话, 收件箱, 运行, 定时任务, 用量, 审计 — what agents are doing and the record of what they did), 构建 (模型, 能力, 执行层, 集成, 凭据 — what an agent is assembled from and given), 团队 (成员, 设置). A group is a claim about what kind of thing a row is, so a row moves group when its kind changes, not when the list gets long.
- **Sidebar rows:** 30px, 8px padding, 6px radius, 14px text inheriting the sidebar grey, 16px muted icon 8px before the label; hover = ink 3%; active (`aria-current="page"`) = ink 6% tint, 500 weight, text and icon return to ink. No border and no shadow on the active row. Focus = 2px indigo-40% ring.
- **Workspace row:** 32px, 8px padding, 6px radius, one 13px text line "Parsar / Workspace" with a 14px chevrons-up-down icon; brand 500 ink, separator and workspace muted; hover tint, open = pressed tint. The menu is a 300px floating layer (8px radius, hairline, 4px inner padding, pop-in).
- **Account row:** pinned bottom, top hairline, 10px above; a 24px initials tile (ink-7%, 12px/500) with the name 13px/500 and role 12px muted as a hover-tinted trigger; the two-segment sun/moon theme toggle to its right (hairline frame, 2px inset, 22×26px segments, 14px icons; the resolved segment sits on paper with the control shadow and ink icon, the other is muted).
- **Menus:** floating shadow, hairline, 8px radius, 4px padding, 13px items with 4px radius and a pressed tint when highlighted, a 14px muted check on the chosen item, 1px hairline separators 4px apart, pop-in entrance.
- **Skip link:** ink background, white 14px/500 text, 6px radius, floating shadow; slides in from above on focus.

### Dialogs (`Dialog`, `AlertDialog`)
Dialogs are built like the rail, not like a card: a floating paper panel (448px max, 8px radius, hairline, floating shadow) whose header and footer are hairline-bounded bars.
- **Header:** a 48px minimum bar bounded below by a hairline, holding the 16px/500 ink title — a dialog is a small surface, so it runs the rail's ladder: 16 over a 14px `RailSection` head over 13px content — and, beneath it, an optional 13px muted description. `Dialog` reserves 48px on the right for a 28px ghost close button (top right, 8px/10px inset); `AlertDialog` has no close — its actions are the only exits.
- **Body:** 16px padding, 16px gaps, 13px ink.
- **Footer:** a hairline-topped bar, 12px/16px padding, actions right-aligned 8px apart: outline cancel, then primary or destructive confirm.
- **Motion:** `pop-in` on open, `pop-out` on close, over a 30% inverse overlay that fades in with `overlay-in` and out with `overlay-out`. The overlay never snaps.

### Shared conversation (`/c/<id>`)
One link is one conversation: everyone who opens it lands in the same thread, and that thread is the row the console already lists. The link is an **address, not a ticket** — who may read it is decided by signing in and being a workspace member, which the server enforces on every call the page makes (`requireWorkspaceMember`), so there is no second permission to keep in step with the first.

The surface is shared, the shell is not. `ConversationMain` takes `chrome="bare"`: no admin topbar, no turn-nav rail, no "view run" link — those index and address things a colleague has no use for, and the rail would anchor to the window while the thread stays measured. What remains is the thread, the work trace, the approval bar and the composer, under a 56px bar carrying the mark, who you are talking to, and the theme toggle. Same world, same greys, same accent; the console's density relaxed because this page is read now and then rather than watched all day.

Its states are all three: signing in returns to the link rather than the console (`stashReturnTo` covers the SSO redirect and the 401 bounce, not just the form); a conversation in another workspace says **you cannot open this**, never 404, because 404 reads as a mistyped link; and the composer carries the same sandbox guard the console does, so a send that cannot be served is refused with a reason instead of failing silently.

### Agent exposure (signature)
An Agent is only useful once someone outside the console can reach it, so its exits are a tab on the Agent rather than a setting filed elsewhere. The list is deliberately complete — the always-open exits sit beside the ones you configure, because "which doors are open" is the question and an answer that omits the open ones does not answer it.

A conversation is **one** exit, not two: the console frames it in admin chrome and `/c/<id>` frames it bare, but it is the same thread behind the same membership. The share affordance therefore lives on a conversation row, not on the Agent — and copying a link needs no write access, since a viewer may share what they can already read.

### Filter menu (signature)
`FilterMenu` with `FilterGroup` / `FilterOption` / `FilterToggle` / `FilterSeparator`, over the shared `menuContentClass` / `menuItemClass` in `ui/menu.tsx`. One outline trigger with the `ListFilter` icon, the word 筛选, and the active facet after a muted "·"; a floating panel of single-choice groups and toggles, hairline-separated. `FilterOption` takes an optional `count`, which turns the menu into the list's distribution as well as its filter — and the count is rendered for every known value including zero, because a value with nothing on it is usually the thing the reader opened the menu to check. A list that filters does not invent its own control.

### Ledger (signature)

**Column model.** A ledger declares what each column holds, never a pixel template: `columns={[col.icon(), col.id(132), col.title(), col.meta(104), col.num(64), col.age(80), col.actions(2)]}`. This is the only way to build a ledger; all 26 ledgers in the console are on it. The primitive turns every content column into `minmax(min, weight fr)` — title 200px·2fr, text 120px·1fr, meta 104px·0.8fr, id 112px·0.7fr, age 80px·0.6fr, num 64px·0.5fr, all overridable per call — and keeps `icon` (14px), `check` (16px), `tile` (18px) and `fixed(px)` rigid, so each column has a floor sized for its content, the row always fills the page, and spare width is shared in proportion. `col.actions(n)` declares a **zero-width** track: the action cluster overlays the row instead of reserving space, so the last *content* column of every ledger ends on the same edge. `adaptLegacyTemplate` survives as a compatibility shim for a raw string template; no page uses it, and none should.
`Ledger` (scrolls; takes the grid template once) → `LedgerHeader` → `LedgerGroup` → `LedgerRow` with `LedgerId`, `LedgerNum`, `InitialTile` cells.
- **Header:** sticky, 28px, paper, bottom hairline, 24px gutters, 12px muted labels, `aria-hidden`; numeric columns right-aligned. Every header cell truncates on one line (`min-w-0 truncate whitespace-nowrap`) — a column header never wraps and never grows the header past 28px. It hugs the topbar: nothing sits between the 64px header and the 28px column header.
- **Group:** a 28px full-width button, 22px left / 24px right padding, bottom hairline, 12px text: a 14px muted chevron (rotates -90° when collapsed, 200ms spring), the group word in ink 500, the count muted and tabular; hover tint, inset focus ring.
- **Row:** 36px, bottom hairline, 24px gutters, 10px column gap, 13px ink, `role="option"`, focusable, a `group` for its actions; every cell is vertically centred (`[&>*]:self-center`) and floors at `min-w-0` so long values truncate instead of pushing the grid; hover and keyboard focus = ink 3% tint (150ms settle); selected = indigo 3% tint plus a 2px indigo bar on the left edge. Cells: 14px status icon or 18px `InitialTile` (ink-7%, 4px radius, 11px/500 initial) leading; 500 name with a muted 12px summary after " · "; `LedgerId` mono 12px muted, truncating; `LedgerNum` mono 12px tabular right-aligned in ink (muted when the value is absent); categories 12px muted; age 12px muted right-aligned. Missing values render as a muted "—".
- **Footer (`OffsetPagination`):** 40px, top hairline, 16px padding, 12px muted tabular range, two `sm` outline buttons 4px apart; boundary buttons disable, never hide; hidden entirely when the total is zero. Its strings come only from `common:pagination.range|prev|next`; pages do not override them.
- **Table:** the same idiom as an HTML table (`Table*`): 28px muted 12px header, 36px hairline rows, 12px cell padding, hover and selected tints; no outer card.
- **Skeleton:** paper-muted at 70%, 6px radius, pulse; list skeletons echo the 28px header and 36px rows.

### Capability row (signature)
Every capability an agent has renders as one row of the same shape, whichever kind it is: a 14px status glyph carrying the row's worst state (failed = a missing credential, an unsupported engine or a deleted version; running = a new version or a deprecation; cancelled = a built-in switched off; completed = usable), the name in 13px/500, its kind in 12px muted, the pinned version right-aligned in 12px mono, and the verbs in a hover-revealed `RowActions` cluster — switch version, upgrade, remove, or the built-in's power toggle. Beneath: the description on one truncating line, then only the problems worth acting on, one line each with at most one thing to press.

It says each fact once. The version lives in the version slot and nowhere else; a credential that is set says nothing at all, because the glyph already reports that the row is in order and an "add credential" link beside a credential you have added is an affordance with nowhere to go; provenance lives on the capability's own page. Where there is no version to show, the slot is empty rather than an em dash.

### Detail rail (signature)
`DetailRail` (header · scrolling body · footer, resizable, expandable) with `RailSection`, `PropertyList` / `Property`, segmented `Tabs`.
- **Rail:** 384px by default, draggable 320–640 through a left-edge `ResizeHandle` with the shared `LayoutPrompt`; paper-panel, left hairline. It is the console's only detail surface: every ledger — runs, approvals, connections, agents, connectors, and all three capability catalogues — opens into it. Agent is its widest brief (three tabs, an editable config, an audit trail) and it fits: a tab lives in the URL beside the id, so `&view=full` survives a tab change and the expanded panel does not collapse under the reader. Its *width* is what animates, from and back to zero on a 260ms spring, so the list column widens and narrows in the same motion instead of jumping once the rail is gone. Every closer (the X, clicking the open row, a route change) flips the same `open` prop, so they all play the identical exit; the caller holds the selected item through that exit and drops the mount on `onClosed`. Switching to another row swaps the content and leaves the width alone.
- **Shell (`RailLayout`):** the list column (`min-w-0 flex-1`) and the rail sit in one flex row. Pages compose this, never their own; a ledger's column minimums must total less than the column width the rail leaves behind (824px at the 1440 design width) or the last column clips. The same arithmetic binds the rail's own nested ledgers, which have 384px, not a page: a column that only earns its width on a wide page (a conversation uuid beside a run) comes out.
- **Header:** 64px, bottom hairline, 8px gap: the object's identity — a 14px status icon or connector mark, the name or status word 13px/500 ink, then its badges or a mono id filling the rest — followed by a 28px ghost expand button (Maximize2) and a 28px ghost close button (X); 16px left, 8px right. The name and its badges sit on one baseline here; a detail that still owns a page puts them under the topbar rule with `DetailHeading` instead.
- **Expanded panel:** the Maximize2 button writes `&view=full` to the URL, which lifts the same header · body · footer into a centred 70vw × 70vh panel (min 720px, max 1200px, paper-panel, hairline, 8px radius, floating shadow) over a 30% inverse overlay. It flies in from the rail's side with `modal-in` (420ms spring, from 34vw right at 0.35 scale) and settles back with `modal-out` (260ms settle, to 0.45 scale); the overlay fades 240ms in / 200ms out. Centring lives in the keyframes and `.app-modal-center`, never in a translate utility. Its only control is the Minimize2 collapse button; the close X and the rail stay where they were. Body and footer widen to 24px side padding and hold a 52rem centred measure — the extra width is reading room for previews and code, never a stretched property grid.
- **Body:** 16px padding (8px bottom); the agent line (18px initial tile + 13px/500 name, 12px below); then the property grid.
- **Properties:** a `minmax(5.25rem, max-content)` muted 12px label column — a floor of 84px so short labels line up, growing to the longest label in the list so none of them wrap onto a second line and break the 28px rhythm — 12px gap, 13px ink values, 28px rows, truncating; `mono` values at 12px mono. The label truncates at 12rem with its full text in a `title`, so one long label cannot eat the value column. There is one property grid: a page that widens the label column for itself is a page that has not moved into the rail yet. (`fit-content()` is *not* legal as the max of `minmax()`; a browser drops the whole `grid-template-columns` declaration and the grid silently collapses to one column. `max-content` is the one that works.) Long values (reasons, next actions, capability badges) release the height and wrap. Values are never muted.
- **Segmented tabs:** 28px hairline frame on paper, 2px inset, 2px gap, 24px segments 4px-rounded, 12px muted labels, hover tint; active segment = pressed tint, 500, ink; press scales to 0.97; content starts 16px below.
- **Sections (`RailSection`):** 20px apart, 14px/500 ink head with an optional muted tabular count and one right-aligned action, 4px below the head. The Agent rail's `DetailSection` is an alias of it, so the rail cannot drift into two sizes of the same thing.
- **Steps:** hairline-separated 32px rows: a 14px muted lucide icon, a 12px mono muted index right-aligned in 16px, a truncating 13px ink title with a muted detail after " · ", and a 24px ghost code toggle that reveals raw events in a `VerbatimBlock`.
- **Footer:** 12px/16px padding, top hairline, 8px gap: outline buttons left, the link button pushed right. The object's verbs live here, not in the topbar — a rail-backed list keeps its own header (search, filter, create) while the rail is open.

### Conversation surface (signature)
The one place in the console that is not a ledger: a measured 48rem thread with a composer under it. Its parts are named components, not page-local markup.
- **Composer:** one tonal, borderless panel — paper-muted ground, 16px radius, no border and no shadow — with 16px side padding, 16px above and 12px below. Inside it an auto-growing transparent textarea (14px ink, relaxed leading, 40px minimum, growing to 200px then scrolling; Enter sends, Shift+Enter breaks a line, never mid-IME), then a 32px toolbar row aligned right: the bound agent as an 18px initial tile plus its name in 12px muted, and **one** round 32px ink button (`surface-emphasis` ground, on-emphasis glyph, 999px, press scales to 0.97). It carries an up-arrow to send and becomes a filled square **stop** button while a run is in flight — the same control, never two. It sits in a hairline-free footer at the thread's own measure.
- **WorkTrace:** the collapsible block between a user turn and the answer. A 32px head row — the run's status icon, the status word in 13px muted sans, " · ", the elapsed time in 12px mono tabular, and a chevron that only appears on hover or focus — with **no trailing rule**; the hairline belongs to the step list beneath it. Steps are 32px hairline-separated rows: a 14px muted category icon (terminal / file / search / wrench), the verb in 13px ink, the target in 12px mono muted and truncating, the status icon while unfinished, the elapsed time in 12px mono, and a chevron that rotates 90°. Expanding a step reveals its args and result as 12px mono in paper-muted 6px blocks, capped at 160px and scrolling, mounted lazily and unmounted after the exit. The block opens while running or when a step waits on the user, folds once the answer lands, and never fights a manual toggle; folded and still running, the current step remains as a single tail row.
- **ApprovalBar:** replaces the composer while a permission is pending — the slot holds one control set, never both. A 28px head row: a 14px tool icon (terminal for bash, wrench otherwise), the tool name in 12px muted, and a mono tabular countdown right-aligned (prefixed `1 / n · ` when several wait). Then the request sentence in 13px ink at weight 400, then the command in 12px mono muted, truncating. Actions right-aligned 8px apart: outline **拒绝** (Escape) and primary **允许一次** (Enter), followed by the same round ink stop button the composer shows. There is no "allow for this session": the API carries no session-scoped approval, so deny and allow-once are the whole vocabulary — product truth, not a missing state.
- **TurnNavRail:** a 20px-wide rail on the thread's left edge, one 2px round marker per user turn, 7.5px apart. Resting markers are `line-strong` at 6px; the active turn (the last one starting above the viewport's top third) is ink at 26px. Hovering raises a wave around the pointer — 26 / 20 / 14 / 10px on the neighbours — over 150ms on the settle ease, and floats a 256px paper preview card (8px radius, hairline, floating shadow, two clamped lines of 12px muted) beside the marker with `pop-in`, dismissed with `pop-out` after a 160ms grace. Click or focus jumps the turn to the top of the viewport. Markers are sampled evenly to the height available (8–40, first and last always kept, the active turn swapped into its nearest slot), and the whole rail hides below 960px or with fewer than two turns.

### Resize handle and layout prompt (signature)
- **ResizeHandle:** `role="separator"`, `aria-orientation="vertical"`, `tabIndex 0`; a 6px-wide full-height hit area centred on the panel hairline (3px past the edge), `cursor: col-resize`; inside it a 1px line, transparent at rest, indigo on hover / focus / drag, 150ms settle. Arrow keys resize 8px, shift-arrow 32px; a drag of less than 2px is ignored.
- **LayoutPrompt:** draws nothing of its own. It is one persistent toast, keyed `layout-adjusted:<panel>` from the panel's own storage key, carrying three `sm` buttons 8px apart — primary 保存, outline 临时, ghost 恢复 — raised while a width is unsaved, dismissed on any choice, and dismissed again when the panel unmounts, so a rail that closes mid-question does not leave a strip with dead buttons. It persists because it is a question, and a question that times out has answered itself. It used to draw its own strip at the toast's coordinate, so a confirmation could land on top of it and swallow the clicks; a queue cannot collide with itself.

### Status icon (signature)
`StatusIcon`: a 14px hand-drawn SVG, `currentColor` from the six `status-*` tokens, 1.5px strokes, round caps. Queued dashed ring; running three-quarter arc on the `status-track` ring, spinning 1.8s linear (paused under reduced motion); completed and failed are filled discs with a paper check or x; cancelled a ring with a slash; interrupted a ring with a dash. Decorative unless given a `title`.

## Motion

Short, springy, rare. Every transition is non-linear; nothing is `linear` or default `ease` except the running icon's rotation. Every button-like control (`button`, `[role=button]`, `[role=tab]`) gets one shared transition from the stylesheet: colour, background, border and shadow at 150ms settle, transform at 120ms spring. Every keyframe is declared once in `src/style.css` and exposed as an `--animate-*` utility; components never write their own.

- **spring** (`--ease-spring`, `cubic-bezier(0.34, 1.56, 0.64, 1)`): every entrance and toggle.
- **settle** (`--ease-settle`, `cubic-bezier(0.22, 1, 0.36, 1)`): every exit, hover tint, colour change, row selection, field focus, the RowActions reveal.
- **page-in** (320ms spring; opacity 0 → 1, translateY 10px → 0, scale 0.995 → 1): level-one arrival. `PageTransition` wraps the main column and is re-keyed by the view, so every sidebar navigation replays the same rise once.
- **detail-in** (360ms spring; opacity 0 → 1, translateX 28px → 0): level-two arrival — a detail page pushed in from the right, re-keyed by view *and* entity id. `AdminLayout` picks the level automatically: an entity id on a view outside `RAIL_VIEWS` (runs, approvals, conversations, connections) is a detail; everything else is a page.
- **rail-in** (260ms spring, from 16px right) / **rail-out** (200ms settle, back to 16px right): the detail rail entering once per selection and leaving before it unmounts.
- **pop-out** (150ms settle, to scale 0.97): the reverse of `pop-in` on every floating layer.
- **modal-in** (420ms spring; from translate(−50% + 34vw, −50%) scale 0.35, opaque by 40%) / **modal-out** (260ms settle; to the same offset at scale 0.45): the rail's expanded modal flying from and back to the rail's side. **overlay-in** (240ms settle) / **overlay-out** (200ms settle): the 30% inverse overlay under it.
- **pop-in** (200ms spring, from scale 0.96): menus, tooltips, dialogs, the toast strip, the turn preview.
- **press** (`scale(0.97)`, spring, 120ms): buttons, action icons, segmented tabs and theme segments on active.
- **width spring-back** (`transition: width 420ms spring`): a panel returning to its default width after 恢复; the transition exists only for those 420ms so drags stay direct.
- **reveal** (opacity, 150ms settle): `RowActions` on row hover / focus-within.
- **chevron** (200ms spring): the ledger group chevron rotating to −90°.
- **running** (`spin 1.8s linear infinite`, `--animate-spin-slow`): the running status arc only.
- **collapse** (`grid-template-rows` 0fr → 1fr, 220ms settle, with opacity and a 4px lift on the content): the `WorkTrace` block and its step details; the body mounts on first open and unmounts after the exit.
- `prefers-reduced-motion: reduce` collapses every animation and transition to 0.01ms and removes the press scale and the running spin; the page, rail and modal then simply appear.

### Named Rules
**The Every Entrance Has An Exit Rule.** (See Layout.) If you add an animated arrival, add its reverse in the same commit and keep the component mounted long enough to play it.

**The One Keyframe Home Rule.** Every keyframe and every `--animate-*` utility is declared in `src/style.css`; components reference them and never write their own. Modal centring lives in the keyframes and `.app-modal-center`, never in a translate utility, so the two never stack.

## Do's and Don'ts

### Do:
- **Do** put every action in exactly one place on a screen. If a filter, a search, a count or a theme toggle already exists, reuse it.
- **Do** start every console page with the 64px `PageHeader`, passing `subtitleFor` so the English name appears in 12px muted beside the title.
- **Do** keep status colour inside the 14px status icon and set the status word in ink; lead a row with the status icon when the object has a status.
- **Do** set identifiers, durations, costs, e-mail addresses and timestamps in mono with tabular numerals, right-aligned in numeric columns.
- **Do** use the system font stack unchanged; let macOS render PingFang and Windows render YaHei.
- **Do** separate regions with hairlines and one tone step; use 36px rows and 30px nav rows; let the sticky column header hug the topbar on full-bleed list pages.
- **Do** use spring easing for entrances and toggles, settle for exits and hovers, and gate every animation behind reduced-motion.
- **Do** keep light and dark as one token set; dark is warm grey, never black.
- **Do** build every list on `Ledger` and pass the column template once so header and rows share one grid; build every detail pane on `DetailRail` + `PropertyList`; build every settings body on `PageSection`; build every entry surface on `EntryPanel`.
- **Do** write categories as 12px muted text and reserve `Badge` for one neutral-dot secondary state per row.
- **Do** put navigation tabs in the topbar action slot and content tabs in the 40px hairline row under it.
- **Do** hide `RowActions` until hover or focus, and pull pagination strings only from `common:pagination.*`.
- **Do** declare ledger columns with `col.*` and let the primitive build the grid; keep the 24px outer edges so every list shares the topbar's two edges.
- **Do** keep column headers on one truncating line, centre every cell vertically, and right-align numeric and age columns.
- **Do** give every entrance its exit — `rail-out`, `pop-out`, `overlay-out`, `modal-out` — and keep the component mounted until the animation ends.
- **Do** choose the entrance by level: `page-in` for a sidebar view, `detail-in` for a drill-down.
- **Do** put element-level CSS in `@layer base` so Tailwind's utilities keep winning.
- **Do** build the conversation surface from its named parts: the tonal composer with one round ink send/stop control, `WorkTrace`, `ApprovalBar`, `TurnNavRail`.

### Don't:
- **Don't** add a description or subtitle under a page title, a keyboard-hint line, a version string, or any helper copy that is not content or a control. The one exception, approved by the user, is the `⌘K` kbd inside the search field. The English page name beside the title is a name, not a description.
- **Don't** render the same action twice (a header button and a table-footer button, a sidebar utility row and a nav item, a topbar count and a footer count, a settings tab strip in two places).
- **Don't** use cards, stat tiles, nested bordered boxes, tinted message boxes, or coloured left borders thicker than the 2px selection marker.
- **Don't** wrap a category (role, type, source) in a chip, or put more than one `Badge` on a row.
- **Don't** add padding between the topbar and a ledger's column header, or a margin above the first section of a page.
- **Don't** animate the rail when it expands; only the modal moves, and the modal's only control is collapse.
- **Don't** let anything vanish without its reverse animation, and don't unmount a panel before its exit has played.
- **Don't** reserve a grid track for row actions, give a ledger a different inset than 24px, wrap a column header onto a second line, or hand `Ledger` a raw string template.
- **Don't** add an element rule to `src/style.css` outside `@layer base`; an unlayered `button { font: inherit }` beats every `text-*` utility and resets the console to 16px.
- **Don't** show a send button and a stop button at once, or put a second cancel control on the conversation while the approval bar owns the composer slot.
- **Don't** add a session-scoped approval button; the API has no such decision.
- **Don't** use 700 weight, a third grey, an 11px functional size, or arbitrary pixel sizes outside the scale.
- **Don't** load Google Fonts for Latin or CJK; Noto Sans SC stays a last-resort fallback only.
- **Don't** put colour in text to signal state; tint a background or draw an icon instead.
- **Don't** use linear easing for anything but the running spinner, or declare a keyframe outside `src/style.css`.
- **Don't** render the compatibility strings (`description`, `audit.footer.shownCount`, login `noAccountHint`).
- **Don't** reach past the semantic tokens (`fg-*`, `surface-*`, `line-*`, `accent`, `status-*`, `app-*`) to the raw palette; lint will fail the build.
