#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
mkdir -p "$HOME/.parsar/installer-tests"
test_root="$(mktemp -d "$HOME/.parsar/installer-tests/check.XXXXXX")"
trap 'status=$?; if [ "$status" -ne 0 ]; then cat "$test_root"/*/output.log >&2; fi; rm -rf "$test_root"' EXIT
mkdir -p "$test_root/bin"

# No Docker daemon is used. Record the lifecycle and simulate image identity
# and preparation failures; actual filesystem ownership is checked in Docker.
cat > "$test_root/bin/docker" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "${*//$'\n'/ }" >> "$PARSAR_INSTALL_TEST_LOG"
if [[ "$1" == compose && "$*" == *' run '* ]]; then
  # Compose v2.15 accepts pull_policy in YAML but has no run --pull option.
  [[ "$*" != *'--pull '* && "$*" == *local-images.yml* ]]
  # The installer closes stdin, so Compose must not request an interactive TTY.
  [[ "$*" == *' -T '* ]]
fi
if [[ "$*" == 'compose version --short' ]]; then
  printf '2.15.1\n'
elif [[ "$1" == info ]]; then
  printf 'linux\n'
elif [[ "$*" == *'config --services'* ]]; then
  printf 'postgres\nparsar-server\n'
elif [[ "$*" == *' ps -aq '* ]]; then
  printf 'test-container\n'
elif [[ "$1" == inspect ]]; then
  printf 'running healthy\n'
elif [[ "$*" == *' port parsar-server '* ]]; then
  printf '127.0.0.1:18080\n'
elif [[ "$*" == *'id -u'* ]]; then
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
  : > "$test_root/$case_name/docker.log"
  PATH="$test_root/bin:$PATH" \
    PARSAR_INSTALL_TEST_LOG="$test_root/$case_name/docker.log" \
    PARSAR_INSTALL_TEST_FAIL="$failure" \
    bash "$repo_root/install.sh" --home "$test_root/$case_name/home" \
      --compose-file "$repo_root/docker-compose.yml" "$@" > "$test_root/$case_name/output.log" 2>&1
}

run_case normal ""
normal_log="$test_root/normal/docker.log"
cp "$test_root/normal/home/.env" "$test_root/normal/before.env"
run_case normal ""
cmp "$test_root/normal/before.env" "$test_root/normal/home/.env"
grep -q 'chown -Rh.*12001:14001' "$normal_log"
grep 'chown -Rh' "$normal_log" | grep -q -- '--user 0:0'
grep 'test -w' "$normal_log" | grep -vq -- '--user'
pull_line="$(grep -n ' pull$' "$normal_log" | head -1 | cut -d: -f1)"
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
echo 'Installer data preparation and Core configuration checks passed.'

python3 "$repo_root/scripts/check-installer-cases.py"
