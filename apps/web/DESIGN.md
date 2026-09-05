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
    padding: "0 16px"
  ledger-group:
    textColor: "{colors.ink-muted}"
    typography: "{typography.label}"
    height: "28px"
    padding: "0 14px"
  ledger-row:
    textColor: "{colors.ink}"
    typography: "{typography.row}"
    height: "36px"
    padding: "0 16px"
  ledger-footer:
    textColor: "{colors.ink-muted}"
    typography: "{typography.label}"
    height: "40px"
    padding: "0 16px"
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
  layout-prompt:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    typography: "{typography.body}"
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

This file was written with the user as the spec, refreshed against the Runs build, and refreshed again against the whole shipped console (fifteen views in light and dark, `src/style.css`, `src/components/ui/*`, `src/components/layout/*`, `src/lib/layout-width.ts`). Where the build settled a value the earlier record had approximate (the 64px topbar, the draggable panels, the rail modal), the build's value is recorded here; the user rules themselves are unchanged.

**Key Characteristics:**
- Dense rows, hairline structure, two greys, one accent.
- System typefaces only: SF Pro / PingFang on macOS, Segoe UI / YaHei on Windows.
- Status colour lives only inside 14px status icons (and the 14px glyph of an inline message).
- One control per semantic per screen; no page description subtitles; the English page name is the only companion to a title.
- Springy, non-linear motion, short and rare: one page entrance, one rail entrance, one modal flight.

## Colors

Two tones of ink on paper, one indigo for selection and focus, and six status hues that never leave their icons. Every colour in TSX goes through a semantic token in `src/style.css` (`fg-*`, `surface-*`, `line-*`, `accent`, `status-*`); the raw palette is forbidden by lint. Light and dark are one token set: the same class names resolve to the dark values under `html[data-theme="dark"]`.

### Primary
- **Indigo** (`indigo`, dark `dark-indigo`): the selected row's 2px left marker, focus rings (indigo at 40%), the focused border and 1px ring of inputs, selects and textareas, the caret, text selection (indigo at 18%, dark 28%), the resize handle's hairline while hovered or dragged, and the primary button. **Indigo Emphasis** (`indigo-emphasis`, dark `dark-indigo-emphasis`) is the primary button's hover only. Never used for text, headings, links in body copy, or backgrounds larger than a button.

