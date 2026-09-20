# Product Core integration acceptance — 2026-09-20

Status: **not fully accepted**. Product checks and live workflows have passed, but
`make check` has unresolved host/platform failures outside this change, and the
last narrow fix still needs the required fresh independent re-review. Do not mark
the task complete or merge based on this report alone.

Scope: baseline `d8ed9d42`, branch `codex/product-core-integration`. Parsar retains
its database, members, capability assets, Skills/MCP management, authorization and
business orchestration. All Agent execution uses public Core APIs. No Core feature
completion, full product redesign or mx redeployment is included.

## Validation

| Requirement | Evidence |
| --- | --- |
| Member configuration | `TestCoreAgentContract`, `TestCoreConfigReplacementClearsRemovedFields`, official full inline config/null preservation tests. Config omission preserves settings; supplied config replaces them, allowing deleted advanced fields to stay deleted. |
| New conversations and continuous input | Local product → authenticated Core on mx → real MiniMax-M3. Initial run `b70a7df2-e70e-4892-94e0-cbae7adfa04e` returned `PRODUCT_CORE_OK`; next run `763ba629-9818-4137-93a4-19b5f456913b` returned `PRODUCT_CORE_SECOND_OK` in the same conversation. |
| Conversation deletion | `TestDeletedConversationRetainsCoreCleanup` covers queued/running/failed/completed product states and verifies atomic cancellation, private recovery/event restoration, usage and settlement while public reads remain hidden. |
| Tool results | `TestFunctionOutputProjectionAndReplay` joins native function output by `call_id` and prevents duplicate projection; `TestIncompleteToolResultRetainsFailure` preserves unsuccessful tool outcomes. |
| Cancellation | Live run `4a572994-1302-4247-9ac0-a168ef830d37` became cancelled; the Core binding was submitted and settled. `TestCancellationWaitsForOwningObserver` verifies lane ownership. Pending environment preparation cannot be cancelled immediately through the baseline public Core API; product intent awaits admissible cancellation and settlement. |
| Restart recovery | Product was stopped after run `803aed92-75a5-4cfc-8d9a-df6c3ca0c55f` was submitted and unsettled, then restarted. The same run completed with `PRODUCT_CORE_RECOVERED`. `TestPersistenceInterruptionPreservesCoreExecution` injects PostgreSQL interruptions across eight persistence boundaries. Submission-loss/idempotency and output replay tests cover interruption boundaries; dispatcher interruption does not fail product work. `TestDisabledUnboundMemberFailsRun` verifies deterministic retirement before execution does terminate product work. |
| Failure and accounting | `TestRecoveredFailedDoneCannotCompleteRun`, `TestTerminalUsageAndFailedRecovery`, `TestCoreRetiredAgentRecoveryAndTerminalUsage` cover recovered failure, measured cancelled/failed usage, deduplication and settlement after member retirement. `TestTerminalAdmissionRejectionSettles` covers terminal Core 409 rejection including previously attempted/cancelled input; uncertain receipts remain fenced and recoverable. |
| Workspace isolation | Core client tests reject absent workspace credentials and shared projects. Template route tests verify authorization before upstream access and tenant selection. Core's independent tests exercise project isolation. |
| Assets and unavailable execution | Existing database-backed import/upload/config/OAuth tests remain enabled. The capability library and member pages say asset management is available but runtime activation awaits Core. No old connector delivers assets to execution. |
| Legacy execution removal | Route retirement and `TestLegacyAgentsCannotAdmitNewExecution` cover old HTTP/native/runtime routes and rejection at web, IM, retry and scheduling admission. |
| Parsar UI | Original sidebar and page actions retained; no copied Agents/Environments/Sessions top navigation. Frontend typecheck, design lint and Vite build pass. |
| Generated contracts | `make openapi` and `make sqlc-generate` completed; generated files included and sqlc drift gate passed. |

A later live verification completed runs `26c43f44-ee81-40fd-b251-35ff7706de4d`
and `8df4f8a2-ef11-4c79-939b-cfb10b06c347` in the same conversation, with
`PRODUCT_CORE_FINAL_ONE` and `PRODUCT_CORE_FINAL_TWO`. Each settled with one usage
record. Unsupported-model run `25d8f31f-c48f-4079-b315-f993ad97a3ed` failed explicitly
and settled without a fabricated usage measurement.

Live deletion verification observed run `092ef122-216c-4c45-95a8-b2b52935a385`
running with submitted, unsettled Core work. Deleting its conversation transitioned
it to cancelled, the Core binding settled, and its public run endpoint returned 404.

Browser inspection verified the member list, member configuration and capability
library using the local product preview. Asset creation remains available; runtime
activation limitations appear on both member and capability pages.

Database tests used disposable PostgreSQL 16 databases. Product Store suite and
full `server/internal/dev` database suite passed. Independent Core/client tests,
installer tests, web checks and harness checks passed. Harness native binary
acceptance has one prerequisite-dependent skip; it is not counted as a pass.

## Unresolved required-check failures

On macOS arm64, with Go 1.25.13 and `GOTOOLCHAIN=local`:

```sh
make -k check
```

The `-k` run completes all independent required targets and reports failure overall.
These failures remain:

1. `apps/parsar-daemon/internal/agent/claudecode` and `claudesdk` Go tests exit with
   `signal: killed`. Reproduced from the original checkout as well as this worktree:

   ```sh
   go test ./apps/parsar-daemon/internal/agent/claudecode ./apps/parsar-daemon/internal/agent/claudesdk
   ```

2. `check-cli` reaches the Claude SDK runtime export check, then fails with
   `AssertionError [ERR_ASSERTION]: Exported runtime is unavailable`
   (`actual: 1`, `expected: 0`). Native Claude execution is unavailable on this host.
   No signing/security bypass was attempted.

3. `check-agents-executor` fails to compile its unchanged Rust executor on macOS:
   `export.rs` has `st_dev` i32/u64 mismatches and `workspace_path.rs` references
   Linux-only `OFlags::PATH`. Reproduce with:

   ```sh
   make check-agents-executor
   ```

The affected native adapters and executor have no diff from the comparison
baseline. They were not altered to force a green check. Re-run required checks on
a qualified host before acceptance. Live Core acceptance does not substitute for
the missing required-check results.

Sanitized command output is summarized here. Detailed local logs and disposable
live evidence remain under `~/.parsar/product-core-*.log` and
`~/.parsar/product-core-proof/`; credentials are excluded from this report and PR.

## Independent review status

Fresh independent reviewers inspected the complete tracked/untracked diff against
`d8ed9d42`, with only requirements, acceptance criteria, scope, repository location
and baseline supplied. Evidence-based findings were fixed and relevant checks rerun.
The latest completed review identified a disabled member leaving an unbound run
pending. The fix now distinguishes deterministic `ErrInvalidAgent` from operational
database interruption, and `TestDisabledUnboundMemberFailsRun` passes alongside the
observation-interruption and recovered-failure tests.

A fresh review of that final fix was requested, but the collaboration tool returned
`agent thread limit reached`. Therefore **the final diff has not completed the
required fresh independent re-review**. Do not treat earlier reviews as approval of
this last change. Re-review the entire PR from an independent context before acceptance.

The final required-check invocation was `make -k check` with Go 1.25.13; it retains
the documented platform blockers. Full output is in the local
`~/.parsar/product-core-check-pr.log`. No blocked check or missing review is waived.
