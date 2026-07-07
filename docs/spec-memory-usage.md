# Spec & Memory usage guide

> Audience: product / ops / end users who maintain spec and memory from the
> web admin.
> For internal architecture and extensibility, see
> [spec-memory-module.md](./spec-memory-module.md) and
> [spec-memory-dev.md](./spec-memory-dev.md).

Spec and Memory are the centralized mechanism that persists "project
conventions / user preferences / long-lived background" and auto-injects
them into every agent conversation. Maintain them once and every sandbox
(claude code / opencode / codex) reads them automatically; when the agent
discovers new stable information inside the sandbox, it can write it back.

---

## 1. UI entry points

Admin nav → **Knowledge assets**:

```text
?admin=specs    # engineering spec (workspace-scoped, follows the project)
?admin=memory   # memory (user / project tabs)
```

Confirm you have selected a workspace before opening these pages; the
project tab under Memory additionally requires selecting a project.

---

## 2. Spec (engineering conventions)

**Scope:** workspace. Shared by every project / sandbox in the same
workspace.

**Typical content:**
- The project's tech stack, mandatory naming rules, directory conventions.
- Banned libraries / functions (and why).
- Code style (line width, comment language, commit-message format).
- The team's review checklist.

**Fields:**
| Field | Required | Description |
|------|------|------|
| Title | Yes | One-line summary |
| Body | Yes | Full rule. Markdown paragraphs allowed; the agent injection treats it as plain text |
| Tags | No | Comma-separated. MVP does not filter injection by tag but tags help UI search |

**Source markers (read-only):**
- `manual` — hand-created/edited in the UI.
- `agent` — written back by an agent inside a sandbox via `parsar spec add`.
- `import` — batch-imported from pasted text.

### 2.1 Manual creation

Top-right of the page → **New fragment** → fill in Title + Body + Tags
(optional) → save. Effective immediately; the next sandbox that starts
will pick it up during SessionStart.

### 2.2 Import from text

Top-right of the page → **Import** → paste markdown → **Preview**.

The backend splits by H2/H3: each `##` or `###` heading becomes a
fragment where heading=fragment.title and the following body is
fragment.body. The preview shows each fragment's title / body,
**read-only** (if the split is wrong, revert to fix the source and
re-preview). Confirm → **Import**; all fragments land in one shot with
`source=import`.

Partial failure: if the server crashes mid-write, it returns an error;
fragments already saved remain (visible in the list); the rest need to be
re-organized and pasted again.

### 2.3 Edit / delete

Click a row → edit dialog opens. Delete requires double confirmation and
writes audit.

---

## 3. Memory

**Scope:** two kinds, toggled via a tab.

| Scope | Follows | Typical use |
|-------|------|----------|
| **user** | Current account (cross-workspace) | Personal preferences, long-term role info, things you would rather not repeat |
| **project** | Selected project | Current project background, decision rationale, milestones |

**Four memory types:**

| Type | Use | Suggested Why |
|------|------|-------------|
| **user** | User role, preferences, long-term goals | Usually blank |
| **feedback** | Explicit corrections by the user / confirmed non-obvious decisions | **Strongly recommended** (so no one forgets why later) |
| **project** | Project background, milestones, decision rationale | **Strongly recommended** |
| **reference** | Pointers to external dashboards / docs / Slack channels | Usually blank |

**Fields:**
| Field | Required | Description |
|------|------|------|
| Type | Yes | Pick one of four. **Cannot be changed in edit mode** (if wrong, delete and recreate) |
| Title | No | Short title, aid memory |
| Body | Yes | Main content |
| Why | No | Recommended for feedback / project types; explain the rationale |
| Tags | No | Comma-separated |

### 3.1 Type filter

The pill buttons above the list filter by type (All / user / feedback /
project / reference). Client-side only; does not affect the server fetch.

### 3.2 Audit (project memory only)

Every project memory row has an **Audit** button on the right → opens a
timeline of events for that memory (who, when, did what; actor is user
or agent).

**No Audit entry on user memory** — the MVP audit-read endpoint only
supports project scope; user-scope events cannot be queried yet. Use the
global Audit page (`?admin=audit`) to see them.

### 3.3 Agent write-back

Inside a sandbox, the agent auto-calls `parsar memory add` at appropriate
moments (guided by the memory-write-guide injected into the system
prompt). Rows written by the agent show `agent` in the Source column, and
the audit timeline shows `actor_type=agent`.

---

## 4. Auto-injection

| Timing | Claude Code | OpenCode | Codex |
|------|-------------|----------|-------|
| Sandbox start (SessionStart) | Hook injects full spec + memory | Plugin injects full spec + memory | `AGENTS.md` generated on start |
| Before each prompt (per-turn) | Hook injects incremental memory | Plugin injects incremental memory | **Not supported** |
| Agent-written memory | Picked up by the same-turn hook | Picked up by the same-turn plugin | **Effective only on the next session** |

**Codex known limitation:** no per-turn hook; memory written back by the
agent inside a sandbox needs a session restart to appear in the prompt.
This is a platform limitation, not a bug.

**Injection size:** MVP injects everything full each time, no truncation.
If your memory grows to hundreds of items and prompt size clearly bloats,
delete stale items first (phase 2 will apply tag / time-window smart
filtering).

---

## 5. The `parsar` CLI inside the sandbox

When the sandbox starts, `/usr/local/bin/parsar` is preinstalled; the
token is injected via the `PARSAR_RUNNER_TOKEN` env var and no login is
needed.

Common commands:

```sh
# spec
parsar spec list
parsar spec add --title "Code style" --body "Line width 100, 4-space indent"
parsar spec rm <id>

# memory
parsar memory list --scope user
parsar memory list --scope project --type feedback
parsar memory add --type feedback --body "..." --why "..."
parsar memory rm <id>

# Debug: re-pull the current injection snapshot
parsar sync
```

Humans rarely need to call the CLI directly — the UI covers every use
case. The CLI is primarily for agents to call inside the sandbox and for
occasional ops triage.

---

## 6. What to write / what not to write

**Write:**
- Facts, preferences, and conventions that remain stable across conversations.
- Corrections the user explicitly made (especially the non-obvious ones).
- Key project decision rationale (so you are not asked "why did we do this?" again).

**Do not write:**
- Anything inferable from the code / git history (the agent reads them).
- Bug-fix step-by-steps (the code is the answer).
- One-off task context (throwaway chat).

If unsure, do not write — missing something is easier to fix than storing
something wrong.

---

## 7. FAQ

**Q: I changed the spec, why does the agent still cite the old one?**
A: The current sandbox session was already open; new spec takes effect on
the next SessionStart (the per-turn delta only carries memory, not spec).
Reopen a sandbox.

**Q: Can I recover an accidental deletion?**
A: The DB uses soft-delete, but the UI does not expose a "deleted list".
If you deleted something by mistake, contact the platform maintainer to
set `deleted_at=NULL` directly in the DB.

**Q: Can different workspaces share a spec?**
A: No. Spec is tightly bound to a workspace. Company-wide rules must be
maintained separately in each workspace for now (phase 2 will add
team / org scope).

**Q: Do user memories disappear when I switch workspaces?**
A: No. User memory is bound to the account itself and is visible across
workspaces.

**Q: If I delete a row where `source=agent`, will the agent write it
back?**
A: Not automatically — the agent writes memory when the user reveals new
information; deleted content only reappears if the user brings it up
again.