### Neutral
- **Ink** (`ink`, dark `dark-ink`): warm near-black, never pure black. All content, values, names, active items, actions, section heads and the text of every inline message. In dark the "emphasis" alias (`dark-ink-emphasis`) brightens the active-row text and the selection foreground by one step; in light it is identical to Ink.
- **Ink Muted** (`ink-muted`, dark `dark-ink-muted`): labels, metadata (ids, ages, counts, column headers, group labels, placeholders, field labels and hints), categories (member role, capability type and source), the English page name, inactive tabs, inactive nav icons, resting action icons, disabled text. `fg-subtle` and `fg-faint` exist as aliases and resolve to the same value; there is no third grey for content.
- **Ink Sidebar** (`ink-sidebar`, dark `dark-ink-sidebar`): the resting colour of sidebar text only, set once on the `.app-sidebar` surface; the active nav row returns to full Ink. This is a surface rule, not a third content grey.
- **Ink On Emphasis** (`ink-on-emphasis`): text on an ink or danger background (the skip link, the destructive button).
- **Paper** (`paper`, dark `dark-ground`): the main list ground, the topbar, buttons, inputs, kbd, tooltips, menus, the layout prompt and the entry panel. In dark these all sit on the ground (#191919); nothing lifts to a lighter grey except the muted tint.
- **Paper Panel** (`paper-panel`, dark `dark-panel`): the sidebar, the detail rail and the rail's expanded modal: one step off the ground. Group header rows inside the list stay on the list ground.
- **Paper Muted** (`paper-muted`, dark `dark-surface-muted`): the secondary button, a disabled field, the skeleton, the raw-event `pre` block inside the rail, and the pressed row in a menu.
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
- **Panel Title** (500, 13px, 18px): the status word in the rail header, the agent name at the top of the rail body, the head of a `PageSection`; also every button label, the active nav item, the active tab, the workspace brand, the account name and the "Parsar" wordmark in the entry panel.
- **Body / UI** (400, 14px, 20px): sidebar nav items, form copy, dialogs, the layout prompt, the entry panel's one muted sentence.
- **Row** (400, 13px, 18px): list rows, inputs, selects, textareas, property values, step titles, inline messages, footers.
- **Label** (400, 12px, 16px): column headers, group labels, property labels, field labels and hints, kbd, counts, ages, connector names, categories (role, type, source), tooltips, the topbar's English name, rail section heads (500).
- **Mono** (400, 12px, 16px, `tnum` + `zero`, tabular slashed-zero): identifiers, durations, timestamps, paths, e-mail addresses in rows, model ids, step indices.

### Named Rules
**The Three Weights Rule.** 600 is the page title only. 500 is the name of a thing (workspace, user, row title, run id in the rail), the active nav item or tab, button labels, section heads and the entry title. Everything else, including every label and every value, is 400. 700 does not exist.

**The Five Sizes Rule.** 20 / 14 / 13 / 12 and 12-mono. There is no 11px functional text and no sixth size in the console; 22 belongs to the entry panel alone.

**The Plain Category Rule.** A category (member role, capability type, source, connector) is 12px muted text in its own column, never a chip. A `Badge` marks at most one secondary state per row (disabled, unmanaged, pending) with a neutral dot.

## Layout

Three columns on a 1440 desktop: a 232px sidebar (draggable 200–360), a fluid main column, and a 384px detail rail (draggable 320–640). The page is not responsive below 960px; `body` sets a 960px minimum width. At or below 1360px the ledger row's inline error summary hides; the rail no longer narrows. The shell is `h-screen` with each column scrolling on its own.

Panel edges are the user's. A `ResizeHandle` straddles each inner hairline: a 6px invisible hit area (`role="separator"`, focusable) whose 1px line turns indigo while hovered, focused or dragged; arrow keys move it 8px, shift-arrow 32px. After a drag or key press the `LayoutPrompt` appears once at the top centre of the viewport: a floating paper strip (8px radius, hairline, floating shadow, `pop-in`) with the sentence "布局已调整" and three small buttons: 保存 (primary; writes `localStorage`), 临时 (outline; writes `sessionStorage`), 恢复 (ghost; clears both and springs the panel back to its default over 420ms on the spring ease). Storage keys are `parsar.layout.sidebar` and `parsar.layout.rail`; resolution on load is localStorage → sessionStorage → default.

Sidebar (paper-panel, 1px right hairline, 10px padding): a single 32px text row "Parsar / Workspace ⇕" at the top, then group labels (12px muted, 14px above, 4px below, 8px inset) and 30px nav rows 1px apart, then the account row pinned to the bottom (top hairline, 10px above; 24px avatar tile, name 13px/500, role 12px muted, and a two-segment theme toggle).

Main column: every page begins with the 64px `PageHeader` (title left with its English name; actions right, 8px apart; 24px side padding; bottom hairline). Two page shapes follow:
- **List pages** (Runs, Members, Agents, Capabilities, Models, Connections, Scheduled, Approvals, Audit, Usage): `AdminLayout fullBleed` with `PageHeader className="static mx-0 mb-0"`, so the sticky 28px column header sits directly under the topbar with no padding between them, then grouped 36px rows, then the 40px `OffsetPagination` footer. The topbar action slot holds a 240px search field, one filter or one primary button.
- **Settings pages** (General, Credentials, Runtime, Connectors, Usage, Audit): the same topbar with `SettingsTabs` in its action slot (the segmented control navigating between settings views), then a 24px-padded scrolling body of `PageSection`s (or, on Runtime, Capabilities and Connections, a 40px hairline row holding the content tabs first).
- **Conversations**: a full-bleed two-pane page whose list-panel header is also 64px with a bottom hairline, matching the topbar.

Two tab kinds, two places: *navigation* tabs (`SettingsTabs`) live in the topbar action slot; *content / view* tabs live in a 40px hairline-bottomed row directly under the topbar, 16px inset. Neither is ever repeated on the page.

Row grid (padding 0 16px, column gap 10px; header and rows share one template through `Ledger columns`), Runs example: 14px status icon · 132px run id · fluid agent (18px initial tile, 500 name, muted error summary) · 104px conversation id · 104px connector · 64px duration · 80px age. Every list leads with the status column when the object has a status (Runs, Agents, Connections, Scheduled) or with the 18px initial tile when it is a person or a capability; numbers and ages are right-aligned and tabular; a `RowActions` cluster, when present, is the last column. Group headers indent 14px so the chevron sits on the status-icon column.

Rail (paper-panel, 1px left hairline): 64px header (status icon + word, mono id, then a 28px ghost expand button and a 28px ghost close button; 16px left, 8px right), body padding 16px / 16px / 8px, an 84px-label property grid with 28px rows and 12px column gap, a full-width segmented tab control (24px segments in a 28px frame) with 16px below it, rail sections 20px apart with a 12px/500 head, hairline-separated 32px step rows, and a 12px/16px action footer with a top hairline (outline buttons left, a link with a trailing arrow right). Expanded, the same header · body · footer frame fills a centred 70vw × 70vh modal (min 720px, max 1200px wide) with 24px side padding on body and footer and the collapse button as its only control.

Entry surfaces (login, setup, onboarding, invite, join): `EntryPage` centres a single `EntryPanel` (400px wide, 24px padding) on the paper ground; nothing else is on the page.

Spacing rhythm: 2 / 4 / 8 / 10 / 12 / 16 / 24. Tight inside a group, generous between groups, more space above a heading than below. 10px is the ledger's column gutter and the sidebar's inset; 2px is the inset of every segmented frame; 24px is the topbar's and every settings body's side padding.

### Runs (first surface) note
- The run list has no **model** column: the list API (`AgentRunSummary`) carries no model field. The model appears only in the rail as a mono property from the run detail. Add the column (mono, 12px, muted-bordered, right of agent) when the API does.
- The run list has no **cost** column for the same reason; the row template reserves nothing for it. Add it as a right-aligned `LedgerNum` when the API carries cost.

### Compatibility note
- Some i18n strings survive for compatibility and are never rendered: every page `description`, `audit.footer.shownCount`, and login `noAccountHint`. `PageHeader` accepts `description` and drops it. Do not wire them back into the UI.

## Elevation & Depth

Flat by default. Depth is tonal: panel tone one step off the ground, hairlines for structure. Shadows exist in exactly two sizes and appear only on raised controls (outline and primary buttons, inputs, selects, textareas, the active theme segment) and on floating layers (menus, tooltips, dialogs, the rail's expanded modal, the layout prompt, the entry panel, the skip link). The one dimming layer is the ground's inverse at 30%, under dialogs and the expanded rail.

### Shadow Vocabulary
- **control** (`0 1px 2px rgb(0 0 0 / 6%)`, dark `40%`): outline / primary / destructive buttons, inputs, selects, textareas, the pressed theme segment. Removed on a disabled outline button.
- **floating** (`0 1px 2px rgb(24 24 27 / 4%), 0 8px 24px -12px rgb(24 24 27 / 18%)`, dark `30%` / `55%` black): dropdown menus, tooltips, dialogs, the rail modal, the layout prompt, the `.app-panel` entry card.

### Named Rules
**The Flat-By-Default Rule.** Rows, panels, group headers, sections and the rail never carry a shadow or a tint of their own. If something floats, it is a menu, a tooltip, a dialog, the rail's modal or the layout prompt.

## Shapes

Small radii, straight structure. Controls are 6px (buttons, inputs, selects, textareas, badges, tooltips, nav rows, the workspace row, tab and theme frames); kbd, initial tiles, avatar tiles, tab segments and theme segments are 4px; menus, dialogs, the rail modal, the layout prompt and the entry panel are 8px; the nav count badge is a 999px pill; the raw-event `pre` block is 6px. Button `shape` also offers `pill`, `circle` and `square` for chips-as-buttons, round icon buttons and flush accents. Rows, panels, group headers, sections and the rail are square-cornered and hairline-bounded. Icons are 16px (14px inside rows, buttons, action buttons, messages and the rail), 1.5px stroke, round caps, lucide geometry; the status icon is a hand-drawn 14px SVG. Scrollbars are 8px, thin, transparent-tracked, with a 999px thumb. The resize handle has no visible shape of its own: it is the panel's hairline, recoloured.

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
- **Cluster:** right-aligned in the row's last column, 2px gap, 28px minimum height; hidden (opacity 0) until the row is hovered or holds focus, revealing over 150ms on the settle ease. Pass `always` only for rows whose actions must be discoverable at rest.

### Chips
- **Badge:** 20px, 12px ink text, hairline-strong border, paper background, 6px radius, 6px padding, 6px gap. The `variant` colours only the optional 6px dot (success → completed green, warning → running amber, destructive → failed red, neutral → queued grey with muted text, primary → indigo); `pulse` adds a ping on the dot. At most one per row, for a secondary state (已禁用, 待处理, unmanaged via `ManagedBadge`); capability flags in the rail.
- **Count pill:** 12px muted tabular number on the ink-7% tile, 999px pill, 6px padding (sidebar nav badge).
- **Kbd:** 12px muted sans on paper, hairline-strong border with a 1.5px bottom edge, 4px radius, 2px/4px padding, 20px minimum width, leading-none.

### Cards / Containers
- There are no cards. Containers are the sidebar, the main column, the rail, `PageSection`s and ledger groups, separated by hairlines, spacing and one tone step. Content never sits in a bordered box inside another bordered box. The single floating card is `EntryPanel` (`.app-panel`: hairline, 8px radius, floating shadow, 400px, 24px padding) on login / setup / onboarding / invite / join: a 13px/500 "Parsar" wordmark, an optional 22px/500 title and one 14px muted sentence, the form, and an `EntryFooter` (top hairline, 16px above, message left, buttons right).
- **PageSection:** the section of a settings or full-page detail view: a 28px head row (13px/500 ink title, optional 12px muted tabular count, one right-aligned action), 8px below it, content beneath; sections 24px apart; no border, no background. `RailSection` is the 12px variant inside the rail (20px apart, 2px below the head).

### Inputs / Fields
- **Input:** 28px, paper background, hairline-strong border, control shadow, 6px radius, 8px padding, 13px ink text; search variant adds a 14px muted leading icon (28px left padding) and a trailing kbd (44px right padding).
- **Select:** the native `select` styled as the input: 28px, 8px left / 28px right padding, appearance none, a 14px muted chevron 8px from the right edge.
- **Textarea:** the input's stroke and shadow at 72px minimum height, 6px/8px padding, relaxed line-height.
- **Label / Field:** `Label` is 12px muted, 4px above its control; `Field` stacks label · control · optional 12px muted hint (4px below). An `InlineError` may stand in for the hint.
- **Focus:** border and a 1px ring turn indigo, 150ms settle on border and shadow.
- **Placeholder:** muted. **Disabled:** paper-muted background, 60% opacity, not-allowed cursor.
- **⌘K exception:** the search field on a ledger page may carry a `⌘K` kbd inside its right edge. This is the one keyboard hint the UI shows, approved by the user; no other shortcut is advertised.

### Messages (`ErrorState`, `InlineError`, `InlineNotice`)
- **InlineError:** a 14px failed-red triangle (top-aligned) and 13px ink text, 6px apart; `role="alert"`. Under a field, in an `EntryFooter`, or as a 36px hairline row above a ledger's rows.
- **InlineNotice:** the same shape with the tone in the glyph: success = the completed status icon, error = the failed-red triangle, warning = the running-amber triangle, info = a muted info circle; text in ink; optional trailing action. Inside a full-bleed page it sits in a hairline-bottomed 8px/16px row under the tabs row.
- **ErrorState:** the block form: 14px triangle, 13px/500 title, the message in 12px mono ink, the hint 12px muted, one `sm` outline retry button indented 24px; 16px vertical padding; no red box.
- **Empty:** 20px muted icon, 13px/500 title, muted description, one action, 64px vertical padding, centred.

### Navigation
- **PageHeader (topbar):** 64px, sticky, full column width, bottom hairline, 24px padding, 12px gap; title 20px/600 with the English name (`subtitleFor`) in 12px muted, 8px after it on the same baseline; an optional 12px muted back link before the title; actions right-aligned 8px apart. On full-bleed pages it is `static mx-0 mb-0`. `description` is accepted for type-compatibility and never rendered.
- **SettingsTabs:** the segmented `Tabs` control rendered once per settings page in the topbar action slot, one segment per settings view (general, credentials, runtime, connectors, usage, audit); the sidebar keeps the single 设置 entry active.
- **View tabs row:** content tabs (`TabsList`) in a 40px row, bottom hairline, 16px inset, directly under the topbar; content begins beneath with no top margin.
- **Sidebar rows:** 30px, 8px padding, 6px radius, 14px text inheriting the sidebar grey, 16px muted icon 8px before the label; hover = ink 3%; active (`aria-current="page"`) = ink 6% tint, 500 weight, text and icon return to ink. No border and no shadow on the active row. Focus = 2px indigo-40% ring.
- **Workspace row:** 32px, 8px padding, 6px radius, one 13px text line "Parsar / Workspace" with a 14px chevrons-up-down icon; brand 500 ink, separator and workspace muted; hover tint, open = pressed tint. The menu is a 300px floating layer (8px radius, hairline, 4px inner padding, pop-in).
- **Account row:** pinned bottom, top hairline, 10px above; a 24px initials tile (ink-7%, 12px/500) with the name 13px/500 and role 12px muted as a hover-tinted trigger; the two-segment sun/moon theme toggle to its right (hairline frame, 2px inset, 22×26px segments, 14px icons; the resolved segment sits on paper with the control shadow and ink icon, the other is muted).
- **Menus:** floating shadow, hairline, 8px radius, 4px padding, 13px items with 4px radius and a pressed tint when highlighted, a 14px muted check on the chosen item, 1px hairline separators 4px apart, pop-in entrance.
- **Skip link:** ink background, white 14px/500 text, 6px radius, floating shadow; slides in from above on focus.

### Ledger (signature)
`Ledger` (scrolls; takes the grid template once) → `LedgerHeader` → `LedgerGroup` → `LedgerRow` with `LedgerId`, `LedgerNum`, `InitialTile` cells.
- **Header:** sticky, 28px, paper, bottom hairline, 16px padding, 12px muted labels, `aria-hidden`; numeric columns right-aligned. It hugs the topbar: nothing sits between the 64px header and the 28px column header.
- **Group:** a 28px full-width button, 14px padding, bottom hairline, 12px text: a 14px muted chevron (rotates -90° when collapsed, 200ms spring), the group word in ink 500, the count muted and tabular; hover tint, inset focus ring.
- **Row:** 36px, bottom hairline, 16px padding, 10px gutter, 13px ink, `role="option"`, focusable, a `group` for its actions; hover and keyboard focus = ink 3% tint (150ms settle); selected = indigo 3% tint plus a 2px indigo bar on the left edge. Cells: 14px status icon or 18px `InitialTile` (ink-7%, 4px radius, 11px/500 initial) leading; 500 name with a muted 12px summary after " · "; `LedgerId` mono 12px muted, truncating; `LedgerNum` mono 12px tabular right-aligned in ink (muted when the value is absent); categories 12px muted; age 12px muted right-aligned; `RowActions` last. Missing values render as a muted "—".
- **Footer (`OffsetPagination`):** 40px, top hairline, 16px padding, 12px muted tabular range, two `sm` outline buttons 4px apart; boundary buttons disable, never hide; hidden entirely when the total is zero. Its strings come only from `common:pagination.range|prev|next`; pages do not override them.
- **Table:** the same idiom as an HTML table (`Table*`): 28px muted 12px header, 36px hairline rows, 12px cell padding, hover and selected tints; no outer card.
- **Skeleton:** paper-muted at 70%, 6px radius, pulse; list skeletons echo the 28px header and 36px rows.

### Detail rail (signature)
`DetailRail` (header · scrolling body · footer, resizable, expandable) with `RailSection`, `PropertyList` / `Property`, segmented `Tabs`.
- **Rail:** 384px by default, draggable 320–640 through a left-edge `ResizeHandle` with the shared `LayoutPrompt`; paper-panel, left hairline; enters once per selection with `rail-in` (260ms spring from 16px right). The rail itself never animates on expand or collapse.
- **Header:** 64px, bottom hairline, 8px gap: 14px status icon, the status word 13px/500 ink, the run id mono 12px muted filling the rest, a 28px ghost expand button (Maximize2) then a 28px ghost close button (X); 16px left, 8px right.
- **Expanded modal:** the Maximize2 button lifts the same header · body · footer into a centred 70vw × 70vh panel (min 720px, max 1200px, paper-panel, hairline, 8px radius, floating shadow) over a 30% inverse overlay. It flies in from the rail's side with `modal-in` (420ms spring, from 34vw right at 0.35 scale) and settles back with `modal-out` (260ms settle, to 0.45 scale); the overlay fades 240ms in / 200ms out. Centring lives in the keyframes and `.app-modal-center`, never in a translate utility. Its only control is the Minimize2 collapse button; the close X and the rail stay where they were. Body and footer widen to 24px side padding.
- **Body:** 16px padding (8px bottom); the agent line (18px initial tile + 13px/500 name, 12px below); then the property grid.
- **Properties:** 84px muted 12px label column, 12px gap, 13px ink values, 28px rows, truncating; `mono` values at 12px mono. Long values (reasons, next actions, capability badges) release the height and wrap. Values are never muted.
- **Segmented tabs:** 28px hairline frame on paper, 2px inset, 2px gap, 24px segments 4px-rounded, 12px muted labels, hover tint; active segment = pressed tint, 500, ink; press scales to 0.97; content starts 16px below.
- **Sections (`RailSection`):** 20px apart, 12px/500 ink head with an optional muted tabular count right-aligned, 2px below the head.
- **Steps:** hairline-separated 32px rows: a 14px muted lucide icon, a 12px mono muted index right-aligned in 16px, a truncating 13px ink title with a muted detail after " · ", and a 24px ghost code toggle that reveals raw events in a paper-muted 6px `pre` at 12px mono.
- **Footer:** 12px/16px padding, top hairline, 8px gap: outline buttons left, the link button pushed right.

### Resize handle and layout prompt (signature)
- **ResizeHandle:** `role="separator"`, `aria-orientation="vertical"`, `tabIndex 0`; a 6px-wide full-height hit area centred on the panel hairline (3px past the edge), `cursor: col-resize`; inside it a 1px line, transparent at rest, indigo on hover / focus / drag, 150ms settle. Arrow keys resize 8px, shift-arrow 32px; a drag of less than 2px is ignored.
- **LayoutPrompt:** `role="status"`, portalled to `body`, fixed 12px from the top, horizontally centred; paper, hairline, 8px radius, floating shadow, `pop-in`; 6px vertical padding, 12px left, 6px right; 14px ink sentence, then three `sm` buttons 8px apart: primary 保存, outline 临时, ghost 恢复. It appears only while a width is unsaved and closes on any choice.

### Status icon (signature)
`StatusIcon`: a 14px hand-drawn SVG, `currentColor` from the six `status-*` tokens, 1.5px strokes, round caps. Queued dashed ring; running three-quarter arc on the `status-track` ring, spinning 1.8s linear (paused under reduced motion); completed and failed are filled discs with a paper check or x; cancelled a ring with a slash; interrupted a ring with a dash. Decorative unless given a `title`.

## Motion

Short, springy, rare. Every transition is non-linear; nothing is `linear` or default `ease` except the running icon's rotation. Every button-like control (`button`, `[role=button]`, `[role=tab]`) gets one shared transition from the stylesheet: colour, background, border and shadow at 150ms settle, transform at 120ms spring. Every keyframe is declared once in `src/style.css` and exposed as an `--animate-*` utility; components never write their own.

- **spring** (`--ease-spring`, `cubic-bezier(0.34, 1.56, 0.64, 1)`): every entrance and toggle.
- **settle** (`--ease-settle`, `cubic-bezier(0.22, 1, 0.36, 1)`): every exit, hover tint, colour change, row selection, field focus, the RowActions reveal.
- **page-in** (320ms spring; opacity 0 → 1, translateY 10px → 0, scale 0.995 → 1): `PageTransition` wraps the main column and is re-keyed by the view name, so every route change replays the same rise-and-fade once. There is no exit animation.
- **rail-in** (260ms spring, from 16px right): the detail rail entering once per selection.
- **modal-in** (420ms spring; from translate(−50% + 34vw, −50%) scale 0.35, opaque by 40%) / **modal-out** (260ms settle; to the same offset at scale 0.45): the rail's expanded modal flying from and back to the rail's side. **overlay-in** (240ms settle) / **overlay-out** (200ms settle): the 30% inverse overlay under it.
- **pop-in** (200ms spring, from scale 0.96): menus, tooltips, dialogs, the layout prompt, transient inline confirmations.
- **press** (`scale(0.97)`, spring, 120ms): buttons, action icons, segmented tabs and theme segments on active.
- **width spring-back** (`transition: width 420ms spring`): a panel returning to its default width after 恢复; the transition exists only for those 420ms so drags stay direct.
- **reveal** (opacity, 150ms settle): `RowActions` on row hover / focus-within.
- **chevron** (200ms spring): the ledger group chevron rotating to −90°.
- **running** (`spin 1.8s linear infinite`, `--animate-spin-slow`): the running status arc only.
- **fade-out** (`2s ease-out`, holds 70% then fades): transient confirmations.
- `prefers-reduced-motion: reduce` collapses every animation and transition to 0.01ms and removes the press scale and the running spin; the page, rail and modal then simply appear.

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

### Don't:
- **Don't** add a description or subtitle under a page title, a keyboard-hint line, a version string, or any helper copy that is not content or a control. The one exception, approved by the user, is the `⌘K` kbd inside the search field. The English page name beside the title is a name, not a description.
- **Don't** render the same action twice (a header button and a table-footer button, a sidebar utility row and a nav item, a topbar count and a footer count, a settings tab strip in two places).
- **Don't** use cards, stat tiles, nested bordered boxes, tinted message boxes, or coloured left borders thicker than the 2px selection marker.
- **Don't** wrap a category (role, type, source) in a chip, or put more than one `Badge` on a row.
- **Don't** add padding between the topbar and a ledger's column header, or a margin above the first section of a page.
- **Don't** animate the rail when it expands; only the modal moves, and the modal's only control is collapse.
- **Don't** use 700 weight, a third grey, an 11px functional size, or arbitrary pixel sizes outside the scale.
- **Don't** load Google Fonts for Latin or CJK; Noto Sans SC stays a last-resort fallback only.
- **Don't** put colour in text to signal state; tint a background or draw an icon instead.
- **Don't** use linear easing for anything but the running spinner, or declare a keyframe outside `src/style.css`.
- **Don't** render the compatibility strings (`description`, `audit.footer.shownCount`, login `noAccountHint`).
- **Don't** reach past the semantic tokens (`fg-*`, `surface-*`, `line-*`, `accent`, `status-*`, `app-*`) to the raw palette; lint will fail the build.
