# Environment Files.list

The complete protocol target remains the SDK pinned in [upstream.json](upstream.json).
This is a partial implementation of its public `GET /agents/environments/{id}/files`.
Uploads, Artifacts and other Files operations are outside this change.

## Pinned contract

The [Files resource](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/resources/beta/agents/environments/files.py)
and [list parameters](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/beta/agents/environments/file_list_params.py)
specify optional absolute-directory filtering, limit 1–100, case-sensitive
path-component ordering (default descending), and an opaque `page` token with
unchanged path/order/limit across pages. Limit and path are nullable SDK inputs;
order and page are not nullable when supplied.

Each [EnvironmentFile](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/beta/agents/environments/environment_file.py)
has `environment_id`, `object: agent.environment.file`, absolute `path`, and integer
`size_bytes`. The pinned [TokenPage](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/pagination.py)
requires `data`; `has_more` and `next` are optional and nullable. This implementation
returns `data` and `next` (null on the final page), with no additional page fields.

## Current scope and local policies

- Read direct regular files in one authorized self-hosted workspace directory.
  Omitted path selects the workspace root. Do not recurse or follow symlinks;
  directory, symlink and other non-regular entries are omitted.
- Omitted limit uses 20. Query keys may occur once; empty values, unknown keys and
  malformed query encoding are rejected. The pinned SDK's
  [query serializer](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/_qs.py)
  omits scalar `None` values, so nullable limit/path follow omission behavior.
  Literal `null` and empty scalar query values are not accepted.
- Require an absolute UTF-8 POSIX directory of at most 4096 bytes within the
  workspace. Reject `..` components before normalization, backslash, NUL, CR and LF.
  Normalize redundant separators, `.` and trailing separators before binding a
  cursor or passing the workspace-relative directory to execution.
- Authorize through the existing tenant-scoped Environment lookup before inspecting
  directory paths, cursors or runtime availability. Existing project-shared reads
  remain permitted. The reader receives that exact Environment and rechecks its
  execution ownership; the API never selects a daemon or a local filesystem path.
- Accept only a complete validated native directory, bounded by the shared
  1024-entry limit. Truncation, unknown kinds, missing sizes, duplicate or unsafe
  names, and uncertain output return safe 503 without `data` or `next`. The bound
  applies before filtering non-regular entries, sorting or public pagination.
- Cursors are bounded base64url tokens tied to tenant, Environment, canonical
  directory, effective order/limit, and the full sorted regular-file path/size
  result. Every page rereads the directory. Changed files or parameters invalidate
  continuation with safe 400. No cache, durable cursor registry or snapshot is
  promised; unchanged path/size metadata does not prove unchanged contents.
- Reader errors reuse the existing safe error mapping: not found 404, invalid input
  400 and unavailable execution 503. Native error text never enters the response.
  Listing does not create a Turn, prepare execution or change connection status.

Default limit, omitted-path scope, recursion, non-regular entries, exact invalid or
missing-path errors and cursor invalidation behavior are
local policies or remaining gaps, not verified hosted semantics. The pinned source
does not establish them. Do not interpret the bounded direct-file implementation
as complete Files.list compatibility.

## Acceptance boundary

API tests cover raw response fields, complete-result validation, filters, sorting,
pagination, local cursor policies, authorization order and safe errors. The separate
official-client fixture exercises flat directories generated through a real model,
raw HTTP and pinned SDK pagination, sizes, and two-tenant isolation. It does not
establish unspecified recursive, symlink or snapshot behavior. Runtime availability
and each engine's isolated placement require their own native and service checks.
