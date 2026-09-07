---
version: 1
slug: "src-pages-admin-runspage-tsx"
primary_target: "src/pages/admin/RunsPage.tsx"
related_targets: ["src/components/layout/AdminLayout.tsx","src/style.css"]
---

# Surface brief: admin console shell + Runs page

Scope: the workspace admin console (`AdminLayout` shell) with Runs as the first surface; every other admin page inherits this world. Mode: Operate. Redesign of the whole visual world; product truth, routes, copy semantics and the token/lint constraints are preserved.

Audience and job: engineering-team developers and admins on a desktop browser, scanning agent runs, selecting one, reading its detail, acting (retry / cancel / open conversation). Bilingual zh/en; light and dark.

Constraints: every colour through semantic tokens in `src/style.css`; eight-tick type scale; system font stack (SF Pro / PingFang, Segoe UI / YaHei; Noto Sans SC last); Radix primitives; min-width 960.

User rules (binding): one control per semantic per screen (no duplicate buttons, nav entries, counts); no page description subtitles; strict type system (600 titles only; 500 names / active / buttons; 400 all else; muted grey only for labels and metadata, never values).

Prototype of record: `.impeccable/candidates/e-ledger-compact.html` (candidate 5).

## Direction contract

THESIS: The console is an issue ledger. Every run is one dense 36px row, status is a 14px icon, the keyboard works. It refuses the card-dashboard arrangement (sidebar + stat tiles + card grid) and the document-page arrangement.

OWN-WORLD: near-white ground with a panel one step off; hairline structure, 6px radii, ink plus one muted grey; indigo only for the selection marker, focus, and the one primary button a screen may have (user accepted this in the DESIGN.md round); status colour lives only in status icons; system UI face, mono for ids, paths, timestamps and numbers, never for enum words; sizes 20/14/13/12; weights 600/500/400 per the user rules. The ⌘K badge inside the search field is a user-approved exception to the no-keyboard-hint rule (it was in the approved prototype after the J/K footer hint was removed).

STORY: open Runs, scan grouped rows, select, read the rail, act.

FIRST VIEWPORT: 232px sidebar (Parsar / workspace text row, three nav groups, account row bottom-left with theme toggle); 48px topbar (title left; search and one 筛选 button right); sticky 28px column header; status-grouped 36px rows; footer with pagination (J/K and ⌘K shortcuts wired, never advertised); 384px rail: status header, title, property grid, segmented tabs, steps, action footer.

FORM: Issue Ledger, candidate 1 of the grounded list (the pick), chosen by the user over the roll's assignment; seed key 7e5172bb.

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance.
