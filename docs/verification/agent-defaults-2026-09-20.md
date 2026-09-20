# Agent execution defaults acceptance — 2026-09-20

Status: ready for PR review. A fresh GPT-6 Astra reviewer with high reasoning and
independent context reviewed all tracked/untracked changes against the baseline,
found no grounded actionable issue, and independently passed focused Go tests and
`git diff --check`. The reviewer did not rerun the full gate or live acceptance.
The subsequent image packaging/documentation fix did not receive another blind
review, at the user's explicit direction.

Scope: baseline `6c801316858b120d09668b144861959ed1b030df`, branch
`feat/agent-execution-defaults`. Agent defaults select a model, existing Core
harness and environment template. Product conversations use Core's durable
Session binding; Core owns the actual execution engine and isolated environment.
This adds no native harness, legacy connector or product-side runtime manager.

## Checks

`make openapi`, `make sqlc-generate`, focused Go tests, web typecheck and production
web build passed. The complete `make check` passed on an isolated Linux source
snapshot on `zju_a100_2` (exit 0), including the final source changes. Go 1.25.13,
Node 22.22.0, pnpm 10.30.3 and Rust 1.95.0 were used. The existing optional MiniMax
packaged-native-tools fixture was skipped for its missing prerequisite; it is not
counted as real execution acceptance. Two macOS gate attempts failed in unchanged
adapter process tests (`signal: killed` and preparation timeouts); the Linux gate
ran without exclusions or test modifications.

Logs and wrapper: `~/.parsar/tests/agent-defaults-20260920/{check.sh,make-check.log,make-check.exit}`
on Linux. Local copy: `~/.parsar/tests/agent-defaults/make-check-linux.log`.

The initial PR image build exposed a missing `contracts/agents-api/v1` directory
in the product Docker build. The Dockerfile now copies that shared validation
dependency. A local full-image attempt was blocked by a registry 403 for the
unchanged Node base image; the Go builder target and GitHub image build validate
the packaging separately. Final CI results are attached to PR #713.

## Real execution on mx

Candidate product and Core binaries, the current web build and two Docker runtime
images ran in the isolated `agent-defaults-net` network with separate product/Core
databases. The task containers are `agent-defaults-product`, `agent-defaults-core`
and `agent-defaults-db`; ports 18089 and 18091 bind only to loopback. Existing mx
deployments were not modified. Provider credentials remained in private operator
files and were never stored in Agent configuration or this report.

Workspace: `a09f0bb7-5e59-4cd3-8888-31635023accf`.
Product Agent: `06b323b6-33ee-436b-bd9e-a7b8373e7e9a`.

| Acceptance | Evidence |
| --- | --- |
| Save defaults without starting execution | Product create/read round trip preserved model, harness and template. Saving the Agent and creating two empty conversations left Core Session and task runtime counts at zero. |
| Concurrent independent chats | Codex runs `7b8eec8a-8a6b-4fc5-b7b2-542dbd22e832` and `31b43a59-bfba-4095-bb50-b339a5580ae2` completed with `A_READY` / `B_ISOLATED` and `template-ready`. Different Core Sessions, Environment IDs, native histories and Docker allocations were observed. |
| Actual second harness | After editing the same Agent's default to `claude_sdk`, run `cdf3c828-9c1f-48ef-afd3-8fe3b0799cce` completed with `C_ISOLATED` and `template-ready`. Native Bash observations created/read `only-c.txt`; the earlier chats' files were absent. |
| Restart and reuse | After restarting both task product and Core containers, runs `d3177f20-4d64-4436-8c44-d10bde562361`, `fd5eafc7-b988-4abe-bf0f-632662989f07` and `9c0a8777-0b16-4f6d-8710-234255eee913` completed. A returned `orange-moon ALPHA`, B returned `BETA`, and C returned `violet-sun GAMMA`. Each retained its Session, Environment and effective Agent snapshot. |
| Cancellation | Native foreground `sleep 120` command observations preceded cancellation. Codex run `8e1d40a3-aa6a-4bc2-913e-946e9baf7cf5` and Claude run `9c2bd25a-d094-4f76-80a9-f2e06ace654c` both settled as cancelled while retaining their original environments. |
| Model/template edits affect new chats | Changed the same Agent to `MiniMax-M2.5` and a second template. Run `15803110-10ed-4e95-a04a-39d4616c17c0` completed with `NEW_DEFAULTS`, `template-v2` and confirmation that A/B/C files were absent. The new Session reports the new model and Claude harness; all three earlier Session snapshots remained unchanged. |
| Concurrent creation and lost response retry | Two simultaneous hosted create requests and a third retry returned Session `943a61b3-f196-4280-a0e0-1b7328e19132` and Environment `ada6c4f9-dc81-46c0-b5fc-d02380195fbb`; the allocation ledger contained exactly one row for that environment. Changing the harness under the same idempotency key returned 409. |
| Explicit failures | Unknown harness, unavailable `mcode`, unsupported Claude verbosity and an inaccessible template were rejected before allocation. Saved-Agent inheritance, null clearing and replay after editing the saved Agent passed through public HTTP APIs. |
| UI | Browser inspection confirmed accessible model, harness and template controls, the Core template list and the delayed-allocation explanation. Agent Chat opened an empty composer with the Agent selected; it did not create a conversation or runtime. |

After cancellation and the later defaults edit, all original chats continued again:
runs `99d0cf10-0b05-4a47-bd83-71ad4cc8023e`,
`bed5375c-7da9-45ad-80cc-b9b0b37a9936` and
`fbac76d6-a02a-424a-a93d-06568781d488` completed with unchanged Session,
Environment and effective Agent snapshots.

The first real attempts exposed stale test runtime artifacts: the old Codex image
lacked the current initializer dependency, and the old Claude JavaScript bundle
did not support the current tool-environment contract. The test images were updated
with the current daemon, initializer, Codex policy files and Claude adapter output;
Codex initialization also uses the qualified nested sandbox setting. A test setup
command was corrected to the public `/workspace` path. Those failed attempts are
retained as failures, not execution passes. No unrelated source was changed to
accommodate them.

Final runtime images:

- Codex: `sha256:fc6f2f55a241f64feddd2123ea20e8d0e48d7e4cd44b22d006ca4153d6d5171f`
- Claude SDK: `sha256:bf43c1b63c82d86a73731c52800331c6e492c8fbe8a9ae0a5f5c77d4aebd015d`

Private mx evidence lives under `~/.parsar/tests/agent-defaults-20260920/`:
`manifest.json`, `proof-state.json`, `cancellation-evidence.json`,
`updated-defaults-evidence.json`, `retry-evidence.json` and the validation scripts.
That directory also contains credentials and must not be published wholesale.
Only this report's non-secret observations belong in the PR.

Real native acceptance covers Codex and Claude SDK. MiniMax Code selection and
rejection rules are covered by automated tests, but this report does not claim
real MiniMax Code execution. Model availability remains the configured provider's
responsibility; Core does not guess aliases or silently choose another harness.
