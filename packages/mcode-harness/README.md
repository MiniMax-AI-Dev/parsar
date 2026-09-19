# MiniMax Code workspace bridge

This adapter companion keeps the published MiniMax Code CLI, ACP, model loop and
history. Its trusted MCP server exposes six original native tools inside the
upstream-vendored Linux sandbox. There is no CLI source patch or replacement loop.
Hosted public execution is not qualified by this package alone.

The harness process and ACP Session use a private control directory. Builtin file
tools are disabled. Only the adapter registers this bridge; callers cannot supply
its command, profile, working directory or environment. Workspace project files
are read through sandboxed tools rather than imported by the privileged harness.
Native diff/undo capture is not provided by this path. Common Files/Artifacts use
the bound public workspace independently of the native control directory.

`source.json` pins the native tool and sandbox source. The published CLI is a
separate dependency; both artifacts require qualification. On Linux x86_64:

```sh
MCODE_NATIVE_SOURCE=/absolute/upstream/checkout bash scripts/build-mcode-harness.sh
```

This standalone companion uses its own npm lock and is excluded from the root
pnpm workspace. The build archives the exact source revision, bundles its native tools and sandbox
and installs pinned MCP dependencies. It does not build the upstream CLI. Install
the artifact immutably at `/opt/mcode-harness`. The private profile supplies
`workspace`, `scratch`, `protectedDirs` and `network`. Only the
isolated worker receives the real workspace as its tool root. Missing or mismatched
profiles reject; there is no unsandboxed fallback.

The bridge owns each launcher until exit. MCP cancellation and transport shutdown
stop all owned workers before releasing the bridge. The outer Runtime owns the
native process group. Both boundaries require real Docker cancellation tests.

Native tool schemas are retained. Text and image results use standard MCP content;
video results reject explicitly. The published CLI may add task/skill utility tools;
qualification must inspect the actual inventory rather than assume exactly six.

See [workspace qualification](../../contracts/agents-api/mcode-workspace-v1.md) for
the required tests and stopping conditions. Synthetic isolation probes and native
model runs do not complete public Files/Artifacts or independent Core acceptance.

For the packaged Linux regression, provide an operator-owned private profile and
artifact directory, then run `native.test.mjs` inside the qualified Docker Runtime:

```sh
PARSAR_MCODE_NATIVE_PROFILE=/absolute/private-profile.json \
PARSAR_MCODE_NATIVE_ARTIFACT=/opt/mcode-harness \
node --test packages/mcode-harness/native.test.mjs
```

Run once for each supported network policy. It verifies writable native TMPDIR,
large Bash output retention and subsequent native Read. The ordinary repository
gate skips this case without those explicit inputs; it cannot replace Docker
isolation, cancellation or real-model acceptance.
