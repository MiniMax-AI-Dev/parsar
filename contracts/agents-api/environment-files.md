# Environment files

The complete protocol target remains the SDK pinned in [upstream.json](upstream.json).
The public GET and POST `/agents/environments/{id}/files` have partial coverage.
Inline and source-file (`file_id`) creation target a preconfigured V1 local
Environment. [Source Files](source-files.md) have their own project-owned lifecycle.
Artifacts and public hosted Session provisioning remain unimplemented.

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

- Read direct regular files in one authorized self-hosted or qualified local workspace
  directory. Local public paths are rooted at `/workspace`, independently of the
  physical path frozen into its dedicated Runtime.
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
  Listing does not create a Turn or admit model input. Idle reads use temporary
  read-only preparation; actual transport disconnect/reconnect events remain visible.

Default limit, omitted-path scope, recursion, non-regular entries, exact invalid or
missing-path errors and cursor invalidation behavior are
local policies or remaining gaps, not verified hosted semantics. The pinned source
does not establish them. Do not interpret the bounded direct-file implementation
as complete Files.list compatibility.

## Inline and source-file creation

The pinned create union requires `type: inline`, standard Base64 `data` and an
absolute destination `path` under `/workspace`, or `type: file_id`, `file_id` and
that path. Source IDs resolve only within the authenticated execution project;
filenames, URLs and filesystem paths cannot substitute for an ID. Both members
use the same destination writer. Required null/omitted fields, extra fields,
query parameters and invalid Base64 are rejected. Empty bytes are valid. Inline
paths must be canonical and cannot name the workspace root; the parent must exist.
The current destination limit is 50 MiB for either source, with bounded JSON and 64 KiB daemon
frames. These are local limits and policies, not verified upstream restrictions.

Creation uses the same tenant Environment lookup as listing. The Worker checks the
stored local profile, immutable exact device/Environment binding and live capability.
It never starts a model for upload or supplies a filesystem root from the request.
The deployment must qualify the protected sibling workspace/staging layout in the
[Codex profile](../../services/agents-api/deploy/codex/README.md). A capability or
path declaration alone does not establish isolation or public hosted admission.

Before sending any bytes, persist the mutation identity and request digest under
the Session lock. Pending input/execution and another unresolved upload exclude a
new mutation. The Runtime receives the complete body, verifies its digest and uses
the existing installer to replace the destination with a fresh mode-0600 inode.
Existing hard-link aliases retain their original contents. Uploads do not create
parent directories or preserve destination permissions; exact upstream overwrite
and metadata behavior remain unverified. Later independent tool writes can change
the installed file; the response does not promise a snapshot.

Only an exact committed/rejected receipt settles durable ownership. Caller detach,
connection loss, timeout or missing output cannot be treated as rejection. Unknown
writes remain pending across Core restart and block successor mutation without
replay; read-only recovery remains available. Automatic uncertain-write recovery
and placement replacement are outside this batch. Controlled failures preserve
the destination only when the installer proves rejection, and only against this
operation, not independent workspace writers. Temporary-file cleanup is best effort.

Successful creation returns only the four EnvironmentFile fields. Reuse the common
safe error mapper; current 400/409/413/503 policies and error timing are not evidence
of exact upstream parity. This referenced-source milestone cannot close the complete Files
resource or Environment lifecycle requirements.

Source resolution reads an immutable snapshot before destination admission. A
source deleted before that lookup is unavailable; an already-resolved copy may
finish. Deleting a source never deletes a copied workspace file. A larger general
Files upload can be downloaded but is rejected before Environment dispatch when
it exceeds the destination limit. Exact hosted delete/copy timing is unverified.

## Acceptance boundary

API tests cover raw response fields, complete-result validation, filters, sorting,
pagination, local cursor policies, authorization order and safe errors. The separate
official-client fixture exercises flat directories generated through a real model,
raw HTTP and pinned SDK pagination, sizes, and two-tenant isolation. It does not
establish unspecified recursive, symlink or snapshot behavior. Runtime availability
and each engine's isolated placement require their own native and service checks.

The opt-in `services/agents-api/tests/official_environment_files_create.py` reuses
the pinned SDK and raw HTTP listing assertions. Its stdin supplies the base URL,
preconfigured Environment ID, model-input text, and two private caller token
sources (`token_env` or `token_file`). The invoking native fixture supplies an
existing `uploads` directory and a staging symlink rejection probe, verifies exact
installed hashes, and has a real model consume source-copied text after source
deletion. Its source fixture also streams a 512 MiB upload/download and verifies
that it cannot bypass the smaller destination bound. This distinction
keeps private setup separate from public hosted creation acceptance. Mechanism tests
exercise detached/unknown outcomes and durable gates independently of model output.
