# `internal/` — Go packages shared across subtrees

> Go's internal-package visibility rule: packages under `server/internal/...` can
> only be imported by code inside the `server/` subtree. When a package must be
> shared by **sibling subtrees** — `server/`, `apps/parsar-daemon/`,
> `apps/parsar/` — it has to live in the repo-root `internal/` for the Go
> compiler to accept the imports.

Current subpackages:

| Directory | Shared by | Contents |
|---|---|---|
| `agentdaemon/proto/` | `server/` + `apps/parsar-daemon/` | Wire schema between the server and the daemon binary (message format, version negotiation) |
| `obs/` | server + daemons / CLIs | Shared observability helpers (structured log fields, trace helpers) |
| `runtimecrypto/` | server + parsar-daemon | Runtime envelope crypto (sandbox side decrypts short-lived credentials issued by the server) |

---

## When to put code here

The only test: **does more than one sibling subtree import it?**

- Only used by `server/` → put it under `server/internal/...`
- Only used by `apps/parsar-daemon/` → put it under `apps/parsar-daemon/internal/...`
- Used by two or more subtrees → put it under repo-root `internal/`, which is this directory

---

## Implementation constraints

- Subpackages are plain Go packages and depend on no business-layer package (avoid import cycles).
- Keep the API surface small; expose only the types that genuinely need to cross subtrees.
- Unit tests live next to the subpackage. `make check` runs them.
- If a subpackage ends up used by only one subtree, move it back under that subtree's `internal/`.

---

## Extensibility

If new connector / gateway / capability / audit-sink implementations later need
to be shared across binaries, add them here under a purpose-named subdirectory
(e.g. `internal/connectors/<name>/`). Prefer the existing server-side extension
interfaces under `server/internal/...` first — only reach for this directory
when code genuinely needs to be shared across subtrees.
