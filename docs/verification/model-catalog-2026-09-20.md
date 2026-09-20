# Workspace model catalog acceptance — 2026-09-20

Scope: workspace Providers, write-only API keys, the existing model resource,
Agent catalog selection, and the minimal write-only Core Session execution input.
Parsar owns catalog CRUD and authorization; Core owns execution. No Provider CRUD,
model probes, product roles or legacy runtime were added to Core. The branch remains
unmerged; this scope has not replaced the user-facing mx deployment.

## Verification

- Product tests cover workspace isolation, member/viewer read-only access, owner
  writes, immutable identifiers, exact Agent model resolution, unsupported Harness
  rejection, safe audit records, encrypted key storage, frozen creation retry, key
  rotation/deletion and restart reads.
- Core tests cover strict HTTP input, incompatible configuration rejection,
  write-only responses, encrypted tenant/Session-bound storage, conflicting
  idempotency keys, atomic rollback without an encryption key and fail-closed
  dispatch without operator fallback. Connector tests verify credentials are added
  after ordinary product request persistence and product model IDs stay out of the
  protocol Agent.
- OpenAPI and sqlc artifacts were regenerated. Full `make check` passed on an
  isolated Linux source snapshot using Go 1.25.13, Node 22.22.0 and pnpm 10.30.3.
  A direct macOS multi-package database run hit existing shared-fixture audit
  interference/deadlock; focused checks and the prescribed Linux gate passed.
- Standalone web lint reports pre-existing errors in untouched code in `plugin-slots.ts`,
  `AgentsPage.tsx`, `ConversationsPage.tsx` and `capabilities/index.tsx`. No catalog
  files appear in its findings; the required `make check` gate passed.
- Browser checks exercised Provider/model creation, grouped navigation, Agent model
  selection and save/read. The final main list uses the Runs ledger: larger clickable
  Provider headers, smaller indented model rows without click/focus/hover behavior.
  Provider settings open by mouse or keyboard. Model clicks leave selection unchanged.

## Real model acceptance

An isolated Core service and database on mx used the existing qualified hosted
Codex, Claude Code and MiniMax Code runtime images. Its operator model-options file
was omitted. Each Session supplied its own encrypted execution configuration and
called the real MiniMax M3 provider. All three profiles passed:

1. Exact-model execution and idempotent Session creation.
2. Workspace file creation and public Files listing.
3. Core restart followed by conversation-memory and workspace-file continuation.

A separate local product instance connected to this test Core. It created a Provider,
a model and an Agent through the product API, then ran MiniMax Code through the
normal conversation flow. A deliberately stale submitted model string resolved to
the catalog identifier. After Provider key rotation and deletion, the existing
conversation continued with its original model and file; a fresh conversation
failed explicitly without falling back to another Provider or operator key.

Private scripts, logs and evidence are under `~/.parsar/tests/catalog-live-20260920/`
on mx and `~/.parsar/tests/agent-defaults/catalog-live/` locally. No keys are included
in source or this report. Real-model checks establish these accepted profiles,
not compatibility with every model implementing the same wire protocol.

## Independent review

A fresh GPT-6 Astra reviewer with independent context reviewed the entire branch
and working tree against `6c801316858b120d09668b144861959ed1b030df`. It received only
requirements, acceptance criteria, scope boundaries, repository rules and the
comparison baseline. It found no evidenced actionable in-scope defects and
independently passed focused contract, connector, Core API, execution and server
tests with Go 1.25.13. It did not rerun live execution or the full check gate.
