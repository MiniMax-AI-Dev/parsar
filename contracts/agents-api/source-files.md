# Referenced source Files

Environment `file_id` refers to a general Files API upload, not a local path or
an external provider's file. The contract uses the same SDK/source pin as
[upstream.json](upstream.json): [Files resource](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/resources/files.py),
[create parameters](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/file_create_params.py)
and [FileObject](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/file_object.py).

## Implemented source workflow

| Operation | Current behavior |
| --- | --- |
| `POST /files` | Multipart `file` and `purpose=user_data`; either part order; immutable bytes and metadata commit after full validation |
| `GET /files` | Project-scoped metadata listing with `after`, `limit`, `order` and `purpose`; deterministic creation-time/ID keysets |
| `GET /files/{id}` | Project-owned metadata, without reading the body |
| `GET /files/{id}/content` | Immutable binary stream with declared length; incomplete transfer aborts rather than returning a JSON error as file content |
| `DELETE /files/{id}` | Atomic metadata removal and body unlink; `id`, `object: file`, `deleted: true` |

The configured SDK base URL includes `/v1`. These routes reuse bearer and optional
organization/project header validation but do not require `OpenAI-Beta`. Existing
Agents/Vault routes retain their Beta check. User and service-account keys in the
same configured project share the source resource. Every read/delete/copy lookup
uses that project partition; missing and foreign IDs return the same safe 404.

Listing defaults to 10,000 resources and rejects limits outside the pinned
1–10,000 range. Omitted order uses descending creation order; `asc` and `desc`
use the stored timestamp plus ID as a deterministic keyset. The response includes
`object`, `data`, `first_id`, `last_id` and `has_more`; empty pages use null IDs.
The cursor must name a currently visible File in the same project. The optional
purpose filter is exact; unsupported purposes return an empty page because this
profile stores only `user_data`. Exact hosted default order, invalid/deleted cursor
errors and pagination during concurrent mutation remain unverified local policies.

Metadata includes `id`, `object: file`, `bytes`, Unix-second `created_at`,
`filename`, `purpose: user_data`, deprecated `status: processed`, and nullable
`expires_at`/`status_details`. Here processed means stored bytes are available,
not parsed, indexed or scanned. Filename is metadata only and never a filesystem
path. The current service bounds it to 1–1024 UTF-8 bytes without NUL.

The pinned general upload documentation states 512 MB. This implementation uses
512 MiB with a separate 64 KiB multipart-envelope allowance; exact hosted size-unit
and overhead/error parity are unverified. Streams use bounded chunks and a
five-minute transfer deadline. Complete multipart validation rejects missing,
duplicate, unknown or unsupported parts, invalid purpose and incomplete bodies.
Empty file bytes are valid. No partially validated upload is published.

## Persistence and deletion

The execution database owns source metadata and PostgreSQL large objects through
the already-pinned pgx driver. Upload and deletion are single transactions; a
rollback does not orphan a body or publish partial metadata. Object OIDs are private
and cannot be supplied through the API. No product tables, temporary local upload
directory, external object-storage service or model invocation are required.
Backups must include PostgreSQL large objects. Physical deletion from the live
database does not erase historical WAL/backups; database maintenance governs
reclamation. Downgrade refuses to drop a populated source table.

An admitted content read uses an immutable database snapshot and may finish after
deletion. Later source lookups reject. Environment copy resolves up to its existing
50 MiB destination limit before invoking the same durable writer used by inline
uploads. Source deletion does not undo an admitted or completed workspace copy.
These concurrency/error choices are local policies, not verified hosted parity.
Ambiguous upload commits are not automatically retried; clients may need to retain
their source request evidence. Destination unknown-write handling remains unchanged.

## Remaining scope and verification

Other upload purposes, `expires_after`, resumable Uploads, quotas,
rate-limit parity, Artifacts and full status/error/header compatibility remain
unimplemented or unverified. Unsupported purposes/expiration are rejected. The
pinned request accepts `evals` while FileObject's purpose union omits it; this
discrepancy is recorded, not resolved by inventing a new contract. Current online
Agents limits require separate version qualification before changing the pinned
baseline. This workflow does not enable public hosted Session provisioning.

Store tests use actual PostgreSQL for rollback, project isolation, independent
reads and concurrent deletion. The opt-in 512 MiB test exercises streaming storage.
API tests cover multipart ordering/validation and response/authentication behavior.
The fixed SDK and raw HTTP list regression covers default and bounded pages,
automatic continuation, purpose filtering, same-project sharing, foreign-project
isolation, restart and exact list envelopes against real PostgreSQL.
`official_environment_files_create.py` includes the fixed SDK/raw HTTP source
workflow through `official_source_files.py`; its invoking native fixture must
verify copied hashes, retained copies after source deletion, absence of leaked
database objects and real-model consumption. Controlled tests or SDK parsing alone
do not establish that execution acceptance or complete protocol compatibility.
