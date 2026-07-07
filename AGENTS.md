# Parsar development protocol

This file is the agent-friendly shortcut. The canonical contributor
guide — including the hard rules, worktree workflow, architecture
baseline, web UI rules, and the required `make check` — lives in
[`CONTRIBUTING.md`](./CONTRIBUTING.md). Read it before any
implementation, refactor, or schema / API change.

## HTTP contract (swaggo generation)

`docs/openapi/openapi.yaml` is a **build artifact**, not a hand-edited
file. It is regenerated from swaggo annotations on Go handlers by
`make openapi`. The CI drift check (`.github/workflows/openapi.yml`)
runs the same command in a clean checkout and fails the PR if the
committed file diverges from the annotations.

### When you add or change an endpoint

1. Write / modify the handler.
2. Add or update the swaggo annotation block **directly above the
   `func` line**, with no blank line between:

   ```go
   // createAgent creates a new agent in a workspace. Owner/admin only.
   //
   //	@Summary		Create an agent in a workspace
   //	@Description	Longer prose the operator sees under the summary.
   //	@Tags			agents
   //	@ID				createDevAgent
   //	@Accept			json
   //	@Produce		json
   //	@Param			workspaceID	path	string				true	"Workspace UUID"
   //	@Param			body		body	createAgentBody		true	"Agent create payload"
   //	@Success		201 {object} map[string]interface{} "Created agent"
   //	@Failure		400 {object} map[string]string "workspace_id must be a valid uuid"
   //	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
   //	@Router			/api/v1/workspaces/{workspaceID}/agents [post]
   func createAgent(runtimeStore RuntimeStore, ...) http.HandlerFunc {
   ```

3. Run `make openapi`. The recipe auto-installs
   `github.com/swaggo/swag/cmd/swag@v1.16.4` on first use, regenerates
   `docs/openapi/openapi.yaml`, and prints the new path count.
4. Commit the handler edit **and** the regenerated YAML in the same
   commit.
5. `make check` must still pass.

### Rules the CI enforces (so save yourself a rebase)

- **Tab-separated fields** in the annotation. `//` + Tab + directive +
  Tab-Tab + value. Look at
  `server/internal/api/health.go:livenessHandler` or
  `server/internal/dev/routes.go:createAgent` for the canonical format.
- **`@Router` method lowercase**: `[get] [post] [patch] [delete] [put]`.
- **`@Router` path is the full path**, including `/api/v1/…` or `/dev/…`
  — swag does not know about the outer `r.Route("/api/v1", …)` group.
- **`@Tags` is a single kebab-case tag** from the existing set (agents,
  workspaces, conversations, agent-runs, scheduled-tasks, models,
  secrets, capabilities, auth, me, connections, bootstrap, health,
  runtimes, gateway, dev, feishu, workspace-members, uploads,
  sandboxes, memories, meta, internal, agent-daemon). Introducing a new
  tag is fine — just be deliberate.
- **`@ID` is a globally-unique lowerCamelCase string**. Dev-mode
  handlers prefix with `dev` (`createDevAgent`, `listDevWorkspaceModels`).
- **Every path/query/header parameter is declared**. Missing parameters
  cause swag to emit incomplete specs, which the drift check will
  eventually surface but is faster to catch locally.
- **Handlers must be named functions.** swaggo cannot attach annotations
  to `r.Post("/x", func(w, r) { … })` — the anonymous closure has no
  identifier. Extract to a factory like
  `func fooHandler(deps) http.HandlerFunc { return func(w, r) { … } }`
  and annotate the factory.
- **Do not hand-edit `docs/openapi/openapi.yaml`.** The next
  `make openapi` will overwrite your edits and the CI drift check will
  reject the PR anyway.

### When you deliberately want to skip a handler

Internal helpers, middleware factories, and per-conversation dispatch
closures that are not user-visible routes should have **no** `@Router`
annotation. They are invisible to swag and will not appear in the
spec. Do not add a placeholder `@Router` just to silence a hunch — that
publishes a phantom endpoint.

### Browsing the spec

Once the server is running (`make dev-server-up`):

- `GET /api/v1/openapi.yaml` — raw spec, use for
  `openapi-typescript` / `oapi-codegen` / `curl` piping.
- `GET /docs` — Swagger UI viewer, groups endpoints by tag.

The Go server re-reads the YAML on every request, so a local
`make openapi` regeneration is visible without restarting the server.
