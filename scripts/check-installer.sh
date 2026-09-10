#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
mkdir -p "$HOME/.parsar/installer-tests"
test_root="$(mktemp -d "$HOME/.parsar/installer-tests/check.XXXXXX")"
trap 'rm -rf "$test_root"' EXIT
mkdir -p "$test_root/bin"

# No Docker daemon is used. Record the lifecycle and simulate image identity
# and preparation failures; actual filesystem ownership is checked in Docker.
cat > "$test_root/bin/docker" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$PARSAR_INSTALL_TEST_LOG"
if [[ "$1" == compose && "$*" == *' run '* ]]; then
  # Compose v2.15 accepts pull_policy in YAML but has no run --pull option.
  [[ "$*" != *'--pull '* && "${PARSAR_IMAGE_PULL_POLICY:-}" == never ]]
  # The installer closes stdin, so Compose must not request an interactive TTY.
  [[ "$*" == *' -T '* ]]
fi
if [[ "$*" == *' ps -aq parsar-runtime' && "${PARSAR_INSTALL_TEST_RUNTIME:-}" != "" ]]; then
  printf 'old-runtime\n'
elif [[ "$*" == *'range .Config.Env'* ]]; then
  case "$PARSAR_INSTALL_TEST_RUNTIME" in
    configured) printf 'CLAUDE_CONFIG_DIR=/root/.parsar/claude-code\n' ;;
    custom) printf 'CLAUDE_CONFIG_DIR=/custom-history\n' ;;
  esac
elif [[ "$*" == *'{{.Image}}'* ]]; then
  printf 'sha256:old-image\n'
elif [[ "$1" == cp ]]; then
  case "$PARSAR_INSTALL_TEST_RUNTIME" in
    missing) printf 'Error response from daemon: Could not find the file %s in container old-runtime\n' "${2#*:}" >&2; exit 1 ;;
    copy-failure) printf 'Docker connection failed\n' >&2; exit 1 ;;
  esac
elif [[ "$1" == run && "${PARSAR_INSTALL_TEST_RUNTIME:-}" == conflict ]]; then
  exit 1
fi
if [[ "$*" == *'id -u'* ]]; then
  printf '12001:14001'
elif [[ "$*" == *'chown -Rh'* && "${PARSAR_INSTALL_TEST_FAIL:-}" == ownership ]]; then
  exit 1
elif [[ "$*" == *'test -w'* && "${PARSAR_INSTALL_TEST_FAIL:-}" == writable ]]; then
  exit 1
fi
MOCK
chmod +x "$test_root/bin/docker"

run_case() {
  local case_name="$1" failure="$2"
  shift 2
  mkdir -p "$test_root/$case_name"
  PATH="$test_root/bin:$PATH" \
    PARSAR_INSTALL_TEST_LOG="$test_root/$case_name/docker.log" \
    PARSAR_INSTALL_TEST_FAIL="$failure" \
    PARSAR_INSTALL_TEST_RUNTIME="${PARSAR_INSTALL_TEST_RUNTIME:-}" \
    bash "$repo_root/install.sh" --home "$test_root/$case_name/home" \
      --compose-file "$repo_root/docker-compose.yml" "$@" > "$test_root/$case_name/output.log" 2>&1
}

run_case normal ""
normal_log="$test_root/normal/docker.log"
grep -q 'chown -Rh.*12001:14001' "$normal_log"
grep 'chown -Rh' "$normal_log" | grep -q -- '--user 0:0'
grep 'test -w' "$normal_log" | grep -vq -- '--user'
pull_line="$(grep -n ' pull ' "$normal_log" | head -1 | cut -d: -f1)"
owner_line="$(grep -n 'chown -Rh' "$normal_log" | cut -d: -f1)"
write_line="$(grep -n 'test -w' "$normal_log" | cut -d: -f1)"
up_line="$(grep -n ' up ' "$normal_log" | cut -d: -f1)"
[[ "$pull_line" -lt "$owner_line" && "$owner_line" -lt "$write_line" && "$write_line" -lt "$up_line" ]]

for failure in ownership writable; do
  if run_case "$failure" "$failure"; then
    echo "Installer ignored $failure failure" >&2
    exit 1
  fi
  if grep -q ' up ' "$test_root/$failure/docker.log"; then
    echo "Installer started services after $failure failure" >&2
    exit 1
  fi
done

for runtime in legacy missing configured; do
  PARSAR_INSTALL_TEST_RUNTIME="$runtime" run_case "$runtime" ""
  runtime_log="$test_root/$runtime/docker.log"
  if [[ "$runtime" == configured ]]; then
    ! grep -q '^stop ' "$runtime_log"
    ! grep -q '^cp ' "$runtime_log"
  else
    stop_line="$(grep -n '^stop ' "$runtime_log" | cut -d: -f1)"
    copy_line="$(grep -n '^cp ' "$runtime_log" | head -1 | cut -d: -f1)"
    migrate_line="$(grep -n '^run --rm --volumes-from old-runtime' "$runtime_log" | cut -d: -f1)"
    up_line="$(grep -n ' up ' "$runtime_log" | cut -d: -f1)"
    [[ "$stop_line" -lt "$copy_line" && "$copy_line" -lt "$migrate_line" && "$migrate_line" -lt "$up_line" ]]
  fi
done

for runtime in copy-failure conflict custom; do
  if PARSAR_INSTALL_TEST_RUNTIME="$runtime" run_case "$runtime" ""; then
    echo "Installer ignored runtime history $runtime" >&2
    exit 1
  fi
  ! grep -q ' up ' "$test_root/$runtime/docker.log"
done

mkdir -p "$test_root/raw"
printf 'PARSAR_PG_DATA_DIR=existing-postgres\nPARSAR_MASTER_KEY=existing-secret\n' > "$test_root/raw/original.env"
cp "$test_root/raw/original.env" "$test_root/raw/before.env"
PATH="$test_root/bin:$PATH" PARSAR_HOME="$test_root/raw/backups" \
  PARSAR_INSTALL_TEST_LOG="$test_root/raw/docker.log" PARSAR_INSTALL_TEST_RUNTIME=legacy \
  bash "$repo_root/install.sh" migrate-runtime-history -p existing-stack \
    -f "$repo_root/docker-compose.yml" --env-file "$test_root/raw/original.env" > "$test_root/raw/output.log" 2>&1
cmp "$test_root/raw/before.env" "$test_root/raw/original.env"
grep -Fq "compose -p existing-stack -f $repo_root/docker-compose.yml --env-file $test_root/raw/original.env ps -aq parsar-runtime" "$test_root/raw/docker.log"
! grep -Eq ' (pull|up|chown) ' "$test_root/raw/docker.log"
[[ ! -e "$test_root/raw/backups/.env" && ! -e "$test_root/raw/backups/postgres" ]]

run_case dry "" --dry-run
if grep -Eq ' (run|pull|up) ' "$test_root/dry/docker.log"; then
  echo 'Dry run started a container or pulled an image' >&2
  exit 1
fi
echo 'Installer data preparation and runtime history checks passed.'
