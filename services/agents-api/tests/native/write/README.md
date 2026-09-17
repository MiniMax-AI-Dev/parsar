# Private harness file-write qualification

`TestNativeHarnessFileWrite` reuses the PostgreSQL registry, issued transport
credentials and Docker launcher fixture. It runs the built exact-pin harness,
connects its native EnvironmentManager over Noise and sends bounded binary input
to the existing private file socket. The executor runs the scoped installer under
the native Linux sandbox; no shell command or local file fallback performs writes.

Set `PARSAR_CODEX_HARNESS_ARTIFACT`, `PARSAR_WRITE_HELPER_ARTIFACT`,
`PARSAR_EXECUTOR_PROOF_DIR`, `PARSAR_EXECUTOR_LAUNCHER`, `PARSAR_CODEX_BINARY` and
`PARSAR_PLACEMENT_EXECUTOR_IMAGE` to the qualified artifacts and private evidence
root. Use the existing PostgreSQL test configuration and run:

```sh
go test ./services/agents-api/internal/store -run '^TestNativeHarnessFileWrite$' -count=1 -v -timeout=6m
```

Synthetic acceptance covers empty/binary/full 50 MiB writes, overwrite, hard-link
preservation, unsafe paths, oversized and incomplete input, caller detachment,
subsequent operation ownership and read-only rejection with configured write
selectors. Host-side bytes and inode observations are independent of the returned
commit receipt. Evidence records the harness digest and each payload digest.
Credentials never enter the record; teardown removes the owned containers and
transport credential files. Fixture placement grants only this Environment's
workspace/staging parent to the installer; it does not grant public admission.

Run `TestNativePublicEnvironmentFiles` with a real model API and the same harness
artifact separately for unchanged model execution and public Files.list regression.
Native unit tests cover strict receipt decoding, chunk rejection, bounded input,
owner shutdown and unresolved native deadlines. These finite checks do not attest
durable mutation recovery or production replacement safety.
