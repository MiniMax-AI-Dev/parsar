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
if [[ "$*" == *' run '* ]]; then
  # Compose v2.15 accepts pull_policy in YAML but has no run --pull option.
  [[ "$*" != *'--pull '* && "${PARSAR_IMAGE_PULL_POLICY:-}" == never ]]
  # The installer closes stdin, so Compose must not request an interactive TTY.
  [[ "$*" == *' -T '* ]]
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

run_case dry "" --dry-run
if grep -Eq ' (run|pull|up) ' "$test_root/dry/docker.log"; then
  echo 'Dry run started a container or pulled an image' >&2
  exit 1
fi
echo 'Installer data preparation checks passed.'
