# Parsar development protocol

This file is the agent-friendly shortcut. The canonical contributor
guide — including the hard rules, worktree workflow, architecture
baseline, web UI rules, and the required `make check` — lives in
[`CONTRIBUTING.md`](./CONTRIBUTING.md).

## Architecture documentation

For any substantial implementation, refactor, runtime/process change,
schema/API change, or cross-package behavior change:

- Read [`CONTRIBUTING.md`](./CONTRIBUTING.md) before touching code.
- Update [`CONTRIBUTING.md`](./CONTRIBUTING.md) in the same branch when the
  change creates, removes, or clarifies an architecture rule, ownership
  boundary, workflow, required check, or generated artifact contract.
- Do not let code establish a new convention that is absent from the
  contributor guide.
- Keep this file as a short agent-facing index. Put canonical long-form rules
  in [`CONTRIBUTING.md`](./CONTRIBUTING.md), and delete duplicated prose here
  instead of maintaining two copies.

## Language

Except for user-facing internationalized bilingual copy, comments and
documentation must be written in English.

## Code quality

Full rules: [`CONTRIBUTING.md#code-quality--architecture`](./CONTRIBUTING.md#code-quality--architecture).
Headlines:

- Split before you grow: Go files over ~500 lines / React components over
  ~400 lines are a signal to extract, not append to.
- Grep before you write: reuse an existing formatter / parser / error
  mapper instead of adding a near-duplicate one.
- One error-response helper per API surface — don't add another
  sentinel→status switch.
- Shared frontend logic (formatting, labels) lives once in
  `apps/web/src/lib/`, never copy-pasted per page.
- `server/internal/dev/routes.go`, `server/internal/store/store.go`, and
  `apps/web/src/pages/admin/AgentsPage.tsx` are known oversized files —
  do not add more weight to them; split out the piece you're touching.

## Generated contracts

Full rules: [`CONTRIBUTING.md#required-checks`](./CONTRIBUTING.md#required-checks).
Headlines:

- Handler/API changes require swaggo annotations, `make openapi`, and the
  regenerated `docs/openapi/openapi.yaml`.
- DB query changes require `make sqlc-generate` and regenerated sqlc files.
- Always run `make check` before reporting completion.
